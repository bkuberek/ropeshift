package control

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// intakeActor is who a poll's records name. A source is not a member, and nothing
// from one acts for a person.
var intakeActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}

// IntakeReport says what one poll recorded.
type IntakeReport struct {
	Versions   int
	WorkOrders int
	// Records counts the goals, plans, and requirements a poll changed.
	Records  int
	Problems []planning.SourceProblem
}

// Intake records what a work source holds. A poll reads with no transaction, and
// each item is one short transaction (R17), so a stale read is harmless and a poll
// that stops between items finishes on the next one.
type Intake struct {
	store ports.Store
}

// NewIntake returns the use case over a Store.
func NewIntake(store ports.Store) *Intake {
	return &Intake{store: store}
}

// BindingID names the binding of one source. It holds no path, so the record means
// the same on every machine.
func BindingID(adapter, instance string) string {
	return adapter + ":" + instance
}

// Poll reads one source and records what changed. A change the ledger already holds
// appends nothing, so polling a quiet source costs no revision.
func (i *Intake) Poll(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource) (IntakeReport, error) {
	description, err := source.Describe(ctx)
	if err != nil {
		return IntakeReport{}, err
	}
	id := BindingID(description.Adapter, description.Instance)
	cursor, err := i.cursor(ctx, scope, id)
	if err != nil {
		return IntakeReport{}, err
	}
	batch, err := source.Poll(ctx, cursor)
	if err != nil {
		return IntakeReport{}, err
	}
	crashpoint.Hit("intake", "polled")
	var report IntakeReport
	report.Problems = slices.Clone(batch.Problems)
	held := map[string]string{}
	for _, item := range batch.Records {
		if key, first := repeatedRequirement(item, held); key != "" {
			detail := fmt.Sprintf("%v: requirement %s is already in %s", planning.ErrBadFile, key, first)
			report.Problems = append(report.Problems, planning.SourceProblem{Path: item.Path, Detail: detail})
			continue
		}
		outcome, err := i.recordFile(ctx, scope, item)
		if err != nil {
			return report, err
		}
		report.Records += outcome.records
		if outcome.problem != "" {
			report.Problems = append(report.Problems, planning.SourceProblem{Path: item.Path, Detail: outcome.problem})
		}
	}
	for _, item := range batch.Items {
		outcome, err := i.recordItem(ctx, scope, item)
		if err != nil {
			return report, err
		}
		report.Versions += outcome.versions
		report.WorkOrders += outcome.workOrders
		if outcome.problem != "" {
			report.Problems = append(report.Problems, planning.SourceProblem{Path: item.Path, Detail: outcome.problem})
		}
	}
	health, err := source.Health(ctx)
	if err != nil {
		return report, err
	}
	binding := planning.SourceBinding{
		ID: id, Port: planning.PortWork, Adapter: description.Adapter, Instance: description.Instance,
		Cursor: string(batch.Cursor), Health: health.State, Detail: health.Detail, Problems: report.Problems,
	}
	return report, i.settle(ctx, scope, binding, batch.Cursor == cursor, report.Versions+report.Records > 0)
}

// repeatedRequirement finds a key an earlier file in this batch holds, and otherwise marks
// this file's keys as held. A key is one requirement, so two files cannot both define it.
func repeatedRequirement(item ports.RecordItem, held map[string]string) (string, string) {
	if item.Requirements == nil {
		return "", ""
	}
	for _, spec := range item.Requirements.Requirements {
		if first, repeated := held[spec.ID]; repeated {
			return spec.ID, first
		}
	}
	for _, spec := range item.Requirements.Requirements {
		held[spec.ID] = item.Path
	}
	return "", ""
}

func (i *Intake) cursor(ctx context.Context, scope ledger.WorkspaceID, id string) (ports.Cursor, error) {
	var cursor ports.Cursor
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		binding, err := tx.Planning().Binding(id)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		cursor = ports.Cursor(binding.Cursor)
		return err
	})
	return cursor, err
}

// settle records a binding that changed. The cursor moves only with a record, so a
// commit that touches nothing the ledger holds appends nothing, and an unmoved
// source keeps the problems its last read found.
func (i *Intake) settle(ctx context.Context, scope ledger.WorkspaceID, next planning.SourceBinding, unmoved, recorded bool) error {
	return i.store.Write(ctx, scope, "intake.settle", func(tx ports.WriteTx) error {
		current, err := tx.Planning().Binding(next.ID)
		bound := err == nil
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		if unmoved {
			next.Problems = current.Problems
		}
		if next.Health == planning.HealthOK && len(next.Problems) > 0 {
			next.Health = planning.HealthBadFiles
			next.Detail = fmt.Sprintf("%d files cannot be read", len(next.Problems))
		}
		if !recorded {
			next.Cursor = current.Cursor
			if !bound {
				next.Cursor = ""
			}
		}
		if bound && sameBinding(current, next) {
			return nil
		}
		if err := tx.Planning().SaveBinding(next); err != nil {
			return err
		}
		event, err := planning.RecordEvent(planning.EventBindingChanged, planning.SubjectBinding, next.ID, next, intakeActor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

func sameBinding(a, b planning.SourceBinding) bool {
	return a.Cursor == b.Cursor && a.Health == b.Health && a.Detail == b.Detail && slices.Equal(a.Problems, b.Problems)
}

type itemOutcome struct {
	versions   int
	workOrders int
	records    int
	problem    string
}

// recordItem is one transaction. The revision serializes it with every other
// writer, so the digest it compares cannot change before it commits.
func (i *Intake) recordItem(ctx context.Context, scope ledger.WorkspaceID, item ports.WorkItem) (itemOutcome, error) {
	var outcome itemOutcome
	err := i.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		r := &itemRecorder{tx: tx, item: item}
		var err error
		outcome = itemOutcome{}
		outcome, err = r.record()
		return err
	})
	return outcome, err
}

type itemRecorder struct {
	tx   ports.WriteTx
	item ports.WorkItem
}

func (r *itemRecorder) record() (itemOutcome, error) {
	contract, isNew, err := r.nextVersion()
	if err != nil || contract.ID == "" {
		return itemOutcome{}, err
	}
	repeated, err := r.foreignOrder(contract.ID)
	if err != nil {
		return itemOutcome{}, err
	}
	if repeated != "" {
		return itemOutcome{problem: fmt.Sprintf("%v: work order %s belongs to another contract", planning.ErrBadFile, repeated)}, nil
	}
	named, known, err := r.namedPlan()
	if err != nil {
		return itemOutcome{}, err
	}
	if !known {
		return itemOutcome{problem: fmt.Sprintf("%v: plan %s is not known", planning.ErrBadFile, r.item.Contract.Plan)}, nil
	}
	if named != "" {
		contract.PlanID = named
	}
	if isNew {
		if err := r.recordNewContract(contract, named != ""); err != nil {
			return itemOutcome{}, err
		}
	}
	if err := r.writer().contract(contract); err != nil {
		return itemOutcome{}, err
	}
	crashpoint.Hit("intake.item", "versioned")
	if !isNew {
		if err := r.repin(contract); err != nil {
			return itemOutcome{}, err
		}
		crashpoint.Hit("intake.item", "repinned")
	}
	created := 0
	for _, spec := range r.item.Contract.WorkOrders {
		made, err := r.recordOrder(contract, spec)
		if err != nil {
			return itemOutcome{}, err
		}
		if made {
			created++
		}
	}
	crashpoint.Hit("intake.item", "ordered")
	return itemOutcome{versions: 1, workOrders: created}, nil
}

// namedPlan resolves the file's `plan` key. No key is known, because such a contract gets
// a plan of its own.
func (r *itemRecorder) namedPlan() (string, bool, error) {
	if r.item.Contract.Plan == "" {
		return "", true, nil
	}
	key := ledger.ExternalKey{Adapter: r.item.Key.Adapter, Instance: r.item.Key.Instance, NativeID: r.item.Contract.Plan}
	link, err := r.tx.Planning().Link(planning.SubjectPlan, key)
	if errors.Is(err, ports.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return link.RecordID, true, nil
}

// repin moves this contract's unstarted work to a new version. A review covers one
// version, so ready work goes back to contract review, and work a person sent back for
// this change returns to it. Started work keeps its pin (FR-PLAN-8).
func (r *itemRecorder) repin(contract planning.Contract) error {
	orders, err := r.tx.WorkOrders().ByContract(contract.ID)
	if err != nil {
		return err
	}
	for _, order := range orders {
		if !workorders.TakesNewVersion(order.State) {
			continue
		}
		if err := r.tx.WorkOrders().Pin(order.ID, contract.Version); err != nil {
			return err
		}
		event, err := workorders.PinEvent(order.ID, contract.Version, intakeActor)
		if err != nil {
			return err
		}
		if _, err := r.tx.Append(event); err != nil {
			return err
		}
		switch order.State {
		case workorders.Ready:
			err = takeEdge(r.tx, order.ID, workorders.TriggerBackToReview, intakeActor)
		case workorders.Planning:
			err = takeEdge(r.tx, order.ID, workorders.TriggerContractSubmitted, intakeActor)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// nextVersion builds the version to record, or a zero contract when the ledger
// already holds this digest.
func (r *itemRecorder) nextVersion() (planning.Contract, bool, error) {
	file := r.item.Contract
	contract := planning.Contract{
		Version: 1, Title: file.Title, Digest: r.item.Digest, Source: r.item.Source, Statement: r.item.Statement,
		Criteria: file.Criteria, Requirements: file.Requirements, Decisions: file.Decisions, Status: planning.StatusActive,
	}
	link, err := r.tx.Planning().Link(planning.SubjectContract, r.item.Key)
	if errors.Is(err, ports.ErrNotFound) {
		if contract.ID, err = newID(r.tx.Now()); err != nil {
			return planning.Contract{}, false, err
		}
		if contract.PlanID, err = newID(r.tx.Now()); err != nil {
			return planning.Contract{}, false, err
		}
		return contract, true, nil
	}
	if err != nil {
		return planning.Contract{}, false, err
	}
	latest, err := r.tx.Planning().LatestContract(link.RecordID)
	if err != nil {
		return planning.Contract{}, false, err
	}
	if latest.Digest == r.item.Digest {
		return planning.Contract{}, false, nil
	}
	contract.ID, contract.Version, contract.PlanID = latest.ID, latest.Version+1, latest.PlanID
	return contract, false, nil
}

// foreignOrder finds a work order this file names that another contract owns. A
// native ID identifies one record, so the file cannot take it over.
func (r *itemRecorder) foreignOrder(contractID string) (string, error) {
	for _, spec := range r.item.Contract.WorkOrders {
		link, err := r.tx.Planning().Link(workorders.SubjectKind, r.orderKey(spec))
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		order, err := r.tx.WorkOrders().Get(link.RecordID)
		if err != nil {
			return "", err
		}
		if order.ContractID != contractID {
			return spec.ID, nil
		}
	}
	return "", nil
}

func (r *itemRecorder) orderKey(spec planning.WorkOrderSpec) ledger.ExternalKey {
	return ledger.ExternalKey{Adapter: r.item.Key.Adapter, Instance: r.item.Key.Instance, NativeID: spec.ID}
}

// recordNewContract gives a contract with no plan key a plan of its own, so one
// file is enough for a first work order.
func (r *itemRecorder) recordNewContract(contract planning.Contract, planned bool) error {
	if !planned {
		plan := planning.Plan{ID: contract.PlanID, Title: contract.Title, Status: planning.StatusActive}
		if err := r.writer().plan(plan); err != nil {
			return err
		}
	}
	return r.writer().link(planning.SubjectContract, contract.ID, r.item.Key)
}

func (r *itemRecorder) writer() planningWriter {
	return planningWriter{tx: r.tx, actor: intakeActor}
}

// planningWriter records planning data with the event each record traces to.
type planningWriter struct {
	tx    ports.WriteTx
	actor ledger.Actor
}

func (w planningWriter) append(eventType, subjectKind, subjectID string, record any) error {
	event, err := planning.RecordEvent(eventType, subjectKind, subjectID, record, w.actor)
	if err != nil {
		return err
	}
	_, err = w.tx.Append(event)
	return err
}

func (w planningWriter) plan(plan planning.Plan) error {
	if err := w.tx.Planning().InsertPlan(plan); err != nil {
		return err
	}
	return w.append(planning.EventPlanRecorded, planning.SubjectPlan, plan.ID, plan)
}

func (w planningWriter) changedPlan(plan planning.Plan) error {
	if err := w.tx.Planning().UpdatePlan(plan); err != nil {
		return err
	}
	return w.append(planning.EventPlanRecorded, planning.SubjectPlan, plan.ID, plan)
}

func (w planningWriter) goal(goal planning.Goal) error {
	if err := w.tx.Planning().SaveGoal(goal); err != nil {
		return err
	}
	return w.append(planning.EventGoalRecorded, planning.SubjectGoal, goal.ID, goal)
}

func (w planningWriter) requirement(requirement planning.Requirement) error {
	if err := w.tx.Planning().InsertRequirement(requirement); err != nil {
		return err
	}
	if err := w.tx.Index(questions.KindRequirement, requirement.ID, requirement.Key+" "+requirement.Text); err != nil {
		return err
	}
	return w.append(planning.EventRequirementVersion, planning.SubjectRequirement, requirement.ID, requirement)
}

func (w planningWriter) contract(contract planning.Contract) error {
	if err := w.tx.Planning().InsertContract(contract); err != nil {
		return err
	}
	if err := w.tx.Index(questions.KindContract, contract.ID, contractText(contract)); err != nil {
		return err
	}
	return w.append(planning.EventContractVersion, planning.SubjectContract, contract.ID, contract)
}

// contractText is what search reads of a contract, at its newest version.
func contractText(contract planning.Contract) string {
	parts := []string{contract.Title, contract.Statement}
	for _, criterion := range contract.Criteria {
		parts = append(parts, criterion.Text)
	}
	return strings.Join(parts, " ")
}

func (w planningWriter) link(kind, id string, key ledger.ExternalKey) error {
	link := ledger.ExternalLink{RecordKind: kind, RecordID: id, Key: key, DisplayKey: key.NativeID}
	if err := w.tx.Planning().InsertLink(link); err != nil {
		return err
	}
	return w.append(planning.EventLinkRecorded, planning.SubjectLink, id, link)
}

func (w planningWriter) dependency(edge planning.Dependency) error {
	if err := w.tx.Planning().InsertDependency(edge); err != nil {
		return err
	}
	return w.append(planning.EventDependencyRecorded, planning.SubjectDependency, edge.WorkOrder, edge)
}

// recordOrder creates a work order this contract names for the first time, pinned
// to this version, and carries it to `contract_review`. A work order the ledger
// already holds keeps the version it pinned.
func (r *itemRecorder) recordOrder(contract planning.Contract, spec planning.WorkOrderSpec) (bool, error) {
	key := r.orderKey(spec)
	if _, err := r.tx.Planning().Link(workorders.SubjectKind, key); err == nil {
		return false, nil
	} else if !errors.Is(err, ports.ErrNotFound) {
		return false, err
	}
	id, err := newID(r.tx.Now())
	if err != nil {
		return false, err
	}
	record, err := workorders.New(workorders.WorkOrder{
		ID:              id,
		Kind:            workorders.KindChange,
		Title:           spec.Title,
		RepositoryID:    r.repository(spec),
		Priority:        spec.Priority,
		Size:            spec.Size,
		Requirements:    contract.Requirements,
		Needs:           spec.Needs,
		ContractID:      contract.ID,
		ContractVersion: contract.Version,
	})
	if err != nil {
		return false, err
	}
	if err := r.tx.WorkOrders().Insert(record); err != nil {
		return false, err
	}
	event, err := workorders.CreatedEvent(record, intakeActor)
	if err != nil {
		return false, err
	}
	if _, err := r.tx.Append(event); err != nil {
		return false, err
	}
	if err := r.writer().link(workorders.SubjectKind, id, key); err != nil {
		return false, err
	}
	for _, dependsOn := range spec.DependsOn {
		if err := r.dependency(id, dependsOn); err != nil {
			return false, err
		}
	}
	for _, trigger := range []workorders.Trigger{workorders.TriggerAccepted, workorders.TriggerContractSubmitted} {
		if err := takeEdge(r.tx, id, trigger, intakeActor); err != nil {
			return false, err
		}
	}
	return true, nil
}

// repository names the repository a work order targets. The current repository is
// the source itself, whose instance identifies its history.
func (r *itemRecorder) repository(spec planning.WorkOrderSpec) string {
	if spec.Repo == planning.CurrentRepository {
		return r.item.Key.Instance
	}
	return spec.Repo
}

func (r *itemRecorder) dependency(workOrder, dependsOn string) error {
	return r.writer().dependency(planning.Dependency{
		WorkOrder: workOrder, DependsOn: dependsOn, Adapter: r.item.Key.Adapter, Instance: r.item.Key.Instance,
		Kind: planning.DependencyBlocking,
	})
}

// newID generates a client ID. The time comes from the caller, because every
// transaction reads one clock.
func newID(now int64) (string, error) {
	var entropy [ledger.ULIDEntropy]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return ledger.EncodeULID(now, entropy), nil
}

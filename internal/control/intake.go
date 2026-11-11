package control

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// intakeActor is who a poll's records name. A source is not a member, and nothing
// from one acts for a person.
var intakeActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}

// IntakeReport says what one poll recorded.
type IntakeReport struct {
	Versions   int
	WorkOrders int
	// Records counts the goals, plans, documents, and requirements a poll changed.
	Records int
	// Conflicts counts the files a poll left out because the base under them moved.
	Conflicts int
	// Edges counts the derived dependencies a poll added or removed, from milestones and shared contracts.
	Edges int
	// PlanVersions counts the plan versions a poll made, each with a request for a member to approve it.
	PlanVersions int
	Problems     []planning.SourceProblem

	// read names the version a poll found of each file it named, and changed the files whose version
	// or record it wrote. They are what settles a problem an accepted commit found.
	read    map[string]string
	changed map[string]bool
}

// Intake records what a work source holds. A poll reads with no transaction, and
// each item is one short transaction (R17), so a stale read is harmless and a poll
// that stops between items finishes on the next one.
type Intake struct {
	store   ports.Store
	windows messages.Windows
}

// NewIntake returns the use case over a Store.
func NewIntake(store ports.Store) *Intake {
	return &Intake{store: store}
}

// Telling sets how long what a new contract version sends a lane stays useful.
func (i *Intake) Telling(windows messages.Windows) *Intake {
	i.windows = windows
	return i
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
	if err := i.keepRepository(ctx, scope, description); err != nil {
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
	if batch, err = i.offPlan(ctx, scope, description.Instance, batch); err != nil {
		return IntakeReport{}, err
	}
	report, err := i.record(ctx, scope, source, batch, planOrigin{})
	if err != nil {
		return report, err
	}
	health, err := source.Health(ctx)
	if err != nil {
		return report, err
	}
	binding := planning.SourceBinding{
		ID: id, Port: planning.PortWork, Adapter: description.Adapter, Instance: description.Instance,
		Cursor: string(batch.Cursor), Health: health.State, Detail: health.Detail,
	}
	read := settling{binding: binding, report: report, unmoved: batch.Cursor == cursor, recorded: report.Versions+report.Records+report.Edges+report.PlanVersions > 0}
	return report, i.settle(ctx, scope, source, read)
}

// keepRepository makes the record of the repository a source reads, or refreshes the two facts git gives
// of it (FR-REPO-1). A source that says nothing of a repository makes none.
func (i *Intake) keepRepository(ctx context.Context, scope ledger.WorkspaceID, description ports.SourceDescription) error {
	if description.Instance == "" || (description.Remote == "" && description.DefaultBranch == "") {
		return nil
	}
	repositories := NewRepositories(i.store)
	// A poll finds the same facts nearly every time, and a read costs the workspace lock nothing. A
	// repository the project removed is read no more, which each poll says.
	if known, err := repositories.Get(ctx, scope, description.Instance); err == nil {
		if known.Removed() {
			return errRemoved(known)
		}
		if known.Remote == description.Remote && known.DefaultBranch == description.DefaultBranch && known.Name != "" {
			return nil
		}
	}
	_, err := repositories.Detect(ctx, scope, DetectedRepository{
		ID: description.Instance, Remote: description.Remote, DefaultBranch: description.DefaultBranch, Directory: description.Directory,
	}, intakeActor)
	return err
}

// record stores what one read of a source found, item by item.
func (i *Intake) record(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource, batch ports.ChangeBatch, origin planOrigin) (IntakeReport, error) {
	report := IntakeReport{read: map[string]string{}, changed: map[string]bool{}}
	report.Problems = slices.Clone(batch.Problems)
	for _, problem := range batch.Problems {
		report.saw(problem.Path, problem.Source)
	}
	batch, refused := refuseCycles(batch)
	for _, file := range refused {
		report.saw(file.path, file.source)
		report.Problems = append(report.Problems, planning.SourceProblem{Path: file.path, Detail: file.detail, Source: file.source})
	}
	held := map[string]string{}
	refs := refsOf(batch)
	for _, item := range batch.Records {
		report.saw(item.Path, item.Source)
		if detail := repeatedKey(item, held); detail != "" {
			report.Problems = append(report.Problems, planning.SourceProblem{Path: item.Path, Detail: detail, Source: item.Source})
			continue
		}
		outcome, err := i.recordFile(ctx, scope, source, item, refs)
		if err != nil {
			return report, err
		}
		report.Records += outcome.records
		report.note(item.Path, item.Source, outcome)
	}
	for _, item := range batch.Items {
		report.saw(item.Path, item.Source)
		outcome, err := i.recordItem(ctx, scope, source, item, refs)
		if err != nil {
			return report, err
		}
		report.Versions += outcome.versions
		report.WorkOrders += outcome.workOrders
		report.note(item.Path, item.Source, outcome)
	}
	if len(batch.Records)+len(batch.Items) == 0 {
		return report, nil
	}
	edges, err := i.reconcileEdges(ctx, scope)
	report.Edges = edges
	if err != nil {
		return report, err
	}
	report.PlanVersions, err = i.reconcilePlanVersions(ctx, scope, refs.plans, origin)
	return report, err
}

func (r *IntakeReport) saw(path, source string) {
	if source != "" {
		r.read[path] = source
	}
}

func (r *IntakeReport) note(path, source string, outcome itemOutcome) {
	r.Conflicts += outcome.conflicts
	if outcome.versions+outcome.records > 0 {
		r.changed[path] = true
	}
	if outcome.problem != "" {
		r.Problems = append(r.Problems, planning.SourceProblem{Path: path, Detail: outcome.problem, Source: source, Conflict: outcome.conflicts > 0})
	}
}

// unreadLineage says a transaction needs what only a source can say, and a transaction asks no
// source (R17).
type unreadLineage struct{ read, recorded string }

func (u *unreadLineage) Error() string {
	return "the lineage of " + u.read + " against " + u.recorded + " is unread"
}

// lineages are the answers a source gave for one file.
type lineages map[unreadLineage]ports.Lineage

// of answers from what a source said, and otherwise stops the transaction to ask it. A version with
// no source has nothing to compare, and a read of the version itself is no change.
func (l lineages) of(read, recorded string) (ports.Lineage, error) {
	if recorded == "" || recorded == read {
		return ports.LineageAhead, nil
	}
	pair := unreadLineage{read: read, recorded: recorded}
	if stands, known := l[pair]; known {
		return stands, nil
	}
	return "", &pair
}

// withLineage runs one file's transaction with what its source says of that file. The transaction
// finds which versions it stands against, under the revision that decides what it writes, and one it
// has no answer for stops it, so the question goes to the source with nothing held open. An answer
// names two versions that never change, so one a later pass reads again stays true.
func withLineage(ctx context.Context, source ports.WorkSource, write func(lineages) error) error {
	known := lineages{}
	for {
		err := write(known)
		var unread *unreadLineage
		if !errors.As(err, &unread) {
			return err
		}
		stands, err := source.Relate(ctx, unread.read, unread.recorded)
		switch {
		case errors.Is(err, ports.ErrUnsupported):
			stands = ports.LineageAhead
		case err != nil:
			return err
		}
		known[*unread] = stands
	}
}

// baseMoved is the problem of a file that changed what the ledger's version changed, which is a
// conflict and not the next version. A merge holds both, so the merge is what intake reads next.
func baseMoved(kind, id string) string {
	return fmt.Sprintf("%v: %s %s has a version this commit never held, and this commit changes it too. Merge the two, and intake reads the merge as the next version",
		planning.ErrBaseMoved, kind, id)
}

// repeatedKey finds a key an earlier file in this batch holds, and otherwise marks this file's keys as
// held. A requirement is one record, and so is a document, so two files cannot both define either.
func repeatedKey(item ports.RecordItem, held map[string]string) string {
	var names []string
	if head, ok := item.Head(); ok {
		names = append(names, "document "+head.Ref())
	}
	if item.Requirements != nil {
		for _, spec := range item.Requirements.Requirements {
			names = append(names, "requirement "+spec.ID)
		}
	}
	for _, name := range names {
		if first, repeated := held[name]; repeated {
			return fmt.Sprintf("%v: %s is already in %s", planning.ErrBadFile, name, first)
		}
	}
	for _, name := range names {
		held[name] = item.Path
	}
	return ""
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

// settling is one read of a source, for its binding to record.
type settling struct {
	binding planning.SourceBinding
	report  IntakeReport
	// plan is the accepted `plan` work order whose commit this read is of, and empty for a read of
	// where the source stands.
	plan string
	// unmoved says where the source stands did not move, so this read found nothing.
	unmoved bool
	// recorded says this read changed a record, which is what moves the cursor.
	recorded bool
}

// settle records a binding that changed. The cursor moves only with a record, so a
// commit that touches nothing the ledger holds appends nothing, and an unmoved
// source keeps the problems its last read found. Settling a problem an accepted commit found can
// need what only the source says of a file's history, which withLineage asks with no transaction open.
func (i *Intake) settle(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource, read settling) error {
	return withLineage(ctx, source, func(known lineages) error {
		return i.store.Write(ctx, scope, "intake.settle", func(tx ports.WriteTx) error {
			next := read.binding
			current, err := tx.Planning().Binding(next.ID)
			bound := err == nil
			if err != nil && !errors.Is(err, ports.ErrNotFound) {
				return err
			}
			if next.Problems, err = read.problems(current.Problems, known); err != nil {
				return err
			}
			if next.Health == planning.HealthOK && len(next.Problems) > 0 {
				next.Health = planning.HealthBadFiles
				next.Detail = fmt.Sprintf("%d files cannot be read", len(next.Problems))
			}
			if !read.recorded {
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
	})
}

// problems is what the binding reports after this read. A read of where the source stands replaces
// what the last such read found, unless the source did not move, and names no version, because the
// next read that moves finds the same again. A read of an accepted commit keeps those, and adds what
// it found under its plan. A problem an accepted commit found is read once, so it stays through both
// until a read of its file settles it (D17).
func (s settling) problems(current []planning.SourceProblem, known lineages) ([]planning.SourceProblem, error) {
	var standing, held []planning.SourceProblem
	for _, problem := range current {
		if problem.WorkOrder == "" {
			standing = append(standing, problem)
			continue
		}
		settled, err := s.settles(problem, known)
		if err != nil {
			return nil, err
		}
		if !settled {
			held = append(held, problem)
		}
	}
	switch {
	case s.plan != "":
		for _, problem := range s.report.Problems {
			problem.WorkOrder = s.plan
			held = append(held, problem)
		}
	case !s.unmoved:
		standing = nil
		for _, problem := range s.report.Problems {
			problem.Source = ""
			standing = append(standing, problem)
		}
	}
	return slices.Concat(standing, held), nil
}

// settles says this read settles a problem an accepted commit found: it read the file at a version
// whose history holds the one the problem names, or, for a file nobody could read, the ledger took the
// file from this read. A conflict needs the history, because a version that never held the plan's text
// still conflicts with it.
func (s settling) settles(problem planning.SourceProblem, known lineages) (bool, error) {
	read, named := s.report.read[problem.Path]
	if !named {
		return false, nil
	}
	if s.report.changed[problem.Path] && !problem.Conflict {
		return true, nil
	}
	stands, err := known.of(read, problem.Source)
	return stands == ports.LineageAhead, err
}

func sameBinding(a, b planning.SourceBinding) bool {
	return a.Cursor == b.Cursor && a.Health == b.Health && a.Detail == b.Detail && slices.Equal(a.Problems, b.Problems)
}

type itemOutcome struct {
	versions   int
	workOrders int
	records    int
	conflicts  int
	problem    string
}

// recordItem is one transaction. The revision serializes it with every other
// writer, so the digest it compares cannot change before it commits.
func (i *Intake) recordItem(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource, item ports.WorkItem, refs *batchRefs) (itemOutcome, error) {
	var outcome itemOutcome
	err := withLineage(ctx, source, func(known lineages) error {
		return i.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
			r := &itemRecorder{tx: tx, item: item, windows: i.windows, known: known, refs: refs}
			var err error
			outcome = itemOutcome{}
			outcome, err = r.record()
			return err
		})
	})
	return outcome, err
}

type itemRecorder struct {
	tx      ports.WriteTx
	item    ports.WorkItem
	windows messages.Windows
	known   lineages
	// refs are the milestones and contracts the whole batch holds, which this file's references resolve
	// against before the ledger.
	refs *batchRefs
	// moved says this file's base moved, once nextVersion has read that.
	moved bool
	// latest is the version the ledger holds when this file says what that version does, once
	// nextVersion has read that.
	latest *planning.Contract
}

func (r *itemRecorder) record() (itemOutcome, error) {
	contract, isNew, err := r.nextVersion()
	if err != nil {
		return itemOutcome{}, err
	}
	if r.moved {
		return itemOutcome{conflicts: 1, problem: baseMoved("contract", r.item.Contract.ID)}, nil
	}
	if contract.ID == "" {
		return r.standingProblems()
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
	if problem, err := r.placement(contract.PlanID); err != nil || problem != "" {
		return itemOutcome{problem: problem}, err
	}
	pins, unknown, err := r.resolveDocuments()
	if err != nil {
		return itemOutcome{}, err
	}
	if len(unknown) != 0 {
		return itemOutcome{problem: fmt.Sprintf("%v: contract %s cites %s, which the ledger does not hold",
			planning.ErrBadFile, r.item.Contract.ID, strings.Join(unknown, ", "))}, nil
	}
	contract.Documents = pins
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
		if err := r.tellLanes(contract); err != nil {
			return itemOutcome{}, err
		}
		crashpoint.Hit("intake.item", "told")
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
	problem, err := r.problemsOf(contract)
	return itemOutcome{versions: 1, workOrders: created, problem: problem}, err
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

// tellLanes tells each lane pinned to an earlier version, which the pack item index finds. A
// lane stays on its pin, so a new version never changes one silently, and policy decides whether
// it goes on (FR-PLAN-8).
func (r *itemRecorder) tellLanes(contract planning.Contract) error {
	touched := reach{lanes: map[string]int64{}}
	if err := addPinnedLanes(r.tx, &touched, contextpacks.KindContract, contract.ID); err != nil {
		return err
	}
	if err := addRunningLanes(r.tx, &touched); err != nil {
		return err
	}
	ref := planning.ContractKey(contract.ID, contract.Version)
	return tellPinned(r.tx, touched, r.windows, pinnedNotice{
		kind: messages.KindContractNotice, ref: ref, body: contextpacks.ContractNotice(contract.Title, ref), reason: "contract version " + ref,
	})
}

// pinnedNotice is what a lane hears when a record its pack holds has a new version.
type pinnedNotice struct {
	kind messages.Kind
	// ref names the new version, and keeps a repeated pass from telling a lane twice.
	ref    string
	body   string
	reason string
}

// tellPinned leaves one notice for each lane, and asks the ones a policy says to park.
func tellPinned(tx ports.WriteTx, touched reach, windows messages.Windows, notice pinnedNotice) error {
	if len(touched.lanes) == 0 {
		return nil
	}
	policy, err := lanePolicyInForce(tx)
	if err != nil {
		return err
	}
	for _, lane := range slices.Sorted(maps.Keys(touched.lanes)) {
		draft := messages.Draft{
			ID: messages.DerivedID(string(notice.kind), notice.ref, lane), To: messages.Target{Lane: lane},
			Kind: notice.kind, Body: messages.Clip(notice.body),
		}
		if _, err := sendIn(tx, draft, windows, intakeActor); err != nil {
			return err
		}
		if policy.OnContractVersion != authority.LanePark {
			continue
		}
		if err := askToPark(tx, lane, touched.lanes[lane], notice.reason, intakeActor); err != nil {
			return err
		}
	}
	return nil
}

// nextVersion builds the version to record, or a zero contract when this read changes nothing: the
// ledger holds this digest, or the ledger's version was written knowing what this commit holds. A
// commit that never saw the ledger's version and changes the file too sets moved.
func (r *itemRecorder) nextVersion() (planning.Contract, bool, error) {
	file := r.item.Contract
	contract := planning.Contract{
		Version: 1, Title: file.Title, Digest: r.item.Digest, Source: r.item.Source, Statement: r.item.Statement,
		Criteria: file.Criteria, Requirements: file.Requirements, Decisions: file.Decisions, Status: planning.StatusActive,
		Milestone: file.Milestone, Shared: file.Shared, Uses: file.Uses, Orders: orderParts(file),
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
		r.latest = &latest
		return planning.Contract{}, false, nil
	}
	stands, err := r.known.of(r.item.Source, latest.Source)
	if err != nil {
		return planning.Contract{}, false, err
	}
	if stands != ports.LineageAhead {
		r.moved = stands == ports.LineageDiverged
		return planning.Contract{}, false, nil
	}
	contract.ID, contract.Version, contract.PlanID = latest.ID, latest.Version+1, latest.PlanID
	return contract, false, nil
}

// resolveDocuments pins each document this file cites to the version the ledger holds, which is the
// one at the commit that was read: documents come before contracts in a batch. It names each citation
// the ledger holds no document for.
func (r *itemRecorder) resolveDocuments() ([]planning.DocumentPin, []string, error) {
	var pins []planning.DocumentPin
	var unknown []string
	for _, ref := range r.item.Contract.Documents {
		latest, err := r.tx.Planning().LatestDocumentVersion(ref)
		switch {
		case errors.Is(err, ports.ErrNotFound):
			unknown = append(unknown, ref)
		case err != nil:
			return nil, nil, err
		default:
			pins = append(pins, planning.DocumentPin{Document: ref, Version: latest.Version})
		}
	}
	return pins, unknown, nil
}

// standingProblems reports what still stands against a version this file did not change, because a
// requirements document can change under a contract that did not.
func (r *itemRecorder) standingProblems() (itemOutcome, error) {
	if r.latest == nil {
		return itemOutcome{}, nil
	}
	problem, err := r.problemsOf(*r.latest)
	return itemOutcome{problem: problem}, err
}

// problemsOf names what keeps a recorded contract's work back while it stands: criteria that serve no
// requirement, and repositories the project does not hold.
func (r *itemRecorder) problemsOf(contract planning.Contract) (string, error) {
	untraced, err := r.untraced(contract)
	if err != nil {
		return "", err
	}
	unknown, err := r.unknownRepositories()
	if err != nil || untraced == "" || unknown == "" {
		return untraced + unknown, err
	}
	return untraced + "; " + unknown, nil
}

// unknownRepositories names each repository the file's work orders name that the project does not hold
// (FR-REPO-6). The work is recorded either way, and the definition of ready holds it back.
func (r *itemRecorder) unknownRepositories() (string, error) {
	var unknown []string
	for _, spec := range r.item.Contract.WorkOrders {
		_, name, err := r.repository(spec)
		if err != nil {
			return "", err
		}
		if name != "" && !slices.Contains(unknown, strconv.Quote(name)) {
			unknown = append(unknown, strconv.Quote(name))
		}
	}
	if len(unknown) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%v: contract %s names repository %s, which the project does not hold", planning.ErrBadFile, r.item.Contract.ID, strings.Join(unknown, ", ")), nil
}

// untraced names each criterion of a contract version that serves no requirement its plan has, when
// the plan has a requirements document. The version is recorded either way, and the definition of
// ready is what holds its work back (FR-DOC-5).
func (r *itemRecorder) untraced(contract planning.Contract) (string, error) {
	known, traced, err := planRequirements(r.tx.Planning(), contract.PlanID)
	if err != nil || !traced {
		return "", err
	}
	found := planning.Trace(contract.Criteria, known)
	if len(found) == 0 {
		return "", nil
	}
	details := make([]string, len(found))
	for i, one := range found {
		details[i] = one.String()
	}
	return fmt.Sprintf("%v: contract %s: %s", planning.ErrBadFile, r.item.Contract.ID, strings.Join(details, "; ")), nil
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

// planVersion keeps one version of a plan's file.
func (w planningWriter) planVersion(version planning.PlanVersion) error {
	if err := w.tx.Planning().InsertPlanVersion(version); err != nil {
		return err
	}
	return w.append(planning.EventPlanVersion, planning.SubjectPlan, version.PlanID, version)
}

// document keeps a document with one new version of it.
func (w planningWriter) document(record planning.DocumentRecord, version planning.DocumentVersion) error {
	if err := w.tx.Planning().SaveDocument(record); err != nil {
		return err
	}
	if err := w.tx.Planning().InsertDocumentVersion(version); err != nil {
		return err
	}
	return w.append(planning.EventDocumentVersion, planning.SubjectDocument, record.ID, planning.DocumentRecorded{Document: record, Version: version})
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
	repository, unknown, err := r.repository(spec)
	if err != nil {
		return false, err
	}
	record, err := workorders.New(workorders.WorkOrder{
		ID:                id,
		Kind:              workorders.KindChange,
		Title:             spec.Title,
		RepositoryID:      repository,
		UnknownRepository: unknown,
		Priority:          spec.Priority,
		Size:              spec.Size,
		Requirements:      contract.Requirements,
		Needs:             spec.Needs,
		Base:              spec.Base,
		ContractID:        contract.ID,
		ContractVersion:   contract.Version,
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

// repository finds the repository a work order targets (FR-REPO-6). The current repository is the source
// itself, whose instance identifies its history, and any other is the one the project holds by that name.
// A name the project does not hold targets nothing yet, and comes back as unknown.
func (r *itemRecorder) repository(spec planning.WorkOrderSpec) (id, unknown string, err error) {
	if spec.Repo == planning.CurrentRepository {
		return r.item.Key.Instance, "", nil
	}
	record, err := r.tx.Repositories().Named(spec.Repo)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && record.Removed()) {
		return "", spec.Repo, nil
	}
	if err != nil {
		return "", "", err
	}
	return record.ID, "", nil
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

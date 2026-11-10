package control

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// batchRefs is what one batch says of itself: which plan each milestone file names, and which plan
// each contract file names and whether it is shared. A reference in a file resolves against the batch
// first, because a batch is one read of the whole tree and a file can name one that sorts after it,
// and then against the ledger.
type batchRefs struct {
	milestones map[string]string
	contracts  map[string]batchContract
}

type batchContract struct {
	plan   string
	shared bool
}

func refsOf(batch ports.ChangeBatch) *batchRefs {
	refs := &batchRefs{milestones: map[string]string{}, contracts: map[string]batchContract{}}
	for _, item := range batch.Records {
		if file := item.File.Milestone; file != nil {
			refs.milestones[file.ID] = file.Plan
		}
	}
	for _, item := range batch.Items {
		refs.contracts[item.Contract.ID] = batchContract{plan: item.Contract.Plan, shared: item.Contract.Shared}
	}
	return refs
}

// milestoneHeld says a plan holds a milestone. The batch's file decides when the batch has one, and
// otherwise the ledger's document does. A milestone of another plan is not held.
func (b *batchRefs) milestoneHeld(planned ports.PlanningReader, id, planKey, planID string) (bool, error) {
	if plan, inBatch := b.milestones[id]; inBatch {
		return plan == planKey, nil
	}
	document, err := planned.Document(planning.DocumentRef(planning.FileMilestone, id))
	if errors.Is(err, ports.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return document.PlanID == planID, nil
}

// contractHeld says a plan holds a contract, and whether that contract is shared. A contract of
// another plan is not held. The key names the source the referring file came from, because a native ID
// belongs to one source.
func (b *batchRefs) contractHeld(planned ports.PlanningReader, key ledger.ExternalKey, planKey, planID string) (held, shared bool, err error) {
	if c, inBatch := b.contracts[key.NativeID]; inBatch {
		return c.plan == planKey, c.shared, nil
	}
	link, err := planned.Link(planning.SubjectContract, key)
	if errors.Is(err, ports.ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	latest, err := planned.LatestContract(link.RecordID)
	if err != nil {
		return false, false, err
	}
	return latest.PlanID == planID, latest.Shared, nil
}

// placement checks the milestone and the shared contracts a contract file names against its plan. It
// returns what is wrong, and nothing when the file sits where it says.
func (r *itemRecorder) placement(planID string) (string, error) {
	file := r.item.Contract
	if file.Milestone == "" && len(file.Uses) == 0 {
		return "", nil
	}
	if file.Plan == "" {
		var places []string
		if file.Milestone != "" {
			places = append(places, "is in milestone "+file.Milestone)
		}
		if len(file.Uses) != 0 {
			places = append(places, "uses "+strings.Join(file.Uses, ", "))
		}
		return fmt.Sprintf("%v: contract %s %s, and names no plan to find them in", planning.ErrBadFile, file.ID, strings.Join(places, " and ")), nil
	}
	var problems []string
	if file.Milestone != "" {
		held, err := r.refs.milestoneHeld(r.tx.Planning(), file.Milestone, file.Plan, planID)
		if err != nil {
			return "", err
		}
		if !held {
			problems = append(problems, fmt.Sprintf("contract %s is in milestone %s, which is not a milestone of %s", file.ID, file.Milestone, planNamed(file.Plan)))
		}
	}
	for _, use := range file.Uses {
		key := r.item.Key
		key.NativeID = use
		held, shared, err := r.refs.contractHeld(r.tx.Planning(), key, file.Plan, planID)
		if err != nil {
			return "", err
		}
		switch {
		case !held:
			problems = append(problems, fmt.Sprintf("contract %s uses %s, which is not a contract of %s", file.ID, use, planNamed(file.Plan)))
		case !shared:
			problems = append(problems, fmt.Sprintf("contract %s uses %s, which is not marked shared", file.ID, use))
		}
	}
	if len(problems) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%v: %s", planning.ErrBadFile, strings.Join(problems, "; ")), nil
}

// milestoneDependencies checks that each milestone a milestone file depends on is a milestone of the
// same plan, and returns what is wrong.
func (r *fileRecorder) milestoneDependencies(file planning.MilestoneFile, planID string) (string, error) {
	var problems []string
	for _, dependency := range file.DependsOn {
		held, err := r.refs.milestoneHeld(r.tx.Planning(), dependency, file.Plan, planID)
		if err != nil {
			return "", err
		}
		if !held {
			problems = append(problems, fmt.Sprintf("milestone %s depends on %s, which is not a milestone of %s", file.ID, dependency, planNamed(file.Plan)))
		}
	}
	if len(problems) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%v: %s", planning.ErrBadFile, strings.Join(problems, "; ")), nil
}

// planNamed says which plan a file's references resolve in.
func planNamed(key string) string {
	if key == "" {
		return "the files that name no plan"
	}
	return "plan " + key
}

// refusal is a file a batch gives up, with the reason a person reads.
type refusal struct {
	path, source, detail string
}

// planFiles is what a batch says of the structure of one plan, and what the plan is called in a problem.
type planFiles struct {
	what  string
	shape planning.Shape
}

// refuseCycles takes out of a batch every file of a plan whose dependencies form a cycle, whether the
// cycle runs through milestones, shared contracts, or work orders, alone or together, and says which
// files it took out. A plan version with a cycle is not a version: the ledger keeps what it holds, and
// the next poll that finds the cycle gone records the plan (FR-MILE-4). A contract that names no plan has
// a plan of its own, so only its own work orders can make a cycle.
func refuseCycles(batch ports.ChangeBatch) (ports.ChangeBatch, []refusal) {
	plans := map[string]*planFiles{}
	of := func(key, what string) *planFiles {
		if plans[key] == nil {
			plans[key] = &planFiles{what: what}
		}
		return plans[key]
	}
	for _, item := range batch.Records {
		if milestone := item.File.Milestone; milestone != nil {
			what := "plan " + milestone.Plan
			if milestone.Plan == "" {
				what = "the milestones that name no plan"
			}
			plan := of(planKey(milestone.Plan), what)
			plan.shape.Milestones = append(plan.shape.Milestones, planning.ShapeMilestone{ID: milestone.ID, DependsOn: milestone.DependsOn})
		}
	}
	for _, item := range batch.Items {
		file := item.Contract
		what := "plan " + file.Plan
		if file.Plan == "" {
			what = "contract " + file.ID
		}
		plan := of(itemPlanKey(item), what)
		contract := planning.ShapeContract{ID: file.ID, Milestone: file.Milestone, Uses: file.Uses}
		for _, order := range file.WorkOrders {
			contract.Orders = append(contract.Orders, planning.ShapeOrder{ID: order.ID, DependsOn: order.DependsOn})
		}
		plan.shape.Contracts = append(plan.shape.Contracts, contract)
	}
	details := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(plans)) {
		if cycle := plans[key].shape.Cycle(); cycle != nil {
			details[key] = fmt.Sprintf("%v: %s has a dependency cycle: %s", planning.ErrBadFile, plans[key].what, planning.FormatCycle(cycle))
		}
	}
	if len(details) == 0 {
		return batch, nil
	}
	var refused []refusal
	var kept ports.ChangeBatch
	kept.Cursor, kept.Problems = batch.Cursor, batch.Problems
	for _, item := range batch.Records {
		if detail, cyclic := details[recordPlanKey(item)]; cyclic {
			refused = append(refused, refusal{path: item.Path, source: item.Source, detail: detail})
			continue
		}
		kept.Records = append(kept.Records, item)
	}
	for _, item := range batch.Items {
		if detail, cyclic := details[itemPlanKey(item)]; cyclic {
			refused = append(refused, refusal{path: item.Path, source: item.Source, detail: detail})
			continue
		}
		kept.Items = append(kept.Items, item)
	}
	return kept, refused
}

// planKey names a plan in the groups refuseCycles makes. A record that names no plan is in the group
// of the files that name none.
func planKey(plan string) string { return "plan " + plan }

// recordPlanKey names the group a record file is in: the plan it is, or names. A goal is in none.
func recordPlanKey(item ports.RecordItem) string {
	if item.Plan != nil {
		return planKey(item.Plan.ID)
	}
	if head, document := item.Head(); document {
		return planKey(head.Plan)
	}
	return ""
}

// itemPlanKey names the group a contract file is in. A contract that names no plan has a plan of its
// own, so it is a group alone.
func itemPlanKey(item ports.WorkItem) string {
	if item.Contract.Plan == "" {
		return "contract " + item.Contract.ID
	}
	return planKey(item.Contract.Plan)
}

// reconcileEdges brings the derived dependencies of every plan in line with the plan as the ledger holds
// it, and returns how many it added or removed. It runs after a batch is recorded, and it is
// idempotent: a plan whose derived dependencies already match is only read, so a poll that changes
// nothing appends nothing. A poll that stops before it runs leaves the cursor where it was, and the
// next poll runs it (FR-MILE-2, FR-MILE-3).
func (i *Intake) reconcileEdges(ctx context.Context, scope ledger.WorkspaceID) (int, error) {
	var drifted []string
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		plans, err := tx.Planning().Plans()
		if err != nil {
			return err
		}
		view, err := loadView(tx.Planning())
		if err != nil {
			return err
		}
		for _, plan := range plans {
			add, remove, err := derivedDifference(view, tx.Planning(), tx.WorkOrders(), plan.ID)
			if err != nil {
				return err
			}
			if len(add)+len(remove) > 0 {
				drifted = append(drifted, plan.ID)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, plan := range drifted {
		err := i.store.Write(ctx, scope, "intake.edges", func(tx ports.WriteTx) error {
			// The plan is read again under the revision, because another writer may have moved it since.
			view, err := loadView(tx.Planning())
			if err != nil {
				return err
			}
			add, remove, err := derivedDifference(view, tx.Planning(), tx.WorkOrders(), plan)
			if err != nil {
				return err
			}
			w := planningWriter{tx: tx, actor: intakeActor}
			for _, edge := range remove {
				if err := w.removedDependency(edge); err != nil {
					return err
				}
			}
			for _, edge := range add {
				if err := w.dependency(edge); err != nil {
					return err
				}
			}
			if err := holdBackReadyWork(tx, add); err != nil {
				return err
			}
			changed += len(add) + len(remove)
			return nil
		})
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// holdBackReadyWork sends work that was `ready` back to `contract_review` when it gained a dependency
// on work that is not done, because the definition of ready runs only on the way into `ready`. The
// review stays, so the work returns to `ready` on its own once what it waits for is done. Work that is
// claimed, running, or past that is left alone, because it started under the plan it read.
func holdBackReadyWork(tx ports.WriteTx, gained []planning.Dependency) error {
	for _, edge := range gained {
		fact, err := dependencyFact(tx.Planning(), tx.WorkOrders(), edge)
		if err != nil {
			return err
		}
		if !fact.Blocking || (fact.Known && fact.State == workorders.Done) {
			continue
		}
		current, err := tx.WorkOrders().Get(edge.WorkOrder)
		if err != nil {
			return err
		}
		if current.State != workorders.Ready {
			continue
		}
		if err := takeEdge(tx, current.ID, workorders.TriggerBackToReview, intakeActor); err != nil {
			return err
		}
	}
	return nil
}

// removedDependency takes away a derived dependency the plan no longer implies.
func (w planningWriter) removedDependency(edge planning.Dependency) error {
	if err := w.tx.Planning().DeleteDependency(edge); err != nil {
		return err
	}
	return w.append(planning.EventDependencyRemoved, planning.SubjectDependency, edge.WorkOrder, edge)
}

// derivedDifference reads a plan from the ledger and compares the dependencies its structure implies
// with the derived ones the ledger holds. A dependency a file wrote is never in either list.
func derivedDifference(view workspaceView, planned ports.PlanningReader, orders ports.WorkOrderReader, plan string) (add, remove []planning.Dependency, err error) {
	shape, targets, err := view.planShape(planned, orders, plan)
	if err != nil {
		return nil, nil, err
	}
	wanted := map[planning.Dependency]bool{}
	for _, edge := range shape.Expand() {
		wanted[targets.dependency(edge)] = true
	}
	held := map[planning.Dependency]bool{}
	for _, contract := range shape.Contracts {
		for _, order := range contract.Orders {
			stored, err := planned.Dependencies(order.ID)
			if err != nil {
				return nil, nil, err
			}
			for _, edge := range stored {
				if edge.Kind.Derived() {
					held[edge] = true
				}
			}
		}
	}
	for edge := range wanted {
		if !held[edge] {
			add = append(add, edge)
		}
	}
	for edge := range held {
		if !wanted[edge] {
			remove = append(remove, edge)
		}
	}
	order := func(a, b planning.Dependency) int {
		return cmp.Or(cmp.Compare(a.WorkOrder, b.WorkOrder), cmp.Compare(a.DependsOn, b.DependsOn), cmp.Compare(a.Kind, b.Kind))
	}
	slices.SortFunc(add, order)
	slices.SortFunc(remove, order)
	return add, remove, nil
}

// orderNames says how a dependency names a work order: by the ID its file gave it and the source the
// file came from, as an explicit dependency does, and by its ledger ID when it has no file.
type orderNames map[string]ledger.ExternalKey

func (n orderNames) dependency(edge planning.ShapeEdge) planning.Dependency {
	dependency := planning.Dependency{WorkOrder: edge.Order, DependsOn: edge.DependsOn, Kind: edge.Kind}
	if key, named := n[edge.DependsOn]; named {
		dependency.DependsOn, dependency.Adapter, dependency.Instance = key.NativeID, key.Adapter, key.Instance
	}
	return dependency
}

// workspaceView is what every plan's structure is read against, which a pass reads once: the
// external links, which say how a contract and a work order are named in their files, and every
// contract version.
type workspaceView struct {
	links     []ledger.ExternalLink
	contracts []planning.Contract
}

func loadView(planned ports.PlanningReader) (workspaceView, error) {
	links, err := planned.Links()
	if err != nil {
		return workspaceView{}, err
	}
	contracts, err := planned.Contracts()
	return workspaceView{links: links, contracts: contracts}, err
}

// planShape reads one plan's structure from the ledger: its milestones at their newest version, and its
// contracts at theirs, with the work orders each holds. Contracts and work orders are named by ledger
// ID, so the edges Expand finds are between work orders the ledger holds.
func (v workspaceView) planShape(planned ports.PlanningReader, orders ports.WorkOrderReader, plan string) (planning.Shape, orderNames, error) {
	var shape planning.Shape
	documents, err := planned.PlanDocuments(plan)
	if err != nil {
		return shape, nil, err
	}
	for _, document := range documents {
		if document.Kind != planning.FileMilestone {
			continue
		}
		latest, err := planned.LatestDocumentVersion(document.ID)
		if err != nil {
			return shape, nil, err
		}
		var file planning.MilestoneFile
		if err := latest.Decode(&file); err != nil {
			return shape, nil, fmt.Errorf("milestone %s: %w", document.ID, err)
		}
		shape.Milestones = append(shape.Milestones, planning.ShapeMilestone{ID: file.ID, DependsOn: file.DependsOn})
	}
	contractKeys := map[string]ledger.ExternalKey{}
	contractIDs := map[ledger.ExternalKey]string{}
	names := orderNames{}
	for _, link := range v.links {
		switch link.RecordKind {
		case planning.SubjectContract:
			contractKeys[link.RecordID] = link.Key
			contractIDs[link.Key] = link.RecordID
		case workorders.SubjectKind:
			names[link.RecordID] = link.Key
		}
	}
	newest := map[string]planning.Contract{}
	for _, contract := range v.contracts {
		if contract.PlanID == plan && contract.Version >= newest[contract.ID].Version {
			newest[contract.ID] = contract
		}
	}
	for _, id := range slices.Sorted(maps.Keys(newest)) {
		contract := newest[id]
		held := planning.ShapeContract{ID: id, Milestone: contract.Milestone}
		for _, use := range contract.Uses {
			key := contractKeys[id]
			key.NativeID = use
			if shared, resolved := contractIDs[key]; resolved {
				held.Uses = append(held.Uses, shared)
			}
		}
		rows, err := orders.ByContract(id)
		if err != nil {
			return shape, nil, err
		}
		for _, row := range rows {
			held.Orders = append(held.Orders, planning.ShapeOrder{ID: row.ID})
		}
		shape.Contracts = append(shape.Contracts, held)
	}
	return shape, names, nil
}

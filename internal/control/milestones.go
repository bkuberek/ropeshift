package control

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
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
			plan := of(planKey(milestone.Plan), planNamed(milestone.Plan))
			plan.shape.Milestones = append(plan.shape.Milestones, planning.ShapeMilestone{ID: milestone.ID, DependsOn: milestone.DependsOn})
		}
	}
	for _, item := range batch.Items {
		file := item.Contract
		plan := of(itemPlanKey(item), planNamed(file.Plan))
		if file.Plan == "" {
			plan.what = "contract " + file.ID
		}
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

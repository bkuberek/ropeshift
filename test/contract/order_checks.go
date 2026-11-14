package contract

import (
	"encoding/json"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// OrderChecks hold the order of work across repositories (ADR-0020, ADR-0022, FR-REPO-3, FR-REPO-10): work that
// depends on work in another repository is ready only once that work is done, never starts from its accepted
// commit, and merges only after it, even when a later plan version adds the dependency to work already
// waiting to merge.
var OrderChecks = []Check{
	{"DO1", "a dependency in another repository keeps work out of ready while it waits in acceptance, and lets it pass once it is done", checkCrossRepositoryReadiness},
	{"DO2", "starting from accepted work never starts from a dependency in another repository", checkAcceptedStartStaysInItsRepository},
	{"DO3", "approved work waits in integrating for a dependency a later plan version adds, records the wait once, and merges once the dependency is done", checkMergeWaitsForItsDependencies},
}

// dependent records work in a repository that depends on other work, passes its review, and returns what the
// definition of ready said.
func (r *Rig) dependent(s ports.Store, repository string, dependsOn ...string) (workorders.WorkOrder, control.ReviewResult, error) {
	ops := r.NewOps(s)
	made, err := ops.Record(r.Ctx, r.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Build on the other repository", RepositoryID: repository,
			MaxAttempts: defaultMaxAttempts, Needs: map[string]string{"driver": "claude-code"},
		},
		Statement: "Use the work it depends on.",
		Criteria:  criteriaOf(criterionOne),
		DependsOn: dependsOn,
	}, r.Actor)
	if err != nil {
		return made, control.ReviewResult{}, err
	}
	result, err := ops.Pass(r.Ctx, r.Scope, made.ID, r.Actor)
	return made, result, err
}

// heldBy says the definition of ready kept work back for a dependency in a state.
func heldBy(result control.ReviewResult, state workorders.State) bool {
	return result.State != workorders.Ready && slices.ContainsFunc(result.Failures, func(f workorders.ReadyFailure) bool {
		return f.Check == workorders.CheckDependencies && strings.Contains(f.Detail, string(state))
	})
}

// promoted runs the tick that moves reviewed work whose definition of ready now passes, and reads the work.
func (r *Rig) promoted(s ports.Store, id string) (workorders.WorkOrder, error) {
	if _, err := r.NewOps(s).Promote(r.Ctx, r.Scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}); err != nil {
		return workorders.WorkOrder{}, err
	}
	return r.WorkOrder(s, id)
}

func checkCrossRepositoryReadiness(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	w.git.Grow(w.path("repo-api"), "main")
	dependency, err := w.order("repo-api", "")
	if err != nil {
		return err
	}
	holder, err := r.Running(s, dependency, "a")
	if err != nil {
		return err
	}
	finished := LaneFinished(w.git.Add(), LaneCheck(criterionOne, true))
	finished.Branch = "lane/" + dependency
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, finished, r.worker("a")); err != nil {
		return err
	}
	if err := r.stateIs(s, dependency, workorders.Acceptance); err != nil {
		return err
	}
	made, result, err := r.dependent(s, "repo-web", dependency)
	if err != nil {
		return err
	}
	if !heldBy(result, workorders.Acceptance) {
		return Violatedf("work whose dependency in another repository waits in acceptance reads %+v", result)
	}
	if state, err := r.NewOps(s).Accept(r.Ctx, r.Scope, dependency, r.Actor); err != nil || state != workorders.Done {
		return Violatedf("accepting the dependency left it in %s: %v", state, err)
	}
	if row, err := r.promoted(s, made.ID); err != nil || row.State != workorders.Ready {
		return Violatedf("once its dependency is done the work reads %s: %v", row.State, err)
	}
	return nil
}

func checkAcceptedStartStaysInItsRepository(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	path := w.path(startRepository)
	tip := w.git.Grow(path, "main")
	accepted := ledger.StartFromAccepted
	if err := w.repository(func(c *control.RepositoryChange) { c.StartFrom = &accepted }); err != nil {
		return err
	}
	here := w.git.Add(tip)
	same, err := w.accepted(startRepository, here)
	if err != nil {
		return err
	}
	// The other repository's accepted commit is one this repository does not hold: were it read as a start,
	// the two would be on separate lines and the work would wait for a person to join them.
	elsewhere, err := w.accepted("repo-c", w.git.Add())
	if err != nil {
		return err
	}
	id, err := w.order(startRepository, "", same, elsewhere)
	if err != nil {
		return err
	}
	claim, err := w.claim(id)
	if err != nil {
		return err
	}
	if claim.BaseRef != "lane/"+same || claim.BaseCommit != here {
		return Violatedf("the claim starts from %s at %s, want the accepted work of %s at %s", claim.BaseRef, claim.BaseCommit, same, here)
	}
	alone, err := w.order(startRepository, "", elsewhere)
	if err != nil {
		return err
	}
	if claim, err := w.claim(alone); err != nil || claim.BaseRef != "main" || claim.BaseCommit != tip {
		return Violatedf("work whose only dependency is in another repository starts from %s at %s, want main at %s: %v", claim.BaseRef, claim.BaseCommit, tip, err)
	}
	return nil
}

// planOfTwoMilestones is a plan whose second milestone's work changes the repository named, and whose first
// milestone's work changes the repository that holds the files. Neither milestone depends on the other yet.
func planOfTwoMilestones(repository string) *fake.WorkSource {
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(milestonePlan, "Launch", "")))
	source.Write("m1.md", []byte(milestoneOf("m1", milestonePlan, 1)))
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2)))
	source.Write("lib.md", []byte(placedContract{ID: "lib", Plan: milestonePlan, Milestone: "m1", Orders: []orderIn{{ID: "lib-order"}}}.text()))
	source.Write("svc.md", []byte(placedContract{ID: "svc", Plan: milestonePlan, Milestone: "m2", Orders: []orderIn{{ID: "svc-order", Repo: repository}}}.text()))
	return source
}

// waitsOf are the waits for unmerged work recorded about a work order's merge.
func (r *Rig) waitsOf(s ports.Store, id string) ([]ledger.Event, error) {
	var out []ledger.Event
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectIntegration, ID: id})
		for _, event := range events {
			if event.Type == ledger.EventIntegrationWaiting {
				out = append(out, event)
			}
		}
		return err
	})
	return out, err
}

func checkMergeWaitsForItsDependencies(r *Rig) error {
	var source *fake.WorkSource
	f, err := newForgeRigWith(r, ledger.ReviewOnBranch, func(store ports.Store, repository string) (string, error) {
		record, err := r.theRepository(store, repository)
		if err != nil {
			return "", err
		}
		source = planOfTwoMilestones(record.Name)
		if report, err := r.intake(store, source); err != nil || len(report.Problems) != 0 {
			return "", Violatedf("the plan's intake reads %+v: %v", report, err)
		}
		order, err := r.orderByTitle(store, "The work order svc-order")
		if err != nil {
			return "", err
		}
		_, err = r.NewOps(store).Pass(r.Ctx, r.Scope, order.ID, r.Actor)
		return order.ID, err
	})
	if err != nil {
		return err
	}
	s, work := f.store, f.grant.WorkOrder
	level := ledger.PersonApproves
	if _, err := r.change(s, f.repository, control.RepositoryChange{MergeAuthority: &level}, r.Actor); err != nil {
		return err
	}
	if _, err := f.judge(passes("c1", "ok")); err != nil {
		return err
	}
	if err := r.stateIs(s, work, workorders.Integrating); err != nil {
		return err
	}
	// Work in another repository that depends on work waiting to merge is not ready either.
	follow, result, err := r.dependent(s, "repo-follow", work)
	if err != nil {
		return err
	}
	if !heldBy(result, workorders.Integrating) {
		return Violatedf("work whose dependency in another repository waits to merge reads %+v", result)
	}
	// A later plan version makes the work's milestone wait for the first, whose work has not started.
	source.Write("m2.md", []byte(milestoneOf("m2", milestonePlan, 2, "m1")))
	if report, err := r.intake(s, source); err != nil || len(report.Problems) != 0 || report.Edges != 1 {
		return Violatedf("the second version's intake reads %+v: %v", report, err)
	}
	lib, err := r.orderByTitle(s, "The work order lib-order")
	if err != nil {
		return err
	}
	request, err := f.openMergeRequest(authority.MergeApprove)
	if err != nil {
		return err
	}
	if _, err := f.decide(request, f.head, true, "", r.Actor); err != nil {
		return err
	}
	if err := f.noMerge(); err != nil {
		return err
	}
	if _, err := f.integrate(); err != nil {
		return err
	}
	if err := f.noMerge(); err != nil {
		return err
	}
	view, err := f.standing()
	if err != nil || view.State != workorders.Integrating || !strings.Contains(view.Why, lib.ID) {
		return Violatedf("the merge waits %q in %s: %v", view.Why, view.State, err)
	}
	waits, err := r.waitsOf(s, work)
	if err != nil {
		return err
	}
	if len(waits) != 1 {
		return Violatedf("the wait is recorded %d times, want once: %+v", len(waits), waits)
	}
	var wait struct {
		Commit   string                `json:"commit"`
		Unmerged []workorders.Unmerged `json:"unmerged"`
	}
	if err := json.Unmarshal(waits[0].Data, &wait); err != nil {
		return err
	}
	if wait.Commit != f.head || len(wait.Unmerged) != 1 || wait.Unmerged[0].WorkOrder != lib.ID || wait.Unmerged[0].State != workorders.ContractReview {
		return Violatedf("the wait reads %s", waits[0].Data)
	}
	// The first milestone's work is done, and the next pass merges.
	if _, err := r.NewOps(s).Pass(r.Ctx, r.Scope, lib.ID, r.Actor); err != nil {
		return err
	}
	holder, err := r.Running(s, lib.ID, "b")
	if err != nil {
		return err
	}
	finished := LaneFinished(r.ID("lib-commit"), LaneCheck("c1", true))
	finished.Branch = "lane/lib"
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, finished, r.worker("b")); err != nil {
		return err
	}
	if state, err := r.NewOps(s).Accept(r.Ctx, r.Scope, lib.ID, r.Actor); err != nil || state != workorders.Done {
		return Violatedf("accepting the first milestone's work left it in %s: %v", state, err)
	}
	if _, err := f.integrate(); err != nil {
		return err
	}
	merge, err := f.oneMerge()
	if err != nil || merge.Commit != f.head {
		return Violatedf("once the dependency is done the worker is asked %+v: %v", merge, err)
	}
	if err := f.mergeCame(merge, ledger.MergeResult{Outcome: ledger.MergeMerged, MergeCommit: r.ID("merged")}); err != nil {
		return err
	}
	if err := r.stateIs(s, work, workorders.Done); err != nil {
		return err
	}
	if row, err := r.promoted(s, follow.ID); err != nil || row.State != workorders.Ready {
		return Violatedf("once its dependency merged the following work reads %s: %v", row.State, err)
	}
	return r.CheckProjection(s)
}

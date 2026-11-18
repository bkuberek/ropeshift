package contract

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// designRoot is the first-parent root of the design repository a consolidation imports, which is also the ID of
// its record when the project holds it.
const designRoot = "design-root"

// consolidating is the restructuring a member chose for a consolidation of the design repository, with the code
// tree poc left out.
func consolidating() *planning.Restructure {
	digest := planning.DigestSHA256 + strings.Repeat("c", 64)
	return &planning.Restructure{
		Shape: planning.ShapeConsolidate, Digest: digest, Root: "/work/ws", DesignPath: "/work/ws/repos/design", DesignRoot: designRoot,
		LeaveOut: []string{"poc"}, Parts: map[string]string{planning.PartCollisions: digest},
	}
}

// consolidatedBranch grows, in the start world's repository, an adoption branch that begins where main stands
// and holds the merge that imports the design repository's history, with a commit after it. It returns the
// merge, the design tip, and the branch's head.
func (w *startWorld) consolidatedBranch() (merge, tip, head string) {
	path := w.path(startRepository)
	base := w.git.Touch(path, "main", "README.md")
	w.git.Hold(base, ".gitignore", "README.md", "workspace.md")
	tip = w.git.Add()
	w.git.Hold(tip, ".gitignore", "README.md", "docs/vision.md", "poc/go.mod", "poc/main.go")
	merge = w.git.Add(base, tip)
	w.git.Name(merge, planning.ImportCommitSubject("design"))
	w.git.Point(path, "lane/adopt", merge)
	head = w.git.Touch(path, "lane/adopt", "plan.md")
	return merge, tip, head
}

// consolidationAsked runs a consolidating adoption's lane to the end of its branch and reads what its request says
// of the consolidation.
func (w *startWorld) consolidationAsked(head string) (*planning.Consolidation, error) {
	id, err := w.adoptOrder(startRepository, consolidating())
	if err != nil {
		return nil, err
	}
	if err := w.adoptLaneEnds(id, head); err != nil {
		return nil, err
	}
	_, adoption, err := w.pendingAdoption(control.NewIntake(w.s).Checking(w.starts))
	if err != nil {
		return nil, err
	}
	if adoption == nil {
		return nil, Violatedf("the consolidating adoption's request groups nothing")
	}
	return adoption.Consolidation, nil
}

// heldDesign registers the old design repository in the project, under the ID a consolidation names it by.
func (w *startWorld) heldDesign() (ledger.Repository, error) {
	return w.r.repositories(w.s).Detect(w.r.Ctx, w.r.Scope, control.DetectedRepository{ID: designRoot, DefaultBranch: "main", Directory: "design"},
		ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
}

func checkAConsolidationListsWhatItImported(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	design, err := w.heldDesign()
	if err != nil {
		return err
	}
	merge, tip, head := w.consolidatedBranch()
	consolidation, err := w.consolidationAsked(head)
	if err != nil {
		return err
	}
	want := planning.Consolidation{
		Design: "design", Merge: merge, DesignTip: tip,
		Collisions: []planning.Collision{{Path: ".gitignore", Kept: planning.KeptUnion}, {Path: "README.md", Kept: planning.KeptWorkspace}},
		LeftOut:    []string{"poc"}, Removes: &planning.RemovedRepository{ID: designRoot, Name: design.Name}, PersonMerges: true,
	}
	if consolidation == nil || !reflect.DeepEqual(*consolidation, want) {
		return Violatedf("the consolidation reads\n%+v\nwant\n%+v", consolidation, want)
	}
	return nil
}

func checkAConsolidationTheProjectDoesNotHoldRemovesNothing(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	// The repository merges at person_approves, where ropeshift merges it, so the approval says nothing of the
	// person's merge.
	level := ledger.PersonApproves
	if _, err := r.change(s, startRepository, control.RepositoryChange{MergeAuthority: &level}, r.Actor); err != nil {
		return err
	}
	_, _, head := w.consolidatedBranch()
	consolidation, err := w.consolidationAsked(head)
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Removes != nil || consolidation.PersonMerges || len(consolidation.Collisions) != 2 {
		return Violatedf("a consolidation of a design repository the project does not hold reads %+v", consolidation)
	}
	return nil
}

func checkAConsolidationWhoseMergeIsUnreadSaysWhy(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	if _, err := w.heldDesign(); err != nil {
		return err
	}
	// The branch holds no merge that imports a history.
	path := w.path(startRepository)
	base := w.git.Touch(path, "main", "README.md")
	w.git.Point(path, "lane/adopt", base)
	head := w.git.Touch(path, "lane/adopt", "plan.md")
	consolidation, err := w.consolidationAsked(head)
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Unread == "" || consolidation.Merge != "" || consolidation.DesignTip != "" ||
		consolidation.Design != "design" || !reflect.DeepEqual(consolidation.LeftOut, []string{"poc"}) || consolidation.Removes == nil {
		return Violatedf("a consolidation whose merge cannot be read reads %+v", consolidation)
	}
	return nil
}

// consolidationPending runs a consolidating adoption, with the old design repository held by the project, to its
// request for approval.
func (w *startWorld) consolidationPending() (authority.Approval, ledger.Repository, error) {
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return authority.Approval{}, ledger.Repository{}, err
	}
	design, err := w.heldDesign()
	if err != nil {
		return authority.Approval{}, ledger.Repository{}, err
	}
	_, _, head := w.consolidatedBranch()
	id, err := w.adoptOrder(startRepository, consolidating())
	if err != nil {
		return authority.Approval{}, ledger.Repository{}, err
	}
	if err := w.adoptLaneEnds(id, head); err != nil {
		return authority.Approval{}, ledger.Repository{}, err
	}
	pending, adoption, err := w.pendingAdoption(control.NewIntake(w.s).Checking(w.starts))
	if err != nil {
		return authority.Approval{}, ledger.Repository{}, err
	}
	if adoption == nil || adoption.Consolidation == nil || adoption.Consolidation.Removes == nil {
		return authority.Approval{}, ledger.Repository{}, Violatedf("the consolidation's request lists no removal: %+v", adoption)
	}
	return pending, design, nil
}

func checkApprovingAConsolidationRemovesTheDesignRepository(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	pending, design, err := w.consolidationPending()
	if err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return Violatedf("approving the consolidation: %v", err)
	}
	// The record stays as history, removed, with its name, as a repository a member removes does.
	kept, err := r.theRepository(s, designRoot)
	if err != nil || !kept.Removed() || kept.Name != design.Name || kept.Planning {
		return Violatedf("after approval the design repository's record reads %+v: %v", kept, err)
	}
	events, err := r.repositoryEvents(s, designRoot)
	if err != nil {
		return err
	}
	var removed []ledger.Event
	for _, event := range events {
		if event.Type == ledger.EventRepositoryRemoved {
			removed = append(removed, event)
		}
	}
	if len(removed) != 1 || removed[0].Actor != theMember {
		return Violatedf("the design repository's removals are %+v", removed)
	}
	// The approval and the removal are one transaction: the event that records the approval and the removal's are
	// in the ledger together, and the planning repository is untouched.
	if planned, err := r.theRepository(s, startRepository); err != nil || planned.Removed() {
		return Violatedf("the planning repository reads %+v: %v", planned, err)
	}
	return r.CheckProjection(s)
}

func checkAConsolidationWithOpenWorkInTheDesignRepositoryWaits(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	pending, _, err := w.consolidationPending()
	if err != nil {
		return err
	}
	open, err := w.order(designRoot, "")
	if err != nil {
		return err
	}
	_, err = r.approve(s, pending, true, theMember)
	if !errors.Is(err, ledger.ErrRepositoryState) || !strings.Contains(err.Error(), open) {
		return Violatedf("approving a consolidation while work order %s targets the design repository: %v", open, err)
	}
	// Nothing of the approval happened: the request still waits, and the design repository is still held.
	row, err := r.approvalRow(s, pending.ID)
	if err != nil || row.State != authority.ApprovalPending {
		return Violatedf("the refused approval reads %+v: %v", row, err)
	}
	if held, err := r.theRepository(s, designRoot); err != nil || held.Removed() {
		return Violatedf("the design repository reads %+v: %v", held, err)
	}
	if order, err := r.WorkOrder(s, pending.WorkOrder); err != nil || order.State != workorders.Acceptance {
		return Violatedf("the adoption's work reads %+v: %v", order, err)
	}
	return nil
}

// approvalRow reads one approval as the ledger holds it.
func (r *Rig) approvalRow(s ports.Store, id string) (authority.Approval, error) {
	var row authority.Approval
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Approvals().Approval(id)
		return err
	})
	return row, err
}

// newConsolidationMergeRig is a plan merge rig whose planning work adopts the project, with the restructuring
// given, in a repository with a review target, a merge authority, and a merge method set before its lane ends.
func newConsolidationMergeRig(r *Rig, target ledger.ReviewTarget, level ledger.MergeAuthority, method ledger.MergeMethod, restructure *planning.Restructure) (*planMergeRig, error) {
	f, err := newForgeRigWith(r, target, func(store ports.Store, repository string) (string, error) {
		if _, err := r.change(store, repository, control.RepositoryChange{MergeAuthority: &level, MergeMethod: &method}, r.Actor); err != nil {
			return "", err
		}
		ops := r.NewOps(store)
		order, err := ops.Record(r.Ctx, r.Scope, control.LedgerWork{
			Order: workorders.WorkOrder{
				ID: r.ID("plan"), Kind: workorders.KindPlan, Title: "Plan the retry budget", RepositoryID: repository, MaxAttempts: 2, Adopt: true,
				Restructure: restructure,
			},
			Statement: "Write the plan for a retry budget.",
			Criteria:  []planning.Criterion{{ID: "c1", Text: "The plan is on the branch"}},
		}, r.Actor)
		if err != nil {
			return "", err
		}
		_, err = ops.Pass(r.Ctx, r.Scope, order.ID, r.Actor)
		return order.ID, err
	}, LaneCheck("c1", true))
	if err != nil {
		return nil, err
	}
	return &planMergeRig{forgeRig: f, files: laneFiles("The first draft.")}, nil
}

// mergedWith runs an adoption's plan, in a repository at review_approves that merges with method, through its
// approval to the merge the worker is asked for.
func mergedWith(r *Rig, method ledger.MergeMethod, restructure *planning.Restructure) (ledger.DesiredForge, error) {
	p, err := newConsolidationMergeRig(r, ledger.ReviewOnBranch, ledger.ReviewApproves, method, restructure)
	if err != nil {
		return ledger.DesiredForge{}, err
	}
	if _, err := p.approved(); err != nil {
		return ledger.DesiredForge{}, err
	}
	if _, err := p.tick(); err != nil {
		return ledger.DesiredForge{}, err
	}
	merge, err := p.oneMerge()
	if err == nil && merge.Commit != p.head {
		return merge, Violatedf("the worker is asked to merge %+v, want commit %s", merge, p.head)
	}
	return merge, err
}

func checkAConsolidationMergesWithAMergeCommit(r *Rig) error {
	merge, err := mergedWith(r, ledger.MergeSquash, consolidating())
	if err != nil {
		return err
	}
	if merge.Method != ledger.MergeCommit {
		return Violatedf("in a repository that merges with squash, a consolidation is merged with %s", merge.Method)
	}
	return nil
}

func checkAnAdoptionThatImportsNoHistoryMergesAsTheRepositorySays(r *Rig) error {
	merge, err := mergedWith(r, ledger.MergeSquash, nil)
	if err != nil {
		return err
	}
	if merge.Method != ledger.MergeSquash {
		return Violatedf("in a repository that merges with squash, an adoption that imports no history is merged with %s", merge.Method)
	}
	return nil
}

func checkAConsolidationsPullRequestSaysHowToMergeIt(r *Rig) error {
	for _, tt := range []struct {
		level       ledger.MergeAuthority
		restructure *planning.Restructure
		says        []string
	}{
		{ledger.PersonMerges, consolidating(), []string{planning.MergeCommitKeepsHistory, "merge it with a merge commit"}},
		{ledger.PersonApproves, consolidating(), []string{planning.MergeCommitKeepsHistory, "ropeshift merges it with a merge commit, whatever the repository's merge method"}},
		{ledger.PersonMerges, nil, nil},
	} {
		p, err := newConsolidationMergeRig(r, ledger.ReviewOnPullRequest, tt.level, ledger.MergeSquash, tt.restructure)
		if err != nil {
			return err
		}
		publish, err := p.publish()
		if err != nil {
			return err
		}
		if publish.Kind != ledger.ForgePublish || !strings.Contains(publish.Body, "plan approval") {
			return Violatedf("the adoption's lane end asks %+v", publish)
		}
		for _, want := range tt.says {
			if !strings.Contains(publish.Body, want) {
				return Violatedf("at %s the pull request description lacks %q:\n%s", tt.level, want, publish.Body)
			}
		}
		if tt.says == nil && strings.Contains(publish.Body, "design history") {
			return Violatedf("an adoption that imports no history describes its pull request as\n%s", publish.Body)
		}
	}
	return nil
}

// consolidationChecks reads the checks of a merged consolidation's base, oldest first.
func (r *Rig) consolidationChecks(s ports.Store, workOrder string) ([]ledger.Event, error) {
	var out []ledger.Event
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectConsolidation, ID: workOrder})
		out = events
		return err
	})
	return out, err
}

// historyGaps reads the merged consolidations whose base lacks the design history, as Needs you lists them.
func (r *Rig) historyGaps(s ports.Store) ([]control.HistoryGap, error) {
	var gaps []control.HistoryGap
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		gaps, err = control.HistoryGaps(tx)
		return err
	})
	return gaps, err
}

func checkASquashMergeOutsideRopeshiftRaisesTheNotice(r *Rig) error {
	p, err := newConsolidationMergeRig(r, ledger.ReviewOnPullRequest, ledger.PersonMerges, ledger.MergeCommit, consolidating())
	if err != nil {
		return err
	}
	// The repository's history on the machine: the adoption branch holds the merge that imported the design tip.
	const path = "/work/ws"
	git := fake.NewWorktrees()
	base := git.Grow(path, "main")
	git.Hold(base, "README.md")
	tip := git.Add()
	git.Hold(tip, "README.md", "docs/vision.md")
	merge := git.Add(base, tip)
	git.Name(merge, planning.ImportCommitSubject("design"))
	git.At(p.head, merge)
	starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return path, nil }}
	intake := control.NewIntake(p.store).Checking(starts)
	var asked []string
	tick := func() error {
		_, err := intake.TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(p.files), &asked))
		return err
	}
	if err := p.report(p.opened(1, p.head)); err != nil {
		return err
	}
	if err := tick(); err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(p.store)
	if err != nil {
		return err
	}
	if _, err := r.approve(p.store, pending, true, theMember); err != nil {
		return err
	}
	if err := tick(); err != nil {
		return err
	}
	// Nothing has merged, so nothing is checked.
	if err := intake.CheckConsolidations(r.Ctx, r.Scope); err != nil {
		return err
	}
	if checks, err := r.consolidationChecks(p.store, p.grant.WorkOrder); err != nil || len(checks) != 0 {
		return Violatedf("a consolidation that has not merged was checked: %+v, %v", checks, err)
	}
	// The person squash-merges the pull request on the forge: the base gains one commit that holds the files and
	// not the design history.
	squash := git.Grow(path, "main")
	merged := p.pullRequestAt(p.head)
	merged.State, merged.MergeCommit, merged.MergedAt = ledger.PullRequestMerged, squash, "2026-11-18T10:00:00Z"
	if err := p.read(merged); err != nil {
		return err
	}
	if err := p.stands(workorders.Done); err != nil {
		return err
	}
	if err := intake.CheckConsolidations(r.Ctx, r.Scope); err != nil {
		return err
	}
	gaps, err := r.historyGaps(p.store)
	if err != nil {
		return err
	}
	want := ledger.ConsolidationCheck{Branch: branchName, Base: "main", BaseCommit: squash, DesignTip: tip}
	if len(gaps) != 1 || gaps[0].WorkOrder.ID != p.grant.WorkOrder || gaps[0].Check != want || gaps[0].Since == 0 {
		return Violatedf("after a squash merge the gaps are %+v, want one for %s with %+v", gaps, p.grant.WorkOrder, want)
	}
	// A tick before the next check is due reads no base.
	if err := intake.CheckConsolidations(r.Ctx, r.Scope); err != nil {
		return err
	}
	if checks, err := r.consolidationChecks(p.store, p.grant.WorkOrder); err != nil || len(checks) != 1 {
		return Violatedf("the base was checked again before it was due: %+v, %v", checks, err)
	}
	if r.DB.ShiftClock == nil {
		return nil
	}
	// The person merges the design tip into the base with a merge commit, and the next check clears the gap.
	fixed := git.Add(squash, tip)
	git.Point(path, "main", fixed)
	r.DB.ShiftClock(milliseconds(11 * time.Minute))
	if err := intake.CheckConsolidations(r.Ctx, r.Scope); err != nil {
		return err
	}
	if gaps, err := r.historyGaps(p.store); err != nil || len(gaps) != 0 {
		return Violatedf("after the base gained the design tip the gaps are %+v: %v", gaps, err)
	}
	checks, err := r.consolidationChecks(p.store, p.grant.WorkOrder)
	if err != nil || len(checks) != 2 {
		return Violatedf("the checks are %+v: %v", checks, err)
	}
	r.DB.ShiftClock(milliseconds(48 * time.Hour))
	if err := intake.CheckConsolidations(r.Ctx, r.Scope); err != nil {
		return err
	}
	if again, err := r.consolidationChecks(p.store, p.grant.WorkOrder); err != nil || len(again) != 2 {
		return Violatedf("a base found to hold the design tip was checked again: %+v, %v", again, err)
	}
	return nil
}

func checkRestructuringIsRefusedOnceAPlanIsApproved(r *Rig) error {
	s := r.Store()
	restructure := func(id string) error {
		_, err := r.NewOps(s).Record(r.Ctx, r.Scope, control.LedgerWork{
			Order: workorders.WorkOrder{
				ID: r.ID(id), Kind: workorders.KindPlan, Title: "Adopt the project", Adopt: true, Restructure: consolidating(), MaxAttempts: 1,
			},
			Criteria: criteriaOf(criterionOne),
		}, r.member())
		return err
	}
	_, pending, err := r.aLanePlanAsksForApproval(s, laneFiles("The first draft."))
	if err != nil {
		return err
	}
	if err := restructure("before"); err != nil {
		return Violatedf("a restructuring with a plan that only waits was refused: %v", err)
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return err
	}
	if err := restructure("after"); !errors.Is(err, workorders.ErrAlreadyPlanned) {
		return Violatedf("a restructuring of a project with an approved plan: %v", err)
	}
	return nil
}

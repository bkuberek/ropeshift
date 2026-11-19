package contract

import (
	"context"
	"encoding/json"
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
	w.git.At(designRoot)
	tip = w.git.Add(designRoot)
	w.git.Hold(tip, ".gitignore", "README.md", "docs/vision.md", "poc/go.mod", "poc/main.go")
	merge = w.git.Add(base, tip)
	w.git.Name(merge, planning.ImportCommitSubject("design"))
	w.git.Point(path, "lane/adopt", merge)
	head = w.git.Touch(path, "lane/adopt", "plan.md")
	return merge, tip, head
}

// consolidationAsked runs a consolidating adoption's lane to the end of its branch, with the merge its worker
// reported importing the design repository's history, and reads what its request says of the consolidation.
func (w *startWorld) consolidationAsked(head, imported string, kept ports.Checkouts) (*planning.Consolidation, error) {
	id, err := w.adoptOrder(startRepository, consolidating())
	if err != nil {
		return nil, err
	}
	if err := w.adoptLaneEndsImporting(id, head, imported); err != nil {
		return nil, err
	}
	_, adoption, err := w.pendingAdoption(control.NewIntake(w.s).Checking(w.starts).Placing(kept))
	if err != nil {
		return nil, err
	}
	if adoption == nil {
		return nil, Violatedf("the consolidating adoption's request groups nothing")
	}
	return adoption.Consolidation, nil
}

// heldDesign registers the old design repository in the project, under the ID a consolidation names it by, and
// keeps its checkout at the clone the plan read it from.
func (w *startWorld) heldDesign(kept *fake.Checkouts) (ledger.Repository, error) {
	record, err := w.r.repositories(w.s).Detect(w.r.Ctx, w.r.Scope, control.DetectedRepository{ID: designRoot, DefaultBranch: "main", Directory: "design"},
		ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if err != nil {
		return record, err
	}
	return record, kept.Keep(w.r.Ctx, w.r.Scope, designRoot, consolidating().DesignPath)
}

func checkAConsolidationListsWhatItImported(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	kept := fake.NewCheckouts()
	design, err := w.heldDesign(kept)
	if err != nil {
		return err
	}
	merge, tip, head := w.consolidatedBranch()
	consolidation, err := w.consolidationAsked(head, merge, kept)
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
	merge, _, head := w.consolidatedBranch()
	consolidation, err := w.consolidationAsked(head, merge, fake.NewCheckouts())
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Removes != nil || consolidation.RemovalUnread != "" || consolidation.PersonMerges || len(consolidation.Collisions) != 2 {
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
	kept := fake.NewCheckouts()
	if _, err := w.heldDesign(kept); err != nil {
		return err
	}
	// The branch holds no merge that imports a history.
	path := w.path(startRepository)
	base := w.git.Touch(path, "main", "README.md")
	w.git.Point(path, "lane/adopt", base)
	head := w.git.Touch(path, "lane/adopt", "plan.md")
	consolidation, err := w.consolidationAsked(head, "", kept)
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
	kept := fake.NewCheckouts()
	design, err := w.heldDesign(kept)
	if err != nil {
		return authority.Approval{}, ledger.Repository{}, err
	}
	merge, _, head := w.consolidatedBranch()
	id, err := w.adoptOrder(startRepository, consolidating())
	if err != nil {
		return authority.Approval{}, ledger.Repository{}, err
	}
	if err := w.adoptLaneEndsImporting(id, head, merge); err != nil {
		return authority.Approval{}, ledger.Repository{}, err
	}
	pending, adoption, err := w.pendingAdoption(control.NewIntake(w.s).Checking(w.starts).Placing(kept))
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
// The lane's worker reports importMerge as the merge commit that imported the design repository's history.
func newConsolidationMergeRig(r *Rig, target ledger.ReviewTarget, level ledger.MergeAuthority, method ledger.MergeMethod, restructure *planning.Restructure) (*planMergeRig, error) {
	f, err := newImportingForgeRig(r, target, func(store ports.Store, repository string) (string, error) {
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

// importMerge is the merge commit a consolidation rig's worker reports having made to import the design repository's
// history.
const importMerge = "import-merge"

// newImportingForgeRig is newForgeRigWith for a lane whose end reports importMerge.
func newImportingForgeRig(r *Rig, target ledger.ReviewTarget, ready func(store ports.Store, repository string) (string, error), checks ...workorders.CheckResult) (*forgeRig, error) {
	s := r.Store()
	repository := r.ID("repo")
	if _, err := r.detect(s, repository, forgeRemote, "main"); err != nil {
		return nil, err
	}
	if _, err := r.change(s, repository, control.RepositoryChange{ReviewTarget: &target}, r.Actor); err != nil {
		return nil, err
	}
	st, err := newStatusRigOn(r, s, func(store ports.Store) (string, error) { return ready(store, repository) })
	if err != nil {
		return nil, err
	}
	f := &forgeRig{statusRig: st, repository: repository, head: r.ID("commit")}
	result := LaneFinished(f.head, checks...)
	result.ImportMerge = importMerge
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	reply, err := f.upload(ledger.UploadLaneEnded, raw)
	if err != nil || len(reply.Rejected) != 0 {
		return nil, Violatedf("the lane's end was refused: %+v, %v", reply.Rejected, err)
	}
	return f, nil
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
	git.At(designRoot)
	tip := git.Add(designRoot)
	git.Hold(tip, "README.md", "docs/vision.md")
	git.At(importMerge, base, tip)
	git.Name(importMerge, planning.ImportCommitSubject("design"))
	git.At(p.head, importMerge)
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

// starterRemote and designRemote are the remotes of two repositories cut from one starter, which share their first
// commit and are two repositories, because their remotes differ.
const (
	starterRemote = "git@example.invalid:acme/starter.git"
	designRemote  = "git@example.invalid:acme/design.git"
)

// relativeRemote is a remote that names a path beside a clone, which two unrelated clones can both have.
const relativeRemote = "../design.git"

// sharedFirstCommit registers a repository the project holds under the design repository's first commit, kept by this
// worker at a path of its own with a remote of its own, and places the design clone the plan read beside it with its
// remote.
func (w *startWorld) sharedFirstCommit(keptRemote, designCloneRemote string) (*fake.Checkouts, ledger.Repository, error) {
	kept := fake.NewCheckouts()
	record, err := w.r.repositories(w.s).Detect(w.r.Ctx, w.r.Scope, control.DetectedRepository{ID: designRoot, Remote: keptRemote, DefaultBranch: "main", Directory: "starter"},
		ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if err != nil {
		return nil, record, err
	}
	if err := kept.Keep(w.r.Ctx, w.r.Scope, designRoot, "/work/starter"); err != nil {
		return nil, record, err
	}
	kept.Place(ports.Clone{Path: "/work/starter", ID: designRoot, Remote: keptRemote, DefaultBranch: "main"})
	kept.Place(ports.Clone{Path: consolidating().DesignPath, ID: designRoot, Remote: designCloneRemote, DefaultBranch: "main"})
	return kept, record, nil
}

func checkARepositoryThatSharesTheDesignFirstCommitIsNotRemoved(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	kept, starter, err := w.sharedFirstCommit(starterRemote, designRemote)
	if err != nil {
		return err
	}
	merge, _, head := w.consolidatedBranch()
	id, err := w.adoptOrder(startRepository, consolidating())
	if err != nil {
		return err
	}
	if err := w.adoptLaneEndsImporting(id, head, merge); err != nil {
		return err
	}
	pending, adoption, err := w.pendingAdoption(control.NewIntake(s).Checking(w.starts).Placing(kept))
	if err != nil {
		return err
	}
	if adoption == nil || adoption.Consolidation == nil {
		return Violatedf("the consolidation's request groups nothing: %+v", adoption)
	}
	if c := adoption.Consolidation; c.Removes != nil || !strings.Contains(c.RemovalUnread, starter.Name) || !strings.Contains(c.RemovalUnread, "remote") {
		return Violatedf("a repository that shares the design repository's first commit is listed as %+v", c)
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return Violatedf("approving the consolidation: %v", err)
	}
	if held, err := r.theRepository(s, designRoot); err != nil || held.Removed() {
		return Violatedf("approving removed the repository that shares the first commit: %+v, %v", held, err)
	}
	return nil
}

func checkTheDesignRepositoryKeptElsewhereWithItsRemoteIsRemoved(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	// The worker keeps the record at another clone, whose remote is the design clone's.
	kept, design, err := w.sharedFirstCommit(designRemote, designRemote)
	if err != nil {
		return err
	}
	merge, _, head := w.consolidatedBranch()
	consolidation, err := w.consolidationAsked(head, merge, kept)
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Removes == nil || consolidation.Removes.ID != design.ID || consolidation.RemovalUnread != "" {
		return Violatedf("the design repository with the design clone's remote is listed as %+v", consolidation)
	}
	return nil
}

func checkALaneCannotForgeTheImport(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	merge, tip, head := w.consolidatedBranch()
	// The lane makes a merge of its own with the import's subject, of a history that is not the design repository's.
	path := w.path(startRepository)
	forgedTip := w.git.Add()
	w.git.Hold(forgedTip, "README.md")
	forged := w.git.Add(head, forgedTip)
	w.git.Name(forged, planning.ImportCommitSubject("forged"))
	w.git.Point(path, "lane/adopt", forged)
	after := w.git.Touch(path, "lane/adopt", "plan.md")
	consolidation, err := w.consolidationAsked(after, merge, fake.NewCheckouts())
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Merge != merge || consolidation.DesignTip != tip || consolidation.Design != "design" || consolidation.Unread != "" {
		return Violatedf("with a lane's merge that mimics the import, the consolidation reads %+v, want merge %s and tip %s", consolidation, merge, tip)
	}
	return nil
}

func checkAnImportOfAnotherHistoryIsUnread(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	// The only merge with the import's subject brings a history whose first commit is not the design repository's.
	path := w.path(startRepository)
	base := w.git.Touch(path, "main", "README.md")
	other := w.git.Add()
	merge := w.git.Add(base, other)
	w.git.Name(merge, planning.ImportCommitSubject("design"))
	w.git.Point(path, "lane/adopt", merge)
	head := w.git.Touch(path, "lane/adopt", "plan.md")
	consolidation, err := w.consolidationAsked(head, merge, fake.NewCheckouts())
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Unread == "" || !strings.Contains(consolidation.Unread, "first commit") || consolidation.DesignTip != "" || consolidation.Merge != "" {
		return Violatedf("an import of another history reads %+v", consolidation)
	}
	return nil
}

func checkADesignRepositoryRemovedBeforeApprovalIsLeftAsItIs(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	pending, _, err := w.consolidationPending()
	if err != nil {
		return err
	}
	if _, err := r.repositories(s).Remove(r.Ctx, r.Scope, designRoot, nil, theMember); err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return Violatedf("approving a consolidation whose design repository was removed before: %v", err)
	}
	events, err := r.repositoryEvents(s, designRoot)
	if err != nil {
		return err
	}
	removals := 0
	for _, event := range events {
		if event.Type == ledger.EventRepositoryRemoved {
			removals++
		}
	}
	if held, err := r.theRepository(s, designRoot); err != nil || !held.Removed() || removals != 1 {
		return Violatedf("the design repository reads %+v after %d removals: %v", held, removals, err)
	}
	return nil
}

func checkTheWorkspacePlansWhenTheRemovedDesignRepositoryDid(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	pending, _, err := w.consolidationPending()
	if err != nil {
		return err
	}
	on := true
	if _, err := r.change(s, designRoot, control.RepositoryChange{Planning: &on}, r.Actor); err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return Violatedf("approving the consolidation: %v", err)
	}
	workspace, err := r.theRepository(s, startRepository)
	if err != nil || !workspace.Planning {
		return Violatedf("after the planning design repository was removed, the workspace reads %+v: %v", workspace, err)
	}
	if design, err := r.theRepository(s, designRoot); err != nil || !design.Removed() || design.Planning {
		return Violatedf("the design repository reads %+v: %v", design, err)
	}
	return nil
}

func checkAMergeCommitTheBaseRefusesSaysWhy(r *Rig) error {
	p, err := newConsolidationMergeRig(r, ledger.ReviewOnBranch, ledger.ReviewApproves, ledger.MergeSquash, consolidating())
	if err != nil {
		return err
	}
	if _, err := p.approved(); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	merge, err := p.oneMerge()
	if err != nil {
		return err
	}
	refusal := "remote: error: GH006: Protected branch update failed for refs/heads/main. remote: error: This branch must not contain merge commits."
	if err := p.mergeCame(merge, ledger.MergeResult{Outcome: ledger.MergeRefused, Detail: refusal}); err != nil {
		return err
	}
	held, err := p.openMergeRequest(authority.MergeHeld)
	if err != nil {
		return err
	}
	for _, want := range []string{refusal, "linear history", "merge commit"} {
		if !strings.Contains(held.Summary, want) {
			return Violatedf("the held merge says %q, which lacks %q", held.Summary, want)
		}
	}
	return nil
}

func checkAMergedConsolidationWithNoDesignTipIsTold(r *Rig) error {
	p, err := newConsolidationMergeRig(r, ledger.ReviewOnPullRequest, ledger.PersonMerges, ledger.MergeCommit, consolidating())
	if err != nil {
		return err
	}
	if err := p.report(p.opened(1, p.head)); err != nil {
		return err
	}
	// The plan approval reads no branch, so it names no design tip.
	if _, err := p.approved(); err != nil {
		return err
	}
	if _, err := p.tick(); err != nil {
		return err
	}
	merged := p.pullRequestAt(p.head)
	merged.State, merged.MergeCommit, merged.MergedAt = ledger.PullRequestMerged, "squashed", "2026-11-18T10:00:00Z"
	if err := p.read(merged); err != nil {
		return err
	}
	git := fake.NewWorktrees()
	git.Grow("/work/ws", "main")
	starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/work/ws", nil }}
	intake := control.NewIntake(p.store).Checking(starts)
	if err := intake.CheckConsolidations(r.Ctx, r.Scope); err != nil {
		return err
	}
	// One check that cannot read the base raises nothing.
	if gaps, err := r.historyGaps(p.store); err != nil || len(gaps) != 0 {
		return Violatedf("a merged consolidation whose base could not be checked once shows %+v: %v", gaps, err)
	}
	if r.DB.ShiftClock == nil {
		return nil
	}
	r.DB.ShiftClock(milliseconds(11 * time.Minute))
	if err := intake.CheckConsolidations(r.Ctx, r.Scope); err != nil {
		return err
	}
	gaps, err := r.historyGaps(p.store)
	if err != nil {
		return err
	}
	if len(gaps) != 1 || gaps[0].WorkOrder.ID != p.grant.WorkOrder || gaps[0].Check.Unread == "" || gaps[0].Check.Missing() || gaps[0].Since == 0 {
		return Violatedf("a merged consolidation whose base could not be checked shows %+v", gaps)
	}
	return nil
}

func checkTheCollisionsAreCutShort(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	path := w.path(startRepository)
	shared := make([]string, 0, planning.MaxCollisions+5)
	for i := range planning.MaxCollisions + 5 {
		shared = append(shared, "docs/page-"+strings.Repeat("0", 4-len(itoa(i)))+itoa(i)+".md")
	}
	base := w.git.Touch(path, "main", "README.md")
	w.git.Hold(base, shared...)
	w.git.At(designRoot)
	tip := w.git.Add(designRoot)
	w.git.Hold(tip, shared...)
	merge := w.git.Add(base, tip)
	w.git.Name(merge, planning.ImportCommitSubject("design"))
	w.git.Point(path, "lane/adopt", merge)
	head := w.git.Touch(path, "lane/adopt", "plan.md")
	consolidation, err := w.consolidationAsked(head, merge, fake.NewCheckouts())
	if err != nil {
		return err
	}
	if consolidation == nil || len(consolidation.Collisions) != planning.MaxCollisions || !consolidation.CollisionsTruncated {
		return Violatedf("a consolidation of %d colliding paths lists %d, cut short %v", len(shared), len(consolidation.Collisions), consolidation.CollisionsTruncated)
	}
	return nil
}

// itoa spells a small number.
func itoa(n int) string {
	digits := ""
	for {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
		if n == 0 {
			return digits
		}
	}
}

func checkARelativeRemoteConfirmsNoDesignRepository(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	// Two unrelated clones that share the design repository's first commit both have a remote that names a path
	// beside the clone, and the two spell it alike.
	kept, starter, err := w.sharedFirstCommit(relativeRemote, relativeRemote)
	if err != nil {
		return err
	}
	merge, _, head := w.consolidatedBranch()
	id, err := w.adoptOrder(startRepository, consolidating())
	if err != nil {
		return err
	}
	if err := w.adoptLaneEndsImporting(id, head, merge); err != nil {
		return err
	}
	pending, adoption, err := w.pendingAdoption(control.NewIntake(s).Checking(w.starts).Placing(kept))
	if err != nil {
		return err
	}
	if adoption == nil || adoption.Consolidation == nil {
		return Violatedf("the consolidation's request groups nothing: %+v", adoption)
	}
	if c := adoption.Consolidation; c.Removes != nil || !strings.Contains(c.RemovalUnread, starter.Name) {
		return Violatedf("a repository with the same relative remote as the design clone is listed as %+v", c)
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return Violatedf("approving the consolidation: %v", err)
	}
	if held, err := r.theRepository(s, designRoot); err != nil || held.Removed() {
		return Violatedf("approving removed a repository that only a relative remote matched: %+v, %v", held, err)
	}
	return nil
}

func checkAnUnreadBaseIsRaisedAfterTwoFailedChecksInARow(r *Rig) error {
	if r.DB.ShiftClock == nil {
		return nil
	}
	p, err := newConsolidationMergeRig(r, ledger.ReviewOnPullRequest, ledger.PersonMerges, ledger.MergeCommit, consolidating())
	if err != nil {
		return err
	}
	const path = "/work/ws"
	git := fake.NewWorktrees()
	base := git.Grow(path, "main")
	git.Hold(base, "README.md")
	git.At(designRoot)
	tip := git.Add(designRoot)
	git.Hold(tip, "README.md", "docs/vision.md")
	git.At(importMerge, base, tip)
	git.Name(importMerge, planning.ImportCommitSubject("design"))
	git.At(p.head, importMerge)
	down := false
	starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) {
		if down {
			return "", errors.New("the clone is not at its path")
		}
		return path, nil
	}}
	intake := control.NewIntake(p.store).Checking(starts)
	var asked []string
	if err := p.report(p.opened(1, p.head)); err != nil {
		return err
	}
	if _, err := intake.TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(p.files), &asked)); err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(p.store)
	if err != nil {
		return err
	}
	if _, err := r.approve(p.store, pending, true, theMember); err != nil {
		return err
	}
	squash := git.Grow(path, "main")
	merged := p.pullRequestAt(p.head)
	merged.State, merged.MergeCommit, merged.MergedAt = ledger.PullRequestMerged, squash, "2026-11-18T10:00:00Z"
	if err := p.read(merged); err != nil {
		return err
	}
	// check makes one check of the base, once the wait for it has passed, and reads the gap it leaves.
	check := func(unreadable bool) (control.HistoryGap, bool, error) {
		down = unreadable
		r.DB.ShiftClock(milliseconds(25 * time.Hour))
		if err := intake.CheckConsolidations(r.Ctx, r.Scope); err != nil {
			return control.HistoryGap{}, false, err
		}
		gaps, err := r.historyGaps(p.store)
		if err != nil || len(gaps) > 1 {
			return control.HistoryGap{}, false, Violatedf("the gaps are %+v: %v", gaps, err)
		}
		if len(gaps) == 0 {
			return control.HistoryGap{}, false, nil
		}
		return gaps[0], true, nil
	}
	if _, raised, err := check(true); err != nil || raised {
		return Violatedf("one failed check raised a gap: %v, %v", raised, err)
	}
	if gap, raised, err := check(true); err != nil || !raised || !gap.Unread() {
		return Violatedf("two failed checks in a row raise %+v, %v, %v", gap, raised, err)
	}
	// A check that reads the base ends the run of failures, and finds the history gone.
	if gap, raised, err := check(false); err != nil || !raised || gap.Unread() || gap.Check.BaseCommit != squash {
		return Violatedf("a check that reads the base found %+v, %v, %v", gap, raised, err)
	}
	// One failure after it is the first of a new run: the gap the read found stands.
	if gap, raised, err := check(true); err != nil || !raised || gap.Unread() {
		return Violatedf("a failed check after a read replaced the gap it left with %+v, %v, %v", gap, raised, err)
	}
	if gap, raised, err := check(true); err != nil || !raised || !gap.Unread() {
		return Violatedf("a second failed check in a row raises %+v, %v, %v", gap, raised, err)
	}
	return nil
}

func checkAnImportTheWorkerDidNotRecordIsNotConfirmed(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	// The branch holds a merge with the import's subject over the design repository's history, and the worker
	// reported another commit as its import.
	_, _, head := w.consolidatedBranch()
	recorded := w.git.Add(designRoot)
	consolidation, err := w.consolidationAsked(head, recorded, fake.NewCheckouts())
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Merge != "" || consolidation.DesignTip != "" || !strings.Contains(consolidation.Unread, recorded) {
		return Violatedf("a branch that lacks the merge the worker recorded reads %+v", consolidation)
	}
	return nil
}

func checkAnAdoptionWhoseWorkerRecordedNoImportIsUnread(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	_, _, head := w.consolidatedBranch()
	consolidation, err := w.consolidationAsked(head, "", fake.NewCheckouts())
	if err != nil {
		return err
	}
	if consolidation == nil || consolidation.Merge != "" || consolidation.DesignTip != "" || !strings.Contains(consolidation.Unread, "recorded no") {
		return Violatedf("an adoption whose worker recorded no import reads %+v", consolidation)
	}
	return nil
}

func checkALanesEndKeepsTheImportItReports(r *Rig) error {
	s := r.Store()
	w := r.startWorld(s)
	if err := w.repository(func(*control.RepositoryChange) {}); err != nil {
		return err
	}
	merge, _, head := w.consolidatedBranch()
	id, err := w.adoptOrder(startRepository, consolidating())
	if err != nil {
		return err
	}
	if err := w.adoptLaneEndsImporting(id, head, merge); err != nil {
		return err
	}
	var artifacts []ledger.Artifact
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		artifacts, err = tx.Evidence().Artifacts(id)
		return err
	})
	if err != nil {
		return err
	}
	var imports, branches int
	for _, artifact := range artifacts {
		switch {
		case artifact.Kind == ledger.ArtifactImport && artifact.Digest == merge:
			imports++
		case artifact.Kind == ledger.ArtifactBranch && artifact.Digest == head:
			branches++
		}
	}
	if imports != 1 || branches != 1 {
		return Violatedf("a lane's end that reports the import %s keeps the artifacts %+v", merge, artifacts)
	}
	return nil
}

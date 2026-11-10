package contract

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// VerdictAcceptanceChecks hold what the control plane does with a review's verdicts (ADR-0019, FR-REVIEW-4,
// FR-REVIEW-6, FR-REVIEW-7, FR-REVIEW-8): they are kept in the transaction that ends the review, and they
// settle the work at the repository's merge authority.
var VerdictAcceptanceChecks = []Check{
	{"VA1", "a completed review keeps a verdict for each criterion in the transaction that ends it", checkReviewKeepsItsVerdicts},
	{"VA2", "a review that leaves a criterion out, or judges one the contract lacks, is refused and changes nothing", checkIncompleteReviewIsRefused},
	{"VA3", "a lane that is not a review cannot report verdicts", checkOnlyAReviewReportsVerdicts},
	{"VA4", "a failed criterion sends the work back by rule, citing the review, and the reasons reach the next attempt", checkFailSendsWorkBack},
	{"VA5", "work whose every criterion passed is accepted by the review at review_approves and waits for a member below it", checkPassAcceptsAtReviewApproves},
	{"VA6", "a criterion the review could not judge waits for a member unless a passing forge check covers it", checkCannotJudgeWaits},
	{"VA7", "past the bound on review rounds a failed review waits for a member with its verdicts", checkReviewRoundsAreBounded},
	{"VA8", "a verdict on another attempt or another commit decides nothing", checkStaleVerdictsDecideNothing},
	{"VA9", "a member who decides while a review is open cancels it", checkMemberDecisionCancelsTheReview},
	{"VA10", "the tick sends passed work back when a forge check fails after the review", checkTickReworksAfterTheReview},
	{"VA11", "a review that fails or is canceled leaves the work to its evidence", checkReviewWithoutVerdictsLeavesTheEvidence},
	{"VA12", "a review is not a completed work order in the measures", checkReviewIsNotCompletedWork},
}

func say(criterion string, kind workorders.VerdictKind) workorders.CriterionVerdict {
	return workorders.CriterionVerdict{
		Criterion: criterion, Verdict: kind, Reasons: "because " + criterion + " is " + string(kind), Citations: []string{"main.go:12", "FR-1"},
	}
}

// openReviewOf is the review of a work order that has not ended.
func (r *Rig) openReviewOf(s ports.Store, id string) (workorders.WorkOrder, error) {
	reviews, err := r.reviewsOf(s, id)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	for _, review := range slices.Backward(reviews) {
		if review.OpenReview() {
			return review, nil
		}
	}
	return workorders.WorkOrder{}, Violatedf("%s has no open review among %d", id, len(reviews))
}

// reviewEnds runs the open review of a work order on a lane and ends it with a report.
func (r *Rig) reviewEnds(s ports.Store, id string, result workorders.LaneResult) error {
	review, err := r.openReviewOf(s, id)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, review.ID, "a")
	if err != nil {
		return err
	}
	return r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a"))
}

// reviewSays ends the open review of a work order with its verdicts.
func (r *Rig) reviewSays(s ports.Store, id string, said ...workorders.CriterionVerdict) error {
	result := laneResult(workorders.OutcomeCompleted, "")
	result.Verdicts = said
	return r.reviewEnds(s, id, result)
}

// atLevel puts the repository acceptance speaks of at a merge authority.
func (r *Rig) atLevel(s ports.Store, level ledger.MergeAuthority) error {
	if _, err := r.detect(s, AcceptanceRepository, "", "main"); err != nil {
		return err
	}
	_, err := r.change(s, AcceptanceRepository, control.RepositoryChange{MergeAuthority: &level}, r.Actor)
	return err
}

// judged is a work order whose lane completed at a commit and whose review waits for a lane.
func (r *Rig) judged(s ports.Store, criteria ...string) (id, head string, err error) {
	head = r.ID("commit")
	id, err = r.AwaitingReview(s, criteria, LaneFinished(head))
	return id, head, err
}

func (r *Rig) verdictsCounted(s ports.Store, id string) (workorders.AcceptanceView, error) {
	return r.NewOps(s).Standing(r.Ctx, r.Scope, id)
}

var reviewer = ledger.Actor{Kind: ledger.ActorSystem, ID: "review"}

func checkReviewKeepsItsVerdicts(r *Rig) error {
	s := r.Store()
	id, head, err := r.judged(s, criterionOne, criterionTwo)
	if err != nil {
		return err
	}
	review, err := r.openReviewOf(s, id)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, review.ID, "a")
	if err != nil {
		return err
	}
	var writes []string
	stepped := r.StoreWith(func(op, name string) {
		if name == StepBefore {
			writes = append(writes, op)
		}
	})
	result := laneResult(workorders.OutcomeCompleted, "")
	result.Verdicts = []workorders.CriterionVerdict{say(criterionTwo, workorders.VerdictPass), say(criterionOne, workorders.VerdictPass)}
	if err := r.NewOps(stepped).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a")); err != nil {
		return err
	}
	if !slices.Equal(writes, []string{"lane.end"}) {
		return Violatedf("ending the review took the transactions %v, and its verdicts are kept in the one that ends it", writes)
	}
	reviewed, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	got, err := r.verdictsOf(s, id)
	if err != nil {
		return err
	}
	if len(got) != 2 {
		return Violatedf("the work holds %d verdicts, want 2", len(got))
	}
	ended, err := r.WorkOrder(s, review.ID)
	if err != nil {
		return err
	}
	for _, v := range got {
		if v.Review != review.ID || v.WorkOrder != id || v.Attempt != reviewed.Fence || v.Commit != head ||
			v.Lane != holder.Lane || v.Generation != holder.Generation || v.Fence != ended.Fence || v.Verdict != workorders.VerdictPass {
			return Violatedf("the verdict for %s reads %+v, at attempt %d and commit %s", v.Criterion, v, reviewed.Fence, head)
		}
	}
	if err := r.stateIs(s, review.ID, workorders.Done); err != nil {
		return err
	}
	view, err := r.verdictsCounted(s, id)
	if err != nil {
		return err
	}
	if len(view.Verdicts) != 2 || view.Rounds != 1 || len(view.Reviews) != 1 || view.Reviews[0].ID != review.ID || view.Authority != ledger.PersonMerges {
		return Violatedf("the standing reads %+v", view)
	}
	// The verdicts follow the contract's order, whatever order the review wrote them in.
	if view.Verdicts[0].Criterion != criterionOne || view.Verdicts[1].Criterion != criterionTwo {
		return Violatedf("the standing lists %s then %s", view.Verdicts[0].Criterion, view.Verdicts[1].Criterion)
	}
	return r.CheckProjection(s)
}

func checkIncompleteReviewIsRefused(r *Rig) error {
	s := r.Store()
	id, _, err := r.judged(s, criterionOne, criterionTwo)
	if err != nil {
		return err
	}
	review, err := r.openReviewOf(s, id)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, review.ID, "a")
	if err != nil {
		return err
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	for name, said := range map[string][]workorders.CriterionVerdict{
		"a verdict left out":               {say(criterionOne, workorders.VerdictPass)},
		"no verdict at all":                nil,
		"a criterion the contract has not": {say(criterionOne, workorders.VerdictPass), say(criterionTwo, workorders.VerdictPass), say("c9", workorders.VerdictPass)},
		"a criterion twice":                {say(criterionOne, workorders.VerdictPass), say(criterionOne, workorders.VerdictFail), say(criterionTwo, workorders.VerdictPass)},
		"a verdict with no reasons":        {say(criterionOne, workorders.VerdictPass), {Criterion: criterionTwo, Verdict: workorders.VerdictFail}},
	} {
		result := laneResult(workorders.OutcomeCompleted, "")
		result.Verdicts = said
		if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a")); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("%s: %v", name, err)
		}
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused review moved the ledger from %d to %d: %v", before, after, err)
	}
	if err := r.stateIs(s, review.ID, workorders.Running); err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
		return err
	}
	if got, err := r.verdictsOf(s, id); err != nil || len(got) != 0 {
		return Violatedf("a refused review left %d verdicts: %v", len(got), err)
	}
	// A review that cannot judge reports blocked or failed, and says nothing about the criteria.
	blocked := laneResult(workorders.OutcomeBlocked, "needs an answer")
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, blocked, r.worker("a")); err != nil {
		return Violatedf("a blocked review was refused: %v", err)
	}
	return nil
}

func checkOnlyAReviewReportsVerdicts(r *Rig) error {
	s := r.Store()
	work, err := r.recordedWork(s, criterionOne)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, work.ID, "a")
	if err != nil {
		return err
	}
	result := LaneFinished(r.ID("commit"))
	result.Verdicts = []workorders.CriterionVerdict{say(criterionOne, workorders.VerdictPass)}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, result, r.worker("a")); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a change lane reporting its own verdict: %v", err)
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a refused end moved the ledger from %d to %d: %v", before, after, err)
	}
	return r.stateIs(s, work.ID, workorders.Running)
}

func checkFailSendsWorkBack(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	var id, reviewID string
	var reviewed workorders.WorkOrder
	rig, err := newStatusRigOn(r, s, func(ports.Store) (string, error) {
		var err error
		if id, _, err = r.judged(s, criterionOne, criterionTwo); err != nil {
			return "", err
		}
		review, err := r.openReviewOf(s, id)
		if err != nil {
			return "", err
		}
		reviewID = review.ID
		if reviewed, err = r.WorkOrder(s, id); err != nil {
			return "", err
		}
		if err := r.reviewSays(s, id, say(criterionOne, workorders.VerdictPass), say(criterionTwo, workorders.VerdictFail)); err != nil {
			return "", err
		}
		return id, nil
	})
	if err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Running); err != nil {
		return Violatedf("after a failed criterion the work is not back in the lanes' hands: %v", err)
	}
	count, event, err := r.eventsAbout(s, id, workorders.EventType(workorders.TriggerRework))
	if err != nil {
		return err
	}
	if count != 1 || event.Actor != reviewer {
		return Violatedf("%d rework events, the last by %+v, want one by %+v", count, event.Actor, reviewer)
	}
	var change workorders.Change
	if err := json.Unmarshal(event.Data, &change); err != nil {
		return err
	}
	if !strings.Contains(change.Reason, reviewID) || !strings.Contains(change.Reason, criterionTwo) || strings.Contains(change.Reason, criterionOne) {
		return Violatedf("the rework reason is %q, and it should cite review %s and name %s alone", change.Reason, reviewID, criterionTwo)
	}
	note, err := r.noteOf(s, id)
	if err != nil {
		return err
	}
	if note.Review != reviewID || note.Attempt != reviewed.Fence || note.Reason != change.Reason {
		return Violatedf("the note reads %+v for review %s at attempt %d", note, reviewID, reviewed.Fence)
	}
	// The next attempt is told what the review found, from the desired state the worker reads.
	state, err := rig.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: rig.auth})
	if err != nil {
		return err
	}
	if len(state.Leases) != 1 || state.Leases[0].Rework == nil {
		return Violatedf("desired state %+v", state.Leases)
	}
	got := state.Leases[0].Rework
	if got.Review != reviewID || len(got.Findings) != 1 {
		return Violatedf("the next lane is told %+v", *got)
	}
	finding := got.Findings[0]
	if finding.Criterion != criterionTwo || finding.Text != "It holds: c2" || finding.Reasons != say(criterionTwo, workorders.VerdictFail).Reasons ||
		!slices.Equal(finding.Citations, []string{"main.go:12", "FR-1"}) {
		return Violatedf("the finding reads %+v", finding)
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	return ops.Rebuild(r.Ctx, r.Scope, r.Actor)
}

// noteOf reads why a work order last went back.
func (r *Rig) noteOf(s ports.Store, id string) (workorders.ReworkNote, error) {
	var note workorders.ReworkNote
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		note, err = tx.Evidence().Reworked(id)
		return err
	})
	return note, err
}

func checkPassAcceptsAtReviewApproves(r *Rig) error {
	s := r.Store()
	for _, tt := range []struct {
		level ledger.MergeAuthority
		want  workorders.State
	}{
		{ledger.ReviewApproves, workorders.Done},
		{ledger.PersonApproves, workorders.Acceptance},
		{ledger.PersonMerges, workorders.Acceptance},
	} {
		if err := r.atLevel(s, tt.level); err != nil {
			return err
		}
		id, _, err := r.judged(s, criterionOne, criterionTwo)
		if err != nil {
			return err
		}
		review, err := r.openReviewOf(s, id)
		if err != nil {
			return err
		}
		if err := r.reviewSays(s, id, say(criterionOne, workorders.VerdictPass), say(criterionTwo, workorders.VerdictPass)); err != nil {
			return err
		}
		if err := r.stateIs(s, id, tt.want); err != nil {
			return Violatedf("at %s: %v", tt.level, err)
		}
		count, event, err := r.eventsAbout(s, id, workorders.EventType(workorders.TriggerAcceptancePassed))
		if err != nil {
			return err
		}
		if tt.want == workorders.Done && (count != 1 || event.Actor != reviewer) {
			return Violatedf("at %s %d acceptances, the last by %+v, want one by %+v", tt.level, count, event.Actor, reviewer)
		}
		if tt.want == workorders.Acceptance && count != 0 {
			return Violatedf("at %s the work was accepted without a member", tt.level)
		}
		if tt.want == workorders.Acceptance {
			// It waits with the verdicts a member reads, and a member's word still accepts it.
			view, err := r.verdictsCounted(s, id)
			if err != nil {
				return err
			}
			if len(view.Verdicts) != 2 || view.Authority != tt.level {
				return Violatedf("at %s the standing reads %+v", tt.level, view)
			}
			if view.Why == "" {
				return Violatedf("at %s the standing does not say what the work waits for", tt.level)
			}
			if state, err := r.NewOps(s).Accept(r.Ctx, r.Scope, id, r.Actor); err != nil || state != workorders.Done {
				return Violatedf("a member accepting reviewed work: %v, %v", state, err)
			}
			// Accepted work waits for nobody, so its standing says so.
			if view, err := r.verdictsCounted(s, id); err != nil || view.Why != "" {
				return Violatedf("accepted work still says it waits: %q: %v", view.Why, err)
			}
		}
		if err := r.stateIs(s, review.ID, workorders.Done); err != nil {
			return err
		}
	}
	return r.CheckProjection(s)
}

func checkCannotJudgeWaits(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	forge := &fake.Forge{}
	if err := r.atLevel(s, ledger.ReviewApproves); err != nil {
		return err
	}

	// A lane's own check does not cover what the review could not judge, so the work waits.
	head := r.ID("commit")
	laneOnly, err := r.AwaitingReview(s, []string{criterionOne, criterionTwo}, LaneFinished(head, LaneCheck(criterionTwo, true)))
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, laneOnly, say(criterionOne, workorders.VerdictPass), say(criterionTwo, workorders.VerdictCannotJudge)); err != nil {
		return err
	}
	if err := r.stateIs(s, laneOnly, workorders.Acceptance); err != nil {
		return err
	}
	if moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick()); err != nil || len(moved) != 0 {
		return Violatedf("a tick with no forge result moved %v: %v", moved, err)
	}

	// A passing forge check on that criterion covers it, and the tick accepts at review_approves.
	other := r.ID("commit")
	covered, err := r.AwaitingReview(s, []string{criterionOne, criterionTwo}, LaneFinished(other))
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, covered, say(criterionOne, workorders.VerdictPass), say(criterionTwo, workorders.VerdictCannotJudge)); err != nil {
		return err
	}
	if err := r.stateIs(s, covered, workorders.Acceptance); err != nil {
		return err
	}
	r.forgeSays(forge, other, forgeCheck(criterionTwo, workorders.CheckPassed))
	moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick())
	if err != nil || !slices.Equal(moved, []string{covered}) {
		return Violatedf("a tick with a covering forge result moved %v: %v", moved, err)
	}
	if err := r.stateIs(s, covered, workorders.Done); err != nil {
		return err
	}

	// Below review_approves the same cover still leaves the word to a member.
	if err := r.atLevel(s, ledger.PersonMerges); err != nil {
		return err
	}
	third := r.ID("commit")
	waiting, err := r.AwaitingReview(s, []string{criterionOne, criterionTwo}, LaneFinished(third))
	if err != nil {
		return err
	}
	r.forgeSays(forge, third, forgeCheck(criterionTwo, workorders.CheckPassed))
	if err := r.reviewSays(s, waiting, say(criterionOne, workorders.VerdictPass), say(criterionTwo, workorders.VerdictCannotJudge)); err != nil {
		return err
	}
	if moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick()); err != nil || slices.Contains(moved, waiting) {
		return Violatedf("a tick below review_approves moved %v: %v", moved, err)
	}
	return r.stateIs(s, waiting, workorders.Acceptance)
}

func checkReviewRoundsAreBounded(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	failing := func() error {
		result := laneResult(workorders.OutcomeCompleted, "")
		result.Verdicts = []workorders.CriterionVerdict{say(criterionOne, workorders.VerdictFail)}
		result.MaxReviewRounds = 2
		return r.reviewEnds(s, id, result)
	}
	if err := failing(); err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Ready); err != nil {
		return Violatedf("round 1 of 2: %v", err)
	}
	if err := r.attempt(s, id, LaneFinished(r.ID("commit"))); err != nil {
		return err
	}
	if err := failing(); err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
		return Violatedf("round 2 of 2 should wait for a member: %v", err)
	}
	view, err := r.verdictsCounted(s, id)
	if err != nil {
		return err
	}
	if view.Rounds != 2 || len(view.Verdicts) != 1 || view.Verdicts[0].Verdict != workorders.VerdictFail {
		return Violatedf("the standing reads %+v", view)
	}
	if moved, err := ops.Settle(r.Ctx, r.Scope, nil, r.tick()); err != nil || len(moved) != 0 {
		return Violatedf("a tick moved work that waits for a member: %v, %v", moved, err)
	}
	// The member who reads it can still send it back, and it goes back as before.
	if state, err := ops.Rework(r.Ctx, r.Scope, id, reworkReason, r.Actor); err != nil || state != workorders.Ready {
		return Violatedf("a member sending back bounded work: %v, %v", state, err)
	}
	return r.CheckProjection(s)
}

func checkStaleVerdictsDecideNothing(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	if err := r.atLevel(s, ledger.ReviewApproves); err != nil {
		return err
	}
	// A verdict on the commit another attempt left does not speak for this one, even where the new
	// attempt ends at the same commit.
	head := r.ID("commit")
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(head))
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, id, say(criterionOne, workorders.VerdictFail)); err != nil {
		return err
	}
	if err := r.attempt(s, id, LaneFinished(head)); err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
		return err
	}
	view, err := r.verdictsCounted(s, id)
	if err != nil {
		return err
	}
	if len(view.Verdicts) != 0 || view.Rounds != 1 || len(view.Reviews) != 2 {
		return Violatedf("a new attempt at the same commit is judged by %+v", view)
	}
	if moved, err := ops.Settle(r.Ctx, r.Scope, nil, r.tick()); err != nil || len(moved) != 0 {
		return Violatedf("a tick acted on an old verdict: %v, %v", moved, err)
	}

	// A verdict bound to a commit other than the attempt's head is on record and decides nothing.
	review, err := r.openReviewOf(s, id)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, review.ID, "a")
	if err != nil {
		return err
	}
	reviewed, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	elsewhere := workorders.Verdict{
		CriterionVerdict: say(criterionOne, workorders.VerdictFail), Review: review.ID, WorkOrder: id, Attempt: reviewed.Fence,
		Commit: "not-the-head", Lane: holder.Lane, Generation: holder.Generation, Fence: holder.Fence + 1,
	}
	if err := r.recordVerdicts(s, elsewhere); err != nil {
		return err
	}
	if view, err = r.verdictsCounted(s, id); err != nil || len(view.Verdicts) != 0 {
		return Violatedf("a verdict on another commit counts: %+v: %v", view.Verdicts, err)
	}
	return r.stateIs(s, id, workorders.Acceptance)
}

func checkMemberDecisionCancelsTheReview(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	for _, tt := range []struct {
		name    string
		decide  func(id string) (workorders.State, error)
		want    workorders.State
		claimed bool
	}{
		{"accepting", func(id string) (workorders.State, error) { return ops.Accept(r.Ctx, r.Scope, id, r.Actor) }, workorders.Done, false},
		{"sending back", func(id string) (workorders.State, error) {
			return ops.Rework(r.Ctx, r.Scope, id, reworkReason, r.Actor)
		}, workorders.Ready, false},
		{"accepting a review already running", func(id string) (workorders.State, error) { return ops.Accept(r.Ctx, r.Scope, id, r.Actor) }, workorders.Done, true},
	} {
		id, _, err := r.judged(s, criterionOne)
		if err != nil {
			return err
		}
		review, err := r.openReviewOf(s, id)
		if err != nil {
			return err
		}
		if tt.claimed {
			if _, err := r.Running(s, review.ID, "a"); err != nil {
				return err
			}
		}
		if state, err := tt.decide(id); err != nil || state != tt.want {
			return Violatedf("%s with the review open: %v, %v", tt.name, state, err)
		}
		if err := r.stateIs(s, review.ID, workorders.Canceled); err != nil {
			return Violatedf("%s: %v", tt.name, err)
		}
		if count, event, err := r.eventsAbout(s, review.ID, workorders.EventType(workorders.TriggerCanceled)); err != nil || count != 1 || event.Actor != r.Actor {
			return Violatedf("%s: %d cancellations of the review, the last by %+v: %v", tt.name, count, event.Actor, err)
		}
		// A repeated decision leaves the canceled review alone.
		if tt.want == workorders.Done {
			if state, err := ops.Accept(r.Ctx, r.Scope, id, r.Actor); err != nil || state != workorders.Done {
				return Violatedf("%s again: %v, %v", tt.name, state, err)
			}
		}
	}
	// A review that already ended has nothing to cancel.
	id, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, id, say(criterionOne, workorders.VerdictPass)); err != nil {
		return err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, id, r.Actor); err != nil {
		return err
	}
	if err := r.stateIs(s, review.ID, workorders.Done); err != nil {
		return err
	}
	return r.CheckProjection(s)
}

func checkTickReworksAfterTheReview(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	forge := &fake.Forge{}
	head := r.ID("commit")
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(head))
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, id, say(criterionOne, workorders.VerdictPass)); err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
		return err
	}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckFailed))
	moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick())
	if err != nil || !slices.Equal(moved, []string{id}) {
		return Violatedf("a tick with a failed forge result moved %v: %v", moved, err)
	}
	if err := r.stateIs(s, id, workorders.Ready); err != nil {
		return err
	}
	note, err := r.noteOf(s, id)
	if err != nil {
		return err
	}
	if !strings.Contains(note.Reason, "ci-"+criterionOne) || note.Review != "" {
		return Violatedf("the note reads %+v, and it should name the failed check and cite no review", note)
	}
	return r.CheckProjection(s)
}

func checkReviewWithoutVerdictsLeavesTheEvidence(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	forge := &fake.Forge{}

	// A review that failed gave no verdict, so a forge result that covers every criterion settles the work
	// as it did before the review existed.
	head := r.ID("commit")
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(head))
	if err != nil {
		return err
	}
	if err := r.reviewEnds(s, id, laneResult(workorders.OutcomeFailed, "crashed")); err != nil {
		return err
	}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckPassed))
	moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick())
	if err != nil || !slices.Equal(moved, []string{id}) {
		return Violatedf("after a failed review a tick moved %v: %v", moved, err)
	}

	// A review a member canceled gave none either.
	other := r.ID("commit")
	canceled, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(other))
	if err != nil {
		return err
	}
	review, err := r.openReviewOf(s, canceled)
	if err != nil {
		return err
	}
	if err := ops.Cancel(r.Ctx, r.Scope, review.ID, "not needed", r.Actor); err != nil {
		return err
	}
	r.forgeSays(forge, other, forgeCheck(criterionOne, workorders.CheckPassed))
	moved, err = ops.Settle(r.Ctx, r.Scope, forge, r.tick())
	if err != nil || !slices.Equal(moved, []string{canceled}) {
		return Violatedf("after a canceled review a tick moved %v: %v", moved, err)
	}
	return r.stateIs(s, canceled, workorders.Done)
}

func checkReviewIsNotCompletedWork(r *Rig) error {
	s := r.Store()
	if err := r.atLevel(s, ledger.ReviewApproves); err != nil {
		return err
	}
	id, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, id, say(criterionOne, workorders.VerdictPass)); err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Done); err != nil {
		return err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	if err := r.stateIs(s, review.ID, workorders.Done); err != nil {
		return err
	}
	var report measures.Report
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		report, err = control.ReportIn(tx, "7d", 7*24*time.Hour)
		return err
	})
	if err != nil {
		return err
	}
	if report.Completed != 1 {
		return Violatedf("the measures count %d completed work orders, and one change and its review ended", report.Completed)
	}
	return nil
}

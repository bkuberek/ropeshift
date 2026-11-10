package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ReviewWorkChecks are what the ledger promises about the review of a completed change (FR-REVIEW-1,
// FR-REVIEW-2, FR-REVIEW-3, FR-REVIEW-5).
var ReviewWorkChecks = []Check{
	{"RW1", "a review keeps what it judges through the projection, a replay, and a rebuild", checkReviewRoundTrips},
	{"RW2", "a lane that completes a change leaves a review of its commit, in the transaction that ended it", checkCompletionOpensAReview},
	{"RW3", "work reworked and completed again is reviewed again, at its own attempt", checkReworkIsReviewedAgain},
	{"RW4", "a lane that left no commit, or did not complete, leaves no review", checkNoCommitNoReview},
	{"RW5", "a review that completes is done and leaves no review, and one that does not complete decides nothing", checkReviewEndsAlone},
	{"RW6", "work waits in acceptance while its review is open, and a member can still settle it", checkWorkWaitsForItsReview},
	{"RW7", "a review starts at the commit it judges, on the branch that holds it", checkReviewStartsAtTheCommit},
	{"RW8", "a review is never dispatched to a lane of the work it judges", checkReviewIsNotDispatchedToTheReviewedLane},
	{"RW9", "a review's pack holds the checks the reviewed lane reported, as information", checkReviewPackHoldsTheReportedChecks},
	{"RW10", "a worker is told what its review lane judges", checkWorkerIsToldWhatIsJudged},
	{"RW11", "a review lane's end asks the worker to take its worktree away", checkReviewLaneIsArchived},
	{"RW12", "a worker is told where the reviewed lane began, to read its diff from", checkWorkerIsToldWhereTheWorkBegan},
	{"RW13", "a review a member canceled leaves its worktree to be taken away when its lane ends", checkCanceledReviewLaneIsArchived},
	{"RW14", "the work a contract planned does not include the reviews that pin it, so a new version moves none of them", checkContractPlannedNoReview},
}

// workOf is ledger work in the repository acceptance speaks of, under a contract of these criteria.
func workOf(r *Rig, criteria ...string) control.LedgerWork {
	return control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Accept it", RepositoryID: AcceptanceRepository, MaxAttempts: 1, Priority: 2,
			Requirements: []string{"PAY-12"},
		},
		Statement: "Do the work.",
		Criteria:  criteriaOf(criteria...),
	}
}

// aReviewOf puts the review of an accepted shape of work in the ledger directly, as the store sees it.
func (r *Rig) aReviewOf(s ports.Store, of workorders.WorkOrder, fence int64) (workorders.WorkOrder, error) {
	review, err := workorders.NewReview(r.ID("review"), of, fence)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	err = s.Write(r.Ctx, r.Scope, "review.make", func(tx ports.WriteTx) error {
		if err := tx.WorkOrders().Insert(review); err != nil {
			return err
		}
		event, err := workorders.CreatedEvent(review, r.Actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	return review, err
}

// reviewsOf lists the reviews of a work order, oldest first.
func (r *Rig) reviewsOf(s ports.Store, id string) ([]workorders.WorkOrder, error) {
	var out []workorders.WorkOrder
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Kind == workorders.KindReview && row.Reviews == id {
				out = append(out, row)
			}
		}
		return nil
	})
	slices.SortFunc(out, func(a, b workorders.WorkOrder) int { return int(a.ReviewsFence - b.ReviewsFence) })
	return out, err
}

// theReview returns the one review a work order has.
func (r *Rig) theReview(s ports.Store, id string) (workorders.WorkOrder, error) {
	reviews, err := r.reviewsOf(s, id)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	if len(reviews) != 1 {
		return workorders.WorkOrder{}, Violatedf("%s has %d reviews, want 1", id, len(reviews))
	}
	return reviews[0], nil
}

func checkReviewRoundTrips(r *Rig) error {
	s := r.Store()
	of := workorders.WorkOrder{
		ID: r.ID("wo"), Kind: workorders.KindChange, Title: "Add the retry budget", RepositoryID: "repo-a", ContractID: "c-1", ContractVersion: 2,
		MaxAttempts: 2,
	}
	review, err := r.aReviewOf(s, of, 5)
	if err != nil {
		return err
	}
	read := func(when string) error {
		got, err := r.WorkOrder(s, review.ID)
		if err != nil {
			return err
		}
		if diff := workorders.Diff(got, review); len(diff) != 0 {
			return Violatedf("%s the review differs in %v: %+v", when, diff, got)
		}
		return nil
	}
	if err := read("after it was made,"); err != nil {
		return err
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	return read("after a rebuild,")
}

func checkCompletionOpensAReview(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	work, err := r.recordedWork(s, criterionOne, criterionTwo)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, work.ID, "a")
	if err != nil {
		return err
	}
	var writes []string
	stepped := r.StoreWith(func(op, name string) {
		if name == StepBefore {
			writes = append(writes, op)
		}
	})
	head := r.ID("commit")
	if err := r.NewOps(stepped).EndLane(r.Ctx, r.Scope, holder, LaneFinished(head, LaneCheck(criterionOne, true)), r.worker("a")); err != nil {
		return err
	}
	if !slices.Equal(writes, []string{"lane.end"}) {
		return Violatedf("ending the lane took the transactions %v, and the review is made in the one that ends it", writes)
	}
	reviewed, err := r.WorkOrder(s, work.ID)
	if err != nil {
		return err
	}
	review, err := r.theReview(s, work.ID)
	if err != nil {
		return err
	}
	want := workorders.WorkOrder{
		ID: review.ID, Kind: workorders.KindReview, State: workorders.Ready, Title: "Review: " + work.Title, RepositoryID: work.RepositoryID,
		Priority: work.Priority, Size: work.Size, Requirements: work.Requirements, Needs: work.Needs, ContractID: work.ContractID,
		ContractVersion: work.ContractVersion, Reviews: work.ID, ReviewsFence: reviewed.Fence, MaxAttempts: work.MaxAttempts,
	}
	if diff := workorders.Diff(review, want); len(diff) != 0 {
		return Violatedf("the review differs from what was meant in %v: %+v", diff, review)
	}
	if reviewed.State != workorders.Acceptance {
		return Violatedf("the reviewed work reads %s, want acceptance", reviewed.State)
	}
	count, event, err := r.eventsAbout(s, review.ID, workorders.EventCreated)
	if err != nil {
		return err
	}
	if count != 1 || event.Actor != r.worker("a") {
		return Violatedf("%d creations of the review, the last by %+v", count, event.Actor)
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if _, err := ops.Standing(r.Ctx, r.Scope, work.ID); err != nil {
		return err
	}
	return nil
}

// recordedWork puts ready work in the ledger under a contract of these criteria.
func (r *Rig) recordedWork(s ports.Store, criteria ...string) (workorders.WorkOrder, error) {
	ops := r.NewOps(s)
	work := workOf(r, criteria...)
	order, err := ops.Record(r.Ctx, r.Scope, work, r.Actor)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	if _, err := ops.Pass(r.Ctx, r.Scope, order.ID, r.Actor); err != nil {
		return workorders.WorkOrder{}, err
	}
	return r.WorkOrder(s, order.ID)
}

func checkReworkIsReviewedAgain(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	first, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	if _, err := ops.Rework(r.Ctx, r.Scope, id, reworkReason, r.Actor); err != nil {
		return err
	}
	again := r.ID("commit")
	if err := r.attempt(s, id, LaneFinished(again)); err != nil {
		return err
	}
	reviews, err := r.reviewsOf(s, id)
	if err != nil {
		return err
	}
	reviewed, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if len(reviews) != 2 || reviews[0].ID != first.ID || reviews[1].ReviewsFence != reviewed.Fence || reviews[1].ReviewsFence <= first.ReviewsFence {
		return Violatedf("after a rework the reviews read %+v, and the work is at fence %d", reviews, reviewed.Fence)
	}
	return r.CheckProjection(s)
}

func checkNoCommitNoReview(r *Rig) error {
	s := r.Store()
	for _, tt := range []struct {
		name   string
		result workorders.LaneResult
	}{
		{"a completed lane that left no commit", laneResult(workorders.OutcomeCompleted, "")},
		{"a blocked lane", laneResult(workorders.OutcomeBlocked, "needs an answer")},
		{"a failed lane", laneResult(workorders.OutcomeFailed, "crashed")},
	} {
		work, err := r.recordedWork(s, criterionOne)
		if err != nil {
			return err
		}
		holder, err := r.Running(s, work.ID, "a")
		if err != nil {
			return err
		}
		if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, tt.result, r.worker("a")); err != nil {
			return err
		}
		if reviews, err := r.reviewsOf(s, work.ID); err != nil || len(reviews) != 0 {
			return Violatedf("%s left reviews %+v: %v", tt.name, reviews, err)
		}
	}
	return nil
}

func checkReviewEndsAlone(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, review.ID, "a")
	if err != nil {
		return err
	}
	// A review lane leaves no branch of its own, and its verdicts decide the work it judged, not itself.
	result := laneResult(workorders.OutcomeCompleted, "")
	result.Checks = []workorders.CheckResult{LaneCheck(criterionOne, true)}
	result.Verdicts = []workorders.CriterionVerdict{say(criterionOne, workorders.VerdictPass)}
	if err := ops.EndLane(r.Ctx, r.Scope, holder, result, r.worker("a")); err != nil {
		return err
	}
	if err := r.stateIs(s, review.ID, workorders.Done); err != nil {
		return err
	}
	if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
		return err
	}
	if again, err := r.reviewsOf(s, review.ID); err != nil || len(again) != 0 {
		return Violatedf("a review's completion made reviews %+v: %v", again, err)
	}
	for _, eventType := range []string{workorders.EventType(workorders.TriggerLaneCompleted), workorders.EventType(workorders.TriggerAcceptancePassed)} {
		if count, _, err := r.eventsAbout(s, review.ID, eventType); err != nil || count != 1 {
			return Violatedf("%d events of %s on the review: %v", count, eventType, err)
		}
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if err := ops.Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if err := r.stateIs(s, review.ID, workorders.Done); err != nil {
		return fmt.Errorf("after a rebuild: %w", err)
	}

	// A review that blocks or fails decides nothing about the work, and nothing replaces it.
	for _, tt := range []struct {
		result workorders.LaneResult
		want   workorders.State
	}{
		{laneResult(workorders.OutcomeBlocked, "needs an answer"), workorders.Blocked},
		{laneResult(workorders.OutcomeFailed, "crashed"), workorders.Failed},
	} {
		other, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
		if err != nil {
			return err
		}
		pending, err := r.theReview(s, other)
		if err != nil {
			return err
		}
		held, err := r.Running(s, pending.ID, "a")
		if err != nil {
			return err
		}
		if err := ops.EndLane(r.Ctx, r.Scope, held, tt.result, r.worker("a")); err != nil {
			return err
		}
		if err := r.stateIs(s, pending.ID, tt.want); err != nil {
			return err
		}
		if err := r.stateIs(s, other, workorders.Acceptance); err != nil {
			return err
		}
	}
	return nil
}

func checkWorkWaitsForItsReview(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	forge := &fake.Forge{}

	// A forge that says the work is green does not move it while its review is open.
	head := r.ID("commit")
	waiting, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(head))
	if err != nil {
		return err
	}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckPassed))
	moved, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick())
	if err != nil || len(moved) != 0 {
		return Violatedf("a tick moved %v while the review was open: %v", moved, err)
	}
	if err := r.stateIs(s, waiting, workorders.Acceptance); err != nil {
		return err
	}

	// A member can still accept it, or send it back, as before, and the review nobody needs any more ends.
	open, err := r.openReviewOf(s, waiting)
	if err != nil {
		return err
	}
	if state, err := ops.Accept(r.Ctx, r.Scope, waiting, r.Actor); err != nil || state != workorders.Done {
		return Violatedf("a member accepting work with an open review: %v, %v", state, err)
	}
	if err := r.stateIs(s, open.ID, workorders.Canceled); err != nil {
		return err
	}
	sentBack, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	if state, err := ops.Rework(r.Ctx, r.Scope, sentBack, reworkReason, r.Actor); err != nil || state != workorders.Ready {
		return Violatedf("a member sending work back with an open review: %v, %v", state, err)
	}

	// Once the review ends, the tick settles the work as it always did.
	other := r.ID("commit")
	later, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(other))
	if err != nil {
		return err
	}
	r.forgeSays(forge, other, forgeCheck(criterionOne, workorders.CheckPassed))
	if err := r.finishReviews(s, later); err != nil {
		return err
	}
	moved, err = ops.Settle(r.Ctx, r.Scope, forge, r.tick())
	if err != nil || !slices.Equal(moved, []string{later}) {
		return Violatedf("after the review ended a tick moved %v: %v", moved, err)
	}
	return r.stateIs(s, later, workorders.Done)
}

func checkReviewStartsAtTheCommit(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	head := r.ID("commit")
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(head))
	if err != nil {
		return err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	request := r.Request(review.ID, "a")
	grant, err := ops.Claim(r.Ctx, r.Scope, request, r.worker("a"))
	if err != nil {
		return err
	}
	claim, err := r.claimOf(s, request.ClaimID)
	if err != nil {
		return err
	}
	if claim.BaseRef != branchName || claim.BaseCommit != head {
		return Violatedf("the review began at %s %s, want %s %s", claim.BaseRef, claim.BaseCommit, branchName, head)
	}
	if grant.ContractID != review.ContractID || grant.ContractVersion != review.ContractVersion {
		return Violatedf("the review was granted contract %s@%d, want %s@%d", grant.ContractID, grant.ContractVersion, review.ContractID, review.ContractVersion)
	}
	return nil
}

func (r *Rig) claimOf(s ports.Store, id string) (workorders.Claim, error) {
	var claim workorders.Claim
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		claim, err = tx.Claims().Find(id)
		return err
	})
	return claim, err
}

func checkReviewIsNotDispatchedToTheReviewedLane(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	var theirs []string
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Lanes().Rows()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.WorkOrder == id {
				theirs = append(theirs, row.ID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(theirs) == 0 {
		return Violatedf("the reviewed work ran no lane, so this proves nothing")
	}
	if _, err := ops.Claim(r.Ctx, r.Scope, r.Request(review.ID, "a"), r.worker("a")); err != nil {
		return err
	}
	for _, lane := range theirs {
		if _, err := ops.Dispatch(r.Ctx, r.Scope, review.ID, lane, r.Actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("the review was dispatched to lane %s of the work it judges: %v", lane, err)
		}
	}
	if _, err := ops.Dispatch(r.Ctx, r.Scope, review.ID, r.ID("lane"), r.Actor); err != nil {
		return Violatedf("a lane of its own was refused: %v", err)
	}
	return nil
}

func checkReviewPackHoldsTheReportedChecks(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.AwaitingReview(s, []string{criterionOne, criterionTwo}, LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true), LaneCheck(criterionTwo, false)))
	if err != nil {
		return err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	request := r.Request(review.ID, "a")
	if _, err := ops.Claim(r.Ctx, r.Scope, request, r.worker("a")); err != nil {
		return err
	}
	pack, err := r.packOf(s, request.ClaimID)
	if err != nil {
		return err
	}
	var reported []contextpacks.Item
	for _, item := range pack.Items {
		if item.Kind == contextpacks.KindChecks {
			reported = append(reported, item)
		}
	}
	if len(reported) != 1 || reported[0].Trust != contextpacks.TrustLane || !strings.HasPrefix(reported[0].Ref, id+"@") {
		return Violatedf("the pack names the checks %+v", reported)
	}
	for _, want := range []string{"check-c1", "passed", "check-c2", "failed", "information", "It holds: c1", "It holds: c2"} {
		if !strings.Contains(pack.Text, want) {
			return Violatedf("the review's pack leaves out %q:\n%s", want, pack.Text)
		}
	}
	// The work itself gets no such section: it has no work of another lane to read.
	first, err := r.packOfLane(s, id)
	if err != nil {
		return err
	}
	for _, item := range first.Items {
		if item.Kind == contextpacks.KindChecks {
			return Violatedf("the work's own pack holds %+v", item)
		}
	}
	return r.CheckPacks(s)
}

// packOfLane reads the pack the first claim on a work order pinned.
func (r *Rig) packOfLane(s ports.Store, id string) (contextpacks.Pack, error) {
	var pack contextpacks.Pack
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		claims, err := tx.Claims().List()
		if err != nil {
			return err
		}
		for _, claim := range claims {
			if claim.WorkOrder == id {
				pack, err = tx.Packs().Latest(claim.PackID)
				return err
			}
		}
		return Violatedf("%s has no claim", id)
	})
	return pack, err
}

func checkWorkerIsToldWhatIsJudged(r *Rig) error {
	s := r.Store()
	head := r.ID("commit")
	var reviewID string
	rig, err := newStatusRigOn(r, s, func(ports.Store) (string, error) {
		id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(head))
		if err != nil {
			return "", err
		}
		review, err := r.theReview(s, id)
		reviewID = review.ID
		return review.ID, err
	})
	if err != nil {
		return err
	}
	state, err := rig.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: rig.auth})
	if err != nil {
		return err
	}
	if len(state.Leases) != 1 || state.Leases[0].WorkOrder != reviewID {
		return Violatedf("desired state %+v", state.Leases)
	}
	lease := state.Leases[0]
	if lease.Kind != string(workorders.KindReview) || lease.Attempt == nil {
		return Violatedf("the review's lease reads %+v", lease)
	}
	reviewed, err := r.reviewedOf(s, reviewID)
	if err != nil {
		return err
	}
	want := ledger.DesiredAttempt{WorkOrder: reviewed.ID, Title: reviewed.Title, Branch: branchName, Commit: head}
	if *lease.Attempt != want {
		return Violatedf("the review is told %+v, want %+v", *lease.Attempt, want)
	}
	if lease.BaseRef != branchName || lease.BaseCommit != head {
		return Violatedf("the review's lease starts at %s %s, want %s %s", lease.BaseRef, lease.BaseCommit, branchName, head)
	}
	return nil
}

func (r *Rig) reviewedOf(s ports.Store, review string) (workorders.WorkOrder, error) {
	row, err := r.WorkOrder(s, review)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	return r.WorkOrder(s, row.Reviews)
}

func checkReviewLaneIsArchived(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	var reviewID, reviewedID string
	rig, err := newStatusRigOn(r, s, func(ports.Store) (string, error) {
		id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
		if err != nil {
			return "", err
		}
		review, err := r.theReview(s, id)
		reviewID, reviewedID = review.ID, id
		return review.ID, err
	})
	if err != nil {
		return err
	}
	desired := func() ([]ledger.DesiredArchive, error) {
		state, err := rig.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: rig.auth})
		return state.Archives, err
	}
	if archives, err := desired(); err != nil || len(archives) != 0 {
		return Violatedf("before the review ended the worker is asked to archive %+v: %v", archives, err)
	}
	holder := workorders.Holder{Lease: rig.grant.Lease, Lane: rig.lane, Generation: 1}
	ended := laneResult(workorders.OutcomeCompleted, "")
	ended.Verdicts = []workorders.CriterionVerdict{say(criterionOne, workorders.VerdictPass)}
	if err := ops.EndLane(r.Ctx, r.Scope, holder, ended, r.worker(rig.auth.WorkerID)); err != nil {
		return err
	}
	archives, err := desired()
	want := []ledger.DesiredArchive{{Lane: rig.lane, Repository: AcceptanceRepository, Forced: true}}
	if err != nil || !slices.Equal(archives, want) {
		return Violatedf("after the review ended the worker is asked to archive %+v, want %+v: %v", archives, want, err)
	}
	if count, _, err := r.eventsAbout(s, rig.lane, ledger.EventLaneArchiveRequested); err != nil || count != 1 {
		return Violatedf("%d archive requests on the review's lane: %v", count, err)
	}
	// Nothing is asked of the lanes that did the work: a branch's worktree is a member's to remove.
	_ = reviewID
	var theirs []string
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Lanes().Rows()
		for _, row := range rows {
			if row.WorkOrder == reviewedID {
				theirs = append(theirs, row.ID)
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	for _, lane := range theirs {
		if count, _, err := r.eventsAbout(s, lane, ledger.EventLaneArchiveRequested); err != nil || count != 0 {
			return Violatedf("%d archive requests on lane %s of the work reviewed: %v", count, lane, err)
		}
	}
	return nil
}

func checkWorkerIsToldWhereTheWorkBegan(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	w := r.startWorld(s)
	base := w.git.Grow(w.path(startRepository), "main")
	id, err := w.order(startRepository, "")
	if err != nil {
		return err
	}
	grant, err := w.leases.Claim(r.Ctx, r.Scope, r.Request(id, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	lane := r.ID("lane")
	if _, err := ops.Dispatch(r.Ctx, r.Scope, id, lane, r.Actor); err != nil {
		return err
	}
	generation, err := ops.StartLane(r.Ctx, r.Scope, grant.Lease, lane, r.worker("a"))
	if err != nil {
		return err
	}
	head := r.ID("commit")
	if err := ops.EndLane(r.Ctx, r.Scope, workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}, LaneFinished(head), r.worker("a")); err != nil {
		return err
	}
	var reviewID string
	rig, err := newStatusRigOn(r, s, func(ports.Store) (string, error) {
		review, err := r.theReview(s, id)
		reviewID = review.ID
		return review.ID, err
	})
	if err != nil {
		return err
	}
	state, err := rig.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: rig.auth})
	if err != nil {
		return err
	}
	if len(state.Leases) != 1 || state.Leases[0].WorkOrder != reviewID || state.Leases[0].Attempt == nil {
		return Violatedf("desired state %+v", state.Leases)
	}
	attempt := state.Leases[0].Attempt
	if attempt.BaseRef != "main" || attempt.BaseCommit != base || attempt.Commit != head {
		return Violatedf("the review is told the work began at %s %s and ended at %s, want main %s and %s", attempt.BaseRef, attempt.BaseCommit, attempt.Commit, base, head)
	}
	return nil
}

func checkCanceledReviewLaneIsArchived(r *Rig) error {
	s := r.Store()
	var reviewID string
	rig, err := newStatusRigOn(r, s, func(ports.Store) (string, error) {
		id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
		if err != nil {
			return "", err
		}
		review, err := r.theReview(s, id)
		reviewID = review.ID
		return review.ID, err
	})
	if err != nil {
		return err
	}
	if err := r.NewOps(s).Cancel(r.Ctx, r.Scope, reviewID, "dropped", r.Actor); err != nil {
		return err
	}
	result, err := json.Marshal(laneResult(workorders.OutcomeFailed, workorders.ReasonFenced))
	if err != nil {
		return err
	}
	if err := accepted(rig.upload(ledger.UploadLaneEnded, result)); err != nil {
		return err
	}
	state, err := rig.plane.Desired(r.Ctx, ledger.DesiredRequest{Auth: rig.auth})
	if err != nil {
		return err
	}
	want := []ledger.DesiredArchive{{Lane: rig.lane, Repository: AcceptanceRepository, Forced: true}}
	if !slices.Equal(state.Archives, want) {
		return Violatedf("after the canceled review's lane ended the worker is asked to archive %+v, want %+v", state.Archives, want)
	}
	return nil
}

// A new version of a contract re-pins its unstarted work and sends ready work back to contract review.
// A review pins the version of the work it judges, which is the version that work ran on, so it is not
// among the contract's work.
func checkContractPlannedNoReview(r *Rig) error {
	s := r.Store()
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	review, err := r.theReview(s, id)
	if err != nil {
		return err
	}
	var planned []workorders.WorkOrder
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		planned, err = tx.WorkOrders().ByContract(review.ContractID)
		return err
	})
	if err != nil {
		return err
	}
	if len(planned) != 1 || planned[0].ID != id {
		return Violatedf("the contract planned %+v, want only %s", planned, id)
	}
	return nil
}

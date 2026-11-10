package contract

import (
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// CalibrationChecks hold what the control plane records when a member decides on work a review judged
// (ADR-0019, FR-CAL-1, FR-CAL-3): whether the member agreed, bound to the reviewed commit, in the
// transaction that makes the decision, and never a change to the merge authority level.
var CalibrationChecks = []Check{
	{"CAL1", "the store keeps a calibration whole, once for one review, source, and decision, and refuses one that is not whole", checkCalibrationRoundTrips},
	{"CAL2", "a member who accepts or sends back reviewed work leaves a calibration bound to the commit, in the transaction of the decision", checkMemberDecisionIsCalibrated},
	{"CAL3", "agreement follows the verdict: accepting after a pass or sending back after a fail agrees, the opposite disagrees, and a criterion nobody could judge is unknown", checkAgreementFollowsTheVerdict},
	{"CAL4", "a decision of the control plane, and a decision on work no review judged, leaves no calibration", checkOnlyMembersAfterVerdictsAreCalibrated},
	{"CAL5", "a defect found later in work accepted after a pass is a disagreement with that verdict, bound to its commit digest", checkLaterDefectIsCalibrated},
	{"CAL6", "any number of agreements leaves the merge authority level where a member put it", checkCalibrationNeverChangesTheLevel},
}

// calibrationsOf reads every calibration of a work order, oldest first.
func (r *Rig) calibrationsOf(s ports.Store, id string) ([]workorders.Calibration, error) {
	var out []workorders.Calibration
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Evidence().Calibrations()
		for _, c := range all {
			if c.WorkOrder == id {
				out = append(out, c)
			}
		}
		return err
	})
	return out, err
}

func checkCalibrationRoundTrips(r *Rig) error {
	s := r.Store()
	record := workorders.Calibration{
		ID: r.ID("cal"), WorkOrder: "wo-1", Review: "review-1", Commit: "commit-1", Repository: "repo-1", Authority: ledger.PersonApproves,
		Outcome: workorders.ReviewFail, Decision: workorders.DecisionAccept, Agreement: workorders.AgreementDisagree, Source: workorders.SourceDecision,
		Member: "member-1", Reason: "the failure is a test of the old behavior",
		Verdicts: []workorders.SeenVerdict{{Criterion: criterionOne, Verdict: workorders.VerdictPass}, {Criterion: criterionTwo, Verdict: workorders.VerdictFail}},
	}
	write := func(c workorders.Calibration) (bool, error) {
		var fresh bool
		err := s.Write(r.Ctx, r.Scope, "calibration.record", func(tx ports.WriteTx) error {
			var err error
			fresh, err = tx.Evidence().RecordCalibration(c)
			if err != nil {
				return err
			}
			_, err = tx.Append(ledger.EventDraft{
				Type: workorders.EventEvidenceRecorded, Actor: r.Actor, Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: c.WorkOrder},
			})
			return err
		})
		return fresh, err
	}
	if fresh, err := write(record); err != nil || !fresh {
		return Violatedf("a first calibration: new %v, error %v", fresh, err)
	}
	again := record
	again.ID, again.Reason = r.ID("cal"), "another word"
	if fresh, err := write(again); err != nil || fresh {
		return Violatedf("the same review, source, and decision again: new %v, error %v", fresh, err)
	}
	got, err := r.calibrationsOf(s, "wo-1")
	if err != nil {
		return err
	}
	if len(got) != 1 {
		return Violatedf("the store holds %d calibrations of one decision", len(got))
	}
	record.CreatedAt = got[0].CreatedAt
	if got[0].CreatedAt == 0 || !calibrationsEqual(got[0], record) {
		return Violatedf("the store read back %+v, want %+v", got[0], record)
	}
	// The same review judged by a later defect is another record, and a second review is another one.
	later := record
	later.ID, later.Decision, later.Outcome, later.Agreement, later.Source = r.ID("cal"), workorders.DecisionRework, workorders.ReviewPass, workorders.AgreementDisagree, workorders.SourceLaterDefect
	if fresh, err := write(later); err != nil || !fresh {
		return Violatedf("a later defect against the same review: new %v, error %v", fresh, err)
	}
	for name, bad := range map[string]workorders.Calibration{
		"no commit": func() workorders.Calibration { c := record; c.ID, c.Commit = r.ID("cal"), ""; return c }(),
		"agreement that cannot be": func() workorders.Calibration {
			c := record
			c.ID, c.Agreement = r.ID("cal"), workorders.AgreementAgree
			return c
		}(),
	} {
		if _, err := write(bad); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("%s: %v", name, err)
		}
	}
	return nil
}

func calibrationsEqual(a, b workorders.Calibration) bool {
	return a.ID == b.ID && a.WorkOrder == b.WorkOrder && a.Review == b.Review && a.Commit == b.Commit && a.Repository == b.Repository &&
		a.Authority == b.Authority && a.Outcome == b.Outcome && a.Decision == b.Decision && a.Agreement == b.Agreement && a.Source == b.Source &&
		a.Member == b.Member && a.Reason == b.Reason && a.CreatedAt == b.CreatedAt && slices.Equal(a.Verdicts, b.Verdicts)
}

func checkMemberDecisionIsCalibrated(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	if err := r.atLevel(s, ledger.PersonApproves); err != nil {
		return err
	}
	accepted, head, err := r.judged(s, criterionOne, criterionTwo)
	if err != nil {
		return err
	}
	review, err := r.openReviewOf(s, accepted)
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, accepted, say(criterionOne, workorders.VerdictPass), say(criterionTwo, workorders.VerdictPass)); err != nil {
		return err
	}
	var writes []string
	stepped := r.StoreWith(func(op, name string) {
		if name == StepBefore {
			writes = append(writes, op)
		}
	})
	if state, err := r.NewOps(stepped).Accept(r.Ctx, r.Scope, accepted, r.Actor); err != nil || state != workorders.Done {
		return Violatedf("a member accepting: %v, %v", state, err)
	}
	if !slices.Equal(writes, []string{"acceptance.accept"}) {
		return Violatedf("accepting took the transactions %v, and the calibration is written in the one that decides", writes)
	}
	got, err := r.calibrationsOf(s, accepted)
	if err != nil {
		return err
	}
	if len(got) != 1 {
		return Violatedf("accepting reviewed work left %d calibrations", len(got))
	}
	c := got[0]
	if c.Review != review.ID || c.Commit != head || c.Repository != AcceptanceRepository || c.Authority != ledger.PersonApproves ||
		c.Outcome != workorders.ReviewPass || c.Decision != workorders.DecisionAccept || c.Agreement != workorders.AgreementAgree ||
		c.Source != workorders.SourceDecision || c.Member != r.Actor.ID || c.CreatedAt == 0 {
		return Violatedf("the calibration reads %+v", c)
	}
	if !slices.Equal(c.Verdicts, []workorders.SeenVerdict{{Criterion: criterionOne, Verdict: workorders.VerdictPass}, {Criterion: criterionTwo, Verdict: workorders.VerdictPass}}) {
		return Violatedf("the calibration saw %+v", c.Verdicts)
	}
	// A repeated acceptance changes nothing, so it adds no record.
	if _, err := ops.Accept(r.Ctx, r.Scope, accepted, r.Actor); err != nil {
		return err
	}
	if again, err := r.calibrationsOf(s, accepted); err != nil || len(again) != 1 {
		return Violatedf("a repeated acceptance: %d calibrations, %v", len(again), err)
	}

	// A member sending work back leaves one with the reason the member gave.
	sent, sentHead, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, sent, say(criterionOne, workorders.VerdictPass)); err != nil {
		return err
	}
	if _, err := ops.Rework(r.Ctx, r.Scope, sent, "the migration is missing", r.Actor); err != nil {
		return err
	}
	got, err = r.calibrationsOf(s, sent)
	if err != nil {
		return err
	}
	if len(got) != 1 {
		return Violatedf("sending reviewed work back left %d calibrations", len(got))
	}
	c = got[0]
	if c.Commit != sentHead || c.Decision != workorders.DecisionRework || c.Agreement != workorders.AgreementDisagree ||
		c.Reason != "the migration is missing" || c.Outcome != workorders.ReviewPass {
		return Violatedf("the calibration of the rework reads %+v", c)
	}
	return r.CheckProjection(s)
}

func checkAgreementFollowsTheVerdict(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	// A failed review sends work back by rule until its rounds are used up, so a member meets a fail
	// only at the bound, and the bound here is one round.
	for _, tt := range []struct {
		name     string
		verdicts []workorders.VerdictKind
		decide   workorders.CalibrationDecision
		outcome  workorders.ReviewOutcome
		want     workorders.Agreement
	}{
		{"accepting a pass", []workorders.VerdictKind{workorders.VerdictPass, workorders.VerdictPass}, workorders.DecisionAccept, workorders.ReviewPass, workorders.AgreementAgree},
		{"sending back a pass", []workorders.VerdictKind{workorders.VerdictPass, workorders.VerdictPass}, workorders.DecisionRework, workorders.ReviewPass, workorders.AgreementDisagree},
		{"sending back a fail", []workorders.VerdictKind{workorders.VerdictPass, workorders.VerdictFail}, workorders.DecisionRework, workorders.ReviewFail, workorders.AgreementAgree},
		{"accepting a fail", []workorders.VerdictKind{workorders.VerdictPass, workorders.VerdictFail}, workorders.DecisionAccept, workorders.ReviewFail, workorders.AgreementDisagree},
		{"accepting what could not be judged", []workorders.VerdictKind{workorders.VerdictPass, workorders.VerdictCannotJudge}, workorders.DecisionAccept, workorders.ReviewCannotJudge, workorders.AgreementUnknown},
		{"sending back what could not be judged", []workorders.VerdictKind{workorders.VerdictCannotJudge, workorders.VerdictPass}, workorders.DecisionRework, workorders.ReviewCannotJudge, workorders.AgreementUnknown},
	} {
		id, _, err := r.judged(s, criterionOne, criterionTwo)
		if err != nil {
			return err
		}
		result := laneResult(workorders.OutcomeCompleted, "")
		result.MaxReviewRounds = 1
		result.Verdicts = []workorders.CriterionVerdict{say(criterionOne, tt.verdicts[0]), say(criterionTwo, tt.verdicts[1])}
		if err := r.reviewEnds(s, id, result); err != nil {
			return err
		}
		if err := r.stateIs(s, id, workorders.Acceptance); err != nil {
			return Violatedf("%s: the work should wait for a member: %v", tt.name, err)
		}
		if tt.decide == workorders.DecisionAccept {
			_, err = ops.Accept(r.Ctx, r.Scope, id, r.Actor)
		} else {
			_, err = ops.Rework(r.Ctx, r.Scope, id, reworkReason, r.Actor)
		}
		if err != nil {
			return Violatedf("%s: %v", tt.name, err)
		}
		got, err := r.calibrationsOf(s, id)
		if err != nil {
			return err
		}
		if len(got) != 1 || got[0].Outcome != tt.outcome || got[0].Agreement != tt.want || got[0].Decision != tt.decide {
			return Violatedf("%s: %+v", tt.name, got)
		}
	}
	return nil
}

func checkOnlyMembersAfterVerdictsAreCalibrated(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)

	// The control plane accepting reviewed work at review_approves is no member's word.
	if err := r.atLevel(s, ledger.ReviewApproves); err != nil {
		return err
	}
	byRule, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, byRule, say(criterionOne, workorders.VerdictPass)); err != nil {
		return err
	}
	if err := r.stateIs(s, byRule, workorders.Done); err != nil {
		return err
	}
	// Nor is a rework by rule after a failed criterion.
	sentBack, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, sentBack, say(criterionOne, workorders.VerdictFail)); err != nil {
		return err
	}
	if err := r.stateIs(s, sentBack, workorders.Ready); err != nil {
		return err
	}
	// Below review_approves the work waits for a member, who is the one to decide the rest.
	if err := r.atLevel(s, ledger.PersonMerges); err != nil {
		return err
	}

	// Nor is the control plane's own acceptance of reviewed work whose criteria a forge check covers.
	forge := &fake.Forge{}
	head := r.ID("commit")
	byTick, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(head))
	if err != nil {
		return err
	}
	r.forgeSays(forge, head, forgeCheck(criterionOne, workorders.CheckPassed))
	if err := r.reviewSays(s, byTick, say(criterionOne, workorders.VerdictPass)); err != nil {
		return err
	}
	if _, err := ops.Settle(r.Ctx, r.Scope, forge, r.tick()); err != nil {
		return err
	}
	if state, err := ops.Accept(r.Ctx, r.Scope, byTick, r.tick()); err != nil || state != workorders.Done {
		return Violatedf("the control plane accepting covered work: %v, %v", state, err)
	}

	// A member who decides while the review is open has no verdict to agree with.
	early, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, early, r.Actor); err != nil {
		return err
	}
	// A review that ended without verdicts leaves the work to its evidence, and a member's word on it
	// has no verdict to follow.
	empty, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit")))
	if err != nil {
		return err
	}
	if err := r.finishReviews(s, empty); err != nil {
		return err
	}
	if _, err := ops.Rework(r.Ctx, r.Scope, empty, reworkReason, r.Actor); err != nil {
		return err
	}
	for name, id := range map[string]string{"tick accept": byTick, "rule accept": byRule, "rule rework": sentBack, "early accept": early, "no verdicts": empty} {
		if got, err := r.calibrationsOf(s, id); err != nil || len(got) != 0 {
			return Violatedf("%s left calibrations %+v: %v", name, got, err)
		}
	}
	return nil
}

func checkLaterDefectIsCalibrated(r *Rig) error {
	s := r.Store()
	if err := r.atLevel(s, ledger.ReviewApproves); err != nil {
		return err
	}
	done, head, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, done, say(criterionOne, workorders.VerdictPass)); err != nil {
		return err
	}
	if err := r.stateIs(s, done, workorders.Done); err != nil {
		return err
	}
	review, err := r.theReview(s, done)
	if err != nil {
		return err
	}
	old, err := r.Assumed(s, "Which log level do retries use?", done)
	if err != nil {
		return err
	}
	result, err := r.overturn(s, old.ID)
	if err != nil {
		return err
	}
	if len(result.Rework) != 1 {
		return Violatedf("overturning a decision the done work rested on made %d rework work orders", len(result.Rework))
	}
	got, err := r.calibrationsOf(s, done)
	if err != nil {
		return err
	}
	if len(got) != 1 {
		return Violatedf("a defect found in approved work left %d calibrations", len(got))
	}
	c := got[0]
	if c.Source != workorders.SourceLaterDefect || c.Decision != workorders.DecisionRework || c.Agreement != workorders.AgreementDisagree ||
		c.Outcome != workorders.ReviewPass || c.Review != review.ID || c.Commit != head || c.Authority != ledger.ReviewApproves || c.Member != r.Actor.ID || c.Reason == "" {
		return Violatedf("the calibration of the later defect reads %+v", c)
	}
	// Work no review judged has no verdict to disagree with.
	plain, err := r.Completed(s, true)
	if err != nil {
		return err
	}
	other, err := r.Assumed(s, "Which log level do retries use now?", plain)
	if err != nil {
		return err
	}
	if _, err := r.overturn(s, other.ID); err != nil {
		return err
	}
	if got, err := r.calibrationsOf(s, plain); err != nil || len(got) != 0 {
		return Violatedf("work no review judged left calibrations %+v: %v", got, err)
	}
	return nil
}

func checkCalibrationNeverChangesTheLevel(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	for _, level := range []ledger.MergeAuthority{ledger.PersonMerges, ledger.PersonApproves} {
		if err := r.atLevel(s, level); err != nil {
			return err
		}
		before, err := r.repositoryEvents(s, AcceptanceRepository)
		if err != nil {
			return err
		}
		for range 12 {
			id, _, err := r.judged(s, criterionOne)
			if err != nil {
				return err
			}
			if err := r.reviewSays(s, id, say(criterionOne, workorders.VerdictPass)); err != nil {
				return err
			}
			if _, err := ops.Accept(r.Ctx, r.Scope, id, r.Actor); err != nil {
				return err
			}
		}
		record, err := r.theRepository(s, AcceptanceRepository)
		if err != nil {
			return err
		}
		if record.Level() != level {
			return Violatedf("after twelve agreements the level is %s, and a member set %s", record.Level(), level)
		}
		events, err := r.repositoryEvents(s, AcceptanceRepository)
		if err != nil {
			return err
		}
		if len(events) != len(before) {
			return Violatedf("twelve agreements added %d events to the repository's record, and none of them is a decision of the ledger's", len(events)-len(before))
		}
	}
	return nil
}

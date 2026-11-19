package contract

import (
	"encoding/json"
	"errors"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// tokenVerdict is a verdict whose reasons and citations hold a credential, and whose citations hold line breaks.
func tokenVerdict() workorders.CriterionVerdict {
	return workorders.CriterionVerdict{
		Criterion: criterionOne, Verdict: workorders.VerdictPass, Reasons: "The token " + aToken + " is read from config.yaml.",
		Citations: []string{"config.yaml:4\r\nmain.go:9", "key " + aToken + "\nFR-1", "plain.go:1"},
	}
}

func checkVerdictsReachNoRecordUnredacted(r *Rig) error {
	s := r.Store()
	id, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	if err := r.reviewSays(s, id, tokenVerdict()); err != nil {
		return err
	}
	if count, first, err := r.eventsHolding(s, aToken); err != nil || count != 0 {
		return Violatedf("%d events of a review's end hold a credential of its verdicts, the first a %s: %v", count, first, err)
	}
	view, err := r.verdictsCounted(s, id)
	if err != nil {
		return err
	}
	if len(view.Verdicts) != 1 {
		return Violatedf("the work holds %d verdicts, want 1", len(view.Verdicts))
	}
	got := view.Verdicts[0]
	if strings.Contains(got.Reasons, aToken) || got.Reasons != "The token [redacted: github_token] is read from config.yaml." {
		return Violatedf("the verdict keeps the reasons %q", got.Reasons)
	}
	if len(got.Citations) != 3 || strings.Contains(strings.Join(got.Citations, " "), aToken) {
		return Violatedf("the verdict keeps the citations %q", got.Citations)
	}
	for _, citation := range got.Citations {
		if strings.ContainsAny(citation, "\r\n") {
			return Violatedf("the verdict keeps a citation of more than one line: %q", citation)
		}
	}
	return r.CheckProjection(s)
}

func checkVerdictReasonsAreBounded(r *Rig) error {
	s := r.Store()
	id, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	over := say(criterionOne, workorders.VerdictPass)
	over.Reasons = strings.Repeat("x", 4096+1)
	if err := r.reviewSays(s, id, over); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a verdict whose reasons are over %d bytes ends the review with %v, want an invalid report", 4096, err)
	}
	if _, err := r.openReviewOf(s, id); err != nil {
		return Violatedf("a refused report left the review ended: %v", err)
	}
	other, _, err := r.judged(s, criterionOne)
	if err != nil {
		return err
	}
	atBound := say(criterionOne, workorders.VerdictPass)
	atBound.Reasons = strings.Repeat("x", 4096)
	if err := r.reviewSays(s, other, atBound); err != nil {
		return Violatedf("a verdict whose reasons are %d bytes: %v", 4096, err)
	}
	return r.CheckProjection(s)
}

func checkCanceledLaneVerdictsReachNoEventUnredacted(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := accepted(s.report(s.working)); err != nil {
		return err
	}
	if err := r.NewOps(s.store).Cancel(r.Ctx, r.Scope, s.grant.WorkOrder, "dropped", r.Actor); err != nil {
		return err
	}
	result := laneResult(workorders.OutcomeFailed, workorders.ReasonFenced)
	result.Verdicts = []workorders.CriterionVerdict{tokenVerdict()}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := accepted(s.upload(ledger.UploadLaneEnded, raw)); err != nil {
		return err
	}
	if err := s.expectEnded(workorders.ReasonCanceled); err != nil {
		return err
	}
	if count, first, err := r.eventsHolding(s.store, aToken); err != nil || count != 0 {
		return Violatedf("%d events of a canceled lane's end hold a credential of its verdicts, the first a %s: %v", count, first, err)
	}
	return nil
}

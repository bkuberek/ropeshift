package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// hasLine reports a line of output that begins with a label.
func hasLine(text, label string) bool {
	for line := range strings.Lines(text) {
		if strings.HasPrefix(line, label) {
			return true
		}
	}
	return false
}

// reviewedWork is work that waited for acceptance until its review gave a verdict it cannot judge.
func reviewedWork(t *testing.T, s *session) {
	t.Helper()
	w := apitest.NewWorld(t, s.daemon)
	w.Running("wo", "Trim the log").End(workorders.OutcomeCompleted, "")
	w.Reviewed("wo", workorders.CriterionVerdict{
		Criterion: "c1", Verdict: workorders.VerdictCannotJudge,
		Reasons:   "The log is trimmed in trim.go,\nbut nothing I can run shows it keeps the last day.",
		Citations: []string{"trim.go:30", "FR-LOG-2"},
	})
}

// A person at a terminal reads the review's verdict for each criterion before deciding (FR-MERGE-4).
func TestWorkShowPrintsTheReviewsAndTheirVerdicts(t *testing.T) {
	s := newSession(t)
	reviewedWork(t, s)
	text := s.must("work", "show", "wo")
	for _, want := range []string{
		"\tdone\tattempt 2",
		"verdict\tc1\tcannot_judge\tThe log is trimmed in trim.go, but nothing I can run shows it keeps the last day.\ttrim.go:30, FR-LOG-2",
		"waiting\tfor a member: the review could not judge criterion c1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("show leaves out %q:\n%s", want, text)
		}
	}
	if !hasLine(text, "review\t") {
		t.Errorf("show names no review:\n%s", text)
	}
}

func TestWorkShowJSONKeepsTheWorkOrderAndAddsTheReview(t *testing.T) {
	s := newSession(t)
	reviewedWork(t, s)
	var detail api.WorkDetail
	if err := json.Unmarshal([]byte(s.must("work", "show", "wo", "--json")), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != "wo" || detail.State != workorders.Acceptance || len(detail.JudgedBy) != 1 || len(detail.Verdicts) != 1 {
		t.Errorf("detail %+v", detail)
	}
	// What read the work order before still reads it.
	var row workorders.WorkOrder
	if err := json.Unmarshal([]byte(s.must("work", "show", "wo", "--json")), &row); err != nil || row.ID != "wo" {
		t.Errorf("row %+v: %v", row, err)
	}
}

func TestWorkShowOfWorkNobodyReviewedPrintsNoReview(t *testing.T) {
	s := newSession(t)
	s.create("plain")
	text := s.must("work", "show", "plain")
	if hasLine(text, "review\t") || hasLine(text, "verdict\t") {
		t.Errorf("show of unreviewed work:\n%s", text)
	}
	var detail api.WorkDetail
	if err := json.Unmarshal([]byte(s.must("work", "show", "plain", "--json")), &detail); err != nil || len(detail.JudgedBy) != 0 || len(detail.Verdicts) != 0 {
		t.Errorf("detail %+v: %v", detail, err)
	}
}

func TestWorkAcceptancePrintsTheVerdicts(t *testing.T) {
	s := newSession(t)
	reviewedWork(t, s)
	text := s.must("work", "acceptance", "wo")
	if !strings.Contains(text, "verdict\tc1\tcannot_judge\t") {
		t.Errorf("acceptance leaves out the verdicts:\n%s", text)
	}
}

// A planning branch's approval is a member's plan approval, and work show names the plan version it approved.
func TestWorkShowNamesThePlanVersionThatApprovedAMerge(t *testing.T) {
	detail := api.WorkDetail{WorkOrder: workorders.WorkOrder{ID: "plan-1", Kind: workorders.KindPlan, State: workorders.Done}, Integrations: []ledger.IntegrationRecord{{
		ID: "i1", WorkOrder: "plan-1", ApprovedCommit: "1111aaaa2222bbbb", MergeAuthority: ledger.ReviewApproves, ApprovedByKind: ledger.ApprovedByMember,
		ApprovedByID: "dana", ApprovedAt: 1, MergedCommit: "3333cccc4444dddd", MergedAt: 2, Plan: "01PLAN", PlanVersion: 2,
	}}}
	text := cli.WorkDetailText(detail)
	if want := "approved\t1111aaaa2222\tby member dana\tat review_approves\tmerged as 3333cccc4444\tplan 01PLAN version 2\n"; !strings.Contains(text, want) {
		t.Errorf("show leaves out %q:\n%s", want, text)
	}
}

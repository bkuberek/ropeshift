package top

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/workorders"
)

func verdict(kind workorders.VerdictKind) workorders.Verdict {
	return workorders.Verdict{CriterionVerdict: workorders.CriterionVerdict{Criterion: "c", Verdict: kind, Reasons: "x"}}
}

// A line of work that waits for acceptance says what its review found, in the words the web UI uses, so
// a person at a terminal reads the count before opening the work.
func TestAnAcceptanceLineSaysWhatTheReviewFound(t *testing.T) {
	need := func(verdicts ...workorders.Verdict) api.NeedItem {
		return api.NeedItem{Kind: api.NeedWork, ID: "wo", Work: &api.WorkNeed{
			Reason: api.ReasonAcceptance, WorkOrder: &workorders.WorkOrder{ID: "wo", Title: "Trim the log"},
			Standing: &workorders.AcceptanceView{Verdicts: verdicts},
		}}
	}
	tests := []struct {
		name string
		item api.NeedItem
		want string
	}{
		{"every kind", need(verdict(workorders.VerdictPass), verdict(workorders.VerdictFail), verdict(workorders.VerdictCannotJudge)), "review: 1 pass, 1 fail, 1 cannot judge"},
		{"only passes", need(verdict(workorders.VerdictPass), verdict(workorders.VerdictPass)), "review: 2 pass"},
		{"a cannot judge", need(verdict(workorders.VerdictCannotJudge)), "review: 1 cannot judge"},
		{"no verdict", need(), ""},
	}
	for _, tt := range tests {
		line := what(tt.item)
		switch {
		case tt.want == "" && strings.Contains(line, "review:"):
			t.Errorf("%s: %q", tt.name, line)
		case tt.want != "" && !strings.Contains(line, tt.want):
			t.Errorf("%s: %q, want %q", tt.name, line, tt.want)
		}
	}
	if line := what(api.NeedItem{Kind: api.NeedWork, Work: &api.WorkNeed{Reason: api.ReasonAcceptance}}); strings.Contains(line, "review:") {
		t.Errorf("work with no standing: %q", line)
	}
}

// A recovery request says which act the orchestrator asked for, on which work, why, and the limit it went
// past, so a person at a terminal reads what approving it does.
func TestARecoveryLineSaysWhatItAsksAndWhy(t *testing.T) {
	detail := `{"act":"work_retry","work_order_id":"wo-1","reason":"the network came back","check":"max_attempts","limit":"the work order used 1 of max_attempts 1","policy_version":1}`
	item := api.NeedItem{Kind: api.NeedApproval, ID: "recovery-1", Approval: &authority.Approval{
		ID: "recovery-1", Kind: authority.KindRecovery, WorkOrder: "wo-1", Subject: "work_retry", RequestedBy: "orchestrator:w1", Detail: detail,
	}}
	line := what(item)
	for _, want := range []string{"work_retry", "wo-1", "the network came back", "the work order used 1 of max_attempts 1"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line %q does not say %q", line, want)
		}
	}
	if strings.Contains(line, "lane  asks") {
		t.Errorf("a recovery request reads as a lane's: %q", line)
	}
}

package top

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
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

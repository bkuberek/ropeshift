package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// costs runs work that reports what its lanes spent, and has a person accept each.
func costs(t *testing.T, s *session, w *apitest.World, spent ...float64) {
	t.Helper()
	for i, usd := range spent {
		id := "wo-" + string(rune('a'+i))
		lane := w.Running(id, "Ship "+id)
		lane.Finish(workorders.LaneResult{
			Report: workorders.Report{Outcome: workorders.OutcomeCompleted}, Branch: "lane/" + lane.ID, Head: "abc123",
			CostUSD: usd, UnknownCostTurns: int64(i),
		})
		if _, err := s.daemon.Client.Accept(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
}

// line is the line of a measures text that a name heads.
func line(t *testing.T, text, name string) string {
	t.Helper()
	for candidate := range strings.Lines(text) {
		if strings.HasPrefix(candidate, name+"\t") {
			return strings.TrimSuffix(candidate, "\n")
		}
	}
	t.Fatalf("no %q line in:\n%s", name, text)
	return ""
}

// The measures read as the web UI states them: each kind of interruption in the singular for one,
// and money in dollars and cents, while the JSON keeps every digit.
func TestMeasuresReadAsTheWebUIStatesThem(t *testing.T) {
	s := newSession(t)
	w := apitest.NewWorld(t, s.daemon)
	costs(t, s, w, 1.25, 1.35)
	approval := w.Running("wo-asking", "Run the tests").Approval("approval-1", `{"command":"go test ./..."}`)
	if _, err := s.daemon.Client.DecideApproval(t.Context(), approval.ID, api.DecideApproval{Digest: approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	text := s.must("measures")
	if got, want := line(t, text, "interruptions"), "interruptions\t0 batches of questions, 1 approval, 0 forced actions"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if got, want := line(t, text, "cost per outcome, change"), "cost per outcome, change\tat least $1.30 each, $2.60 over 2 done"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	var report measures.Report
	if err := json.Unmarshal([]byte(s.must("measures", "--json")), &report); err != nil {
		t.Fatal(err)
	}
	if cost := report.CostPerOutcome["change"]; cost.TotalUSD != 1.25+1.35 || cost.PerDone == nil || *cost.PerDone != (1.25+1.35)/2 {
		t.Errorf("the JSON lost a digit: %+v", cost)
	}
}

// A cost too small to show in cents says so, rather than reading as nothing.
func TestMeasuresSayACostUnderACent(t *testing.T) {
	s := newSession(t)
	costs(t, s, apitest.NewWorld(t, s.daemon), 0.004)
	if got, want := line(t, s.must("measures"), "cost per outcome, change"), "cost per outcome, change\tunder $0.01 each, under $0.01 over 1 done"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

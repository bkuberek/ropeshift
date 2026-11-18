package ghcli

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// The read schedule needs to tell a run still waiting for a runner from one at work, and when the work began
// (FR-SPEND-20): a check keeps its run status and its start, and a run that is queued, waiting, requested, or
// pending is queued, whatever the forge calls it.
func TestACheckKeepsItsRunStatusAndStart(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, _, _ := opened(t, forge, repo, gh)
	const start = "2026-01-02T09:00:00Z"
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		pr := s.Find(1)
		pr.Checks = []ghstate.Check{
			{Typename: "CheckRun", Name: "build", Status: "IN_PROGRESS", StartedAt: start},
			{Typename: "CheckRun", Name: "lint", Status: "QUEUED"},
			{Typename: "CheckRun", Name: "unit", Status: "COMPLETED", Conclusion: "SUCCESS", StartedAt: start},
			{Typename: "CheckRun", Name: "wait", Status: "WAITING"},
			{Typename: "CheckRun", Name: "ask", Status: "REQUESTED"},
			{Typename: "StatusContext", Context: "legacy", State: "PENDING"},
		}
		pr.Required = []string{"build", "lint", "unit"}
	})
	read, err := forge.PullRequest(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ledger.PullRequestCheck{}
	for _, check := range read.Checks {
		got[check.Name] = check
	}
	want := map[string]struct {
		status  ledger.CIRunStatus
		started string
	}{
		"build": {ledger.CIRunInProgress, start}, "lint": {ledger.CIRunQueued, ""}, "unit": {ledger.CIRunCompleted, start},
		"wait": {ledger.CIRunQueued, ""}, "ask": {ledger.CIRunQueued, ""}, "legacy": {"", ""},
	}
	for name, w := range want {
		if check := got[name]; check.Status != w.status || check.StartedAt != w.started {
			t.Errorf("%s reads status %q started %q, want %q %q", name, check.Status, check.StartedAt, w.status, w.started)
		}
	}
	if !got["build"].Required || got["wait"].Required {
		t.Errorf("the required marks read %+v", read.Checks)
	}
}

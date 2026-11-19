package server_test

import (
	"context"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestGoldenPractices writes what the practices routes answer, as the web UI reads them: a repository whose latest result
// holds a practice of each status and a finding, with a request open beside it; a repository no check reported; and the
// answer of a request, with an entry that holds the request and one that holds why none was made.
func TestGoldenPractices(t *testing.T) {
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{
		Clock: clock.Dialect,
		AssistantHosts: func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
			return map[string]control.AssistantHost{"plan": {Checkouts: map[string]bool{"repo-a": true}}}, nil
		},
		LocalWorker: func(context.Context) (string, error) { return "plan", nil },
	})
	connectWorker(t, d, "plan", ledger.ProtocolVersion)
	d.HoldClone(t, "repo-a", "widgets")
	d.HoldClone(t, "repo-b", "gadgets")
	checked, err := d.Client.CheckPractices(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	keepResult(t, d, "request-earlier", practices.Result{
		Repository: "repo-a", Commit: "1111111111111111111111111111111111111111", BaseBranch: "main", Worker: "plan",
		CheckedAt:    time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC),
		TableVersion: practices.TableVersion, RulesVersion: ledger.CredentialRulesVersion, Skipped: 1,
		Partial: "the repository is a shallow clone, so the history before its shallow boundary was not scanned",
		Practices: []practices.PracticeStatus{
			{ID: practices.EnvIgnored, Status: practices.StatusFinding},
			{ID: practices.Secrets, Status: practices.StatusFinding},
			{ID: practices.CI, Status: practices.StatusMet},
			{ID: practices.PreCommit, Status: practices.StatusMet},
			{ID: practices.Lockfile, Status: practices.StatusNotApplicable, Reason: "no package manager is detected"},
			{ID: practices.License, Status: practices.StatusUnknown, Reason: "the forge could not be read"},
		},
		Findings: []practices.Finding{
			{ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored"},
			{
				ID: "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f", Practice: practices.Secrets, Severity: practices.High, Detail: "a github_token credential",
				Rule: "github_token", Path: "config/dev.yaml", Line: 3, Commit: "aaaaaaaaaaaa", Digest: "1a2b3c4d5e6f1a2b3c4d5e6f",
			},
		},
	})
	if _, err := d.Client.AcknowledgePractices(t.Context(), api.PracticesAcknowledge{
		Repository: "widgets", Finding: "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f", Reason: "a token the parser tests read",
	}); err != nil {
		t.Fatal(err)
	}
	list, err := d.Client.Practices(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "practices", steady(pretty(t, list)))
	holdsGolden(t, "practices-checked", steady(pretty(t, checked)))
}

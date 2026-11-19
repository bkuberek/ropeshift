package api_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/practices"
)

// A repository no check has reported reads with its name and ID and nothing else.
func TestAPracticesViewOfARepositoryNoCheckReportedHoldsNoResult(t *testing.T) {
	view := api.PracticesViewOf(ledger.Repository{ID: "repo-a", Name: "widgets"}, nil, nil, 0)
	if view.Repository != "repo-a" || view.Name != "widgets" || view.Result != nil || view.Request != nil || len(view.Acknowledgement) != 0 {
		t.Errorf("the view is %+v", view)
	}
}

// Each practice and each finding of a result reads with the table's why and fix, its template, and the command that
// prints the template, which is empty for a practice with none.
func TestAPracticesViewCarriesTheTablesWordsForEachPracticeAndFinding(t *testing.T) {
	checked := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	record := ledger.PracticesRecord{
		Repository: "repo-a", Request: "req-1", Commit: "abc", CheckedAt: checked.UnixMilli(),
		Result: practices.Result{
			Repository: "repo-a", Commit: "abc", BaseBranch: "main", Worker: "w", CheckedAt: checked, TableVersion: 1, RulesVersion: 1, Skipped: 2,
			Practices: []practices.PracticeStatus{
				{ID: practices.EnvIgnored, Status: practices.StatusFinding},
				{ID: practices.Secrets, Status: practices.StatusUnknown, Reason: "the scan could not run"},
			},
			Findings: []practices.Finding{{ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored"}},
		},
	}
	view := api.PracticesViewOf(ledger.Repository{ID: "repo-a", Name: "widgets"}, &record, nil, 0)
	if view.Result == nil || view.Result.Request != "req-1" || view.Result.Commit != "abc" || view.Result.Skipped != 2 || !view.Result.CheckedAt.Equal(checked) {
		t.Fatalf("the result reads %+v", view.Result)
	}
	env, secrets := view.Result.Practices[0], view.Result.Practices[1]
	if env.Severity != practices.High || env.Why == "" || env.Fix == "" || env.Template != "repository/gitignore" || env.Command != "ropeshift template show repository/gitignore" {
		t.Errorf("env-ignored reads %+v", env)
	}
	if secrets.Status != practices.StatusUnknown || secrets.Reason != "the scan could not run" || secrets.Template != "" || secrets.Command != "" {
		t.Errorf("secrets reads %+v, want its reason and no template or command", secrets)
	}
	finding := view.Result.Findings[0]
	if finding.ID != "env-ignored:ignore:." || finding.Why != env.Why || finding.Fix != env.Fix || finding.Command != env.Command || finding.Detail == "" {
		t.Errorf("the finding reads %+v", finding)
	}
}

// The open request reads with the state it stands in at the time of the read.
func TestAPracticesViewSaysTheStateOfTheOpenRequest(t *testing.T) {
	request := ledger.PracticesRequest{ID: "req-1", Repository: "repo-a", Worker: "w", Cause: ledger.PracticesMember, RequestedAt: 1000, ExpiresAt: 2000, State: ledger.PracticesOffered}
	if got := api.PracticesViewOf(ledger.Repository{ID: "repo-a"}, nil, &request, 1500).Request; got == nil || got.State != ledger.PracticesOffered {
		t.Errorf("before its expiry the request reads %+v", got)
	}
	if got := api.PracticesRequestViewOf(request, 2000); got.State != ledger.PracticesExpired {
		t.Errorf("at its expiry the request reads %+v", got)
	}
}

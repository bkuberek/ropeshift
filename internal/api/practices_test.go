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
	view := api.PracticesViewOf(ledger.Repository{ID: "repo-a", Name: "widgets"}, nil, nil, nil, 0, nil)
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
			Partial: "the repository is a shallow clone",
			Practices: []practices.PracticeStatus{
				{ID: practices.EnvIgnored, Status: practices.StatusFinding},
				{ID: practices.Secrets, Status: practices.StatusUnknown, Reason: "the scan could not run"},
			},
			Findings: []practices.Finding{{ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored"}},
		},
	}
	view := api.PracticesViewOf(ledger.Repository{ID: "repo-a", Name: "widgets"}, &record, nil, nil, 0, nil)
	if view.Result == nil || view.Result.Request != "req-1" || view.Result.Commit != "abc" || view.Result.Skipped != 2 || view.Result.Partial != "the repository is a shallow clone" || !view.Result.CheckedAt.Equal(checked) {
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
	if got := api.PracticesViewOf(ledger.Repository{ID: "repo-a"}, nil, &request, nil, 1500, nil).Request; got == nil || got.State != ledger.PracticesOffered {
		t.Errorf("before its expiry the request reads %+v", got)
	}
	if got := api.PracticesRequestViewOf(request, 2000, nil); got.State != ledger.PracticesExpired {
		t.Errorf("at its expiry the request reads %+v", got)
	}
}

// A finding a member acknowledged stays in the result's list, read with the acknowledgement and marked acknowledged,
// and its practice stays a finding: nothing is filtered out, and a practice's status says what the check found. An
// acknowledgement of a finding the result does not hold is listed beside it.
func TestAPracticesViewKeepsAnAcknowledgedFindingAndMarksIt(t *testing.T) {
	checked := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	record := ledger.PracticesRecord{
		Repository: "repo-a", Request: "req-1", Commit: "abc", CheckedAt: checked.UnixMilli(),
		Result: practices.Result{
			Repository: "repo-a", Commit: "abc", BaseBranch: "main", Worker: "w", CheckedAt: checked, TableVersion: 1, RulesVersion: 1,
			Practices: []practices.PracticeStatus{{ID: practices.EnvIgnored, Status: practices.StatusFinding}, {ID: practices.Codeowners, Status: practices.StatusFinding}},
			Findings: []practices.Finding{
				{ID: "env-ignored:ignore:.", Practice: practices.EnvIgnored, Subject: "ignore:.", Severity: practices.High, Detail: "`.env` is not ignored"},
				{ID: "codeowners", Practice: practices.Codeowners, Severity: practices.Low, Detail: "no CODEOWNERS file"},
			},
		},
	}
	at := time.Date(2026, 11, 18, 10, 15, 0, 0, time.UTC)
	acks := []ledger.PracticesAcknowledgement{
		{Repository: "repo-a", Finding: "codeowners", Reason: "owned by the platform team", Member: "member-1", AcknowledgedAt: at.UnixMilli()},
		{Repository: "repo-a", Finding: "lockfile:npm", Reason: "not in this result", Member: "member-2", AcknowledgedAt: at.UnixMilli()},
	}
	view := api.PracticesViewOf(ledger.Repository{ID: "repo-a", Name: "widgets"}, &record, nil, acks, 0, nil)
	if len(view.Result.Findings) != 2 {
		t.Fatalf("the result lists %d findings, want both: an acknowledged finding is kept", len(view.Result.Findings))
	}
	open, kept := view.Result.Findings[0], view.Result.Findings[1]
	if open.Acknowledged || open.Acknowledgement != nil {
		t.Errorf("the open finding reads %+v", open)
	}
	want := api.PracticesAcknowledgement{Finding: "codeowners", Reason: "owned by the platform team", Member: "member-1", AcknowledgedAt: at}
	if !kept.Acknowledged || kept.Acknowledgement == nil || *kept.Acknowledgement != want {
		t.Errorf("the acknowledged finding reads %+v, want acknowledged with %+v", kept, want)
	}
	if status := view.Result.Practices[1]; status.ID != practices.Codeowners || status.Status != practices.StatusFinding {
		t.Errorf("the practice reads %+v, want it still a finding", status)
	}
	if len(view.Acknowledgement) != 2 || view.Acknowledgement[0] != want || view.Acknowledgement[1].Finding != "lockfile:npm" {
		t.Errorf("the acknowledgements read %+v, want each one, by finding as given", view.Acknowledgement)
	}
}

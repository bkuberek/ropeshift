package practices_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/practices"
)

const (
	firstCommit  = "1111111111111111111111111111111111111111"
	secondCommit = "2222222222222222222222222222222222222222"
)

var leaked = practices.SecretMatch{Rule: "github_token", Path: "config/dev.yaml", Line: 3, Commit: "aaaaaaaaaaaa", Digest: "1a2b3c4d5e6f7a8b9c0d"}

// checked is a result of a checkout at a commit, from a scan that started at from, that found the matches.
func checked(commit, from string, unknown string, matches ...practices.SecretMatch) practices.Result {
	in := practices.Input{
		Repository: "repo", Commit: commit, BaseBranch: "main", Worker: "w1", RulesVersion: 1, ScannedFrom: from,
		CheckedAt: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC), Secrets: matches, SecretsUnknown: unknown,
		Paths: []string{"SECURITY.md"}, Read: func(string) (string, bool) { return "", false },
	}
	return practices.Evaluate(in)
}

func findingsOf(r practices.Result, id practices.ID) []string {
	var ids []string
	for _, f := range r.Findings {
		if f.Practice == id {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

func secretsOf(t *testing.T, r practices.Result) practices.PracticeStatus {
	t.Helper()
	for _, p := range r.Practices {
		if p.ID == practices.Secrets {
			return p
		}
	}
	t.Fatal("the result has no status for secrets")
	return practices.PracticeStatus{}
}

// An incremental scan reads only the commits after the one the earlier scan ended at, so the secrets the earlier
// result found stay findings: a commit that added one keeps it in the history.
func TestCarryForwardKeepsTheEarlierSecretsOfAnIncrementalScan(t *testing.T) {
	previous := checked(firstCommit, "", "", leaked)
	next := checked(secondCommit, firstCommit, "")
	if got := secretsOf(t, next).Status; got != practices.StatusMet {
		t.Fatalf("a scan that found nothing is %s", got)
	}
	got := practices.CarryForward(previous, next)
	if status := secretsOf(t, got); status.Status != practices.StatusFinding || status.Reason != "" {
		t.Errorf("secrets are %+v, want a finding", status)
	}
	if want := findingsOf(previous, practices.Secrets); !slices.Equal(findingsOf(got, practices.Secrets), want) || len(want) != 1 {
		t.Errorf("the secrets findings are %v, want %v", findingsOf(got, practices.Secrets), want)
	}
	if got.Commit != secondCommit || got.ScannedFrom != firstCommit {
		t.Errorf("the result reads %s from %s, want the new scan's", got.Commit, got.ScannedFrom)
	}
	if ids := findingsOf(next, practices.Secrets); len(ids) != 0 || secretsOf(t, next).Status != practices.StatusMet {
		t.Errorf("carrying changed the result it was given: %v", ids)
	}
}

// A secret the new scan finds again is one finding, and one it finds besides is added.
func TestCarryForwardDoesNotDoubleAFindingTheScanFindsAgain(t *testing.T) {
	other := practices.SecretMatch{Rule: "npm_token", Path: ".npmrc", Line: 1, Commit: "bbbbbbbbbbbb", Digest: "ffeeddccbbaa99887766"}
	previous := checked(firstCommit, "", "", leaked)
	next := checked(secondCommit, firstCommit, "", leaked, other)
	got := practices.CarryForward(previous, next)
	if ids := findingsOf(got, practices.Secrets); len(ids) != 2 {
		t.Errorf("the secrets findings are %v, want the two once each", ids)
	}
}

// Only the secrets of the earlier result carry, and only into a scan that continued from its commit.
func TestCarryForwardCarriesOnlySecretsOfTheResultTheScanContinuedFrom(t *testing.T) {
	previous := checked(firstCommit, "", "", leaked)
	previous.Findings = append(previous.Findings, practices.Finding{ID: "codeowners", Practice: practices.Codeowners, Severity: practices.Low})
	for name, tt := range map[string]struct {
		next practices.Result
		want int
	}{
		"a full scan":                     {checked(secondCommit, "", ""), 0},
		"a scan from another commit":      {checked(secondCommit, "3333333333333333333333333333333333333333", ""), 0},
		"a scan that could not run":       {checked(secondCommit, firstCommit, "the secret scan failed"), 0},
		"an incremental scan that is met": {checked(secondCommit, firstCommit, ""), 1},
	} {
		t.Run(name, func(t *testing.T) {
			got := practices.CarryForward(previous, tt.next)
			if ids := findingsOf(got, practices.Secrets); len(ids) != tt.want {
				t.Errorf("the secrets findings are %v, want %d", ids, tt.want)
			}
			if ids := findingsOf(got, practices.Codeowners); len(ids) != len(findingsOf(tt.next, practices.Codeowners)) {
				t.Errorf("a finding of another practice was carried: %v", ids)
			}
		})
	}
	unknown := checked(secondCommit, firstCommit, "the secret scan failed")
	if got := secretsOf(t, practices.CarryForward(previous, unknown)); got.Status != practices.StatusUnknown || got.Reason == "" {
		t.Errorf("secrets are %+v, want unknown with its reason", got)
	}
}

// A result whose scan did not read every commit cannot say its history holds no secret: a met practice reads unknown
// with the reason, a finding stays a finding, and nothing else changes.
func TestSecretsUnverifiedReadsAMetPracticeUnknownAndKeepsAFinding(t *testing.T) {
	met := checked(secondCommit, firstCommit, "")
	got := practices.SecretsUnverified(practices.Result{}, met, "the commits between were read by no scan")
	if status := secretsOf(t, got); status.Status != practices.StatusUnknown || status.Reason != "the commits between were read by no scan" {
		t.Errorf("secrets are %+v, want unknown with the reason", status)
	}
	if status := secretsOf(t, met); status.Status != practices.StatusMet {
		t.Errorf("the result it was given reads %+v", status)
	}
	found := checked(secondCommit, firstCommit, "", leaked)
	if status := secretsOf(t, practices.SecretsUnverified(practices.Result{}, found, "why")); status.Status != practices.StatusFinding || status.Reason != "" {
		t.Errorf("a finding reads %+v, want it kept", status)
	}
	unknown := checked(secondCommit, firstCommit, "the scan failed")
	if status := secretsOf(t, practices.SecretsUnverified(practices.Result{}, unknown, "why")); status.Status != practices.StatusUnknown || status.Reason != "the scan failed" {
		t.Errorf("an unknown practice reads %+v, want it as it was", status)
	}
}

// A result that cannot say its history holds no secret still holds the secrets the result it would have continued from
// found, with the IDs and commits they had: a secret that was found stays found. The practice reads a finding when any
// secret is held, and unknown with the reason when none is.
func TestSecretsUnverifiedKeepsTheSecretsOfTheKeptResult(t *testing.T) {
	previous := checked(firstCommit, "", "", leaked)
	want := findingsOf(previous, practices.Secrets)
	if len(want) != 1 {
		t.Fatalf("the kept result holds %v", want)
	}
	for name, next := range map[string]practices.Result{
		"a met practice":      checked(secondCommit, "3333333333333333333333333333333333333333", ""),
		"an unknown practice": checked(secondCommit, "3333333333333333333333333333333333333333", "the scan failed"),
	} {
		t.Run(name, func(t *testing.T) {
			got := practices.SecretsUnverified(previous, next, "the commits between were read by no scan")
			if status := secretsOf(t, got); status.Status != practices.StatusFinding || status.Reason != "" {
				t.Errorf("secrets are %+v, want a finding", status)
			}
			if ids := findingsOf(got, practices.Secrets); !slices.Equal(ids, want) {
				t.Errorf("the secrets findings are %v, want %v", ids, want)
			}
			for _, f := range got.Findings {
				if f.Practice == practices.Secrets && f.Commit != leaked.Commit {
					t.Errorf("the finding %s is of the commit %s, want it kept as %s", f.ID, f.Commit, leaked.Commit)
				}
			}
			if got.Commit != secondCommit {
				t.Errorf("the result reads %s, want the new scan's", got.Commit)
			}
			if ids := findingsOf(next, practices.Secrets); len(ids) != 0 {
				t.Errorf("the result it was given gained %v", ids)
			}
		})
	}
	other := practices.SecretMatch{Rule: "npm_token", Path: ".npmrc", Line: 1, Commit: "bbbbbbbbbbbb", Digest: "ffeeddccbbaa99887766"}
	found := checked(secondCommit, "3333333333333333333333333333333333333333", "", leaked, other)
	if ids := findingsOf(practices.SecretsUnverified(previous, found, "why"), practices.Secrets); len(ids) != 2 {
		t.Errorf("the secrets findings are %v, want each once", ids)
	}
	clean := checked(secondCommit, "3333333333333333333333333333333333333333", "")
	if status := secretsOf(t, practices.SecretsUnverified(checked(firstCommit, "", ""), clean, "why")); status.Status != practices.StatusUnknown || status.Reason != "why" {
		t.Errorf("with no secret held the practice reads %+v, want unknown with the reason", status)
	}
	previous.Truncated = true
	if got := practices.SecretsUnverified(previous, clean, "why"); !got.Truncated {
		t.Errorf("the result does not say the kept result left findings out")
	}
}

package practices_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/practices"
)

// leaks is n distinct secret matches, each in a file of its own.
func leaks(n int) []practices.SecretMatch {
	out := make([]practices.SecretMatch, 0, n)
	for i := range n {
		out = append(out, practices.SecretMatch{
			Rule: "github_token", Path: fmt.Sprintf("config/%04d.yaml", i), Line: 1, Commit: "aaaaaaaaaaaa",
			Digest: fmt.Sprintf("%012x%08x", i, i),
		})
	}
	return out
}

// A result holds at most MaxSecretFindings secret findings, however many the scan matched, so a report never outgrows
// the route it travels, and it says that findings were left out. The practice reads finding, never met.
func TestEvaluateCapsTheSecretFindingsAndSaysSo(t *testing.T) {
	for name, tt := range map[string]struct {
		matches   int
		want      int
		truncated bool
	}{
		"under the cap":    {practices.MaxSecretFindings - 1, practices.MaxSecretFindings - 1, false},
		"at the cap":       {practices.MaxSecretFindings, practices.MaxSecretFindings, false},
		"over the cap":     {practices.MaxSecretFindings + 50, practices.MaxSecretFindings, true},
		"far over the cap": {5000, practices.MaxSecretFindings, true},
	} {
		t.Run(name, func(t *testing.T) {
			result := checked(secondCommit, "", "", leaks(tt.matches)...)
			got := findingsOf(result, practices.Secrets)
			if len(got) != tt.want || result.Truncated != tt.truncated {
				t.Errorf("%d secret findings, truncated %v; want %d, %v", len(got), result.Truncated, tt.want, tt.truncated)
			}
			if status := secretsOf(t, result); status.Status != practices.StatusFinding {
				t.Errorf("secrets are %+v, want a finding", status)
			}
			if !slices.IsSorted(got) {
				t.Errorf("the kept findings are not the first by ID")
			}
		})
	}
	raw, err := json.Marshal(checked(secondCommit, "", ""))
	if err != nil || strings.Contains(string(raw), "truncated") {
		t.Errorf("a result that left nothing out says %s (%v)", raw, err)
	}
}

// Carrying the earlier secrets into an incremental result keeps the cap, and the flag stays once findings were left out.
func TestCarryForwardKeepsTheCapAndTheFlag(t *testing.T) {
	previous := checked(firstCommit, "", "", leaks(practices.MaxSecretFindings+10)...)
	if !previous.Truncated {
		t.Fatal("the earlier result left nothing out")
	}
	next := checked(secondCommit, firstCommit, "", practices.SecretMatch{Rule: "npm_token", Path: ".npmrc", Line: 1, Commit: "bbbbbbbbbbbb", Digest: "ffeeddccbbaa99887766"})
	got := practices.CarryForward(previous, next)
	if n := len(findingsOf(got, practices.Secrets)); n != practices.MaxSecretFindings || !got.Truncated {
		t.Errorf("%d secret findings, truncated %v; want %d, true", n, got.Truncated, practices.MaxSecretFindings)
	}
	fresh := practices.CarryForward(checked(firstCommit, "", "", leaks(3)...), checked(secondCommit, firstCommit, ""))
	if n := len(findingsOf(fresh, practices.Secrets)); n != 3 || fresh.Truncated {
		t.Errorf("%d secret findings, truncated %v; want 3, false", n, fresh.Truncated)
	}
}

// Bound is what the control plane does to a result it was sent: it clips the text of each reason and detail to one
// line of at most MaxText bytes on a rune boundary, cuts every other field there, and caps the secrets, whatever the
// worker did.
func TestBoundClipsTheTextAndCapsTheFindings(t *testing.T) {
	long := strings.Repeat("€", 3000)
	result := checked(secondCommit, "", "", leaks(practices.MaxSecretFindings)...)
	// A worker that did not cap what it found.
	for i := range 5 {
		result.Findings = append(result.Findings, practices.Finding{
			ID: fmt.Sprintf("secrets:github_token:extra/%d.yaml:abcdefabcdef", i), Practice: practices.Secrets, Severity: practices.High,
		})
	}
	result.Practices[0].Reason = "line one\nline two " + long
	result.Findings[0].Detail = long
	result.Findings[0].Path = long
	result.Worker = long
	got := practices.Bound(result)
	if len(got.Practices[0].Reason) > practices.MaxText || strings.ContainsAny(got.Practices[0].Reason, "\r\n") || !utf8.ValidString(got.Practices[0].Reason) ||
		!strings.HasPrefix(got.Practices[0].Reason, "line one line two ") {
		t.Errorf("the reason is %d bytes: %.40q", len(got.Practices[0].Reason), got.Practices[0].Reason)
	}
	for name, text := range map[string]string{"detail": got.Findings[0].Detail, "path": got.Findings[0].Path, "worker": got.Worker} {
		if len(text) > practices.MaxText || !utf8.ValidString(text) {
			t.Errorf("the %s is %d bytes, valid UTF-8 %v", name, len(text), utf8.ValidString(text))
		}
	}
	if n := len(findingsOf(got, practices.Secrets)); n != practices.MaxSecretFindings || !got.Truncated {
		t.Errorf("%d secret findings, truncated %v; want %d, true", n, got.Truncated, practices.MaxSecretFindings)
	}
	if len(result.Practices[0].Reason) <= practices.MaxText || result.Truncated {
		t.Error("bounding changed the result it was given")
	}
}

// Two finding IDs that differ only past MaxText bytes stay two IDs when they are clipped, a clipped ID stays within the
// bound and is the same each time it is clipped, and an ID that fits is left as it is.
func TestBoundKeepsClippedFindingIDsDistinctAndStable(t *testing.T) {
	shared := strings.Repeat("a", practices.MaxText)
	short := "secrets:github_token:config/dev.yaml:1a2b3c4d5e6f"
	multibyte := strings.Repeat("€", 1000) + "x"
	result := practices.Result{Findings: []practices.Finding{
		{ID: shared + "-one", Practice: practices.EnvIgnored, Severity: practices.High},
		{ID: shared + "-two", Practice: practices.EnvIgnored, Severity: practices.High},
		{ID: short, Practice: practices.Secrets, Severity: practices.High},
		{ID: multibyte, Practice: practices.EnvIgnored, Severity: practices.High},
		{ID: strings.Repeat("a", practices.MaxText), Practice: practices.EnvIgnored, Severity: practices.High},
	}}
	got := practices.Bound(result)
	if got.Findings[0].ID == got.Findings[1].ID {
		t.Fatalf("two IDs that share their first %d bytes collide: %.40q", practices.MaxText, got.Findings[0].ID)
	}
	for i, f := range got.Findings {
		if len(f.ID) > practices.MaxText || !utf8.ValidString(f.ID) {
			t.Errorf("the ID of finding %d is %d bytes, valid UTF-8 %v", i, len(f.ID), utf8.ValidString(f.ID))
		}
	}
	if got.Findings[2].ID != short || got.Findings[4].ID != result.Findings[4].ID {
		t.Errorf("an ID that fits was changed: %q, %.20q", got.Findings[2].ID, got.Findings[4].ID)
	}
	if got.Findings[3].ID == multibyte || len(got.Findings[3].ID) > practices.MaxText {
		t.Errorf("the multibyte ID was not clipped within the bound: %d bytes", len(got.Findings[3].ID))
	}
	again := practices.Bound(result)
	for i := range got.Findings {
		if got.Findings[i].ID != again.Findings[i].ID {
			t.Errorf("clipping the ID of finding %d twice gives %.40q and %.40q", i, got.Findings[i].ID, again.Findings[i].ID)
		}
	}
	if once := practices.Bound(got); once.Findings[0].ID != got.Findings[0].ID || once.Findings[1].ID != got.Findings[1].ID {
		t.Error("bounding a bounded result changed a clipped ID")
	}
}

// Validate refuses a result no worker makes: a practice or a status or a severity the tables do not hold, a practice
// twice or none, and a finding of no practice.
func TestValidateRefusesAResultNoWorkerMakes(t *testing.T) {
	good := checked(secondCommit, "", "")
	if err := good.Validate(); err != nil {
		t.Fatalf("a result of Evaluate is refused: %v", err)
	}
	for name, change := range map[string]func(*practices.Result){
		"an unknown practice": func(r *practices.Result) { r.Practices[0].ID = "tabs" },
		"an unknown status":   func(r *practices.Result) { r.Practices[0].Status = "mostly" },
		"a practice twice":    func(r *practices.Result) { r.Practices = append(r.Practices, r.Practices[0]) },
		"a practice missing":  func(r *practices.Result) { r.Practices = r.Practices[1:] },
		"no secrets": func(r *practices.Result) {
			r.Practices = slices.DeleteFunc(r.Practices, func(p practices.PracticeStatus) bool { return p.ID == practices.Secrets })
		},
		"secrets twice": func(r *practices.Result) {
			r.Practices = append(r.Practices, practices.PracticeStatus{ID: practices.Secrets, Status: practices.StatusMet})
		},
		"a finding of no practice": func(r *practices.Result) {
			r.Findings = append(r.Findings, practices.Finding{ID: "x", Practice: "tabs", Severity: practices.Low})
		},
		"an unknown severity": func(r *practices.Result) {
			r.Findings = append(r.Findings, practices.Finding{ID: "codeowners", Practice: practices.Codeowners, Severity: "grave"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := checked(secondCommit, "", "")
			bad.Findings = slices.Clone(bad.Findings)
			bad.Practices = slices.Clone(bad.Practices)
			change(&bad)
			if err := bad.Validate(); err == nil {
				t.Error("the result was accepted")
			}
		})
	}
}

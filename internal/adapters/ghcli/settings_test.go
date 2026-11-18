package ghcli

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

const (
	repoPath    = "repos/acme/widgets"
	branchPath  = repoPath + "/branches/main"
	rulesPath   = repoPath + "/rules/branches/main"
	reviewsPath = branchPath + "/protection/required_pull_request_reviews"
	// resetAt is 2100-01-01T00:00:00Z, a window's end that is always ahead.
	resetAt     = 4102444800
	resetAtText = "2100-01-01T00:00:00Z"
)

// readSettings changes the fake forge, reads the settings of the base branch `main`, and returns the facts, the
// fake's directory, and the error.
func readSettings(t *testing.T, change func(*ghstate.State)) (practices.ForgeFacts, string, error) {
	t.Helper()
	forge, repo, gh, _ := fixture(t)
	doubles.EditFakeGH(t, gh, change)
	facts, err := forge.Settings(t.Context(), repo, "main")
	return facts, gh, err
}

func settingsCalls(t *testing.T, gh string) []string {
	t.Helper()
	var paths []string
	for _, args := range callsWith(t, gh, "api") {
		paths = append(paths, args[len(args)-1])
	}
	return paths
}

func pullRequestRule(count int) ghstate.Rule {
	return ghstate.Rule{Type: "pull_request", Parameters: map[string]any{"required_approving_review_count": count}}
}

func refused(status int, message string) ghstate.SettingsAnswer {
	return ghstate.SettingsAnswer{Status: status, Message: message}
}

// The settings are read with `gh api` under the person's sign-in, from the binding's owner and repository, in the
// order PD11 gives, and a branch that is not protected has no protection read.
func TestSettingsReadsTheEndpointsOfTheBinding(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) { s.Visibility, s.Protected, s.ReviewCount = "public", true, 1 })
	if err != nil {
		t.Fatal(err)
	}
	want := practices.ForgeFacts{Visibility: practices.VisibilityPublic, Protected: true, ReviewCount: 1}
	if facts != want {
		t.Fatalf("the facts are %+v, want %+v", facts, want)
	}
	if got, wantPaths := settingsCalls(t, gh), []string{repoPath, branchPath, rulesPath, reviewsPath}; !slices.Equal(got, wantPaths) {
		t.Errorf("gh read %q, want %q", got, wantPaths)
	}
	for _, call := range doubles.FakeGHCalls(t, gh) {
		if !slices.Equal(call.Args[:4], []string{"api", "--include", "--hostname", "github.com"}) {
			t.Errorf("gh ran %q, which is no read of the host's API", call.Args)
		}
	}
}

// The visibility rows of the outcome table.
func TestSettingsReadsTheVisibility(t *testing.T) {
	for _, tt := range []struct {
		visibility string
		want       practices.Visibility
	}{{"public", practices.VisibilityPublic}, {"private", practices.VisibilityPrivate}, {"internal", practices.VisibilityInternal}} {
		t.Run(tt.visibility, func(t *testing.T) {
			facts, _, err := readSettings(t, func(s *ghstate.State) { s.Visibility = tt.visibility })
			if err != nil || facts.Visibility != tt.want || facts.VisibilityUnknown != "" {
				t.Fatalf("visibility %q reads %+v: %v", tt.visibility, facts, err)
			}
		})
	}
}

// A visibility the table does not name is no visibility, and no finding.
func TestSettingsDoesNotGuessAVisibility(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) { s.Visibility = "secret" })
	if err != nil || facts.Visibility != "" || !strings.Contains(facts.VisibilityUnknown, `"secret"`) {
		t.Fatalf("an unknown visibility reads %+v: %v", facts, err)
	}
}

// Both reads answer 200 and neither says protected: the branch is not protected, the forge settled that no review is
// required, and nothing is unknown (the table's `finding` rows).
func TestSettingsReadsAnUnprotectedBranch(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) { s.Visibility = "public" })
	if err != nil || facts != (practices.ForgeFacts{Visibility: practices.VisibilityPublic}) {
		t.Fatalf("an unprotected branch reads %+v: %v", facts, err)
	}
	if got := settingsCalls(t, gh); !slices.Equal(got, []string{repoPath, branchPath, rulesPath}) {
		t.Errorf("gh read %q, but an unprotected branch has no protection to read", got)
	}
}

// Branch protection alone: the branch read says protected, the rules list is empty, and the protection read gives the
// count.
func TestSettingsReadsClassicProtection(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("%d reviews", count), func(t *testing.T) {
			facts, _, err := readSettings(t, func(s *ghstate.State) { s.Protected, s.ReviewCount = true, count })
			want := practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true, ReviewCount: count}
			if err != nil || facts != want {
				t.Fatalf("classic protection with %d reviews reads %+v: %v", count, facts, err)
			}
		})
	}
}

// A 404 whose message holds "not enabled" is a protection with no required reviews.
func TestSettingsReadsProtectionWithNoReviewRule(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) {
		s.Protected = true
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{reviewsPath: refused(404, "Pull request reviews not enabled")}
	})
	want := practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true}
	if err != nil || facts != want {
		t.Fatalf("a protection with reviews not enabled reads %+v: %v", facts, err)
	}
}

// Ruleset protection alone: any rule is protected, the branch read says it is not, and a ruleset with no pull request
// rule requires no review, with no protection read to make.
func TestSettingsReadsRulesetProtection(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) {
		s.Rules = []ghstate.Rule{{Type: "deletion"}, {Type: "non_fast_forward"}}
	})
	want := practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true}
	if err != nil || facts != want {
		t.Fatalf("a ruleset with no review rule reads %+v: %v", facts, err)
	}
	if got := settingsCalls(t, gh); !slices.Equal(got, []string{repoPath, branchPath, rulesPath}) {
		t.Errorf("gh read %q, but no classic protection exists to read", got)
	}
}

// A pull request rule with a count of at least 1 meets the review, and the largest count of the rules is the count.
func TestSettingsReadsARulesetsReviewCount(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) {
		s.Rules = []ghstate.Rule{{Type: "deletion"}, pullRequestRule(1), pullRequestRule(3)}
	})
	want := practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true, ReviewCount: 3}
	if err != nil || facts != want {
		t.Fatalf("a ruleset requiring reviews reads %+v: %v", facts, err)
	}
	if got := settingsCalls(t, gh); !slices.Equal(got, []string{repoPath, branchPath, rulesPath}) {
		t.Errorf("gh read %q, but the ruleset settled the review", got)
	}
}

// A pull request rule that asks for no approval is protection and no review.
func TestSettingsReadsAPullRequestRuleOfNoApprovals(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) { s.Rules = []ghstate.Rule{pullRequestRule(0)} })
	want := practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true}
	if err != nil || facts != want {
		t.Fatalf("a rule of no approvals reads %+v: %v", facts, err)
	}
}

// A ruleset that requires a review needs no protection read, so a protection read the forge refuses does not matter.
func TestSettingsTakesTheRulesetsReviewWithoutTheProtectionRead(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) {
		s.Protected, s.Rules = true, []ghstate.Rule{pullRequestRule(1)}
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{reviewsPath: refused(403, "Resource not accessible")}
	})
	want := practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true, ReviewCount: 1}
	if err != nil || facts != want {
		t.Fatalf("reads %+v: %v", facts, err)
	}
}

// The protection read refused with a 403: the branch is protected, and the review is unknown, never a finding.
func TestSettingsLeavesTheReviewUnknownWhenTheProtectionReadIsRefused(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) {
		s.Protected = true
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{reviewsPath: refused(403, "Resource not accessible by integration")}
	})
	if err != nil || !facts.Protected || facts.ProtectedUnknown != "" || facts.ReviewCount != 0 {
		t.Fatalf("a refused protection read reads %+v: %v", facts, err)
	}
	if !strings.Contains(facts.ReviewUnknown, "HTTP 403") || !strings.Contains(facts.ReviewUnknown, "Resource not accessible by integration") {
		t.Errorf("the review is unknown for %q, which omits the forge's message", facts.ReviewUnknown)
	}
}

// Either read that says protected makes the branch protected, though the other read failed.
func TestSettingsIsProtectedWhenEitherReadSaysSoAndTheOtherFailed(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*ghstate.State)
	}{
		{"the rules failed", func(s *ghstate.State) {
			s.Protected = true
			s.SettingsStatus = map[string]ghstate.SettingsAnswer{rulesPath: refused(403, "Resource not accessible")}
		}},
		{"the branch failed", func(s *ghstate.State) {
			s.Rules = []ghstate.Rule{{Type: "deletion"}}
			s.SettingsStatus = map[string]ghstate.SettingsAnswer{branchPath: refused(401, "Bad credentials")}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			facts, _, err := readSettings(t, tt.change)
			if err != nil || !facts.Protected || facts.ProtectedUnknown != "" {
				t.Fatalf("reads %+v: %v", facts, err)
			}
		})
	}
}

// With the rules unread, a protection that requires no review does not settle that no ruleset requires one, so the
// review stays unknown; a protection that requires one settles it.
func TestSettingsDoesNotSettleNoReviewWhileTheRulesAreUnread(t *testing.T) {
	rules := map[string]ghstate.SettingsAnswer{rulesPath: refused(403, "Resource not accessible")}
	facts, _, err := readSettings(t, func(s *ghstate.State) { s.Protected, s.SettingsStatus = true, rules })
	if err != nil || !facts.Protected || facts.ReviewCount != 0 || !strings.Contains(facts.ReviewUnknown, "HTTP 403") {
		t.Fatalf("a count of 0 with the rules unread reads %+v: %v", facts, err)
	}
	facts, _, err = readSettings(t, func(s *ghstate.State) { s.Protected, s.ReviewCount, s.SettingsStatus = true, 2, rules })
	if err != nil || facts.ReviewCount != 2 || facts.ReviewUnknown != "" {
		t.Fatalf("a count of 2 with the rules unread reads %+v: %v", facts, err)
	}
}

// Every error path of a read sets the unknown reason of the fact it decides, with the forge's status and message.
func TestSettingsSetsTheUnknownReasonOfEachFailedRead(t *testing.T) {
	failures := []struct {
		name     string
		answer   ghstate.SettingsAnswer
		contains string
	}{
		{"unauthorized", refused(401, "Bad credentials"), "HTTP 401: Bad credentials"},
		{"forbidden", refused(403, "Resource not accessible by personal access token"), "HTTP 403: Resource not accessible"},
		{"not found", refused(404, "Not Found"), "HTTP 404: Not Found"},
		{"server error", refused(500, "Server Error"), "HTTP 500: Server Error"},
	}
	paths := []struct {
		name   string
		path   string
		change func(*ghstate.State)
		check  func(t *testing.T, facts practices.ForgeFacts, reason string)
	}{
		{"the repository read", repoPath, func(*ghstate.State) {}, func(t *testing.T, facts practices.ForgeFacts, reason string) {
			if facts.Visibility != "" || facts.VisibilityUnknown != reason || facts.ProtectedUnknown != "" || facts.ReviewUnknown != "" {
				t.Errorf("the repository read left %+v, want only the visibility unknown as %q", facts, reason)
			}
		}},
		{"the branch read", branchPath, func(*ghstate.State) {}, func(t *testing.T, facts practices.ForgeFacts, reason string) {
			if facts.Protected || facts.ProtectedUnknown != reason || facts.ReviewUnknown != reason || facts.VisibilityUnknown != "" {
				t.Errorf("the branch read left %+v, want protection and review unknown as %q", facts, reason)
			}
		}},
		{"the rules read", rulesPath, func(*ghstate.State) {}, func(t *testing.T, facts practices.ForgeFacts, reason string) {
			if facts.Protected || facts.ProtectedUnknown != reason || facts.ReviewUnknown != reason || facts.VisibilityUnknown != "" {
				t.Errorf("the rules read left %+v, want protection and review unknown as %q", facts, reason)
			}
		}},
		{"the protection read", reviewsPath, func(s *ghstate.State) { s.Protected = true }, func(t *testing.T, facts practices.ForgeFacts, reason string) {
			if !facts.Protected || facts.ProtectedUnknown != "" || facts.ReviewCount != 0 || facts.ReviewUnknown != reason {
				t.Errorf("the protection read left %+v, want only the review unknown as %q", facts, reason)
			}
		}},
	}
	for _, path := range paths {
		for _, failure := range failures {
			t.Run(path.name+" "+failure.name, func(t *testing.T) {
				facts, _, err := readSettings(t, func(s *ghstate.State) {
					path.change(s)
					s.SettingsStatus = map[string]ghstate.SettingsAnswer{path.path: failure.answer}
				})
				if err != nil {
					t.Fatalf("a refused read is no error: %v", err)
				}
				reason := facts.VisibilityUnknown + facts.ProtectedUnknown
				if path.path == reviewsPath {
					reason = facts.ReviewUnknown
				}
				if !strings.Contains(reason, failure.contains) {
					t.Fatalf("the reason is %q, want it to hold %q", reason, failure.contains)
				}
				path.check(t, facts, reason)
			})
		}
	}
}

// The unknown reason is clipped as every message of the adapter is.
func TestSettingsClipsALongMessage(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) {
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{repoPath: refused(500, strings.Repeat("x", 3*maxDetail))}
	})
	if err != nil || len(facts.VisibilityUnknown) > maxDetail+len("HTTP 500: ") {
		t.Fatalf("a long message left a reason of %d bytes: %v", len(facts.VisibilityUnknown), err)
	}
}

// A `gh` that is not on PATH leaves every fact unknown, with the reason, and is no error.
func TestSettingsWithNoGHLeavesEveryFactUnknown(t *testing.T) {
	forge, repo, _, _ := fixture(t)
	forge.Path = filepath.Join(t.TempDir(), Binary)
	facts, err := forge.Settings(t.Context(), repo, "main")
	if err != nil {
		t.Fatalf("a missing gh is no error: %v", err)
	}
	for name, reason := range map[string]string{"visibility": facts.VisibilityUnknown, "protected": facts.ProtectedUnknown, "review": facts.ReviewUnknown} {
		if !strings.Contains(reason, "not on PATH") {
			t.Errorf("the %s is unknown for %q, want the missing gh", name, reason)
		}
	}
	if facts.Visibility != "" || facts.Protected || facts.ReviewCount != 0 {
		t.Errorf("a missing gh decided %+v", facts)
	}
}

// A primary rate limit sets every fact unknown with the window's end, makes no more reads, and returns the facts with
// an error that carries the rate, which the worker holds its reads for.
func TestSettingsRateLimitedLeavesTheFactsUnknownAndReturnsTheRate(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) { s.RateLimit = &ghstate.RateLimit{Exhausted: true, Reset: resetAt} })
	var limited *ports.RateLimitedError
	if !errors.Is(err, ports.ErrForgeRateLimited) || !errors.As(err, &limited) || limited.Rate.Reset.Unix() != resetAt || !limited.Rate.Exhausted {
		t.Fatalf("the error is %v, want one that wraps the rate limit with its reset", err)
	}
	reason := "rate limited until " + resetAtText
	want := practices.ForgeFacts{VisibilityUnknown: reason, ProtectedUnknown: reason, ReviewUnknown: reason}
	if facts != want {
		t.Errorf("the facts are %+v, want %+v", facts, want)
	}
	if got := settingsCalls(t, gh); len(got) != 1 {
		t.Errorf("gh read %q after the rate limit refused the first read", got)
	}
}

// A limit that arrives after some reads leaves what they settled, and the facts that remain unknown.
func TestSettingsRateLimitedAfterSomeReadsKeepsWhatTheyDecided(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) {
		s.Visibility, s.Protected = "public", true
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{reviewsPath: {
			Status: 403, Message: "API rate limit exceeded for user ID 1.",
			Headers: map[string]string{"X-Ratelimit-Remaining": "0", "X-Ratelimit-Reset": fmt.Sprint(resetAt)},
		}}
	})
	if !errors.Is(err, ports.ErrForgeRateLimited) {
		t.Fatalf("the error is %v, want the rate limit", err)
	}
	want := practices.ForgeFacts{Visibility: practices.VisibilityPublic, Protected: true, ReviewUnknown: "rate limited until " + resetAtText}
	if facts != want {
		t.Errorf("the facts are %+v, want %+v", facts, want)
	}
	if got := settingsCalls(t, gh); len(got) != 4 {
		t.Errorf("gh read %q, want the four reads", got)
	}
}

// A secondary limit asks for a wait, and the reason names the end of it.
func TestSettingsRateLimitedBySecondaryLimitNamesTheWait(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) { s.RateLimit = &ghstate.RateLimit{Secondary: true, RetryAfter: 90} })
	var limited *ports.RateLimitedError
	if !errors.As(err, &limited) || limited.Rate.RetryAfter.Seconds() != 90 || !limited.Rate.Secondary {
		t.Fatalf("the error is %v, want the secondary limit with its wait", err)
	}
	for name, reason := range map[string]string{"visibility": facts.VisibilityUnknown, "protected": facts.ProtectedUnknown, "review": facts.ReviewUnknown} {
		if !strings.HasPrefix(reason, "rate limited until 20") || !strings.HasSuffix(reason, "Z") {
			t.Errorf("the %s is unknown for %q, want a time", name, reason)
		}
	}
}

// A branch keeps its slashes in the path of each branch read, each segment escaped, and a name that is no branch is
// read as nothing, with the facts it decides unknown.
func TestSettingsReadsABranchWithSlashesAndNeverClimbs(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	facts, err := forge.Settings(t.Context(), repo, "release/1.2")
	if err != nil || facts.Protected {
		t.Fatalf("reads %+v: %v", facts, err)
	}
	want := []string{repoPath, repoPath + "/branches/release/1.2", repoPath + "/rules/branches/release/1.2"}
	if got := settingsCalls(t, gh); !slices.Equal(got, want) {
		t.Fatalf("gh read %q, want %q", got, want)
	}
	for _, branch := range []string{"../../x", "a//b", "a/./b", ""} {
		before := len(doubles.FakeGHCalls(t, gh))
		facts, err := forge.Settings(t.Context(), repo, branch)
		if err != nil || facts.ProtectedUnknown == "" || facts.ReviewUnknown == "" || facts.Protected {
			t.Errorf("branch %q reads %+v: %v", branch, facts, err)
		}
		if made := len(doubles.FakeGHCalls(t, gh)) - before; made != 1 {
			t.Errorf("branch %q made %d reads, want only the repository's", branch, made)
		}
	}
	if _, err := forge.Settings(t.Context(), repo, "main?x=1#y"); err != nil {
		t.Fatal(err)
	}
	calls := settingsCalls(t, gh)
	if last := calls[len(calls)-1]; last != repoPath+"/rules/branches/main%3Fx=1%23y" {
		t.Errorf("a branch with a question mark and a hash was read as %s", last)
	}
}

package ghcli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

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
	// rulesQuery is what the rules read asks for, and rulesCall is the address it is made at.
	rulesQuery = "?per_page=100"
	rulesCall  = rulesPath + rulesQuery
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
	if got, wantPaths := settingsCalls(t, gh), []string{repoPath, branchPath, rulesCall, reviewsPath}; !slices.Equal(got, wantPaths) {
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
	if got := settingsCalls(t, gh); !slices.Equal(got, []string{repoPath, branchPath, rulesCall}) {
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
	if got := settingsCalls(t, gh); !slices.Equal(got, []string{repoPath, branchPath, rulesCall}) {
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
	if got := settingsCalls(t, gh); !slices.Equal(got, []string{repoPath, branchPath, rulesCall}) {
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

// Either read that says protected makes the branch protected, though the other read failed. The review is read from the
// protection, and stays unknown where nothing settles it: with the branch read failed, a protection that is not there
// answers a 404 that is no "not enabled", and with the rules unread, a count of 0 does not rule out a ruleset.
func TestSettingsIsProtectedWhenEitherReadSaysSoAndTheOtherFailed(t *testing.T) {
	for _, tt := range []struct {
		name          string
		change        func(*ghstate.State)
		reviewUnknown string
	}{
		{"the rules failed", func(s *ghstate.State) {
			s.Protected = true
			s.SettingsStatus = map[string]ghstate.SettingsAnswer{rulesPath: refused(403, "Resource not accessible")}
		}, "HTTP 403: Resource not accessible"},
		{"the branch failed", func(s *ghstate.State) {
			s.Rules = []ghstate.Rule{{Type: "deletion"}}
			s.SettingsStatus = map[string]ghstate.SettingsAnswer{branchPath: refused(401, "Bad credentials")}
		}, "HTTP 404: Branch not protected"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			facts, gh, err := readSettings(t, tt.change)
			if err != nil || !facts.Protected || facts.ProtectedUnknown != "" || facts.ReviewCount != 0 {
				t.Fatalf("reads %+v: %v", facts, err)
			}
			if !slices.Contains(settingsCalls(t, gh), reviewsPath) {
				t.Errorf("gh read %q, but the protection read was needed", settingsCalls(t, gh))
			}
			if !strings.Contains(facts.ReviewUnknown, tt.reviewUnknown) {
				t.Errorf("the review is unknown for %q, want %q", facts.ReviewUnknown, tt.reviewUnknown)
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

// failedRead is one of the four reads, the state that makes it matter, and what the facts hold when it settles
// nothing: its fact unknown with the reason, and the others as they were.
type failedRead struct {
	name   string
	path   string
	change func(*ghstate.State)
	check  func(t *testing.T, facts practices.ForgeFacts, reason string)
}

func failedReads() []failedRead {
	return []failedRead{
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
}

// reasonOfRead is the unknown reason of the fact the read decides.
func (r failedRead) reasonOf(facts practices.ForgeFacts) string {
	if r.path == reviewsPath {
		return facts.ReviewUnknown
	}
	return facts.VisibilityUnknown + facts.ProtectedUnknown
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
	for _, read := range failedReads() {
		for _, failure := range failures {
			t.Run(read.name+" "+failure.name, func(t *testing.T) {
				facts, _, err := readSettings(t, func(s *ghstate.State) {
					read.change(s)
					s.SettingsStatus = map[string]ghstate.SettingsAnswer{read.path: failure.answer}
				})
				if err != nil {
					t.Fatalf("a refused read is no error: %v", err)
				}
				reason := read.reasonOf(facts)
				if !strings.Contains(reason, failure.contains) {
					t.Fatalf("the reason is %q, want it to hold %q", reason, failure.contains)
				}
				read.check(t, facts, reason)
			})
		}
	}
}

// A 200 whose body is no JSON settles nothing on any of the four reads, and is no error.
func TestSettingsLeavesTheFactUnknownForABodyThatIsNoJSON(t *testing.T) {
	for _, read := range failedReads() {
		t.Run(read.name, func(t *testing.T) {
			facts, _, err := readSettings(t, func(s *ghstate.State) {
				read.change(s)
				s.SettingsStatus = map[string]ghstate.SettingsAnswer{read.path: {Status: 200, Body: "<html>not the API</html>"}}
			})
			if err != nil {
				t.Fatalf("a body that is no JSON is no error: %v", err)
			}
			reason := read.reasonOf(facts)
			if !strings.Contains(reason, "reading gh api") {
				t.Fatalf("the reason is %q, want the read of the body", reason)
			}
			read.check(t, facts, reason)
		})
	}
}

// The unknown reason, prefix included, holds at most maxDetail bytes.
func TestSettingsClipsALongMessage(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) {
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{repoPath: refused(500, strings.Repeat("x", 3*maxDetail))}
	})
	if err != nil || len(facts.VisibilityUnknown) > maxDetail {
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
	want := []string{repoPath, repoPath + "/branches/release/1.2", repoPath + "/rules/branches/release/1.2" + rulesQuery}
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
	if last := calls[len(calls)-1]; last != repoPath+"/rules/branches/main%3Fx=1%23y"+rulesQuery {
		t.Errorf("a branch with a question mark and a hash was read as %s", last)
	}
}

// A window's end is the wait only when the window is exhausted: a refusal that names a rate limit with requests left
// and no wait says nothing of when to read again, and one that asks for a wait gives that wait.
func TestSettingsRateLimitedUsesTheResetOnlyForAnExhaustedWindow(t *testing.T) {
	refusal := func(headers map[string]string) func(*ghstate.State) {
		return func(s *ghstate.State) {
			s.SettingsStatus = map[string]ghstate.SettingsAnswer{repoPath: {Status: 403, Message: "API rate limit exceeded for user ID 1.", Headers: headers}}
		}
	}
	reset := fmt.Sprint(resetAt)
	facts, _, err := readSettings(t, refusal(map[string]string{"X-Ratelimit-Remaining": "7", "X-Ratelimit-Reset": reset}))
	if !errors.Is(err, ports.ErrForgeRateLimited) || facts.VisibilityUnknown != "rate limited" {
		t.Errorf("a window with requests left and no wait reads %q: %v", facts.VisibilityUnknown, err)
	}
	facts, _, err = readSettings(t, refusal(map[string]string{"X-Ratelimit-Remaining": "7", "X-Ratelimit-Reset": reset, "Retry-After": "90"}))
	if !errors.Is(err, ports.ErrForgeRateLimited) || !strings.HasPrefix(facts.VisibilityUnknown, "rate limited until 20") || strings.Contains(facts.VisibilityUnknown, resetAtText) {
		t.Errorf("a wait with requests left reads %q: %v", facts.VisibilityUnknown, err)
	}
	facts, _, err = readSettings(t, refusal(map[string]string{"X-Ratelimit-Remaining": "0", "X-Ratelimit-Reset": reset, "Retry-After": "90"}))
	if !errors.Is(err, ports.ErrForgeRateLimited) || facts.VisibilityUnknown != "rate limited until "+resetAtText {
		t.Errorf("an exhausted window reads %q: %v", facts.VisibilityUnknown, err)
	}
}

// A canceled context returns its error and no facts, and reads nothing.
func TestSettingsCanceledReturnsTheContextError(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	facts, err := forge.Settings(ctx, repo, "main")
	if !errors.Is(err, context.Canceled) || facts != (practices.ForgeFacts{}) {
		t.Fatalf("a canceled context reads %+v: %v", facts, err)
	}
	if got := settingsCalls(t, gh); len(got) != 0 {
		t.Errorf("gh read %q for a canceled context", got)
	}
}

// The owner and the repository are escaped in the path, so nothing of them starts a query or a fragment.
func TestSettingsEscapesTheOwnerAndTheRepository(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	repo.Binding.Owner, repo.Binding.Repo = "acme?x=1", "wid#gets"
	if _, err := forge.Settings(t.Context(), repo, "main"); err != nil {
		t.Fatal(err)
	}
	if got := settingsCalls(t, gh); len(got) == 0 || got[0] != "repos/acme%3Fx=1/wid%23gets" {
		t.Errorf("gh read %q, want the escaped repository first", got)
	}
}

// An owner or repository that climbs, or starts with a dash and so reads as a flag, is read as nothing: no call is
// made, and every fact is unknown.
func TestSettingsRefusesAnOwnerOrRepositoryThatClimbsOrIsAFlag(t *testing.T) {
	for _, tt := range []struct{ owner, repo string }{
		{"..", "widgets"}, {"acme", ".."}, {"-acme", "widgets"}, {"acme", "-widgets"}, {"", "widgets"}, {"acme", ""}, {".", "widgets"},
	} {
		t.Run(tt.owner+"/"+tt.repo, func(t *testing.T) {
			forge, repo, gh, _ := fixture(t)
			repo.Binding.Owner, repo.Binding.Repo = tt.owner, tt.repo
			facts, err := forge.Settings(t.Context(), repo, "main")
			if err != nil || facts.VisibilityUnknown == "" || facts.ProtectedUnknown == "" || facts.ReviewUnknown == "" || facts.Visibility != "" || facts.Protected {
				t.Fatalf("reads %+v: %v", facts, err)
			}
			if got := settingsCalls(t, gh); len(got) != 0 {
				t.Errorf("gh read %q", got)
			}
		})
	}
}

// A pull request rule whose parameters are missing, or whose count does not read, is a review rule that cannot be
// read, so the review stays unknown unless another rule, or the protection, requires one.
func TestSettingsLeavesTheReviewUnknownForAPullRequestRuleItCannotRead(t *testing.T) {
	for _, tt := range []struct {
		name string
		rule ghstate.Rule
	}{
		{"no parameters", ghstate.Rule{Type: "pull_request"}},
		{"no count", ghstate.Rule{Type: "pull_request", Parameters: map[string]any{"dismiss_stale_reviews_on_push": true}}},
		{"a count that is no number", ghstate.Rule{Type: "pull_request", Parameters: map[string]any{"required_approving_review_count": "two"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			facts, _, err := readSettings(t, func(s *ghstate.State) { s.Rules = []ghstate.Rule{tt.rule} })
			if err != nil || !facts.Protected || facts.ReviewCount != 0 || !strings.Contains(facts.ReviewUnknown, "required_approving_review_count") {
				t.Fatalf("an unreadable rule reads %+v: %v", facts, err)
			}
			facts, _, err = readSettings(t, func(s *ghstate.State) { s.Rules = []ghstate.Rule{tt.rule, pullRequestRule(1)} })
			if err != nil || facts.ReviewCount != 1 || facts.ReviewUnknown != "" {
				t.Errorf("a readable rule beside it reads %+v: %v", facts, err)
			}
			facts, _, err = readSettings(t, func(s *ghstate.State) { s.Protected, s.ReviewCount, s.Rules = true, 0, []ghstate.Rule{tt.rule} })
			if err != nil || facts.ReviewCount != 0 || facts.ReviewUnknown == "" {
				t.Errorf("a protection of no reviews beside it reads %+v: %v", facts, err)
			}
			facts, _, err = readSettings(t, func(s *ghstate.State) { s.Protected, s.ReviewCount, s.Rules = true, 2, []ghstate.Rule{tt.rule} })
			if err != nil || facts.ReviewCount != 2 || facts.ReviewUnknown != "" {
				t.Errorf("a protection of two reviews beside it reads %+v: %v", facts, err)
			}
		})
	}
}

// manyRules is n rules that need no review, with one more after them.
func manyRules(n int, last ghstate.Rule) []ghstate.Rule {
	rules := make([]ghstate.Rule, 0, n+1)
	for range n {
		rules = append(rules, ghstate.Rule{Type: "deletion"})
	}
	return append(rules, last)
}

func rulesPage(page int) string { return fmt.Sprintf("%s&page=%d", rulesCall, page) }

// A review rule on a later page of the rules is found, and settles the review with no protection read.
func TestSettingsFollowsThePagesOfTheRulesToAReviewRule(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) {
		s.Rules, s.RulesPerPage = manyRules(40, pullRequestRule(2)), 30
	})
	want := practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true, ReviewCount: 2}
	if err != nil || facts != want {
		t.Fatalf("a review rule on the second page reads %+v: %v", facts, err)
	}
	if got, wantPaths := settingsCalls(t, gh), []string{repoPath, branchPath, rulesCall, rulesPage(2)}; !slices.Equal(got, wantPaths) {
		t.Errorf("gh read %q, want %q", got, wantPaths)
	}
}

// A review rule on the first page ends the reading, though more pages follow.
func TestSettingsStopsAtThePageThatHoldsAReviewRule(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) {
		s.Rules, s.RulesPerPage = append([]ghstate.Rule{pullRequestRule(1)}, manyRules(40, ghstate.Rule{Type: "deletion"})...), 10
	})
	if err != nil || facts.ReviewCount != 1 || facts.ReviewUnknown != "" {
		t.Fatalf("reads %+v: %v", facts, err)
	}
	if got, wantPaths := settingsCalls(t, gh), []string{repoPath, branchPath, rulesCall}; !slices.Equal(got, wantPaths) {
		t.Errorf("gh read %q, want %q", got, wantPaths)
	}
}

// Rules past the fifth page are not read: with no review rule on the five pages, the review is unknown, and the branch
// is still protected. Five pages that end there settle it.
func TestSettingsStopsAfterFivePagesOfRules(t *testing.T) {
	more := "the branch has more rules than ropeshift reads"
	facts, gh, err := readSettings(t, func(s *ghstate.State) { s.Rules, s.RulesPerPage = manyRules(69, pullRequestRule(2)), 10 })
	want := practices.ForgeFacts{Visibility: practices.VisibilityPrivate, Protected: true, ReviewUnknown: more}
	if err != nil || facts != want {
		t.Fatalf("a review rule on the seventh page reads %+v: %v", facts, err)
	}
	wantPaths := []string{repoPath, branchPath, rulesCall, rulesPage(2), rulesPage(3), rulesPage(4), rulesPage(5)}
	if got := settingsCalls(t, gh); !slices.Equal(got, wantPaths) {
		t.Errorf("gh read %q, want %q", got, wantPaths)
	}
	facts, _, err = readSettings(t, func(s *ghstate.State) {
		s.Protected, s.ReviewCount = true, 2
		s.Rules, s.RulesPerPage = manyRules(69, pullRequestRule(2)), 10
	})
	if err != nil || facts.ReviewCount != 2 || facts.ReviewUnknown != "" {
		t.Errorf("a protection of two reviews beside the unread rules reads %+v: %v", facts, err)
	}
	facts, _, err = readSettings(t, func(s *ghstate.State) {
		s.Protected, s.ReviewCount = true, 0
		s.Rules, s.RulesPerPage = manyRules(69, pullRequestRule(2)), 10
	})
	if err != nil || facts.ReviewCount != 0 || facts.ReviewUnknown != more {
		t.Errorf("a protection of no reviews beside the unread rules reads %+v: %v", facts, err)
	}
	facts, gh, err = readSettings(t, func(s *ghstate.State) { s.Rules, s.RulesPerPage = manyRules(49, ghstate.Rule{Type: "deletion"}), 10 })
	if err != nil || !facts.Protected || facts.ReviewCount != 0 || facts.ReviewUnknown != "" {
		t.Errorf("exactly five pages of no review rule read %+v: %v", facts, err)
	}
	if got := settingsCalls(t, gh); len(got) != 7 {
		t.Errorf("gh read %q, want the five pages", got)
	}
}

// A later page that cannot be read leaves what the earlier pages decided, and the review unknown.
func TestSettingsLeavesTheReviewUnknownWhenALaterPageFails(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) {
		s.Rules, s.RulesPerPage = manyRules(40, pullRequestRule(2)), 30
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{rulesPage(2): refused(403, "Resource not accessible")}
	})
	if err != nil || !facts.Protected || facts.ProtectedUnknown != "" || facts.ReviewCount != 0 || !strings.Contains(facts.ReviewUnknown, "HTTP 403") {
		t.Fatalf("a second page the forge refused reads %+v: %v", facts, err)
	}
}

// A protection body with no count settles nothing: the review stays unknown, with a reason, and is not 0.
func TestSettingsLeavesTheReviewUnknownForAProtectionWithNoCount(t *testing.T) {
	for _, body := range []string{`{}`, `{"required_approving_review_count":null}`} {
		t.Run(body, func(t *testing.T) {
			facts, _, err := readSettings(t, func(s *ghstate.State) {
				s.Protected = true
				s.SettingsStatus = map[string]ghstate.SettingsAnswer{reviewsPath: {Status: 200, Body: body}}
			})
			if err != nil || !facts.Protected || facts.ReviewCount != 0 || !strings.Contains(facts.ReviewUnknown, "required_approving_review_count") {
				t.Fatalf("a protection with no count reads %+v: %v", facts, err)
			}
		})
	}
}

// A count of 0 that the body states is a review the forge settled as not required.
func TestSettingsReadsAProtectionCountOfZero(t *testing.T) {
	facts, _, err := readSettings(t, func(s *ghstate.State) {
		s.Protected = true
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{reviewsPath: {Status: 200, Body: `{"required_approving_review_count":0}`}}
	})
	if err != nil || facts.ReviewCount != 0 || facts.ReviewUnknown != "" {
		t.Fatalf("a stated count of 0 reads %+v: %v", facts, err)
	}
}

// The rel of a Link header is matched case-insensitively, quoted or not, among the relations of a link and among the
// links of a header (https://www.rfc-editor.org/rfc/rfc8288).
func TestHasNextPage(t *testing.T) {
	for _, tt := range []struct {
		name string
		link string
		want bool
	}{
		{"quoted", `<https://api.github.com/x?page=2>; rel="next"`, true},
		{"unquoted", `<https://api.github.com/x?page=2>; rel=next`, true},
		{"upper case quoted", `<https://api.github.com/x?page=2>; REL="next"`, true},
		{"upper case unquoted", `<https://api.github.com/x?page=2>; REL=next`, true},
		{"mixed case value", `<https://api.github.com/x?page=2>; Rel="NeXt"`, true},
		{"no space after the semicolon", `<https://api.github.com/x?page=2>;rel=next`, true},
		{"a relation list", `<https://api.github.com/x?page=2>; rel="prev next"`, true},
		{"after other links", `<https://api.github.com/x?page=1>; rel="prev", <https://api.github.com/x?page=3>; rel="next"`, true},
		{"after another parameter", `<https://api.github.com/x?page=2>; title="a, b"; rel=next`, true},
		{"a comma in the address", `<https://api.github.com/x?a=1,2&page=2>; rel=next`, true},
		{"prev only", `<https://api.github.com/x?page=1>; rel="prev"`, false},
		{"first and last", `<https://api.github.com/x?page=1>; rel="first", <https://api.github.com/x?page=9>; rel="last"`, false},
		{"a relation that only holds next", `<https://api.github.com/x?page=2>; rel="nextpage"`, false},
		{"next in the address", `<https://api.github.com/rel=next>; rel="last"`, false},
		{"next in another parameter", `<https://api.github.com/x?page=2>; title="rel=next"; rel="last"`, false},
		{"empty", ``, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasNextPage(tt.link); got != tt.want {
				t.Errorf("hasNextPage(%q) is %t, want %t", tt.link, got, tt.want)
			}
		})
	}
}

// A page whose Link header names the next page in the unquoted form is followed to that page.
func TestSettingsFollowsAnUnquotedNextLink(t *testing.T) {
	facts, gh, err := readSettings(t, func(s *ghstate.State) {
		s.Rules = []ghstate.Rule{pullRequestRule(2)}
		s.SettingsStatus = map[string]ghstate.SettingsAnswer{rulesCall: {
			Status: 200, Body: `[{"type":"deletion"}]`, Headers: map[string]string{"Link": `<https://api.github.com/x?page=2>; REL=next`},
		}}
	})
	if err != nil || facts.ReviewCount != 2 {
		t.Fatalf("an unquoted next link reads %+v: %v", facts, err)
	}
	if got, want := settingsCalls(t, gh), []string{repoPath, branchPath, rulesCall, rulesPage(2)}; !slices.Equal(got, want) {
		t.Errorf("gh read %q, want %q", got, want)
	}
}

// A reason is cut on a rune boundary and holds at most maxDetail bytes, its prefix included.
func TestSettingsBoundsAReasonOfMultiByteText(t *testing.T) {
	for _, message := range []string{strings.Repeat("é", 3*maxDetail), strings.Repeat("\u202e世", 3*maxDetail), "a" + strings.Repeat("😀", 3*maxDetail)} {
		facts, _, err := readSettings(t, func(s *ghstate.State) {
			s.SettingsStatus = map[string]ghstate.SettingsAnswer{repoPath: refused(500, message)}
		})
		reason := facts.VisibilityUnknown
		if err != nil || len(reason) > maxDetail || !utf8.ValidString(reason) || !strings.HasPrefix(reason, "HTTP 500: ") {
			t.Fatalf("a long multi-byte message left a reason of %d bytes, valid UTF-8 %t: %v", len(reason), utf8.ValidString(reason), err)
		}
	}
}

// clip cuts on a rune boundary, whatever byte the bound falls on.
func TestClipCutsOnARuneBoundary(t *testing.T) {
	for _, unit := range []string{"é", "世", "😀"} {
		for pad := range 4 {
			text := strings.Repeat("a", pad) + strings.Repeat(unit, maxDetail)
			got := clip(text)
			if len(got) > maxDetail || !utf8.ValidString(got) || !strings.HasPrefix(text, got) {
				t.Errorf("clip of %d bytes of %q after %d bytes is %d bytes, valid UTF-8 %t", len(text), unit, pad, len(got), utf8.ValidString(got))
			}
		}
	}
	if got := clip("  short \n"); got != "short" {
		t.Errorf("clip trims to %q", got)
	}
}

// A reason that quotes text with %q holds at most maxDetail bytes, though the quoting grows what it quotes.
func TestSettingsBoundsAQuotedReason(t *testing.T) {
	hostile := strings.Repeat("\x01\u202e", maxDetail)
	forge, repo, _, _ := fixture(t)
	repo.Binding.Owner, repo.Binding.Repo = "-"+hostile, "widgets"
	facts, err := forge.Settings(t.Context(), repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{facts.VisibilityUnknown, facts.ProtectedUnknown, facts.ReviewUnknown} {
		if reason == "" || len(reason) > maxDetail || !utf8.ValidString(reason) {
			t.Errorf("the repository reason is %d bytes, valid UTF-8 %t", len(reason), utf8.ValidString(reason))
		}
	}
	forge, repo, _, _ = fixture(t)
	facts, err = forge.Settings(t.Context(), repo, hostile+"//")
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{facts.ProtectedUnknown, facts.ReviewUnknown} {
		if reason == "" || len(reason) > maxDetail || !utf8.ValidString(reason) {
			t.Errorf("the branch reason is %d bytes, valid UTF-8 %t", len(reason), utf8.ValidString(reason))
		}
	}
	facts, _, err = readSettings(t, func(s *ghstate.State) { s.Visibility = hostile })
	if err != nil || facts.VisibilityUnknown == "" || len(facts.VisibilityUnknown) > maxDetail || !utf8.ValidString(facts.VisibilityUnknown) {
		t.Errorf("the visibility reason is %d bytes: %v", len(facts.VisibilityUnknown), err)
	}
}

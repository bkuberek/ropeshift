package ghcli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
)

// The cheap reads of CI (FR-SPEND-20) are held to what they cost: one GraphQL call for several pull requests, and
// a conditional request for each part of a probe, which carries the ETag the last answer gave.

// FR-SPEND-20: several pull requests of a repository are one call, where one read each is two calls apiece.
func TestSeveralPullRequestsAreOneCall(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, _, before := opened(t, forge, repo, gh)
	if _, err := forge.ReadPullRequests(t.Context(), repo, []int64{ref.Number, 7, 9}); err != nil {
		t.Fatal(err)
	}
	calls := doubles.FakeGHCalls(t, gh)[before:]
	if len(calls) != 1 || calls[0].Args[0] != "api" || !slices.Contains(calls[0].Args, "graphql") {
		t.Fatalf("a query of three pull requests took %d calls: %v", len(calls), argsOf(calls))
	}
	query := ""
	for _, arg := range calls[0].Args {
		if strings.HasPrefix(arg, "query=") {
			query = arg
		}
	}
	for _, want := range []string{"p1: pullRequest(number: 1)", "p7: pullRequest(number: 7)", "p9: pullRequest(number: 9)", "isRequired(pullRequestNumber: 7)"} {
		if !strings.Contains(query, want) {
			t.Errorf("the query lacks %q:\n%s", want, query)
		}
	}
}

// A probe carries the ETag of each of its three earlier answers, and a probe that has none carries no condition.
func TestAProbeCarriesTheETagsOfItsLastAnswers(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, head, before := opened(t, forge, repo, gh)
	seed, err := forge.ProbeChecks(t.Context(), ref, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forge.ProbeChecks(t.Context(), ref, head, seed.Tag); err != nil {
		t.Fatal(err)
	}
	calls := doubles.FakeGHCalls(t, gh)[before:]
	if len(calls) != 6 {
		t.Fatalf("two probes took %d calls: %v", len(calls), argsOf(calls))
	}
	for i, call := range calls {
		conditional := slices.Contains(call.Args, "-H")
		if (i < 3) == conditional {
			t.Errorf("call %d, %v, is conditional: %v", i, call.Args, conditional)
		}
		if conditional && !strings.HasPrefix(call.Args[slices.Index(call.Args, "-H")+1], "If-None-Match: \"") {
			t.Errorf("call %d carries %v, not an ETag", i, call.Args)
		}
	}
	paths := []string{"repos/acme/widgets/pulls/1", "repos/acme/widgets/commits/" + head + "/check-runs?per_page=100", "repos/acme/widgets/commits/" + head + "/status?per_page=100"}
	for i, want := range paths {
		if got := calls[i].Args[len(calls[i].Args)-1]; got != want {
			t.Errorf("probe call %d asks %q, want %q", i, got, want)
		}
	}
}

// A head that is not a commit ID is never put in a path: the probe says changed and asks nothing.
func TestAProbeAsksNothingOfAHeadThatIsNoCommit(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, _, before := opened(t, forge, repo, gh)
	probe, err := forge.ProbeChecks(t.Context(), ref, "../../user", "")
	if err != nil || probe.Unchanged || probe.Tag != "" {
		t.Fatalf("a probe of a path read %+v: %v", probe, err)
	}
	if calls := doubles.FakeGHCalls(t, gh)[before:]; len(calls) != 0 {
		t.Errorf("a probe of a path took calls: %v", argsOf(calls))
	}
}

// The headers of a response reach the caller: what is left of the window and when it ends, and the least time the
// endpoint asks between two reads.
func TestAResponseReportsItsRateHeaders(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, head, _ := opened(t, forge, repo, gh)
	reset := time.Now().Add(7 * time.Minute).Truncate(time.Second)
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.PollInterval, s.Remaining, s.Reset = 90, 0, reset.Unix() })
	probe, err := forge.ProbeChecks(t.Context(), ref, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := (ports.ForgeRate{Exhausted: true, Reset: reset, PollInterval: 90 * time.Second}); probe.Rate != want {
		t.Errorf("a probe reports %+v, want %+v", probe.Rate, want)
	}
	read, err := forge.ReadPullRequests(t.Context(), repo, []int64{ref.Number})
	if err != nil {
		t.Fatal(err)
	}
	if read.Rate.PollInterval != 90*time.Second || !read.Rate.Exhausted || !read.Rate.Reset.Equal(reset) {
		t.Errorf("a query reports %+v", read.Rate)
	}
}

// A query whose answer names a head that is not the last commit's, or whose checks run past what it asked for, is
// left out so that the caller reads that pull request on its own.
func TestAQueryLeavesOutAPullRequestItCannotAnswerWhole(t *testing.T) {
	const whole = `{"number":3,"state":"OPEN","headRefOid":"aaaa","commits":{"nodes":[{"commit":{"oid":"aaaa","statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":false},"nodes":[
		{"__typename":"CheckRun","name":"unit","status":"COMPLETED","conclusion":"SUCCESS","isRequired":true},
		{"__typename":"StatusContext","context":"legacy","state":"PENDING","isRequired":false}]}}}}]}}`
	for name, text := range map[string]string{
		"a last commit that is not the head": strings.Replace(whole, `"oid":"aaaa"`, `"oid":"bbbb"`, 1),
		"checks past the first hundred":      strings.Replace(whole, `"hasNextPage":false`, `"hasNextPage":true`, 1),
		"no commit":                          `{"number":3,"state":"OPEN","headRefOid":"aaaa","commits":{"nodes":[]}}`,
	} {
		var pr batched
		if err := json.Unmarshal([]byte(text), &pr); err != nil {
			t.Fatal(err)
		}
		if _, ok := pr.pullRequest(); ok {
			t.Errorf("%s came back whole", name)
		}
	}
	var pr batched
	if err := json.Unmarshal([]byte(whole), &pr); err != nil {
		t.Fatal(err)
	}
	read, ok := pr.pullRequest()
	if !ok || len(read.Checks) != 2 || read.Checks[0].Name != "legacy" || read.Checks[0].Required || read.Checks[1].Name != "unit" || !read.Checks[1].Required {
		t.Errorf("a whole answer reads %+v, %v", read, ok)
	}
}

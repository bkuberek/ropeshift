package contract

import (
	"context"
	"errors"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// The checks of the cheap reads (FR-SPEND-20): several pull requests in one query, a conditional probe of one
// pull request's checks, and a refusal for a rate limit that says how long to wait.

// openTwo opens a pull request on each of two branches, each with a check of its own.
func openTwo(t *testing.T, ctx context.Context, rig ForgeRig) (ledger.PullRequest, ledger.PullRequest) {
	t.Helper()
	rig.Push(t, "ropeshift/wo-1")
	rig.Push(t, "ropeshift/wo-2")
	first, second := openPR(t, ctx, rig, "ropeshift/wo-1"), openPR(t, ctx, rig, "ropeshift/wo-2")
	rig.SetChecks(t, first.Number,
		ledger.PullRequestCheck{Name: "unit", State: ledger.CheckPassed, DetailsURI: "https://ci.example/unit", Required: true},
		ledger.PullRequestCheck{Name: "e2e", State: ledger.CheckPending, DetailsURI: "https://ci.example/e2e", Required: true})
	rig.SetChecks(t, second.Number, ledger.PullRequestCheck{Name: "lint", State: ledger.CheckFailed, DetailsURI: "https://ci.example/lint"})
	return first, second
}

func checkBatchRead(t *testing.T, ctx context.Context, rig ForgeRig) {
	first, second := openTwo(t, ctx, rig)
	read, err := rig.Forge.ReadPullRequests(ctx, rig.Repo, []int64{first.Number, second.Number})
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range []int64{first.Number, second.Number} {
		got, ok := read.Pulls[number]
		if !ok {
			t.Fatalf("the query left pull request %d out: %+v", number, read.Pulls)
		}
		want := readPR(t, ctx, rig, number)
		if got.Number != want.Number || got.State != want.State || got.HeadCommit != want.HeadCommit || got.Branch != want.Branch || got.Base != want.Base {
			t.Errorf("pull request %d reads %+v in a query and %+v on its own", number, got, want)
		}
		if len(got.Checks) != len(want.Checks) {
			t.Fatalf("pull request %d has %+v in a query and %+v on its own", number, got.Checks, want.Checks)
		}
		for i := range want.Checks {
			if !sameCheck(got.Checks[i], want.Checks[i]) {
				t.Errorf("pull request %d check %d reads %+v in a query and %+v on its own", number, i, got.Checks[i], want.Checks[i])
			}
		}
	}
}

func checkBatchReadsMergeAndOmitsUnknown(t *testing.T, ctx context.Context, rig ForgeRig) {
	first, second := openTwo(t, ctx, rig)
	merge := rig.Merge(t, second.Number)
	read, err := rig.Forge.ReadPullRequests(ctx, rig.Repo, []int64{first.Number, second.Number, 9999})
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Pulls[second.Number]; got.State != ledger.PullRequestMerged || got.MergeCommit != merge {
		t.Errorf("a merged pull request reads %+v in a query, want merged as %s", got, merge)
	}
	if got := read.Pulls[first.Number]; got.State != ledger.PullRequestOpen {
		t.Errorf("an open pull request reads %+v in a query", got)
	}
	if _, ok := read.Pulls[9999]; ok {
		t.Errorf("a pull request the forge does not hold came back: %+v", read.Pulls[9999])
	}
}

func checkProbeSeesChange(t *testing.T, ctx context.Context, rig ForgeRig) {
	first, _ := openTwo(t, ctx, rig)
	ref := ports.PullRequestRef{Repo: rig.Repo, Number: first.Number}
	head := readPR(t, ctx, rig, first.Number).HeadCommit
	seed, err := rig.Forge.ProbeChecks(ctx, ref, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if seed.Unchanged || seed.Tag == "" {
		t.Fatalf("a probe with nothing to compare reads %+v, want changed with a tag", seed)
	}
	same, err := rig.Forge.ProbeChecks(ctx, ref, head, seed.Tag)
	if err != nil {
		t.Fatal(err)
	}
	if !same.Unchanged || same.Tag != seed.Tag {
		t.Errorf("a probe of what did not change reads %+v, want unchanged under %q", same, seed.Tag)
	}
	rig.SetChecks(t, first.Number, ledger.PullRequestCheck{Name: "unit", State: ledger.CheckPassed, Required: true}, ledger.PullRequestCheck{Name: "e2e", State: ledger.CheckPassed, Required: true})
	moved, err := rig.Forge.ProbeChecks(ctx, ref, head, seed.Tag)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Unchanged || moved.Tag == seed.Tag {
		t.Errorf("a probe after a check finished reads %+v, want changed under a new tag", moved)
	}
	again, err := rig.Forge.ProbeChecks(ctx, ref, head, moved.Tag)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Unchanged {
		t.Errorf("a probe under the new tag reads %+v, want unchanged", again)
	}
}

func checkProbeSeesNewHead(t *testing.T, ctx context.Context, rig ForgeRig) {
	first, _ := openTwo(t, ctx, rig)
	ref := ports.PullRequestRef{Repo: rig.Repo, Number: first.Number}
	head := readPR(t, ctx, rig, first.Number).HeadCommit
	seed, err := rig.Forge.ProbeChecks(ctx, ref, head, "")
	if err != nil {
		t.Fatal(err)
	}
	rig.Push(t, "ropeshift/wo-1")
	pushed, err := rig.Forge.ProbeChecks(ctx, ref, head, seed.Tag)
	if err != nil {
		t.Fatal(err)
	}
	if pushed.Unchanged {
		t.Errorf("a probe after a push to the branch reads %+v, want changed", pushed)
	}
}

func checkRateLimitWaits(t *testing.T, ctx context.Context, rig ForgeRig) {
	first, second := openTwo(t, ctx, rig)
	ref := ports.PullRequestRef{Repo: rig.Repo, Number: first.Number}
	head := readPR(t, ctx, rig, first.Number).HeadCommit
	reset := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	for _, limit := range []ports.ForgeRate{{RetryAfter: 30 * time.Second, Secondary: true}, {Exhausted: true, Reset: reset}} {
		rig.Limit(t, limit)
		var limited *ports.RateLimitedError
		_, err := rig.Forge.ReadPullRequests(ctx, rig.Repo, []int64{first.Number, second.Number})
		if !errors.Is(err, ports.ErrForgeRateLimited) || !errors.As(err, &limited) || limited.Rate.RetryAfter != limit.RetryAfter || limited.Rate.Exhausted != limit.Exhausted || !limited.Rate.Reset.Equal(limit.Reset) {
			t.Errorf("a query under %+v answers %v, want a rate limit that says so", limit, err)
		}
		_, err = rig.Forge.ProbeChecks(ctx, ref, head, "")
		if !errors.Is(err, ports.ErrForgeRateLimited) || !errors.As(err, &limited) || limited.Rate.RetryAfter != limit.RetryAfter || !limited.Rate.Reset.Equal(limit.Reset) {
			t.Errorf("a probe under %+v answers %v, want a rate limit that says so", limit, err)
		}
		if _, err := rig.Forge.PullRequest(ctx, ref); !errors.Is(err, ports.ErrForgeRateLimited) {
			t.Errorf("a read under %+v answers %v, want a rate limit", limit, err)
		}
	}
	rig.Limit(t, ports.ForgeRate{})
	if _, err := rig.Forge.ReadPullRequests(ctx, rig.Repo, []int64{first.Number}); err != nil {
		t.Errorf("a query after the limit lifted answers %v", err)
	}
}

// forgeReadChecks are the checks of the cheap reads, in the order RunForge runs them.
var forgeReadChecks = []struct {
	name string
	run  func(*testing.T, context.Context, ForgeRig)
}{
	{"several pull requests read in one query as they read one at a time", checkBatchRead},
	{"a query reads a merged pull request and leaves out one the forge does not hold", checkBatchReadsMergeAndOmitsUnknown},
	{"a probe says unchanged until a check changes, and gives the tag to use next", checkProbeSeesChange},
	{"a probe sees a push to the branch", checkProbeSeesNewHead},
	{"a read the forge refuses for a rate limit says how long to wait", checkRateLimitWaits},
}

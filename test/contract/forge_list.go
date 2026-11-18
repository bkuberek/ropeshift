package contract

import (
	"context"
	"fmt"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// The checks of the listing of a repository's open pull requests (FR-ADOPT-2): what each one shows, newest first,
// only the open ones, and at most ports.MaxOpenPullRequests with the boolean that says more exist.

// summaryOf is an open pull request as a listing shows it.
func summaryOf(rig ForgeRig, number int64, branch string) ledger.PullRequestSummary {
	return ledger.PullRequestSummary{
		Number: number, Title: fmt.Sprintf("Pull request %d", number), Branch: branch, Base: "main",
		URL: fmt.Sprintf("https://%s/pull/%d", rig.Repo.Binding, number), UpdatedAt: "2026-02-03T04:05:06Z",
	}
}

// heldPulls holds open pull requests numbered from first to last.
func heldPulls(t *testing.T, rig ForgeRig, first, last int64) []ledger.PullRequestSummary {
	t.Helper()
	var pulls []ledger.PullRequestSummary
	for number := first; number <= last; number++ {
		pulls = append(pulls, summaryOf(rig, number, fmt.Sprintf("feature/n%d", number)))
	}
	rig.HoldOpen(t, pulls...)
	return pulls
}

func checkListsOpenPulls(t *testing.T, ctx context.Context, rig ForgeRig) {
	draft := summaryOf(rig, 9, "feature/n9")
	draft.Draft = true
	rig.HoldOpen(t, summaryOf(rig, 7, "feature/n7"), summaryOf(rig, 8, "feature/n8"), draft)
	rig.Push(t, "ropeshift/wo-1")
	rig.Push(t, "ropeshift/wo-2")
	opened := openPR(t, ctx, rig, "ropeshift/wo-1")
	rig.Merge(t, openPR(t, ctx, rig, "ropeshift/wo-2").Number)
	got, more, err := rig.Forge.OpenPullRequests(ctx, rig.Repo)
	if err != nil || more {
		t.Fatalf("listed %+v, more %v: %v", got, more, err)
	}
	if len(got) != 4 || got[0].Number != opened.Number || got[1].Number != 9 || got[2].Number != 8 || got[3].Number != 7 {
		t.Fatalf("listed %+v, want the open pull requests newest first and not the merged one", got)
	}
	if got[0].Branch != "ropeshift/wo-1" || got[0].Base != "main" || got[0].Title != "Add the retry budget" || got[0].URL == "" || got[0].Draft {
		t.Errorf("the pull request opened through the forge lists as %+v", got[0])
	}
	for _, pull := range got[1:] {
		want := summaryOf(rig, pull.Number, pull.Branch)
		want.Draft = pull.Number == 9
		if pull != want {
			t.Errorf("pull request %d lists as %+v, want %+v", pull.Number, pull, want)
		}
	}
}

func checkListsNoneOpen(t *testing.T, ctx context.Context, rig ForgeRig) {
	got, more, err := rig.Forge.OpenPullRequests(ctx, rig.Repo)
	if err != nil || more || len(got) != 0 {
		t.Errorf("a repository with none open lists %+v, more %v: %v", got, more, err)
	}
}

func checkListHoldsTheMost(t *testing.T, ctx context.Context, rig ForgeRig) {
	heldPulls(t, rig, 1, ports.MaxOpenPullRequests)
	got, more, err := rig.Forge.OpenPullRequests(ctx, rig.Repo)
	if err != nil || more || len(got) != ports.MaxOpenPullRequests {
		t.Fatalf("a repository with %d open lists %d, more %v: %v", ports.MaxOpenPullRequests, len(got), more, err)
	}
}

func checkListTruncates(t *testing.T, ctx context.Context, rig ForgeRig) {
	heldPulls(t, rig, 1, ports.MaxOpenPullRequests+1)
	got, more, err := rig.Forge.OpenPullRequests(ctx, rig.Repo)
	if err != nil || !more || len(got) != ports.MaxOpenPullRequests {
		t.Fatalf("a repository with %d open lists %d, more %v: %v", ports.MaxOpenPullRequests+1, len(got), more, err)
	}
	if got[0].Number != ports.MaxOpenPullRequests+1 || got[len(got)-1].Number != 2 {
		t.Errorf("the list runs from %d to %d, want the newest %d", got[0].Number, got[len(got)-1].Number, ports.MaxOpenPullRequests)
	}
}

// forgeListChecks are the checks of the listing, in the order RunForge runs them.
var forgeListChecks = []struct {
	name string
	run  func(*testing.T, context.Context, ForgeRig)
}{
	{"the open pull requests list newest first with what a person reads of each, and not a merged one", checkListsOpenPulls},
	{"a repository with no open pull request lists none", checkListsNoneOpen},
	{"a repository with the most open pull requests lists them all and says no more exist", checkListHoldsTheMost},
	{"a repository with one more lists the newest and says more exist", checkListTruncates},
}

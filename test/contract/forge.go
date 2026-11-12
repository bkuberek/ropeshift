package contract

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ForgeRig is one forge as a worker runs it, and what a check does to it from outside: a push from the
// checkout, a person's merge, the checks CI reports, and a sign-in that breaks.
type ForgeRig struct {
	Forge ports.Forge
	Repo  ports.ForgeRepo
	// Push moves a branch to a new commit on the forge, as a push from the checkout does, and returns it.
	Push func(t *testing.T, branch string) string
	// Merge merges a pull request as a person does, and returns the merge commit.
	Merge func(t *testing.T, number int64) string
	// SetChecks says what CI reports on a pull request's head.
	SetChecks func(t *testing.T, number int64, checks ...ledger.PullRequestCheck)
	// Comments lists what was posted on a pull request, in order.
	Comments func(t *testing.T, number int64) []string
	// SignOut breaks the sign-in for the binding's host, with what the CLI says of it.
	SignOut func(t *testing.T, detail string)
	// Missing returns the forge as a machine with no CLI runs it.
	Missing func(t *testing.T) ports.Forge
}

// RunForge runs one contract on one forge: the fake, and the GitHub adapter on a fake `gh`, both pass it.
func RunForge(t *testing.T, fresh func(*testing.T) ForgeRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, ForgeRig)
	}{
		{"opening a pull request returns it open at the branch's head", checkPROpens},
		{"opening it again returns the one that is open", checkPROpensOnce},
		{"another branch opens another pull request", checkPRPerBranch},
		{"a read follows the branch's head", checkPRReadFollowsHead},
		{"a merge a person made reads as merged with its merge commit", checkPRReadsMerge},
		{"the checks on the head come back by name with their states", checkPRChecks},
		{"a comment is posted once per key", checkPRCommentOnce},
		{"a sign-in that works is healthy", checkForgeHealthy},
		{"a sign-in with a problem says so", checkForgeSignedOut},
		{"no CLI reads as missing, and an operation is unavailable", checkForgeMissing},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

func openPR(t *testing.T, ctx context.Context, rig ForgeRig, branch string) ledger.PullRequest {
	t.Helper()
	pr, err := rig.Forge.OpenPullRequest(ctx, ports.PullRequestSpec{Repo: rig.Repo, Base: "main", Branch: branch, Title: "Add the retry budget", Body: "The work order."})
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func readPR(t *testing.T, ctx context.Context, rig ForgeRig, number int64) ledger.PullRequest {
	t.Helper()
	pr, err := rig.Forge.PullRequest(ctx, ports.PullRequestRef{Repo: rig.Repo, Number: number})
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func checkPROpens(t *testing.T, ctx context.Context, rig ForgeRig) {
	head := rig.Push(t, "ropeshift/wo-1")
	pr := openPR(t, ctx, rig, "ropeshift/wo-1")
	if pr.Number <= 0 || pr.URL == "" || pr.State != ledger.PullRequestOpen || pr.Branch != "ropeshift/wo-1" || pr.Base != "main" || pr.HeadCommit != head {
		t.Errorf("opened %+v, want open at %s", pr, head)
	}
}

func checkPROpensOnce(t *testing.T, ctx context.Context, rig ForgeRig) {
	rig.Push(t, "ropeshift/wo-1")
	first := openPR(t, ctx, rig, "ropeshift/wo-1")
	again := openPR(t, ctx, rig, "ropeshift/wo-1")
	if again.Number != first.Number || again.URL != first.URL {
		t.Errorf("opened %d and then %d", first.Number, again.Number)
	}
}

func checkPRPerBranch(t *testing.T, ctx context.Context, rig ForgeRig) {
	rig.Push(t, "ropeshift/wo-1")
	rig.Push(t, "ropeshift/wo-2")
	first, second := openPR(t, ctx, rig, "ropeshift/wo-1"), openPR(t, ctx, rig, "ropeshift/wo-2")
	if first.Number == second.Number || second.Branch != "ropeshift/wo-2" {
		t.Errorf("two branches share %+v and %+v", first, second)
	}
}

func checkPRReadFollowsHead(t *testing.T, ctx context.Context, rig ForgeRig) {
	rig.Push(t, "ropeshift/wo-1")
	pr := openPR(t, ctx, rig, "ropeshift/wo-1")
	moved := rig.Push(t, "ropeshift/wo-1")
	read := readPR(t, ctx, rig, pr.Number)
	if read.Number != pr.Number || read.State != ledger.PullRequestOpen || read.HeadCommit != moved {
		t.Errorf("read %+v, want its head at %s", read, moved)
	}
}

func checkPRReadsMerge(t *testing.T, ctx context.Context, rig ForgeRig) {
	head := rig.Push(t, "ropeshift/wo-1")
	pr := openPR(t, ctx, rig, "ropeshift/wo-1")
	merge := rig.Merge(t, pr.Number)
	read := readPR(t, ctx, rig, pr.Number)
	if read.State != ledger.PullRequestMerged || read.MergeCommit != merge || read.MergedAt == "" || read.HeadCommit != head {
		t.Errorf("read %+v, want merged as %s at head %s", read, merge, head)
	}
}

func checkPRChecks(t *testing.T, ctx context.Context, rig ForgeRig) {
	rig.Push(t, "ropeshift/wo-1")
	pr := openPR(t, ctx, rig, "ropeshift/wo-1")
	want := []ledger.PullRequestCheck{
		{Name: "lint", State: ledger.CheckFailed, DetailsURI: "https://ci.example/lint"},
		{Name: "unit", State: ledger.CheckPassed, DetailsURI: "https://ci.example/unit"},
		{Name: "e2e", State: ledger.CheckPending, DetailsURI: "https://ci.example/e2e"},
	}
	rig.SetChecks(t, pr.Number, want...)
	got := readPR(t, ctx, rig, pr.Number).Checks
	if len(got) != 3 || got[0] != want[2] || got[1] != want[0] || got[2] != want[1] {
		t.Errorf("checks %+v", got)
	}
}

func checkPRCommentOnce(t *testing.T, ctx context.Context, rig ForgeRig) {
	rig.Push(t, "ropeshift/wo-1")
	pr := openPR(t, ctx, rig, "ropeshift/wo-1")
	ref := ports.PullRequestRef{Repo: rig.Repo, Number: pr.Number}
	for range 2 {
		if err := rig.Forge.Comment(ctx, ref, "review-1", "c1 pass: it logs each retry"); err != nil {
			t.Fatal(err)
		}
	}
	if err := rig.Forge.Comment(ctx, ref, "review-2", "c1 fail: it does not"); err != nil {
		t.Fatal(err)
	}
	comments := rig.Comments(t, pr.Number)
	if len(comments) != 2 || !strings.Contains(comments[0], "c1 pass: it logs each retry") || !strings.Contains(comments[1], "c1 fail: it does not") {
		t.Errorf("comments %q", comments)
	}
}

func checkForgeHealthy(t *testing.T, ctx context.Context, rig ForgeRig) {
	report, err := rig.Forge.Health(ctx, rig.Repo)
	if err != nil || report.State != ledger.ForgeHealthy {
		t.Errorf("health %+v: %v", report, err)
	}
}

func checkForgeSignedOut(t *testing.T, ctx context.Context, rig ForgeRig) {
	rig.SignOut(t, "the token in keyring is invalid")
	report, err := rig.Forge.Health(ctx, rig.Repo)
	if err != nil || report.State != ledger.ForgeSignedOut || !strings.Contains(report.Detail, "the token in keyring is invalid") {
		t.Errorf("health %+v: %v", report, err)
	}
}

func checkForgeMissing(t *testing.T, ctx context.Context, rig ForgeRig) {
	missing := rig.Missing(t)
	report, err := missing.Health(ctx, rig.Repo)
	if err != nil || report.State != ledger.ForgeMissing || report.Detail == "" {
		t.Errorf("health %+v: %v", report, err)
	}
	rig.Push(t, "ropeshift/wo-1")
	_, err = missing.OpenPullRequest(ctx, ports.PullRequestSpec{Repo: rig.Repo, Base: "main", Branch: "ropeshift/wo-1", Title: "t", Body: "b"})
	if !errors.Is(err, ports.ErrForgeUnavailable) {
		t.Errorf("an open with no CLI: %v", err)
	}
}

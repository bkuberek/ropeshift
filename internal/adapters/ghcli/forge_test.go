package ghcli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
	"ropeshift.com/ropeshift/test/gittest"
)

func TestMain(m *testing.M) { os.Exit(doubles.Main(m)) }

const repoArg = "github.com/acme/widgets"

// fixture is a checkout whose origin is a bare repository beside it, with a branch pushed there, and the
// adapter on a fake `gh`.
func fixture(t *testing.T) (Forge, ports.ForgeRepo, string, string) {
	t.Helper()
	repo := gittest.New(t)
	repo.Commit("README.md", "widgets\n")
	bare := filepath.Join(t.TempDir(), "remote.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Branch("ropeshift/wo-1", "main")
	repo.Git("push", "--quiet", "origin", "main", "ropeshift/wo-1")
	gh := doubles.InstallFakeGH(t)
	bodies := t.TempDir()
	forge := Forge{Path: filepath.Join(gh, Binary), Env: repo.Env(), TempDir: bodies}
	binding := ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}
	return forge, ports.ForgeRepo{Dir: repo.Dir, Binding: binding}, gh, bodies
}

// argsOf lists each call's arguments, with a body file's path, which changes every run, as <file>.
func argsOf(calls []ghstate.Call) [][]string {
	out := make([][]string, 0, len(calls))
	for _, call := range calls {
		args := slices.Clone(call.Args)
		for i := range args {
			if i > 0 && args[i-1] == "--body-file" {
				args[i] = "<file>"
			}
		}
		out = append(out, args)
	}
	return out
}

// TestTheAdapterRunsTheManualsCommands holds each operation to the commands and flags the GitHub CLI's
// manual gives: gh auth status --hostname, gh pr list --head --state --json, gh pr create --base --head
// --title --body-file, gh pr view --json, and gh pr comment --body-file, each with --repo.
func TestTheAdapterRunsTheManualsCommands(t *testing.T) {
	forge, repo, gh, bodies := fixture(t)
	ctx := t.Context()
	if report, err := forge.Health(ctx, repo); err != nil || report.State != ledger.ForgeHealthy {
		t.Fatalf("health %+v: %v", report, err)
	}
	pr, err := forge.OpenPullRequest(ctx, ports.PullRequestSpec{Repo: repo, Base: "main", Branch: "ropeshift/wo-1", Title: "Add the retry budget", Body: "Work order wo-1."})
	if err != nil || pr.Number != 1 {
		t.Fatalf("open %+v: %v", pr, err)
	}
	if _, err := forge.OpenPullRequest(ctx, ports.PullRequestSpec{Repo: repo, Base: "main", Branch: "ropeshift/wo-1", Title: "Add the retry budget", Body: "Work order wo-1."}); err != nil {
		t.Fatal(err)
	}
	ref := ports.PullRequestRef{Repo: repo, Number: 1}
	if err := forge.Comment(ctx, ref, "review-7", "c1: pass"); err != nil {
		t.Fatal(err)
	}
	if err := forge.Comment(ctx, ref, "review-7", "c1: pass"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"auth", "status", "--hostname", "github.com"},
		{"pr", "list", "--repo", repoArg, "--head", "ropeshift/wo-1", "--state", "open", "--json", pullRequestFields},
		{"pr", "create", "--repo", repoArg, "--base", "main", "--head", "ropeshift/wo-1", "--title", "Add the retry budget", "--body-file", "<file>"},
		{"pr", "view", "1", "--repo", repoArg, "--json", pullRequestFields},
		{"pr", "checks", "1", "--repo", repoArg, "--required", "--json", requiredFields},
		{"pr", "list", "--repo", repoArg, "--head", "ropeshift/wo-1", "--state", "open", "--json", pullRequestFields},
		{"pr", "view", "1", "--repo", repoArg, "--json", "comments"},
		{"pr", "comment", "1", "--repo", repoArg, "--body-file", "<file>"},
		{"pr", "view", "1", "--repo", repoArg, "--json", "comments"},
	}
	calls := doubles.FakeGHCalls(t, gh)
	if got := argsOf(calls); !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("gh ran\n%q\nwant\n%q", got, want)
	}
	for _, call := range calls {
		if call.Dir != repo.Dir && !strings.HasSuffix(call.Dir, repo.Dir) {
			t.Errorf("gh ran in %s, not the checkout", call.Dir)
		}
		if call.Env["GH_PROMPT_DISABLED"] == "" || call.Env["GH_NO_UPDATE_NOTIFIER"] == "" {
			t.Errorf("gh ran with prompts or notices on: %v", call.Env)
		}
		for _, arg := range call.Args {
			if arg == "login" || arg == "token" || arg == "--show-token" || arg == "-t" {
				t.Errorf("gh ran %q, which touches the token", call.Args)
			}
		}
		for i, arg := range call.Args {
			if arg == "--body-file" && filepath.Dir(call.Args[i+1]) != bodies {
				t.Errorf("a body file went to %s, not the temporary directory", call.Args[i+1])
			}
		}
	}
	if left, _ := os.ReadDir(bodies); len(left) != 0 {
		t.Errorf("body files stayed behind: %v", left)
	}
	state := doubles.FakeGH(t, gh)
	opened := state.Find(1)
	if opened == nil || opened.Body != "Work order wo-1." || len(opened.Comments) != 1 || !strings.Contains(opened.Comments[0], "c1: pass") || !strings.Contains(opened.Comments[0], "ropeshift:review-7") {
		t.Errorf("the forge holds %+v", opened)
	}
}

// TestASignedOutCLIIsAHealthState reads `gh auth status` exiting with 1 as a sign-in problem with what it
// said, and a missing `gh` as missing, neither as an error.
func TestASignedOutCLIIsAHealthState(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.SignedOut, s.AuthError = true, "the token in keyring is invalid" })
	report, err := forge.Health(t.Context(), repo)
	if err != nil || report.State != ledger.ForgeSignedOut || !strings.Contains(report.Detail, "the token in keyring is invalid") {
		t.Errorf("health %+v: %v", report, err)
	}
	forge.Path = filepath.Join(t.TempDir(), Binary)
	report, err = forge.Health(t.Context(), repo)
	if err != nil || report.State != ledger.ForgeMissing {
		t.Errorf("health with no gh %+v: %v", report, err)
	}
}

// opened opens the pull request of the fixture's branch, and returns it with the calls made so far.
func opened(t *testing.T, forge Forge, repo ports.ForgeRepo, gh string) (ports.PullRequestRef, string, int) {
	t.Helper()
	pr, err := forge.OpenPullRequest(t.Context(), ports.PullRequestSpec{Repo: repo, Base: "main", Branch: "ropeshift/wo-1", Title: "Add the retry budget", Body: "wo-1"})
	if err != nil {
		t.Fatal(err)
	}
	return ports.PullRequestRef{Repo: repo, Number: pr.Number}, pr.HeadCommit, len(doubles.FakeGHCalls(t, gh))
}

// TestAMergeNamesTheReviewedHead holds a merge to the manual's gh pr merge --match-head-commit, with the flag
// of each merge method, after a read of the pull request's merge state; a repeat reads it merged at that head
// and merges nothing twice (https://cli.github.com/manual/gh_pr_merge).
func TestAMergeNamesTheReviewedHead(t *testing.T) {
	for method, flag := range map[ledger.MergeMethod]string{ledger.MergeCommit: "--merge", ledger.MergeSquash: "--squash", ledger.MergeRebase: "--rebase"} {
		t.Run(string(method), func(t *testing.T) {
			forge, repo, gh, _ := fixture(t)
			ref, head, before := opened(t, forge, repo, gh)
			result, err := forge.Merge(t.Context(), ref, head, method)
			if err != nil || result.Outcome != ledger.MergeMerged || len(result.MergeCommit) != 40 || result.MergedAt == "" {
				t.Fatalf("merge %+v: %v", result, err)
			}
			again, err := forge.Merge(t.Context(), ref, head, method)
			if err != nil || again.Outcome != ledger.MergeMerged || again.MergeCommit != result.MergeCommit {
				t.Fatalf("a repeat %+v: %v", again, err)
			}
			want := [][]string{
				{"pr", "view", "1", "--repo", repoArg, "--json", mergeFields},
				{"pr", "merge", "1", "--repo", repoArg, "--match-head-commit", head, flag},
				{"pr", "view", "1", "--repo", repoArg, "--json", mergeFields},
				{"pr", "view", "1", "--repo", repoArg, "--json", mergeFields},
			}
			if got := argsOf(doubles.FakeGHCalls(t, gh))[before:]; !slices.EqualFunc(got, want, slices.Equal) {
				t.Fatalf("gh ran\n%q\nwant\n%q", got, want)
			}
			state := doubles.FakeGH(t, gh)
			if merged := state.Find(1); merged.MergeMethod != string(method) || merged.MergeCommit != result.MergeCommit {
				t.Errorf("the forge holds %+v", merged)
			}
			for _, call := range doubles.FakeGHCalls(t, gh) {
				if slices.Contains(call.Args, "--admin") || slices.Contains(call.Args, "--auto") || slices.Contains(call.Args, "--delete-branch") {
					t.Errorf("gh ran %q", call.Args)
				}
			}
		})
	}
}

// TestAMergeTheForgeRefusesSaysWhy reads the pull request before it merges, so a head that moved past the
// reviewed commit and a merge that conflicts never reach gh pr merge, and a refusal of branch protection
// comes back as a refusal with what gh said, not as an error to try again.
func TestAMergeTheForgeRefusesSaysWhy(t *testing.T) {
	t.Run("a head that moved", func(t *testing.T) {
		forge, repo, gh, _ := fixture(t)
		ref, _, before := opened(t, forge, repo, gh)
		result, err := forge.Merge(t.Context(), ref, strings.Repeat("a", 40), ledger.MergeCommit)
		if err != nil || result.Outcome != ledger.MergeStaleHead || !strings.Contains(result.Detail, "aaaaaaaa") {
			t.Fatalf("merge %+v: %v", result, err)
		}
		if got := argsOf(doubles.FakeGHCalls(t, gh))[before:]; len(got) != 1 || got[0][1] != "view" {
			t.Errorf("gh ran %q", got)
		}
	})
	t.Run("a conflict", func(t *testing.T) {
		forge, repo, gh, _ := fixture(t)
		ref, head, before := opened(t, forge, repo, gh)
		doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.Find(1).Mergeable = "CONFLICTING" })
		result, err := forge.Merge(t.Context(), ref, head, ledger.MergeCommit)
		if err != nil || result.Outcome != ledger.MergeConflict {
			t.Fatalf("merge %+v: %v", result, err)
		}
		if got := argsOf(doubles.FakeGHCalls(t, gh))[before:]; len(got) != 1 {
			t.Errorf("gh ran %q", got)
		}
	})
	t.Run("a branch behind its base", func(t *testing.T) {
		forge, repo, gh, _ := fixture(t)
		ref, head, _ := opened(t, forge, repo, gh)
		doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.Find(1).MergeState = "BEHIND" })
		if result, err := forge.Merge(t.Context(), ref, head, ledger.MergeCommit); err != nil || result.Outcome != ledger.MergeBehind {
			t.Fatalf("merge %+v: %v", result, err)
		}
	})
	t.Run("branch protection", func(t *testing.T) {
		forge, repo, gh, _ := fixture(t)
		ref, head, _ := opened(t, forge, repo, gh)
		doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.Find(1).MergeState = "BLOCKED" })
		result, err := forge.Merge(t.Context(), ref, head, ledger.MergeCommit)
		if err != nil || result.Outcome != ledger.MergeRefused || !strings.Contains(result.Detail, "base branch policy prohibits the merge") {
			t.Fatalf("merge %+v: %v", result, err)
		}
		if state := doubles.FakeGH(t, gh); state.Find(1).State != "OPEN" {
			t.Error("a refused merge merged")
		}
	})
	t.Run("a closed pull request", func(t *testing.T) {
		forge, repo, gh, _ := fixture(t)
		ref, head, _ := opened(t, forge, repo, gh)
		doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.Find(1).State, s.Find(1).Head = "CLOSED", head })
		if result, err := forge.Merge(t.Context(), ref, head, ledger.MergeCommit); err != nil || result.Outcome != ledger.MergeRefused || !strings.Contains(result.Detail, "closed") {
			t.Fatalf("merge %+v: %v", result, err)
		}
	})
	t.Run("an unknown method", func(t *testing.T) {
		forge, repo, gh, _ := fixture(t)
		ref, head, _ := opened(t, forge, repo, gh)
		if _, err := forge.Merge(t.Context(), ref, head, "fast-forward"); err == nil {
			t.Fatal("a merge with no method a forge offers ran")
		}
	})
}

// TestARequiredCheckIsMarked reads the base branch's required checks with gh pr checks --required, and marks
// each on the pull request's head; a pull request with none required marks none
// (https://cli.github.com/manual/gh_pr_checks).
func TestARequiredCheckIsMarked(t *testing.T) {
	forge, repo, gh, _ := fixture(t)
	ref, _, _ := opened(t, forge, repo, gh)
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		s.Find(1).Checks = []ghstate.Check{
			{Typename: "CheckRun", Name: "unit", Status: "COMPLETED", Conclusion: "FAILURE", DetailsURL: "https://ci.example/unit"},
			{Typename: "CheckRun", Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS"},
		}
	})
	read, err := forge.PullRequest(t.Context(), ref)
	if err != nil || len(read.Checks) != 2 || read.Checks[0].Required || read.Checks[1].Required {
		t.Fatalf("with none required, read %+v: %v", read.Checks, err)
	}
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) { s.Find(1).Required = []string{"unit"} })
	read, err = forge.PullRequest(t.Context(), ref)
	want := []ledger.PullRequestCheck{
		{Name: "lint", State: ledger.CheckPassed},
		{Name: "unit", State: ledger.CheckFailed, DetailsURI: "https://ci.example/unit", Required: true},
	}
	if err != nil || !slices.Equal(read.Checks, want) {
		t.Fatalf("read %+v: %v", read.Checks, err)
	}
}

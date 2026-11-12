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

package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	domain "ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// dryResult is what `new --dry-run --json` prints: the plan of an executed run, marked as a dry run.
type dryResult struct {
	newResult
	DryRun       bool                      `json:"dry_run"`
	Repositories []ledger.RepositoryReport `json:"repositories"`
}

// dryOptions stand in for a machine where any git or gh read, and any write of a project, fails the test: a dry run
// that asks for no remote reads nothing.
func dryOptions(t *testing.T) cli.Options {
	t.Helper()
	return cli.Options{
		CloneTop: func(context.Context, string) (string, error) {
			t.Error("a dry run that asks for no remote read the clone that holds the folder")
			return "", nil
		},
		NewProject: func(context.Context, ledger.TemplateWriteRequest, bool) (ledger.TemplateWriteReport, error) {
			t.Error("a dry run ran a project's write")
			return ledger.TemplateWriteReport{}, nil
		},
		ReadForgeAccount: func(context.Context) (ledger.ForgeAccountReport, error) {
			t.Error("a dry run read the GitHub account")
			return ledger.ForgeAccountReport{}, nil
		},
	}
}

// dryRemoteOptions are dryOptions for a dry run that asks for a remote, which asks git which clone holds the folder:
// no clone does.
func dryRemoteOptions(t *testing.T) cli.Options {
	t.Helper()
	opts := dryOptions(t)
	opts.CloneTop = func(context.Context, string) (string, error) { return "", nil }
	return opts
}

func dryJSON(t *testing.T, opts cli.Options, args ...string) dryResult {
	t.Helper()
	code, stdout, stderr := runNew(opts, append(args, "--dry-run", "--json")...)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var result dryResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	return result
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s exists after a dry run (%v)", path, err)
	}
}

// A dry run lists what an executed run writes and the commands it prints, and writes nothing: not the folder, and not
// its parents.
func TestNewDryRunPrintsThePlanOfARealRunAndWritesNothing(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "parent", "orchid")
	dry := dryJSON(t, dryOptions(t), "new", "monorepo", folder)
	requireAbsent(t, filepath.Join(root, "parent"))
	if !dry.DryRun {
		t.Error("the plan does not say it is a dry run")
	}
	executed := newJSON(t, cli.Options{}, "new", "monorepo", folder)
	if !slices.Equal(dry.Written, executed.Written) || len(dry.Written) == 0 || len(dry.Left) != 0 {
		t.Errorf("a dry run lists written %v and left %v, an executed run %v and %v", dry.Written, dry.Left, executed.Written, executed.Left)
	}
	if dry.Path != executed.Path || dry.Project != executed.Project || !slices.Equal(dry.Commands, executed.Commands) {
		t.Errorf("a dry run says %+v, an executed run %+v", dry.newResult, executed)
	}
}

// A file the folder holds is listed as left, as it is in an executed run.
func TestNewDryRunListsTheFilesTheFolderHoldsAsLeft(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "AGENTS.md"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dry := dryJSON(t, dryOptions(t), "new", "monorepo", folder)
	if !slices.Equal(dry.Left, []string{"AGENTS.md"}) || slices.Contains(dry.Written, "AGENTS.md") {
		t.Errorf("left %v, written %v", dry.Left, dry.Written)
	}
	if raw, err := os.ReadFile(filepath.Join(folder, "AGENTS.md")); err != nil || string(raw) != "mine\n" {
		t.Errorf("AGENTS.md reads %q (%v)", raw, err)
	}
}

// The text says it is a plan, lists each file as one it would write, and prints the commands.
func TestNewDryRunTextSaysNothingWasWritten(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	code, stdout, stderr := runNew(dryOptions(t), "new", "repository", folder, "--dry-run")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	for _, want := range []string{"Dry run: nothing was written", "would write\tAGENTS.md", "ropeshift repository add"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the text lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "wrote\t") {
		t.Errorf("the text says a file was written:\n%s", stdout)
	}
	requireAbsent(t, folder)
}

// A remote, a created repository, and a new code repository are planned and not run: no gh, no git, no write.
func TestNewDryRunPlansTheCommitsAndRemotesWithoutRunningThem(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	dry := dryJSON(t, dryRemoteOptions(t), "new", "workspace", folder, "--create-remote", "github:acme", "--new-repository", "api", "--public")
	requireAbsent(t, folder)
	if !slices.Contains(dry.Written, "repos/api/AGENTS.md") {
		t.Errorf("written %v lacks the new repository's files", dry.Written)
	}
	if len(dry.Repositories) != 2 || dry.Repositories[0].Path != "." || dry.Repositories[1].Path != "repos/api" {
		t.Fatalf("repositories %+v", dry.Repositories)
	}
	for _, repository := range dry.Repositories {
		var steps []string
		for _, step := range repository.Steps {
			if step.Outcome != "planned" {
				t.Errorf("%s: step %s is %q", repository.Path, step.Step, step.Outcome)
			}
			steps = append(steps, step.Step)
		}
		if want := []string{"create", "origin", "push"}; !slices.Equal(steps, want) {
			t.Errorf("%s plans %v, want %v", repository.Path, steps, want)
		}
		if repository.Commit != "" || !strings.HasPrefix(repository.Remote, "github.com/acme/") {
			t.Errorf("%s: commit %q, remote %q", repository.Path, repository.Commit, repository.Remote)
		}
	}
	if slices.ContainsFunc(dry.Commands, func(c string) bool { return strings.Contains(c, " init") }) {
		t.Errorf("the plan prints git commands for a first commit it makes itself: %v", dry.Commands)
	}
}

// What a run refuses before it writes anything, a dry run refuses too.
func TestNewDryRunRefusesWhatARealRunRefuses(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	code, _, stderr := runNew(dryOptions(t), "new", "monorepo", folder, "--remote", "a b", "--dry-run")
	if code != cli.ExitUsage || stderr == "" {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	code, _, _ = runNew(dryOptions(t), "new", "monorepo", filepath.Join(t.TempDir(), "!!!"), "--dry-run")
	if code != cli.ExitUsage {
		t.Errorf("a folder that makes no project name: exit %d", code)
	}
}

// With no shape, a dry run asks its questions, prints the plan, and asks nothing more: it reads no GitHub account, so it
// asks for the owner itself.
func TestNewDryRunAsksItsQuestionsAndPrintsThePlan(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	opts := dryRemoteOptions(t)
	opts.Terminal = strings.NewReader("one\ny\n3\nacme\n\n")
	code, stdout, stderr := runNew(opts, "new", folder, "--dry-run")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q\n%s", code, stderr, stdout)
	}
	for _, want := range []string{"Dry run: nothing was written", "would write\tAGENTS.md", "  create\tplanned"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the output lacks %q:\n%s", want, stdout)
		}
	}
	requireAbsent(t, folder)
}

// The visibility a person types is read as a real run reads it, whatever its case, and the plan names the lowercase word.
func TestNewDryRunReadsTheVisibilityAsARunDoes(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	opts := dryRemoteOptions(t)
	opts.Terminal = strings.NewReader("one\ny\n3\nacme\nPublic\n")
	code, stdout, stderr := runNew(opts, "new", folder, "--dry-run")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "  create\tplanned\tpublic") {
		t.Errorf("the plan does not name the repository public:\n%s", stdout)
	}
}

// A folder that a clone's top already is makes no repository of its own, so a remote is refused: a dry run refuses it
// with the reason and the message of the real run, and writes nothing.
func TestNewDryRunRefusesARemoteForAFolderThatHoldsARepository(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(opts cli.Options, extra ...string) (int, string, string) {
		args := append([]string{"new", "workspace", folder, "--create-remote", "github:acme"}, extra...)
		return runNew(opts, args...)
	}
	refusing := func(t *testing.T) cli.Options {
		opts := dryRemoteOptions(t)
		opts.CloneTop = func(_ context.Context, dir string) (string, error) { return dir, nil }
		opts.NewProject = func(_ context.Context, request ledger.TemplateWriteRequest, needsRepository bool) (ledger.TemplateWriteReport, error) {
			report := request.RefuseNoOwnRepository(needsRepository)
			if report == nil {
				t.Error("the real run was not refused")
				return ledger.TemplateWriteReport{}, nil
			}
			return *report, nil
		}
		return opts
	}
	realCode, _, realErr := run(refusing(t))
	dryCode, dryOut, dryErr := run(refusing(t), "--dry-run")
	if realCode != cli.ExitError || !strings.Contains(realErr, ledger.TemplateRefusedNoOwnRepository) {
		t.Fatalf("the real run: exit %d, stderr %q", realCode, realErr)
	}
	if dryCode != realCode || dryErr != realErr || dryOut != "" {
		t.Errorf("the dry run: exit %d, stdout %q, stderr %q; the real run: exit %d, stderr %q", dryCode, dryOut, dryErr, realCode, realErr)
	}
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 0 {
		t.Errorf("the folder holds %v after the runs (%v)", entries, err)
	}
}

// With a git older than the minimum, a dry run that asks for a remote refuses as a run does, in the doctor's words and
// before it reads git.
func TestNewDryRunRefusesAGitBelowTheMinimumBeforeItReadsGit(t *testing.T) {
	old := domain.CheckGit(domain.GitVersion{Major: 2, Minor: 25, Patch: 1}, nil)
	folder := filepath.Join(t.TempDir(), "orchid")
	opts := dryOptions(t)
	opts.Git = func(context.Context) domain.GitReport { return old }
	for _, extra := range [][]string{{}, {"--dry-run"}} {
		args := append([]string{"new", "workspace", folder, "--create-remote", "github:acme"}, extra...)
		code, stdout, stderr := runNew(opts, args...)
		if want := "ropeshift new workspace: " + old.Problem + "\n"; code != cli.ExitError || stdout != "" || stderr != want {
			t.Errorf("%v: exit %d, stdout %q, stderr %q, want %d and %q", extra, code, stdout, stderr, cli.ExitError, want)
		}
	}
	requireAbsent(t, folder)
}

package contract

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// ProcessRig is a runner and the programs a check runs on it.
type ProcessRig struct {
	Runner ports.ProcessRunner
	// PrintEnv prints its environment, PrintArgs its arguments, and PrintDir its
	// directory.
	PrintEnv  string
	PrintArgs string
	PrintDir  string
	// Fail exits with 1.
	Fail string
	// Block runs until a signal ends it. BlockArgs are its arguments.
	Block     string
	BlockArgs []string
	// Dir is a directory for PrintDir.
	Dir string
}

// RunProcessRunner runs one contract on one runner.
func RunProcessRunner(t *testing.T, fresh func(*testing.T) ProcessRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, ProcessRig)
	}{
		{"the environment is exactly the spec's", checkExactEnvironment},
		{"an empty environment stays empty", checkEmptyEnvironment},
		{"arguments reach the binary as written", checkArguments},
		{"a directory is honored", checkDirectory},
		{"an exit code is reported", checkExitCode},
		{"a signal ends a process and is reported", checkSignal},
		{"a spawn never goes through a shell", checkNoShell},
		{"a name PATH would resolve is refused", checkRelativeBinary},
		{"a missing binary is an error", checkMissingBinary},
		{"waiting twice answers twice", checkWaitTwice},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

func runCaptured(t *testing.T, ctx context.Context, rig ProcessRig, spec ports.ProcessSpec) (string, ports.ExitStatus) {
	t.Helper()
	var out bytes.Buffer
	spec.Stdout = &out
	process, err := rig.Runner.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	status, err := process.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return out.String(), status
}

func checkExactEnvironment(t *testing.T, ctx context.Context, rig ProcessRig) {
	t.Setenv("ROPESHIFT_TEST_PARENT_VARIABLE", "must-not-pass")
	env := []string{"HOME=/nowhere", "ROPESHIFT_LANE=lane-1"}
	out, _ := runCaptured(t, ctx, rig, ports.ProcessSpec{Path: rig.PrintEnv, Env: env})
	got := strings.Fields(out)
	slices.Sort(got)
	if !slices.Equal(got, env) {
		t.Errorf("environment %v, want %v", got, env)
	}
}

func checkEmptyEnvironment(t *testing.T, ctx context.Context, rig ProcessRig) {
	out, _ := runCaptured(t, ctx, rig, ports.ProcessSpec{Path: rig.PrintEnv})
	if strings.TrimSpace(out) != "" {
		t.Errorf("a nil environment inherited %q", out)
	}
}

func checkArguments(t *testing.T, ctx context.Context, rig ProcessRig) {
	args := []string{"--flag", "two words", "$HOME; false", "`id`"}
	out, _ := runCaptured(t, ctx, rig, ports.ProcessSpec{Path: rig.PrintArgs, Args: args})
	if got := strings.TrimSpace(out); got != strings.Join(args, " ") {
		t.Errorf("arguments %q", got)
	}
}

func checkDirectory(t *testing.T, ctx context.Context, rig ProcessRig) {
	out, _ := runCaptured(t, ctx, rig, ports.ProcessSpec{Path: rig.PrintDir, Dir: rig.Dir})
	if got := strings.TrimSpace(out); got != rig.Dir {
		t.Errorf("directory %q, want %q", got, rig.Dir)
	}
}

func checkExitCode(t *testing.T, ctx context.Context, rig ProcessRig) {
	_, status := runCaptured(t, ctx, rig, ports.ProcessSpec{Path: rig.Fail})
	if status.Code != 1 || status.Signal != "" {
		t.Errorf("status %+v", status)
	}
}

func checkSignal(t *testing.T, ctx context.Context, rig ProcessRig) {
	process, err := rig.Runner.Start(ctx, ports.ProcessSpec{Path: rig.Block, Args: rig.BlockArgs})
	if err != nil {
		t.Fatal(err)
	}
	if process.PID() == 0 {
		t.Error("no process ID")
	}
	if err := process.Signal(ports.SignalKill); err != nil {
		t.Fatal(err)
	}
	finished := make(chan ports.ExitStatus, 1)
	go func() {
		status, err := process.Wait()
		if err != nil {
			t.Error(err)
		}
		finished <- status
	}()
	select {
	case status := <-finished:
		if status.Signal != ports.SignalKill {
			t.Errorf("status %+v", status)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the signal did not end the process")
	}
}

func checkNoShell(t *testing.T, ctx context.Context, rig ProcessRig) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		if _, err := rig.Runner.Start(ctx, ports.ProcessSpec{Path: shell, Args: []string{"-lc", "true"}}); !errors.Is(err, lanes.ErrShellSpawn) {
			t.Errorf("%s: %v", shell, err)
		}
	}
}

func checkRelativeBinary(t *testing.T, ctx context.Context, rig ProcessRig) {
	if _, err := rig.Runner.Start(ctx, ports.ProcessSpec{Path: "claude"}); !errors.Is(err, lanes.ErrRelativePath) {
		t.Errorf("got %v", err)
	}
}

func checkMissingBinary(t *testing.T, ctx context.Context, rig ProcessRig) {
	if _, err := rig.Runner.Start(ctx, ports.ProcessSpec{Path: "/nonexistent/ropeshift-test-binary"}); err == nil {
		t.Error("a missing binary started")
	}
}

func checkWaitTwice(t *testing.T, ctx context.Context, rig ProcessRig) {
	process, err := rig.Runner.Start(ctx, ports.ProcessSpec{Path: rig.Fail})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		status, err := process.Wait()
		if err != nil || status.Code != 1 {
			t.Errorf("status %+v: %v", status, err)
		}
	}
}

// WorktreeRig is a worktree store and one repository with a commit.
type WorktreeRig struct {
	Worktrees  ports.Worktrees
	Repository string
	// Path names a place for a worktree.
	Path func(name string) string
	// Dirty changes a file in a worktree.
	Dirty func(path string)
}

// RunWorktrees runs one contract on one store.
func RunWorktrees(t *testing.T, fresh func(*testing.T) WorktreeRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, WorktreeRig)
	}{
		{"a lane gets a worktree on its branch", checkWorktreeCreate},
		{"a repository names its default branch", checkDefaultBranch},
		{"a retry returns the same worktree", checkWorktreeRetry},
		{"inspect reads what is there", checkWorktreeInspect},
		{"an absent worktree is not found", checkWorktreeAbsent},
		{"a path holding another branch is a conflict", checkWorktreePathConflict},
		{"a branch checked out elsewhere is a conflict", checkWorktreeBranchConflict},
		{"a clean worktree is removed and its removal repeats", checkWorktreeRemove},
		{"a dirty worktree is refused", checkWorktreeDirty},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

func (r WorktreeRig) create(t *testing.T, ctx context.Context, name, branch string) ports.Worktree {
	t.Helper()
	worktree, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path(name), Branch: branch})
	if err != nil {
		t.Fatal(err)
	}
	return worktree
}

func checkDefaultBranch(t *testing.T, ctx context.Context, r WorktreeRig) {
	branch, err := r.Worktrees.DefaultBranch(ctx, r.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if branch == "" {
		t.Error("a repository with a branch named none")
	}
}

func checkWorktreeCreate(t *testing.T, ctx context.Context, r WorktreeRig) {
	worktree := r.create(t, ctx, "one", "lane/one")
	if worktree.Path != r.Path("one") || worktree.Branch != "lane/one" || worktree.Head == "" || !worktree.Clean {
		t.Errorf("worktree %+v", worktree)
	}
}

func checkWorktreeRetry(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.create(t, ctx, "one", "lane/one")
	second := r.create(t, ctx, "one", "lane/one")
	if first != second {
		t.Errorf("first %+v, retry %+v", first, second)
	}
}

func checkWorktreeInspect(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	r.Dirty(created.Path)
	got, err := r.Worktrees.Inspect(ctx, created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Clean || got.Branch != "lane/one" {
		t.Errorf("inspected %+v", got)
	}
}

func checkWorktreeAbsent(t *testing.T, ctx context.Context, r WorktreeRig) {
	if _, err := r.Worktrees.Inspect(ctx, r.Path("absent")); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func checkWorktreePathConflict(t *testing.T, ctx context.Context, r WorktreeRig) {
	r.create(t, ctx, "one", "lane/one")
	_, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("one"), Branch: "lane/two"})
	if !errors.Is(err, ports.ErrWorktreeConflict) {
		t.Errorf("got %v", err)
	}
}

func checkWorktreeBranchConflict(t *testing.T, ctx context.Context, r WorktreeRig) {
	r.create(t, ctx, "one", "lane/one")
	_, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("two"), Branch: "lane/one"})
	if !errors.Is(err, ports.ErrWorktreeConflict) {
		t.Errorf("got %v", err)
	}
}

func checkWorktreeRemove(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	for range 2 {
		if err := r.Worktrees.Remove(ctx, r.Repository, created.Path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Worktrees.Inspect(ctx, created.Path); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func checkWorktreeDirty(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	r.Dirty(created.Path)
	if err := r.Worktrees.Remove(ctx, r.Repository, created.Path); !errors.Is(err, ports.ErrWorktreeDirty) {
		t.Errorf("got %v", err)
	}
	got, err := r.Worktrees.Inspect(ctx, created.Path)
	if err != nil || got.Clean {
		t.Errorf("the worktree after a refusal %+v: %v", got, err)
	}
}

// ProfileRig is a source and the way a check gives a repository its layer.
type ProfileRig struct {
	Source ports.ProfileSource
	// Repository is a repository with no layer yet.
	Repository string
	// Layer gives the repository a layer.
	Layer func(text string)
}

// RunProfileSource runs one contract on one source.
func RunProfileSource(t *testing.T, fresh func(*testing.T) ProfileRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, ProfileRig)
	}{
		{"a repository with no layer gets the defaults", checkProfileDefaults},
		{"a repository layer changes one key and keeps its neighbors", checkProfileLayer},
		{"a misspelled key is refused", checkProfileUnknownKey},
		{"bad TOML is refused", checkProfileBadTOML},
		{"the digest follows the settings", checkProfileDigest},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

func checkProfileDefaults(t *testing.T, ctx context.Context, r ProfileRig) {
	profile, err := r.Source.Load(ctx, r.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Driver != "claude-code" || profile.Agent.PermissionMode != "default" || profile.Worktree.Root == "" || profile.Worktree.BranchPrefix == "" {
		t.Errorf("profile %+v", profile)
	}
	if profile.Context.Roles["writer"].HandoffTokens == 0 || profile.Lifecycle.ReadyTimeout == "" {
		t.Errorf("profile %+v", profile)
	}
	if profile.RunMode != "" {
		t.Errorf("run mode %q, and the default follows the sign-in kind", profile.RunMode)
	}
}

func checkProfileLayer(t *testing.T, ctx context.Context, r ProfileRig) {
	r.Layer("run_mode = \"headless\"\n\n[agent]\nmodel = \"haiku\"\n\n[context.roles.writer]\nwarn_tokens = 1000\n")
	profile, err := r.Source.Load(ctx, r.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if profile.RunMode != "headless" || profile.Agent.Model != "haiku" {
		t.Errorf("profile %+v", profile)
	}
	if profile.Agent.Effort != "high" {
		t.Errorf("a layer lost a neighbor: effort %q", profile.Agent.Effort)
	}
	writer := profile.Context.Roles["writer"]
	if writer.WarnTokens != 1000 || writer.HandoffTokens == 0 {
		t.Errorf("writer %+v", writer)
	}
	if profile.Context.Roles["orchestrator"].HandoffTokens == 0 {
		t.Error("a layer lost the other role")
	}
}

func checkProfileUnknownKey(t *testing.T, ctx context.Context, r ProfileRig) {
	r.Layer("[lifecycle]\nstall_aftr = \"1m\"\n")
	if _, err := r.Source.Load(ctx, r.Repository); !errors.Is(err, lanes.ErrBadProfile) {
		t.Errorf("got %v", err)
	}
}

func checkProfileBadTOML(t *testing.T, ctx context.Context, r ProfileRig) {
	r.Layer("name = unquoted\n")
	if _, err := r.Source.Load(ctx, r.Repository); !errors.Is(err, lanes.ErrBadProfile) {
		t.Errorf("got %v", err)
	}
}

func checkProfileDigest(t *testing.T, ctx context.Context, r ProfileRig) {
	first, err := r.Source.Load(ctx, r.Repository)
	if err != nil {
		t.Fatal(err)
	}
	before, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	r.Layer("[agent]\nmodel = \"haiku\"\n")
	second, err := r.Source.Load(ctx, r.Repository)
	if err != nil {
		t.Fatal(err)
	}
	after, err := second.Digest()
	if err != nil || after == before {
		t.Errorf("digests %s and %s: %v", before, after, err)
	}
}

package contract

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
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
	Worktrees ports.ReviewTrees
	// Bases answers what the control plane asks of the same repository.
	Bases      ports.Bases
	Repository string
	// Path names a place for a worktree.
	Path func(name string) string
	// Dirty changes a file in a worktree.
	Dirty func(path string)
	// Grow adds a commit on top of a branch of the repository and returns it. A branch with no commit
	// yet gets its first.
	Grow func(branch string) string
	// Touch adds a commit on top of main that writes a file, and returns it.
	Touch func(name string) string
	// Branches lists the repository's branches.
	Branches func() []string
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
		{"a survey reads what a stop must account for", checkWorktreeSurvey},
		{"a survey of nothing is not found", checkWorktreeSurveyAbsent},
		{"a snapshot leaves the worktree as it stood", checkWorktreeSnapshot},
		{"a snapshot of nothing is an error", checkWorktreeSnapshotAbsent},
		{"a push with no remote is an error", checkWorktreePushNoRemote},
		{"a discard removes a dirty worktree and repeats", checkWorktreeDiscard},
		{"a pack left in a worktree is nothing git sees", checkWorktreeContext},
		{"a base branch reads as the commit it stands at, and moves with it", checkBaseTipFollowsTheBranch},
		{"a branch nobody holds is an unavailable base", checkBaseTipOfNothing},
		{"a commit contains the commits it grew from and not the ones that grew from it", checkContainsFollowsAncestry},
		{"a commit the repository lacks is not found", checkContainsOfNothing},
		{"a worktree starts at the commit it is given", checkWorktreeStartsAtItsBase},
		{"a detached worktree starts at its commit and makes no branch", checkDetachedMakesNoBranch},
		{"a retry of a detached worktree returns the same one", checkDetachedRetry},
		{"detached worktrees at one commit do not conflict", checkDetachedAtOneCommit},
		{"a detached worktree and a branch's do not take each other's place", checkDetachedConflicts},
		{"a detached worktree holds nothing a stop must save", checkDetachedHoldsNothingToSave},
		{"a detached worktree is discarded whatever it holds, and the repository keeps its branches", checkDetachedDiscard},
		{"a diff names the patch and the files a commit changed since its base", checkDiffNamesTheChange},
		{"a diff of a commit the repository lacks is not found", checkDiffOfNothing},
		{"a context file is left beside the pack and git sees none of it", checkContextFile},
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

func checkWorktreeSurvey(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	clean, err := r.Worktrees.Survey(ctx, created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Dirty || clean.Branch != "lane/one" || clean.Head == "" || len(clean.DirtyPaths) != 0 {
		t.Errorf("a clean worktree reads %+v", clean)
	}
	r.Dirty(created.Path)
	dirty, err := r.Worktrees.Survey(ctx, created.Path)
	if err != nil || !dirty.Dirty {
		t.Errorf("a dirty worktree reads %+v: %v", dirty, err)
	}
	if len(dirty.DirtyPaths) != 1 || dirty.DirtyPaths[0] == "" {
		t.Errorf("a dirty worktree names %v", dirty.DirtyPaths)
	}
}

func checkWorktreeContext(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	path, err := r.Worktrees.WriteContext(ctx, created.Path, "the pack")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, created.Path) || !strings.HasSuffix(path, lanes.PackFile) {
		t.Errorf("a pack at %s", path)
	}
	again, err := r.Worktrees.WriteContext(ctx, created.Path, "the pack, a later version")
	if err != nil || again != path {
		t.Errorf("a repeat wrote to %s: %v", again, err)
	}
	survey, err := r.Worktrees.Survey(ctx, created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if survey.Dirty || len(survey.DirtyPaths) != 0 {
		t.Errorf("a pack reads as a change: %+v", survey)
	}
	got, err := r.Worktrees.Inspect(ctx, created.Path)
	if err != nil || !got.Clean {
		t.Errorf("a pack dirtied the worktree: %+v: %v", got, err)
	}
	if err := r.Worktrees.Remove(ctx, r.Repository, created.Path); err != nil {
		t.Errorf("a pack held a worktree back: %v", err)
	}
}

func checkWorktreeSurveyAbsent(t *testing.T, ctx context.Context, r WorktreeRig) {
	if _, err := r.Worktrees.Survey(ctx, r.Path("absent")); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func checkWorktreeSnapshot(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	r.Dirty(created.Path)
	commit, err := r.Worktrees.Snapshot(ctx, created.Path, "refs/ropeshift/checkpoints/one/1")
	if err != nil {
		t.Fatal(err)
	}
	if commit == "" {
		t.Error("a snapshot named no commit")
	}
	got, err := r.Worktrees.Inspect(ctx, created.Path)
	if err != nil || got.Clean || got.Branch != "lane/one" {
		t.Errorf("after a snapshot %+v: %v", got, err)
	}
}

func checkWorktreeSnapshotAbsent(t *testing.T, ctx context.Context, r WorktreeRig) {
	if _, err := r.Worktrees.Snapshot(ctx, r.Path("absent"), "refs/ropeshift/checkpoints/one/1"); err == nil {
		t.Error("a snapshot of nothing passed")
	}
}

func checkWorktreePushNoRemote(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	if err := r.Worktrees.Push(ctx, created.Path, "", lanes.HeadsRef("lane/one")); err == nil {
		t.Error("a push with no remote passed")
	}
}

func checkWorktreeDiscard(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	r.Dirty(created.Path)
	for range 2 {
		if err := r.Worktrees.Discard(ctx, r.Repository, created.Path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Worktrees.Inspect(ctx, created.Path); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

// HookRig is a runner and the commands a check runs on it.
type HookRig struct {
	Runner ports.HookRunner
	// Pass exits 0, Fail exits 1, and Slow runs until a signal ends it.
	Pass, Fail, Slow []string
	Dir              string
	Env              []string
}

// RunHookRunner runs one contract on one runner.
func RunHookRunner(t *testing.T, fresh func(*testing.T) HookRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, HookRig)
	}{
		{"a command that passes reports exit 0", checkHookPasses},
		{"a command that fails reports its code and no error", checkHookFails},
		{"a command past its timeout ends with a signal", checkHookTimeout},
		{"a hook with no command is an error", checkHookEmpty},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

func checkHookPasses(t *testing.T, ctx context.Context, r HookRig) {
	result, err := r.Runner.Run(ctx, ports.HookCommand{Argv: r.Pass, Dir: r.Dir, Env: r.Env, Timeout: time.Minute})
	if err != nil || result.Exit.Code != 0 || result.Exit.Signal != "" {
		t.Errorf("result %+v: %v", result, err)
	}
}

func checkHookFails(t *testing.T, ctx context.Context, r HookRig) {
	result, err := r.Runner.Run(ctx, ports.HookCommand{Argv: r.Fail, Dir: r.Dir, Env: r.Env, Timeout: time.Minute})
	if err != nil || result.Exit.Code != 1 {
		t.Errorf("result %+v: %v", result, err)
	}
}

func checkHookTimeout(t *testing.T, ctx context.Context, r HookRig) {
	result, err := r.Runner.Run(ctx, ports.HookCommand{Argv: r.Slow, Dir: r.Dir, Env: r.Env, Timeout: 50 * time.Millisecond})
	if err != nil || result.Exit.Signal == "" {
		t.Errorf("result %+v: %v", result, err)
	}
}

func checkHookEmpty(t *testing.T, ctx context.Context, r HookRig) {
	if _, err := r.Runner.Run(ctx, ports.HookCommand{Dir: r.Dir, Env: r.Env}); err == nil {
		t.Error("a hook with no command passed")
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
	profile, err := r.Source.Load(ctx, r.Repository, "")
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
	profile, err := r.Source.Load(ctx, r.Repository, "")
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
	if _, err := r.Source.Load(ctx, r.Repository, ""); !errors.Is(err, lanes.ErrBadProfile) {
		t.Errorf("got %v", err)
	}
}

func checkProfileBadTOML(t *testing.T, ctx context.Context, r ProfileRig) {
	r.Layer("name = unquoted\n")
	if _, err := r.Source.Load(ctx, r.Repository, ""); !errors.Is(err, lanes.ErrBadProfile) {
		t.Errorf("got %v", err)
	}
}

func checkProfileDigest(t *testing.T, ctx context.Context, r ProfileRig) {
	first, err := r.Source.Load(ctx, r.Repository, "")
	if err != nil {
		t.Fatal(err)
	}
	before, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	r.Layer("[agent]\nmodel = \"haiku\"\n")
	second, err := r.Source.Load(ctx, r.Repository, "")
	if err != nil {
		t.Fatal(err)
	}
	after, err := second.Digest()
	if err != nil || after == before {
		t.Errorf("digests %s and %s: %v", before, after, err)
	}
}

func checkBaseTipFollowsTheBranch(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	tip, err := r.Bases.BaseTip(ctx, r.Repository, "main")
	if err != nil {
		t.Fatal(err)
	}
	if tip.Commit != first || tip.Ref != "main" {
		t.Errorf("tip %+v, want main at %s", tip, first)
	}
	second := r.Grow("main")
	if tip, err = r.Bases.BaseTip(ctx, r.Repository, "main"); err != nil || tip.Commit != second {
		t.Errorf("after the branch moved: %+v: %v", tip, err)
	}
}

func checkBaseTipOfNothing(t *testing.T, ctx context.Context, r WorktreeRig) {
	r.Grow("main")
	for _, branch := range []string{"no-such-branch", "-rf", "", "a..b"} {
		if _, err := r.Bases.BaseTip(ctx, r.Repository, branch); !errors.Is(err, ports.ErrBaseUnavailable) {
			t.Errorf("base %q: %v", branch, err)
		}
	}
}

func checkContainsFollowsAncestry(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	second := r.Grow("main")
	for _, test := range []struct {
		name             string
		ancestor, commit string
		want             bool
	}{
		{"a commit and the one it grew into", first, second, true},
		{"a commit and itself", second, second, true},
		{"a commit and the one it grew from", second, first, false},
	} {
		got, err := r.Bases.Contains(ctx, r.Repository, test.ancestor, test.commit)
		if err != nil || got != test.want {
			t.Errorf("%s: %v, %v", test.name, got, err)
		}
	}
}

func checkContainsOfNothing(t *testing.T, ctx context.Context, r WorktreeRig) {
	tip := r.Grow("main")
	missing := "0123456789012345678901234567890123456789"
	for _, pair := range [][2]string{{missing, tip}, {tip, missing}} {
		if _, err := r.Bases.Contains(ctx, r.Repository, pair[0], pair[1]); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("contains %v: %v", pair, err)
		}
	}
}

func checkWorktreeStartsAtItsBase(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	r.Grow("main")
	worktree, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("based"), Branch: "lane/based", Base: first})
	if err != nil {
		t.Fatal(err)
	}
	if worktree.Head != first {
		t.Errorf("head %s, want %s", worktree.Head, first)
	}
}

func checkDetachedMakesNoBranch(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	r.Grow("main")
	before := r.Branches()
	worktree, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("review"), Base: first, Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	if worktree.Branch != "" || !worktree.Detached || worktree.Head != first || !worktree.Clean || worktree.Path != r.Path("review") {
		t.Errorf("worktree %+v, want detached and clean at %s", worktree, first)
	}
	inspected, err := r.Worktrees.Inspect(ctx, worktree.Path)
	if err != nil || inspected != worktree {
		t.Errorf("inspected %+v: %v", inspected, err)
	}
	if after := r.Branches(); !slices.Equal(after, before) {
		t.Errorf("the repository had branches %v and has %v", before, after)
	}
}

func checkDetachedRetry(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	spec := ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("review"), Base: first, Detached: true}
	one, err := r.Worktrees.Create(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	two, err := r.Worktrees.Create(ctx, spec)
	if err != nil || one != two {
		t.Errorf("first %+v, retry %+v: %v", one, two, err)
	}
}

func checkDetachedAtOneCommit(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	for _, name := range []string{"review-1", "review-2"} {
		if _, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path(name), Base: first, Detached: true}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func checkDetachedConflicts(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	if _, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("review"), Base: first, Detached: true}); err != nil {
		t.Fatal(err)
	}
	r.create(t, ctx, "one", "lane/one")
	asBranch := ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("review"), Branch: "lane/two"}
	if _, err := r.Worktrees.Create(ctx, asBranch); !errors.Is(err, ports.ErrWorktreeConflict) {
		t.Errorf("a branch where a detached worktree stands: %v", err)
	}
	asDetached := ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("one"), Base: first, Detached: true}
	if _, err := r.Worktrees.Create(ctx, asDetached); !errors.Is(err, ports.ErrWorktreeConflict) {
		t.Errorf("a detached worktree where a branch's stands: %v", err)
	}
}

// A review's worktree is thrown away by design, so a stop has nothing in it to save: it reads clean and
// ahead of nothing, whatever the reviewer left in it, and the guard snapshots nothing and holds nothing.
func checkDetachedHoldsNothingToSave(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	worktree, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("review"), Base: first, Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	r.Dirty(worktree.Path)
	found, err := r.Worktrees.Survey(ctx, worktree.Path)
	if err != nil {
		t.Fatal(err)
	}
	if found.Dirty || len(found.DirtyPaths) != 0 || found.Ahead != 0 || found.Branch != "" || found.Remote != "" || found.Head != first {
		t.Errorf("a survey of a detached worktree reads %+v", found)
	}
	if plan := lanes.PlanGuard(found, lanes.GuardRules{Push: true}); plan != (lanes.GuardPlan{}) {
		t.Errorf("a guard would act on a detached worktree: %+v", plan)
	}
	inspected, err := r.Worktrees.Inspect(ctx, worktree.Path)
	if err != nil || inspected.Clean || !inspected.Detached {
		t.Errorf("inspect reads what is there: %+v: %v", inspected, err)
	}
}

func checkDetachedDiscard(t *testing.T, ctx context.Context, r WorktreeRig) {
	first := r.Grow("main")
	before := r.Branches()
	worktree, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("review"), Base: first, Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	r.Dirty(worktree.Path)
	for range 2 {
		if err := r.Worktrees.Discard(ctx, r.Repository, worktree.Path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Worktrees.Inspect(ctx, worktree.Path); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("after a discard: %v", err)
	}
	if after := r.Branches(); !slices.Equal(after, before) {
		t.Errorf("the repository had branches %v and has %v", before, after)
	}
	if _, err := r.Worktrees.Create(ctx, ports.WorktreeSpec{Repository: r.Repository, Path: r.Path("review"), Base: first, Detached: true}); err != nil {
		t.Errorf("the place was not free after a discard: %v", err)
	}
}

func checkDiffNamesTheChange(t *testing.T, ctx context.Context, r WorktreeRig) {
	base := r.Grow("main")
	head := r.Touch("notes.txt")
	diff, err := r.Worktrees.Diff(ctx, r.Repository, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(diff.Files, []string{"notes.txt"}) || !strings.Contains(diff.Patch, "notes.txt") {
		t.Errorf("a diff reads %+v", diff)
	}
	empty, err := r.Worktrees.Diff(ctx, r.Repository, head, head)
	if err != nil || empty.Patch != "" || len(empty.Files) != 0 {
		t.Errorf("a commit against itself: %+v: %v", empty, err)
	}
}

func checkDiffOfNothing(t *testing.T, ctx context.Context, r WorktreeRig) {
	head := r.Grow("main")
	missing := strings.Repeat("a", 40)
	for _, pair := range [][2]string{{missing, head}, {head, missing}} {
		if _, err := r.Worktrees.Diff(ctx, r.Repository, pair[0], pair[1]); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("diff %v: %v", pair, err)
		}
	}
	if _, err := r.Worktrees.Diff(ctx, r.Repository, "--output=x", head); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a base that reads as an option: %v", err)
	}
}

func checkContextFile(t *testing.T, ctx context.Context, r WorktreeRig) {
	created := r.create(t, ctx, "one", "lane/one")
	pack, err := r.Worktrees.WriteContext(ctx, created.Path, "the pack")
	if err != nil {
		t.Fatal(err)
	}
	path, err := r.Worktrees.WriteContextFile(ctx, created.Path, "review.diff", "the diff")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Dir(pack) || filepath.Base(path) != "review.diff" || !strings.HasPrefix(path, created.Path) {
		t.Errorf("a pack at %s and a file at %s", pack, path)
	}
	again, err := r.Worktrees.WriteContextFile(ctx, created.Path, "review.diff", "the diff, again")
	if err != nil || again != path {
		t.Errorf("a repeat wrote to %s: %v", again, err)
	}
	for _, name := range []string{"", "../escape", "nested/file", ".", ".."} {
		if _, err := r.Worktrees.WriteContextFile(ctx, created.Path, name, "x"); err == nil {
			t.Errorf("a file named %q was written", name)
		}
	}
	survey, err := r.Worktrees.Survey(ctx, created.Path)
	if err != nil || survey.Dirty {
		t.Errorf("a context file reads as a change: %+v: %v", survey, err)
	}
	if err := r.Worktrees.Remove(ctx, r.Repository, created.Path); err != nil {
		t.Errorf("a context file held a worktree back: %v", err)
	}
}

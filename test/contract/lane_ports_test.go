package contract

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/hookrun"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/gittest"
)

func TestProcessRunnerContract(t *testing.T) {
	t.Run("os/exec", func(t *testing.T) {
		RunProcessRunner(t, func(t *testing.T) ProcessRig {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return ProcessRig{
				Runner: osexec.Runner{}, PrintEnv: "/usr/bin/env", PrintArgs: "/bin/echo", PrintDir: "/bin/pwd",
				Fail: "/usr/bin/false", Block: "/bin/sleep", BlockArgs: []string{"60"}, Dir: dir,
			}
		})
	})
	t.Run("fake", func(t *testing.T) {
		RunProcessRunner(t, func(*testing.T) ProcessRig {
			runner := fake.NewProcessRunner()
			runner.Install("/bin/print-env", fake.PrintEnv)
			runner.Install("/bin/print-args", fake.PrintArgs)
			runner.Install("/bin/print-dir", fake.PrintDir)
			runner.Install("/bin/fail", fake.Fail)
			runner.Install("/bin/block", fake.Block)
			return ProcessRig{
				Runner: runner, PrintEnv: "/bin/print-env", PrintArgs: "/bin/print-args", PrintDir: "/bin/print-dir",
				Fail: "/bin/fail", Block: "/bin/block", Dir: "/work",
			}
		})
	})
}

func TestWorktreesContract(t *testing.T) {
	t.Parallel()
	t.Run("git", func(t *testing.T) {
		RunWorktrees(t, func(t *testing.T) WorktreeRig {
			repo := gittest.New(t)
			repo.Commit("README.txt", "seed\n")
			lanesDir := filepath.Join(filepath.Dir(repo.Dir), "lanes")
			return WorktreeRig{
				Worktrees: gitcli.Worktrees{Env: repo.Env()}, Bases: gitcli.Worktrees{Env: repo.Env()}, Repository: repo.Dir,
				Path: func(name string) string { return filepath.Join(lanesDir, name) },
				Dirty: func(path string) {
					if err := writeFile(filepath.Join(path, "untracked.txt"), "work\n"); err != nil {
						t.Fatal(err)
					}
				},
				Grow: repo.Advance,
			}
		})
	})
	t.Run("fake", func(t *testing.T) {
		RunWorktrees(t, func(*testing.T) WorktreeRig {
			worktrees := fake.NewWorktrees()
			return WorktreeRig{
				Worktrees: worktrees, Bases: worktrees, Repository: "/work/repository",
				Path:  func(name string) string { return "/work/lanes/" + name },
				Dirty: worktrees.Dirty,
				Grow:  func(branch string) string { return worktrees.Grow("/work/repository", branch) },
			}
		})
	})
}

func TestHookRunnerContract(t *testing.T) {
	t.Parallel()
	t.Run("os/exec", func(t *testing.T) {
		RunHookRunner(t, func(t *testing.T) HookRig {
			return HookRig{
				Runner: hookrun.Runner{Processes: osexec.Runner{}}, Pass: []string{"/usr/bin/true"}, Fail: []string{"/usr/bin/false"},
				Slow: []string{"/bin/sleep", "60"}, Dir: t.TempDir(),
			}
		})
	})
	t.Run("fake", func(t *testing.T) {
		RunHookRunner(t, func(*testing.T) HookRig {
			runner := fake.NewHookRunner()
			runner.Programs["fail"] = func(ports.HookCommand) ports.HookResult {
				return ports.HookResult{Exit: ports.ExitStatus{Code: 1}}
			}
			runner.Programs["slow"] = func(ports.HookCommand) ports.HookResult {
				return ports.HookResult{Exit: ports.ExitStatus{Signal: ports.SignalTerminate}}
			}
			return HookRig{Runner: runner, Pass: []string{"pass"}, Fail: []string{"fail"}, Slow: []string{"slow"}, Dir: "/work"}
		})
	})
}

func TestProfileSourceContract(t *testing.T) {
	t.Parallel()
	t.Run("filesystem", func(t *testing.T) {
		RunProfileSource(t, func(t *testing.T) ProfileRig {
			repository := t.TempDir()
			return ProfileRig{
				Source: profiles.Filesystem{}, Repository: repository,
				Layer: func(text string) {
					path := filepath.Join(repository, profiles.RepositoryFile)
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := writeFile(path, text); err != nil {
						t.Fatal(err)
					}
				},
			}
		})
	})
	t.Run("in-memory", func(t *testing.T) {
		RunProfileSource(t, func(*testing.T) ProfileRig {
			source := fake.NewProfileSource()
			return ProfileRig{
				Source: source, Repository: "/work/repository",
				Layer: func(text string) { source.Put("/work/repository", text) },
			}
		})
	})
}

// TestSpawnReadsNoLoginFiles gives a child a home whose login files would write a
// marker. A direct spawn reads none of them.
func TestSpawnReadsNoLoginFiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	marker := filepath.Join(home, "login-file-ran")
	for _, name := range []string{".profile", ".bash_profile", ".bashrc", ".zshenv", ".zprofile", ".zshrc"} {
		if err := writeFile(filepath.Join(home, name), "touch "+marker+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	process, err := osexec.Runner{}.Start(t.Context(), ports.ProcessSpec{Path: "/usr/bin/env", Env: []string{"HOME=" + home}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a login file ran")
	}
}

// TestWorktreeRetryAfterACrash holds what `git worktree add` can leave behind when
// a process dies around it.
func TestWorktreeRetryAfterACrash(t *testing.T) {
	t.Parallel()
	spec := func(repo *gittest.Repo) ports.WorktreeSpec {
		return ports.WorktreeSpec{
			Repository: repo.Dir, Path: filepath.Join(filepath.Dir(repo.Dir), "lanes", "one"), Branch: "lane/one",
		}
	}
	tests := []struct {
		name  string
		crash func(*testing.T, *gittest.Repo, gitcli.Worktrees, ports.WorktreeSpec)
	}{
		{
			"the directory is gone and git still lists it",
			func(t *testing.T, _ *gittest.Repo, w gitcli.Worktrees, s ports.WorktreeSpec) {
				if _, err := w.Create(t.Context(), s); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(s.Path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			"the branch exists and no worktree does",
			func(t *testing.T, repo *gittest.Repo, _ gitcli.Worktrees, s ports.WorktreeSpec) {
				repo.Git("branch", s.Branch)
			},
		},
		{
			"an empty directory is left at the path",
			func(t *testing.T, _ *gittest.Repo, _ gitcli.Worktrees, s ports.WorktreeSpec) {
				if err := os.MkdirAll(s.Path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := gittest.New(t)
			repo.Commit("README.txt", "seed\n")
			worktrees := gitcli.Worktrees{Env: repo.Env()}
			s := spec(repo)
			tt.crash(t, repo, worktrees, s)
			got, err := worktrees.Create(t.Context(), s)
			if err != nil {
				t.Fatal(err)
			}
			if got.Branch != s.Branch || !got.Clean {
				t.Errorf("worktree %+v", got)
			}
		})
	}
}

func TestWorktreeLeavesAnOccupiedPathAlone(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("README.txt", "seed\n")
	s := ports.WorktreeSpec{Repository: repo.Dir, Path: filepath.Join(filepath.Dir(repo.Dir), "occupied"), Branch: "lane/one"}
	if err := os.MkdirAll(s.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(s.Path, "someone.txt"), "theirs\n"); err != nil {
		t.Fatal(err)
	}
	_, err := gitcli.Worktrees{Env: repo.Env()}.Create(t.Context(), s)
	if !errors.Is(err, ports.ErrWorktreeConflict) {
		t.Errorf("got %v", err)
	}
}

func TestWorktreeStartsAtTheBase(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	first := repo.Commit("README.txt", "one\n")
	repo.Commit("README.txt", "two\n")
	worktree, err := gitcli.Worktrees{Env: repo.Env()}.Create(t.Context(), ports.WorktreeSpec{
		Repository: repo.Dir, Path: filepath.Join(filepath.Dir(repo.Dir), "lanes", "one"), Branch: "lane/one", Base: first,
	})
	if err != nil {
		t.Fatal(err)
	}
	if worktree.Head != first {
		t.Errorf("head %s, want %s", worktree.Head, first)
	}
}

// A repository with a remote starts lanes from what the remote holds, which is not what its own
// branch holds: the checkout a person works in falls behind, and a lane must not.
func TestBaseTipIsWhatTheRemoteHolds(t *testing.T) {
	t.Parallel()
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	local := clone.Git("rev-parse", "main")
	remote := origin.Advance("main")
	tip, err := worktrees.BaseTip(t.Context(), clone.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if tip.Commit != remote || tip.Ref != "origin/main" {
		t.Errorf("tip %+v, want origin/main at %s", tip, remote)
	}
	if now := clone.Git("rev-parse", "main"); now != local {
		t.Errorf("reading the base moved the local branch from %s to %s", local, now)
	}
	// A branch the remote holds and the clone never made is a base too.
	origin.Branch("release/1.2", remote)
	if tip, err = worktrees.BaseTip(t.Context(), clone.Dir, "release/1.2"); err != nil || tip.Commit != remote || tip.Ref != "origin/release/1.2" {
		t.Errorf("a branch only the remote holds: %+v: %v", tip, err)
	}
}

// With a remote there is no other answer than the remote's: a branch only this checkout has, and a
// remote that does not answer, are an unavailable base, never HEAD.
func TestBaseTipNeverFallsBackToTheCheckout(t *testing.T) {
	t.Parallel()
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	clone.Branch("only-here", "main")
	if tip, err := worktrees.BaseTip(t.Context(), clone.Dir, "only-here"); !errors.Is(err, ports.ErrBaseUnavailable) {
		t.Errorf("a branch the remote lacks: %+v, %v", tip, err)
	}
	if err := os.RemoveAll(origin.Dir); err != nil {
		t.Fatal(err)
	}
	if tip, err := worktrees.BaseTip(t.Context(), clone.Dir, "main"); !errors.Is(err, ports.ErrBaseUnavailable) {
		t.Errorf("a remote that does not answer: %+v, %v", tip, err)
	}
}

// TestALaneGetsItsPlace composes the three ports on real git, a real profile file,
// and the allowlist.
func TestALaneGetsItsPlace(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit(profiles.RepositoryFile, "[agent]\nmodel = \"haiku\"\n\n[worktree]\nbranch_prefix = \"work/\"\n")
	parent := map[string]string{"HOME": "/home/someone", "PATH": "/usr/bin", "CLAUDECODE": "1", "ANTHROPIC_API_KEY": "k"}
	provisioner := worker.Provisioner{
		Profiles:  profiles.Filesystem{},
		Worktrees: gitcli.Worktrees{Env: repo.Env()},
		Lookup: func(name string) (string, bool) {
			value, ok := parent[name]
			return value, ok
		},
	}
	lane, err := provisioner.Prepare(t.Context(), worker.LaneRequest{
		Lane: "lane-1", Repository: repo.Dir, Tags: map[string]string{lanes.TagPrefix + "LANE": "lane-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lane.Worktree.Branch != "work/lane-1" || lane.Worktree.Path != filepath.Join(filepath.Dir(repo.Dir), "repository-lanes", "lane-1") {
		t.Errorf("worktree %+v", lane.Worktree)
	}
	if lane.Profile.Agent.Model != "haiku" || lane.ProfileDigest == "" {
		t.Errorf("profile %+v", lane.Profile)
	}
	for _, entry := range lane.Environment {
		if entry == "CLAUDECODE=1" || entry == "ANTHROPIC_API_KEY=k" {
			t.Errorf("%s reached the lane", entry)
		}
	}
	if _, err := os.Stat(filepath.Join(lane.Worktree.Path, "README.txt")); err == nil {
		t.Error("the worktree holds a file its branch does not")
	}
}

// TestDetachedProcessLeavesTheGroup holds what a shim depends on: a signal to the group
// of whatever started it does not reach it.
func TestDetachedProcessLeavesTheGroup(t *testing.T) {
	t.Parallel()
	process, err := osexec.Runner{}.Start(t.Context(), ports.ProcessSpec{Path: "/bin/sleep", Args: []string{"30"}, Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Signal(ports.SignalKill)
		_, _ = process.Wait()
	}()
	group, err := syscall.Getpgid(process.PID())
	if err != nil {
		t.Fatal(err)
	}
	if group != process.PID() || group == syscall.Getpgrp() {
		t.Errorf("process group %d of process %d, and this process's is %d", group, process.PID(), syscall.Getpgrp())
	}
}

// TestOutlivingProcessLeavesTheGroup holds the least a process that must outlive the worker's
// service gets from any runner: the group a service manager signals does not hold it (D20).
func TestOutlivingProcessLeavesTheGroup(t *testing.T) {
	t.Parallel()
	process, err := osexec.Runner{}.Start(t.Context(), ports.ProcessSpec{Path: "/bin/sleep", Args: []string{"30"}, Outlive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Signal(ports.SignalKill)
		_, _ = process.Wait()
	}()
	group, err := syscall.Getpgid(process.PID())
	if err != nil {
		t.Fatal(err)
	}
	if group != process.PID() || group == syscall.Getpgrp() {
		t.Errorf("process group %d of process %d, and this process's is %d", group, process.PID(), syscall.Getpgrp())
	}
}

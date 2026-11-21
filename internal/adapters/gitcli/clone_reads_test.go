package gitcli_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestReadingACloneLeavesItsIndexAsItWas reads a clone whose index holds stale timestamps, which `git status` refreshes
// and writes back when it may take the index's lock. A read takes no lock, so the person's index stays as it was.
func TestReadingACloneLeavesItsIndexAsItWas(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	repo.Commit("b.md", "b\n")
	index := filepath.Join(repo.Dir, ".git", "index")
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(repo.Dir, "a.md"), future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(index, past, past); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (gitcli.Worktrees{Env: repo.Env()}).Edits(t.Context(), repo.Dir, 100); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !info.ModTime().Equal(past.Truncate(time.Second)) && !info.ModTime().Equal(past) {
		t.Errorf("the read rewrote the index: modified %s, want %s, contents equal %v", info.ModTime(), past, bytes.Equal(before, after))
	}
	if _, err := os.Stat(index + ".lock"); err == nil {
		t.Error("the read left the index's lock behind")
	}
}

// shimmed puts a git and a ropeshift on PATH that record each argument they are given, separated by the unit
// separator, one call to a line, and answer the reads the commands make with plausible output.
func shimmed(t *testing.T) (bin, log string) {
	t.Helper()
	bin, log = t.TempDir(), filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\n{ printf '%s' \"$(basename \"$0\")\"; for a in \"$@\"; do printf '\\037%s' \"$a\"; done; printf '\\n'; } >> \"$ARGV_LOG\"\n" +
		"case \"$1\" in rev-parse) echo \"$GITDIR\";; write-tree) echo tree;; commit-tree) echo commit;; esac\n"
	for _, name := range []string{"git", "ropeshift"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return bin, log
}

// calls reads what the shims recorded: each call's program and arguments.
func calls(t *testing.T, log string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for line := range strings.Lines(string(raw)) {
		out = append(out, strings.Split(strings.TrimSuffix(line, "\n"), "\x1f"))
	}
	return out
}

// TestTheCopiedCommandsKeepEachWordUnderShAndZsh runs the commands of every step, as a person pastes them, under sh and
// under zsh with no startup files, on names and paths a shell reads specially, and holds the arguments git and ropeshift
// receive to exactly the names and paths: nothing expands, splits, or becomes an option or a forced push. A path that
// holds a newline or a backslash is in no command.
func TestTheCopiedCommandsKeepEachWordUnderShAndZsh(t *testing.T) {
	shells := [][]string{{"sh", "-c"}}
	if _, err := exec.LookPath("zsh"); err == nil {
		shells = append(shells, []string{"zsh", "-f", "-c"})
	} else {
		t.Log("zsh is not installed, so only sh runs the commands")
	}
	hostile := []string{"+plus.md", "=eq.md", "-dash.md", "~tilde.md", "a b.md", "it's.md", `q"uote.md`, "$HOME.md", "`id`.md", "dir/=x.md", "*.md"}
	refused := []string{"new\nline.md", `back\slash.md`}
	for _, shell := range shells {
		for _, names := range []struct{ branch, remote string }{{"+main", "=origin"}, {"=main", "~up"}} {
			t.Run(shell[0]+" "+names.branch, func(t *testing.T) {
				bin, log := shimmed(t)
				clone := filepath.Join(t.TempDir(), "=my clone $x `y` 'q'")
				if err := os.MkdirAll(clone, 0o700); err != nil {
					t.Fatal(err)
				}
				edits := ledger.CloneEdits{Branch: names.branch, Head: "abc", Remote: names.remote}
				for _, path := range append(slices.Clone(hostile), refused...) {
					edits.Edits = append(edits.Edits, ledger.CloneEdit{Path: path, State: ledger.EditUntracked})
				}
				edits.Edits = append(edits.Edits, ledger.CloneEdit{Path: "=sent.md", State: ledger.EditUnpushed})
				steps := ledger.EditSteps(edits, ledger.EditPlace{
					Project: "=p", Clone: clone, Stamp: "20261120-153000",
					Planning: []ledger.PlanningBranch{{WorkOrder: "plan-1", Lane: "lane-1", Branch: "lane/~x"}},
				})
				if len(steps) != 3 {
					t.Fatalf("steps %+v", steps)
				}
				for _, step := range steps {
					cmd := exec.Command(shell[0], append(shell[1:], strings.Join(step.Commands, "\n"))...)
					cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "ARGV_LOG="+log, "GITDIR="+t.TempDir())
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("%s failed under %s: %v\n%s\n%s", step.Kind, shell[0], err, strings.Join(step.Commands, "\n"), out)
					}
				}
				got := calls(t, log)
				has := func(want []string) {
					t.Helper()
					if !slices.ContainsFunc(got, func(call []string) bool { return slices.Equal(call, want) }) {
						t.Errorf("no call %q among:\n%q", want, got)
					}
				}
				has(append([]string{"git", "update-index", "--add", "--remove", "--"}, hostile...))
				has([]string{"git", "-C", clone, "push", names.remote, "refs/heads/edits/20261120-153000:refs/heads/edits/20261120-153000"})
				has([]string{"git", "-C", clone, "push", names.remote, "refs/heads/" + names.branch + ":refs/heads/" + names.branch})
				has([]string{"git", "read-tree", "refs/heads/lane/~x"})
				for _, call := range got {
					joined := strings.Join(call, " ")
					if strings.Contains(joined, "line.md") || strings.Contains(joined, "slash.md") {
						t.Errorf("a call names a path that holds a newline or a backslash: %q", call)
					}
					if call[0] == "ropeshift" && (len(call) != 10 || call[4] != "--project" || call[5] != "=p" || !strings.Contains(call[9], "$HOME.md, `id`.md")) {
						t.Errorf("the message reads %q", call)
					}
				}
			})
		}
	}
}

// A HEAD git fails to read, as a git that stops part way does, is a failed read, never a clone with no commit, whose
// steps would start a branch with no parent from nothing.
func TestAHeadGitFailsToReadFailsTheRead(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	if err := os.WriteFile(filepath.Join(repo.Dir, "new.md"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	bin := t.TempDir()
	shim := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in 'HEAD^{commit}') echo 'fatal: unable to read HEAD' >&2; exit 128;; esac; done\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	// The command a read runs is found on this process's PATH.
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if got, err := (gitcli.Worktrees{Env: repo.Env()}).Edits(t.Context(), repo.Dir, 10); err == nil {
		t.Errorf("a HEAD git failed to read reads %+v", got)
	}
}

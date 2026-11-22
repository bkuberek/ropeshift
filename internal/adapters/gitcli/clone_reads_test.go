package gitcli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
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

// A commit the ledger records as the forge's merge of a work order, such as a squash, carries no trailer of
// ropeshift's, and is left out of the base's side when it is named as landed work.
func TestACommitThatLandedWorkIsNoPersonsPush(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	clone.Git("checkout", "--quiet", "-b", "lane/lane-1")
	clone.Commit("a.md", "a, by the lane\n")
	clone.Commit("b.md", "b, by the lane\n")
	clone.Git("checkout", "--quiet", "main")
	squashed := origin.Commit("a.md", "a, squashed on the forge\n")
	origin.Commit("b.md", "b, pushed by a person\n")
	clone.Git("fetch", "--quiet", "origin")
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	got, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{Commits: []string{squashed}}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Pushed, []string{"b.md"}) {
		t.Errorf("the base's side %v, want only the person's b.md", got.Pushed)
	}
}

// A rebase on the forge writes a pull request's commits on the base with no trailer, so the ledger records the base's tip
// before the merge and after it, and the commits after the first up to the second are left out of the base's side. A
// person's commit before the range or after it stays, and so does one a range names in a clone that lacks it.
func TestTheCommitsOfARecordedRangeAreNoPersonsPush(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	clone.Git("checkout", "--quiet", "-b", "lane/lane-1")
	clone.Commit("a.md", "a, by the lane\n")
	clone.Git("checkout", "--quiet", "main")
	before := origin.Commit("c.md", "c, pushed by a person\n")
	origin.Commit("a.md", "a, rebased on the forge\n")
	origin.Commit("b.md", "b, rebased on the forge\n")
	after := origin.Commit("d.md", "d, rebased on the forge\n")
	origin.Commit("e.md", "e, pushed by a person\n")
	clone.Git("fetch", "--quiet", "origin")
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	pushed := func(landed ledger.Landed) []string {
		t.Helper()
		got, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", landed, 100)
		if err != nil {
			t.Fatal(err)
		}
		return got.Pushed
	}
	if got := pushed(ledger.Landed{}); !slices.Equal(got, []string{"a.md", "b.md", "c.md", "d.md", "e.md"}) {
		t.Fatalf("with no range the base's side is %v", got)
	}
	if got := pushed(ledger.Landed{Ranges: []ledger.LandedRange{{Before: before, After: after}}}); !slices.Equal(got, []string{"c.md", "e.md"}) {
		t.Errorf("the base's side with the range is %v, want the person's c.md and e.md", got)
	}
	// The ledger names the last commit of the range as the merge's commit too.
	both := ledger.Landed{Commits: []string{after}, Ranges: []ledger.LandedRange{{Before: before, After: after}}}
	if got := pushed(both); !slices.Equal(got, []string{"c.md", "e.md"}) {
		t.Errorf("the base's side with the range and its last commit is %v, want the person's c.md and e.md", got)
	}
	unknown := strings.Repeat("f", 40)
	for name, landed := range map[string]ledger.Landed{
		"a before the clone lacks": {Ranges: []ledger.LandedRange{{Before: unknown, After: after}}},
		"an after the clone lacks": {Ranges: []ledger.LandedRange{{Before: before, After: unknown}}},
		"ends that are no commits": {Ranges: []ledger.LandedRange{{Before: "--all", After: "-h"}}},
	} {
		if got := pushed(landed); !slices.Equal(got, []string{"a.md", "b.md", "c.md", "d.md", "e.md"}) {
			t.Errorf("%s: the base's side is %v, want every path", name, got)
		}
	}
}

// Merges that follow one another each record a range that begins where the one before it ended, and each is left out.
func TestRecordedRangesThatFollowOneAnotherAreEachLeftOut(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	clone.Git("checkout", "--quiet", "-b", "lane/lane-1")
	clone.Commit("a.md", "a, by the lane\n")
	clone.Git("checkout", "--quiet", "main")
	start := origin.Commit("c.md", "c, pushed by a person\n")
	origin.Commit("a.md", "a, rebased on the forge\n")
	middle := origin.Commit("b.md", "b, rebased on the forge\n")
	origin.Commit("d.md", "d, rebased on the forge\n")
	end := origin.Commit("f.md", "f, rebased on the forge\n")
	clone.Git("fetch", "--quiet", "origin")
	landed := ledger.Landed{Ranges: []ledger.LandedRange{{Before: start, After: middle}, {Before: middle, After: end}}}
	got, err := gitcli.Worktrees{Env: clone.Env()}.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", landed, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Pushed, []string{"c.md"}) {
		t.Errorf("the base's side is %v, want only the person's c.md", got.Pushed)
	}
}

// A range counts only when its first end is an ancestor of its last, as a rebase's is: one a worker reported that is not,
// whatever commit it names for either end, leaves no commit out of a person's pushes.
func TestARangeWhoseFirstEndIsNoAncestorOfItsLastLeavesNothingOut(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	clone.Git("checkout", "--quiet", "-b", "lane/lane-1")
	onLane := clone.Commit("a.md", "a, by the lane\n")
	clone.Git("checkout", "--quiet", "main")
	first := origin.Commit("c.md", "c, pushed by a person\n")
	origin.Commit("a.md", "a, rebased on the forge\n")
	last := origin.Commit("b.md", "b, rebased on the forge\n")
	clone.Git("fetch", "--quiet", "origin")
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	every := []string{"a.md", "b.md", "c.md"}
	for name, span := range map[string]ledger.LandedRange{
		"a first end on another line of history": {Before: onLane, After: last},
		"a first end after the last":             {Before: last, After: first},
		"a first end that is the last":           {Before: last, After: last},
	} {
		got, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{Ranges: []ledger.LandedRange{span}}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got.Pushed, every) || !got.Unranged {
			t.Errorf("%s: the base's side is %v and unranged %v, want every path and a merge with no range", name, got.Pushed, got.Unranged)
		}
	}
	got, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{Ranges: []ledger.LandedRange{{Before: first, After: last}}}, 100)
	if err != nil || !slices.Equal(got.Pushed, []string{"c.md"}) || got.Unranged {
		t.Errorf("a range whose first end is an ancestor of its last leaves out its own commits: %v, unranged %v, %v", got.Pushed, got.Unranged, err)
	}
}

// A merge commit the ledger names as one of a pull request with no range is said to lie on the base's side when it lies
// between the lane's branch and the base, and not when it lies before the commit they last shared, or lies nowhere in the
// clone, and no commit of it is left out of a person's pushes.
func TestAMergeCommitWithNoRangeIsSaidToLieOnTheBasesSide(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	before := origin.Commit("b.md", "b, merged on the forge before the lane began\n")
	clone := origin.Clone("registered")
	clone.Git("checkout", "--quiet", "-b", "lane/lane-1")
	clone.Commit("a.md", "a, by the lane\n")
	clone.Git("checkout", "--quiet", "main")
	origin.Commit("a.md", "a, rebased on the forge\n")
	last := origin.Commit("c.md", "c, rebased on the forge\n")
	clone.Git("fetch", "--quiet", "origin")
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	for name, tt := range map[string]struct {
		unranged []string
		want     bool
	}{
		"none":                              {nil, false},
		"one between the fork and the base": {[]string{last}, true},
		"one before the fork":               {[]string{before}, false},
		"one the clone does not hold":       {[]string{strings.Repeat("f", 40)}, false},
		"one that is no commit ID":          {[]string{"--all"}, false},
	} {
		got, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{Unranged: tt.unranged}, 100)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Unranged != tt.want || !slices.Equal(got.Pushed, []string{"a.md", "c.md"}) {
			t.Errorf("%s: unranged %v and the base's side %v, want %v and every path of the commits since the fork", name, got.Unranged, got.Pushed, tt.want)
		}
	}
}

// Only a ref or a fork point git says is missing reads as ErrNotFound: a context that ended is an error of its own, so
// a reading never takes a failure for a branch that is gone.
func TestOnlyAMissingRefReadsAsNotFound(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	clone.Git("branch", "lane/lane-1")
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := worktrees.Diverged(ended, clone.Dir, "lane/lane-1", "main", ledger.Landed{}, 100); err == nil || errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a context that ended reads %v", err)
	}
	if _, err := worktrees.Holds(ended, clone.Dir, "lane/lane-1", "main"); err == nil || errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a context that ended reads %v", err)
	}
	if _, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/gone", "main", ledger.Landed{}, 100); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a missing branch reads %v", err)
	}
	if _, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "trunk", ledger.Landed{}, 100); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a missing base reads %v", err)
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

// What the person's files hold is read as git would store them, without writing an object, and whether a lane's branch
// holds the branch they were committed on is read from the history.
func TestTheContentsOfFilesAndWhatABranchHoldsAreRead(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	repo.Git("branch", "lane/lane-1")
	if err := os.WriteFile(filepath.Join(repo.Dir, "a.md"), []byte("a, edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	objects := repo.Git("count-objects")
	got, err := worktrees.Contents(t.Context(), repo.Dir, []string{"a.md", "gone.md"})
	if err != nil {
		t.Fatal(err)
	}
	if want := repo.Git("hash-object", "a.md"); got["a.md"] != want || got["gone.md"] != "" || len(got) != 2 {
		t.Errorf("contents %v, want a.md at %s", got, want)
	}
	if repo.Git("count-objects") != objects {
		t.Error("reading the contents wrote an object")
	}
	repo.Git("branch", "edits/wo-1-abc", "main")
	if held, err := worktrees.Holds(t.Context(), repo.Dir, "lane/lane-1", "edits/wo-1-abc"); err != nil || !held {
		t.Errorf("the lane's branch holds the edits branch: %v, %v", held, err)
	}
	repo.Commit("b.md", "b\n")
	repo.Git("branch", "-f", "edits/wo-1-abc", "main")
	if held, err := worktrees.Holds(t.Context(), repo.Dir, "lane/lane-1", "edits/wo-1-abc"); err != nil || held {
		t.Errorf("the lane's branch holds a later commit: %v, %v", held, err)
	}
	if held, err := worktrees.Holds(t.Context(), repo.Dir, "lane/lane-1", "edits/none"); err != nil || held {
		t.Errorf("an edits branch nobody made: %v, %v", held, err)
	}
	if _, err := worktrees.Holds(t.Context(), repo.Dir, "lane/gone", "edits/wo-1-abc"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a lane's branch that is gone: %v", err)
	}
}

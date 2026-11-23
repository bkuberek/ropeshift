package claudecode

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// gitLane is a lane's worktree of a member's clone, with the lane's spec.
func gitLane(t *testing.T) (ports.SessionSpec, string) {
	t.Helper()
	clone := t.TempDir()
	committed(t, clone, map[string]string{"README.md": "a clone\n"})
	worktree := filepath.Join(t.TempDir(), "lane")
	if out, err := exec.Command("git", "-C", clone, "worktree", "add", "--quiet", "-b", "lane/1", worktree).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	spec := laneSpec(t)
	spec.Dir, spec.Repository = worktree, clone
	return spec, clone
}

// A lane's own git directory and the one it shares with its clone hold its HEAD, its refs and its objects, and git reads
// them in every command, so the lane's file tools read them with no prompt, where a read anywhere else outside its
// worktree asks.
func TestALaneReadsItsOwnGitDirectoriesWithNoPrompt(t *testing.T) {
	spec, clone := gitLane(t)
	policy := compilePolicy(spec)
	common := filepath.Join(clone, ".git")
	own := filepath.Join(common, "worktrees", "lane")
	for name, call := range map[string]lanes.ToolCall{
		"the common directory's config":    {Tool: "Read", Path: filepath.Join(common, "config"), Cwd: spec.Dir},
		"the common directory's packed":    {Tool: "Grep", Path: filepath.Join(common, "refs"), Cwd: spec.Dir},
		"the lane's own HEAD":              {Tool: "Read", Path: filepath.Join(own, "HEAD"), Cwd: spec.Dir},
		"a listing of the lane's own":      {Tool: "LS", Path: own, Cwd: spec.Dir},
		"the common directory in a shell":  {Tool: "Bash", Command: "cat " + filepath.Join(common, "HEAD"), Cwd: spec.Dir},
		"the lane's own directory in bash": {Tool: "Bash", Command: "ls " + own, Cwd: spec.Dir},
	} {
		if reason, asks := policy.Asks(call); asks {
			t.Errorf("%s asks: %s", name, reason)
		}
	}
}

// Nothing else of the clone is a root: its working files, its sibling worktrees' files, and anywhere else still ask, and a
// path only a shell could name still asks, because no root is worked out from a command substitution.
func TestALanesGitRootsOpenNothingElse(t *testing.T) {
	spec, clone := gitLane(t)
	policy := compilePolicy(spec)
	for name, call := range map[string]lanes.ToolCall{
		"the clone's working file": {Tool: "Read", Path: filepath.Join(clone, "README.md"), Cwd: spec.Dir},
		"a place outside it all":   {Tool: "Read", Path: filepath.Join(t.TempDir(), "notes.txt"), Cwd: spec.Dir},
		"the home directory":       {Tool: "Read", Path: "/etc/hostname", Cwd: spec.Dir},
		"a substitution":           {Tool: "Bash", Command: `cat "$(git rev-parse --git-common-dir)/config"`, Cwd: spec.Dir},
		"a variable":               {Tool: "Bash", Command: `cat "$GIT_DIR/config"`, Cwd: spec.Dir},
	} {
		if _, asks := policy.Asks(call); !asks {
			t.Errorf("%s does not ask", name)
		}
	}
}

// A lane can edit the `.git` file of its worktree, so the directories it reads are the ones its clone registered for it
// and never the ones that file names: a lane that points it elsewhere reads nothing more with no prompt.
func TestALanesGitRootsAreTheOnesItsCloneRegisteredAndNotTheOnesItsGitFileNames(t *testing.T) {
	spec, _ := gitLane(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec.Dir, ".git"), []byte("gitdir: "+outside+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := compilePolicy(spec)
	if _, asks := policy.Asks(lanes.ToolCall{Tool: "Read", Path: filepath.Join(outside, "secret"), Cwd: spec.Dir}); !asks {
		t.Error("a read in the directory the lane's .git file names does not ask")
	}
}

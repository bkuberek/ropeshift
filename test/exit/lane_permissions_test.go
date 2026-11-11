package exit

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// worksThenReachesOut is a change lane whose calls the generated rules decide, as the CLI reads them. It
// creates, moves, and deletes files in its worktree, and stages and commits them. Then it writes beside
// the worktree, deletes beside it, waits for the test, writes through a link that leads out of it, and
// pushes.
const worksThenReachesOut = `{
  "source": "FR-LANE-19: a lane's own work runs with no request, and what reaches out of the worktree asks",
  "turns": [[
    {"init": true},
    {"tool": "Write", "file": "notes.md", "ruled": true},
    {"tool": "Bash", "command": "mkdir -p docs && mv notes.md docs/notes.md && rm old.txt", "ruled": true},
    {"tool": "Bash", "command": "git add -A && git mv docs/notes.md docs/readme.md && git rm -q gone.txt && git commit -q -m work", "ruled": true},
    {"tool": "Write", "file": "../outside.txt", "ruled": true},
    {"tool": "Bash", "command": "rm -rf ../outside", "ruled": true},
    {"wait_file": "outside-made"},
    {"tool": "Write", "file": "out/escaped.txt", "ruled": true},
    {"tool": "Bash", "command": "git push origin HEAD", "ruled": true},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "artifacts": ["branch"], "checks": [{"name": "stand-in", "passed": true}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`

// TestALaneWorksInItsWorktreeWithNoRequestAndAsksForTheRest is FR-LANE-19 on the real binary, the real
// hook, and real git, with each call decided by the rules the driver generated. The lane's edits, file
// moves, deletions, stages, and commit raise no request and land on its branch. A write and a delete
// beside the worktree are refused by the hook, and a write through a link that leads out of the worktree
// and a push each raise a request that a member then refuses, so neither runs.
func TestALaneWorksInItsWorktreeWithNoRequestAndAsksForTheRest(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\napproval_wait = \"60s\"\nhandoff_timeout = \"2s\"\nmax_restarts = 0\n")
	repo.Commit("requirements.md", packRequirementsFile)
	repo.Commit("contract.md", packContractFile)
	repo.Commit("old.txt", "old")
	repo.Commit("gone.txt", "gone")
	if err := os.Symlink(filepath.Join("..", "outside"), filepath.Join(repo.Dir, "out")); err != nil {
		t.Fatal(err)
	}
	repo.Git("add", "out")
	repo.Git("commit", "-q", "-m", "a link out of the worktree")
	laneRoot := filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes")
	outside := filepath.Join(laneRoot, "outside")

	in.path = doubles.InstallFakeClaude(t, "lane-completed")
	dir := doubles.BinDir(in.path)
	if err := os.WriteFile(filepath.Join(dir, doubles.FixtureFile), []byte(worksThenReachesOut), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.home, ".gitconfig"), []byte("[user]\n\tname = Lane\n\temail = lane@example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--wake-tick", "20ms", "--source", repo.Dir)
	defer d.kill()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.startLane(id, "c1", "lane-1")

	// The fake creates a log before it writes a line, so wait for both lines, not the file.
	in.waitFor("the hook's two refusals beside the worktree", func() bool {
		raw, err := os.ReadFile(filepath.Join(dir, "denied.log"))
		return err == nil && strings.Count(string(raw), "\n") == 2
	})
	if requests := in.approvals(); len(requests) != 0 {
		t.Fatalf("the lane's own work raised requests: %+v", requests)
	}
	// The link's target is made only now, after the session's rules were written, so no deny rule names
	// it, and only the allow rule's reading of where the write lands keeps it from running.
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside-made"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	escaped := in.awaitRequest(authority.ApprovalPending)
	if escaped.Subject != "Write" || !strings.Contains(escaped.Path, "outside") {
		t.Fatalf("the write through the link asked %+v", escaped)
	}
	in.must("approval", "deny", escaped.ID, "--digest", escaped.SubjectDigest, "--reason", "Write inside the worktree.")
	var push authority.Approval
	in.waitFor("the push's request", func() bool {
		for _, row := range in.approvals() {
			if row.ID != escaped.ID && row.State == authority.ApprovalPending {
				push = row
				return true
			}
		}
		return false
	})
	if push.Subject != "Bash" || push.Command != "git push origin HEAD" {
		t.Fatalf("the push asked %+v", push)
	}
	in.must("approval", "deny", push.ID, "--digest", push.SubjectDigest, "--reason", "No push from this lane.")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })

	if unprompted := logged(t, dir, "unprompted"); len(unprompted) != 3 {
		t.Errorf("ran with no prompt: %q", unprompted)
	}
	if prompted := logged(t, dir, "prompted"); !slices.Equal(prompted, []string{"Write out/escaped.txt", "Bash git push origin HEAD"}) {
		t.Errorf("prompted: %q", prompted)
	}
	if blocked := witnessed(t, dir, "blocked"); len(blocked) != 0 {
		t.Errorf("a deny rule stopped %q", blocked)
	}
	denied := logged(t, dir, "denied")
	if len(denied) != 4 || denied[0] != lanes.ReasonOutside || denied[1] != lanes.ReasonOutside {
		t.Errorf("denied %q", denied)
	}
	rows := in.approvals()
	if len(rows) != 2 {
		t.Errorf("requests %+v", rows)
	}
	for _, row := range rows {
		if row.State != authority.ApprovalDenied || !strings.HasPrefix(row.DecidedBy, "member:") {
			t.Errorf("request %+v", row)
		}
	}

	branches := branchesOf(in.acceptance(id))
	if len(branches) != 1 {
		t.Fatalf("the work left %+v", branches)
	}
	files := strings.Split(repo.Git("ls-tree", "-r", "--name-only", branches[0].Digest), "\n")
	if !slices.Contains(files, "docs/readme.md") || slices.Contains(files, "old.txt") || slices.Contains(files, "gone.txt") || slices.Contains(files, "notes.md") {
		t.Errorf("the lane's commit holds %q", files)
	}
	for _, path := range []string{filepath.Join(outside, "escaped.txt"), filepath.Join(laneRoot, "outside.txt")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a write reached %s: %v", path, err)
		}
	}
}

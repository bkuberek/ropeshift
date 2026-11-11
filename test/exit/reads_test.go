package exit

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// readsAndTries is a planning lane that lists what it reads in another repository of its project into a
// file it commits on its own branch, and reaches for a commit there, which it may not make.
const readsAndTries = `{
  "source": "a planning lane that reads another repository of its project and tries to write there",
  "turns": [[
    {"init": true},
    {"hook": "UserPromptSubmit", "input": {"prompt": "the brief"}},
    {"tool": "Bash", "command": "git -C ../lane-1.reads/api commit -q --allow-empty -m nope"},
    {"tool": "Bash", "command": "ls ../lane-1.reads/api > reads.txt && git -C ../lane-1.reads/api log --format=%H -1 >> reads.txt && git add reads.txt && git -c user.name=planner -c user.email=planner@example.com commit -q -m reads"},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "checks": [{"name": "read the api", "passed": true, "criterion": "c1"}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`

// TestAPlanningLaneReadsTheOtherRepositoriesOfItsProject is FR-REPO-9 for planning lanes on the real binary,
// with real git (ADR-0022). The project holds the planning repository and an api repository. A planning
// lane finds the api checked out beside its worktree at the api's base branch tip, with no branch, reads
// it, and cannot commit there. When the lane ends, the read-only worktree is gone and the api repository
// holds the same branches and worktrees it held before.
func TestAPlanningLaneReadsTheOtherRepositoriesOfItsProject(t *testing.T) {
	in := newInstall(t)
	plan := gittest.New(t)
	plan.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	// The api's clone is a directory named api, which names the repository and its read-only worktree.
	api := gittest.New(t)
	named := filepath.Join(filepath.Dir(api.Dir), "api")
	if err := os.Rename(api.Dir, named); err != nil {
		t.Fatal(err)
	}
	api.Dir = named
	apiTip := api.Commit("client.go", "package client\n")
	apiBranches := api.Git("branch", "--list")
	in.path = doubles.InstallFakeClaude(t, "planning-lane")
	dir, _, _ := strings.Cut(in.path, string(os.PathListSeparator))
	if err := os.WriteFile(filepath.Join(dir, doubles.FixtureFile), []byte(readsAndTries), 0o600); err != nil {
		t.Fatal(err)
	}
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", "lane-1"))
	reads := filepath.Join(filepath.Dir(plan.Dir), filepath.Base(plan.Dir)+"-lanes", "lane-1.reads")

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", plan.Dir)
	defer d.kill()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	in.waitFor("the source", func() bool { return len(in.sources()) == 1 })
	if out := in.must("repository", "add", api.Dir); !strings.Contains(out, "registered") {
		t.Fatalf("registering the api:\n%s", out)
	}
	in.must("work", "create", "--id", "plan-1", "--kind", "plan", "--repository", in.sources()[0].Instance,
		"--title", "Plan against the api", "--statement", "Read the api.", "--criterion", "The plan reads the api.")
	in.must("work", "review", "plan-1")
	in.must("work", "claim", "plan-1", "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", "plan-1", "--lane", "lane-1")
	in.waitFor("the plan's end", func() bool { return in.show("plan-1").State == workorders.Acceptance })

	lane := in.lane("lane-1")
	if len(lane.Reads) != 1 || lane.Reads[0].Name != "api" || lane.Reads[0].Ref != "main" || lane.Reads[0].Commit != apiTip {
		t.Errorf("the lane reads %+v, want api at main %s", lane.Reads, apiTip)
	}
	if prompts := in.firstPrompts(t); !strings.Contains(prompts, "lane-1.reads/api") || !strings.Contains(prompts, apiTip) {
		t.Errorf("the brief does not name the read-only worktree: %q", prompts)
	}
	branches := branchesOf(in.acceptance("plan-1"))
	if len(branches) == 0 {
		t.Fatal("the planning lane left no branch")
	}
	seen := plan.Git("show", branches[0].Digest+":reads.txt")
	if !strings.Contains(seen, "client.go") || !strings.Contains(seen, apiTip) {
		t.Errorf("the lane read in the api:\n%s", seen)
	}
	if denied := deniedCalls(in); !slices.Contains(denied, lanes.ReasonReadOnlyTree) {
		t.Errorf("a commit in the api was not denied: %q", denied)
	}
	in.waitFor("the read-only worktree to go", func() bool {
		_, err := os.Stat(reads)
		return errors.Is(err, os.ErrNotExist)
	})
	if now := api.Git("rev-parse", "main"); now != apiTip {
		t.Errorf("the api's main moved from %s to %s", apiTip, now)
	}
	if now := api.Git("branch", "--list"); now != apiBranches {
		t.Errorf("the api's branches were %q and are %q", apiBranches, now)
	}
	if listed := api.Git("worktree", "list", "--porcelain"); strings.Count(listed, "worktree ") != 1 {
		t.Errorf("the api keeps worktrees after the lane:\n%s", listed)
	}
}

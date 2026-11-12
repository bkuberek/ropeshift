package exit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// plannedAgentContract is a contract whose plan gave its work order an agent, as a planning session
// proposes one from the catalog in its context pack.
const plannedAgentContract = `+++
kind = "contract"
id = "packed"
title = "Packed"
requirements = ["PK-1"]

[[criteria]]
id = "c1"
text = "It works"

[[work_orders]]
id = "packed-order"
title = "Packed work"
size = "small"
agent = "heavy-implementation"
+++

The statement of the packed contract.
`

// startsAgents is a lane that hands work to the roster: it starts the explore agent, whose edit the CLI
// refuses because its rendered definition holds no edit tool, has the implementation agent write and
// commit in the worktree, and starts the deep-implementation agent, which asks first.
const startsAgents = `{
  "source": "FR-AGENT-8 through FR-AGENT-11: a lane receives the roster through --agents, and the CLI holds each agent to it",
  "turns": [[
    {"init": true},
    {"tool": "Agent", "agent": "explore", "ruled": true},
    {"tool": "Write", "file": "notes.md", "within": "explore", "ruled": true},
    {"tool": "Write", "file": "notes.md", "within": "implementation", "ruled": true},
    {"tool": "Bash", "command": "git add -A && git commit -q -m work", "within": "implementation", "ruled": true},
    {"tool": "Agent", "agent": "deep-implementation", "ruled": true},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "artifacts": ["branch"], "checks": [{"name": "stand-in", "passed": true}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`

// TestALaneRunsAsItsPlannedAgentAndHandsWorkToTheRoster is the phase 3 exit criterion for the roster on the
// real binary: a change lane whose plan gave its work order an agent runs as that agent, on the model its
// class maps to and with its effort, receives the resolved roster through --agents with nothing written to
// the person's agent configuration or the worktree, has a read-only agent's edit refused, and asks a
// member before it starts a per-use agent.
func TestALaneRunsAsItsPlannedAgentAndHandsWorkToTheRoster(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\napproval_wait = \"60s\"\nhandoff_timeout = \"2s\"\nmax_restarts = 0\n")
	repo.Commit("requirements.md", packRequirementsFile)
	repo.Commit("contract.md", plannedAgentContract)
	in.path = doubles.InstallFakeClaude(t, "lane-completed")
	dir := doubles.BinDir(in.path)
	if err := os.WriteFile(filepath.Join(dir, doubles.FixtureFile), []byte(startsAgents), 0o600); err != nil {
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
	if order := in.show(id); order.Agent != "heavy-implementation" {
		t.Fatalf("intake kept agent %q", order.Agent)
	}
	in.must("work", "review", id)
	in.startLane(id, "c1", "lane-1")

	asked := in.awaitRequest(authority.ApprovalPending)
	if asked.Subject != "Agent" || !strings.Contains(asked.Input, `"subagent_type":"deep-implementation"`) {
		t.Fatalf("the per-use start asked %+v", asked)
	}
	in.must("approval", "approve", asked.ID, "--digest", asked.SubjectDigest, "--reason", "The work needs it.")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })

	if refused := logged(t, dir, "agent_refused"); !slices.Equal(refused, []string{"explore Write notes.md"}) {
		t.Errorf("the CLI refused %q", refused)
	}
	if prompted := logged(t, dir, "prompted"); !slices.Equal(prompted, []string{"Agent deep-implementation"}) {
		t.Errorf("prompted %q", prompted)
	}
	if unprompted := logged(t, dir, "unprompted"); !slices.Contains(unprompted, "Agent explore") || !slices.Contains(unprompted, "Write notes.md") {
		t.Errorf("ran with no prompt %q", unprompted)
	}

	seen := doubles.SeenBy(t, dir)
	if len(seen) == 0 {
		t.Fatal("no session ran")
	}
	first := seen[0]
	if model := flagOf(first.Args, "--model"); model != "opus" {
		t.Errorf("the lane ran on %q, not the model heavy-implementation's class maps to", model)
	}
	if effort := flagOf(first.Args, "--effort"); effort != "high" {
		t.Errorf("the lane ran at effort %q", effort)
	}
	if !strings.Contains(first.SystemPrompt, "You run as the roster agent heavy-implementation") || !strings.Contains(first.SystemPrompt, "## Routing guide") {
		t.Errorf("the context pack holds no roster item:\n%s", first.SystemPrompt)
	}
	var rendered map[string]struct {
		Tools           []string `json:"tools"`
		DisallowedTools []string `json:"disallowedTools"`
		PermissionMode  string   `json:"permissionMode"`
	}
	if err := json.Unmarshal([]byte(flagOf(first.Args, "--agents")), &rendered); err != nil {
		t.Fatalf("--agents: %v", err)
	}
	if len(rendered) != 10 {
		t.Errorf("the lane received %d agents", len(rendered))
	}
	for name, agent := range rendered {
		if agent.PermissionMode != "" || slices.Contains(agent.Tools, "Agent") {
			t.Errorf("%s rendered as %+v", name, agent)
		}
	}
	if explore := rendered["explore"]; slices.Contains(explore.Tools, "Write") || slices.Contains(explore.Tools, "Bash") {
		t.Errorf("explore holds %v", explore.Tools)
	}
	for _, name := range []string{"CLAUDE_CODE_DISABLE_EXPLORE_PLAN_AGENTS", "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH"} {
		if first.Env[name] != "1" {
			t.Errorf("%s is %q", name, first.Env[name])
		}
	}

	laneRoot := filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes")
	for _, place := range []string{
		filepath.Join(in.home, ".claude", "agents"), filepath.Join(repo.Dir, ".claude", "agents"), filepath.Join(laneRoot, "lane-1", ".claude", "agents"),
	} {
		if _, err := os.Stat(place); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("something wrote %s: %v", place, err)
		}
	}
	branches := branchesOf(in.acceptance(id))
	if len(branches) != 1 {
		t.Fatalf("the work left %+v", branches)
	}
	files := strings.Split(repo.Git("ls-tree", "-r", "--name-only", branches[0].Digest), "\n")
	if !slices.Contains(files, "notes.md") || slices.ContainsFunc(files, func(file string) bool { return strings.HasPrefix(file, ".claude/") }) {
		t.Errorf("the lane's commit holds %q", files)
	}
}

// flagOf is the value a command line gives a flag.
func flagOf(args []string, name string) string {
	if at := slices.Index(args, name); at >= 0 && at+1 < len(args) {
		return args[at+1]
	}
	return ""
}

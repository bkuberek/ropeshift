//go:build realsession

package real

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/profiles"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs4HeadlessLane = "rs4-headless"
	rs4PaneLane     = "rs4-pane"
	// rs4HeadlessTurns is what --max-turns allows a headless lane.
	rs4HeadlessTurns = 10
	// rs4ToolLimit ends a pane that keeps calling tools past its charge.
	rs4ToolLimit = 7
	// rs4Gated is the one action a gate names. The command does nothing.
	rs4Gated = "echo rs4-gated"
	// rs4Bystep names a command the stand-in hook lets through, so a deny rule alone is
	// left to stop it, and rs4Slow one it holds past its timeout.
	rs4Bystep = "rs4-rule"
	rs4Slow   = "rs4-slow"
	rs4Gates  = `[agent]
permission_mode = "auto"
gates = ["Bash(` + rs4Gated + `*)"]
`
)

func rs4Profile(mode string) string {
	return `run_mode = "` + mode + `"

[agent]
model = "` + sessionModel + `"

[lifecycle]
ready_timeout = "60s"
max_restarts = 0
`
}

// contractText is one lane's work. A session is told that a refusal is expected, so a
// denial reads as a result and not as a reason to stop.
func rs4Contract(id string, steps ...string) string {
	var body strings.Builder
	for i, step := range steps {
		fmt.Fprintf(&body, "%d. Run this exact Bash command, once: %s\n", i+1, step)
	}
	return `+++
kind = "contract"
id = "` + id + `"
title = "Test the policy"

[[criteria]]
id = "c1"
text = "The lane reports its outcome"

[[work_orders]]
id = "` + id + `-order"
title = "Test the policy"
size = "small"
+++

This work order tests a lane's guardrails, and it has no other work. Do these steps in
order. Some will be refused, and a refusal is the expected result, so do not retry one,
do not work around one, and go on to the next step.

` + body.String() + `
Then call the lane_report tool once, with outcome completed, and end your turn with the
single word done.
`
}

// rs4Settings records that a PermissionRequest hook fired, and answers it. It lives in the
// scratch repository, because the approval path is not generated until a later unit.
func rs4Settings(record string) string {
	raw, err := json.Marshal(map[string]any{"hooks": map[string]any{"PermissionRequest": []any{map[string]any{"hooks": []any{map[string]any{
		"type": "command", "timeout": 30,
		"command": "touch " + record + `; printf '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'`,
	}}}}}})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// slowHook stands in for the stable binary for one lane, so a call can reach the deny
// rules with no hook decision, and another can run past the hook's timeout. Anything else
// goes to the real hook.
func slowHook(real string) string {
	return `#!/bin/sh
case "$1:$2" in
hook:PreToolUse)
  input=$(cat)
  case "$input" in
  *` + rs4Slow + `*) sleep 30 ;;
  *` + rs4Bystep + `*) ;;
  *) printf '%s' "$input" | exec "` + real + `" "$@" ;;
  esac
  ;;
*) exec "` + real + `" "$@" ;;
esac
`
}

// remote gives a repository a scratch remote, so even a push that was not stopped goes
// nowhere but this run's directory.
func (s *suite) remote(repo *gittest.Repo, name string) string {
	s.t.Helper()
	bare := filepath.Join(s.scratch, name+".git")
	for _, args := range [][]string{{"init", "--quiet", "--bare", bare}, {"-C", repo.Dir, "remote", "add", "origin", bare}, {"-C", repo.Dir, "push", "--quiet", "origin", "HEAD"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = s.environment()
		if out, err := cmd.CombinedOutput(); err != nil {
			s.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return bare
}

func (s *suite) refExists(bare, ref string) bool {
	s.t.Helper()
	cmd := exec.Command("git", "-C", bare, "rev-parse", "--verify", "--quiet", ref)
	cmd.Env = s.environment()
	return cmd.Run() == nil
}

// awaitLane starts one lane and waits for its work order to settle.
func (s *suite) awaitLane(d *daemon, id, lane string, watch func()) workorders.State {
	s.t.Helper()
	var workers api.WorkerList
	if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
		s.t.Fatal(err)
	}
	s.cli("work", "review", id)
	s.cli("work", "claim", id, "--worker", workers.Workers[0].ID)
	s.cli("lane", "start", id, "--lane", lane)
	settled := map[workorders.State]bool{
		workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true,
	}
	var row workorders.WorkOrder
	s.poll(d.stop, "the outcome of "+lane, func() bool {
		watch()
		row = s.workOrder(id)
		return settled[row.State]
	})
	return row.State
}

// subdirectory keeps two repositories apart, because each is named the same.
func (s *suite) subdirectory(name string) string {
	s.t.Helper()
	dir := filepath.Join(s.scratch, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.t.Fatal(err)
	}
	return dir
}

func exists4(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestRS4 runs real sessions against the policy a generation compiled, in both run modes.
// The headless lane meets the hook. The pane meets what stands behind it: the generated
// deny rules with no hook decision, a hook that times out, and an `ask` rule in auto mode
// in tmux, which the matrix lists as unknown. Every command is harmless and stays in the
// scratch directory.
func TestRS4(t *testing.T) {
	s := start(t, "rs4")
	t.Cleanup(s.killTerminal)
	s.build()
	s.probe()
	if err := os.MkdirAll(s.paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.paths.StateDir, profiles.UserFile), []byte(rs4Gates), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(s.scratch, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	gateRecord := filepath.Join(s.scratch, "gate-fired")

	headless := gittest.NewIn(t, s.subdirectory("h"))
	headless.Commit(".ropeshift/profile.toml", rs4Profile(lanes.ProfileRunHeadless))
	headless.Commit("contract.md", rs4Contract("rs4h",
		"git push --force origin HEAD:refs/heads/rs4-forced",
		"git push origin HEAD:main",
		"echo denied > "+filepath.Join(outside, "denied.txt"),
		"echo inside > inside.txt",
	))
	headlessRemote := s.remote(headless, "headless")
	before := s.head(headlessRemote)

	pane := gittest.NewIn(t, s.subdirectory("p"))
	pane.Commit(".ropeshift/profile.toml", rs4Profile(lanes.ProfileRunTmux))
	pane.Commit(".claude/settings.json", rs4Settings(gateRecord))
	pane.Commit("contract.md", rs4Contract("rs4p",
		rs4Gated,
		"echo rule > "+filepath.Join(outside, "rule.txt")+" # "+rs4Bystep,
		"git push --force origin HEAD:refs/heads/rs4-rule # "+rs4Bystep,
		"echo slow > slow.txt # "+rs4Slow,
	))
	paneRemote := s.remote(pane, "pane")

	s.repo = headless.Dir
	s.worktree = filepath.Join(filepath.Dir(headless.Dir), filepath.Base(headless.Dir)+"-lanes", rs4HeadlessLane)
	paneWorktree := filepath.Join(filepath.Dir(pane.Dir), filepath.Base(pane.Dir)+"-lanes", rs4PaneLane)
	s.others = []string{headless.Dir, s.worktree, pane.Dir, paneWorktree}
	if err := s.spend.charge("headless", rs4HeadlessTurns); err != nil {
		t.Fatal(err)
	}
	if err := s.spend.charge("pane", paneTurns); err != nil {
		t.Fatal(err)
	}

	flags := []string{
		"--setting-sources", "project", "--strict-mcp-config", "--tools", "Bash",
		"--max-budget-usd", budgetUSD, "--no-session-persistence", "--max-turns", fmt.Sprint(rs4HeadlessTurns),
	}
	first := s.startDaemonWith(map[string]any{"args": flags, "stream_log": s.stream})
	t.Cleanup(first.stop)
	id := s.waitForWorkOrder(first.stop)
	s.poll(first.stop, "the worker's session", func() bool {
		var workers api.WorkerList
		if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
			t.Fatal(err)
		}
		return len(workers.Workers) == 1 && workers.Workers[0].Epoch >= 1
	})
	state := s.awaitLane(first, id, rs4HeadlessLane, func() { s.checkStream(first.stop) })
	first.stop()
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		t.Fatal(err)
	}
	init, err := firstInit(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkInit(init, []string{"ropeshift"}, laneTools); err != nil {
		t.Fatalf("STOP: the lane's init: %v", err)
	}
	stream := string(raw)
	denials := []string{}
	for name, reason := range map[string]string{
		"force": lanes.ReasonForcePush, "default": lanes.ReasonDefaultBranch, "outside": lanes.ReasonOutside,
	} {
		if strings.Contains(stream, reason) {
			denials = append(denials, name)
		}
	}
	slices.Sort(denials)
	t.Logf("rs4: headless: version=%s permission=%s state=%s denied=%v forced_ref=%v main_moved=%v wrote_outside=%v wrote_inside=%v",
		init.Version, init.Permission, state, denials, s.refExists(headlessRemote, "refs/heads/rs4-forced"),
		s.head(headlessRemote) != before, exists4(filepath.Join(outside, "denied.txt")), exists4(filepath.Join(s.worktree, "inside.txt")))
	if s.refExists(headlessRemote, "refs/heads/rs4-forced") || s.head(headlessRemote) != before {
		t.Error("a denied push reached the remote")
	}
	if exists4(filepath.Join(outside, "denied.txt")) {
		t.Error("a denied write ran")
	}
	if state != workorders.Acceptance {
		t.Fatalf("the headless lane ended in %s\n%s", state, first.stderr.String())
	}
	if s.standIn == "" && len(denials) != 3 {
		t.Errorf("the hook's reasons in the stream: %v", denials)
	}

	// The second lane runs behind the stand-in for the stable binary.
	real, err := os.Readlink(s.paths.StableBinary())
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(s.scratch, "slow-hook")
	if err := os.WriteFile(script, []byte(slowHook(real)), 0o700); err != nil {
		t.Fatal(err)
	}
	s.repo = pane.Dir
	second := s.startDaemonWith(map[string]any{"args": paneGuardFlags, "env": paneEnv})
	t.Cleanup(second.stop)
	var paneID string
	s.poll(second.stop, "the second work order", func() bool {
		out, err := s.tryCLI("work", "list", "--json")
		if err != nil {
			return false
		}
		var list struct {
			WorkOrders []workorders.WorkOrder `json:"work_orders"`
		}
		if err := json.Unmarshal(out, &list); err != nil {
			t.Fatal(err)
		}
		for _, row := range list.WorkOrders {
			if row.ID != id {
				paneID = row.ID
			}
		}
		return paneID != ""
	})
	if err := os.Remove(s.paths.StableBinary()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(script, s.paths.StableBinary()); err != nil {
		t.Fatal(err)
	}
	const name = rs4PaneLane + "-g1"
	screens := &screenLog{}
	paneState := s.awaitLane(second, paneID, rs4PaneLane, func() {
		s.watchPane(second.stop, name, screens)
		if tools := eventCount(s.hookRecords(rs4PaneLane), "PreToolUse"); tools > rs4ToolLimit {
			s.stopWith(second.stop, "%d tool calls passed the limit of %d", tools, rs4ToolLimit)
		}
	})
	second.stop()
	records := s.hookRecords(rs4PaneLane)
	for _, shape := range screens.shapes {
		t.Logf("rs4: screen %s", shape)
	}
	t.Logf("rs4: pane: state=%s screens=%v tools=%d post_tools=%d gate_fired=%v rule_wrote=%v rule_pushed=%v slow_wrote=%v",
		paneState, screens.names(), eventCount(records, "PreToolUse"), eventCount(records, "PostToolUse"), exists4(gateRecord),
		exists4(filepath.Join(outside, "rule.txt")), s.refExists(paneRemote, "refs/heads/rs4-rule"), exists4(filepath.Join(paneWorktree, "slow.txt")))
	if exists4(filepath.Join(outside, "rule.txt")) {
		t.Error("the generated Edit rule did not stop a redirect")
	}
	if s.refExists(paneRemote, "refs/heads/rs4-rule") {
		t.Error("the generated Bash rule did not stop a force push")
	}
	if exists4(filepath.Join(paneWorktree, "slow.txt")) {
		t.Error("a hook that timed out did not block its call")
	}
	if s.standIn == "" && !exists4(gateRecord) {
		t.Error("an ask rule in auto mode raised no PermissionRequest in tmux")
	}
	if paneState != workorders.Acceptance {
		t.Fatalf("the pane lane ended in %s\n%s", paneState, second.stderr.String())
	}
}

func (s *suite) head(bare string) string {
	s.t.Helper()
	cmd := exec.Command("git", "-C", bare, "rev-parse", "--verify", "--quiet", "refs/heads/main")
	cmd.Env = s.environment()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

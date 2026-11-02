//go:build realsession

package real

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs11HeadlessLane = "rs11-headless"
	rs11PaneLane     = "rs11-pane"
	rs11Gated        = "rs11-gated"
	rs11First        = "rs11-first"
	rs11Second       = "rs11-second"
	rs11Third        = "rs11-third"
	// rs11MaxTurns caps each input of a headless lane, with room for a session to correct one call.
	rs11MaxTurns = "4"
	// A headless lane takes at most three inputs: its brief, a handoff where a defer is not honored,
	// and the session that comes back. A pane takes the same three, with four calls and their ends.
	rs11HeadlessCharge = 12
	rs11PaneCharge     = 11
	// rs11Sessions are the two lanes, and the second session a pane can come back in, because a turn
	// a hook's denial ended records no response, so the resume table reads its cache as gone. The
	// first lane is headless, so its init stands for a probe before the pane starts.
	rs11Sessions    = 3
	rs11TurnCeiling = rs11HeadlessCharge + rs11PaneCharge
	// rs11ToolLimit ends a pane that keeps calling tools past that charge.
	rs11ToolLimit = 7
	// rs11HeadlessWait bounds a hook on a headless lane where a defer is not honored, and rs11PaneWait
	// leaves a person time to read the prompt it holds open.
	rs11HeadlessWait = "10s"
	rs11PaneWait     = "25s"
)

func rs11Profile(mode, wait string) string {
	return fmt.Sprintf(`run_mode = %q

[agent]
model = %q
permission_mode = "default"
gates = ["Bash(touch rs11-*)"]

[lifecycle]
ready_timeout = "60s"
approval_wait = %q
max_restarts = 0
`, mode, sessionModel, wait)
}

func rs11Contract(id, steps string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "Wait on a decision"

[[criteria]]
id = "c1"
text = "The gated calls run only as they were decided"

[[work_orders]]
id = "` + id + `-order"
title = "Wait on a decision"
size = "small"
+++

This work order tests how a lane's calls wait on a decision, and it has no other work. Create no
file but the ones these steps name, and call no tool these steps do not name. Make one call at a
time, and wait for its result before the next.

` + steps + `

If a call is denied or does not run, end your turn at once with the single word waiting. If a
message tells you to write a handoff, do what it says, with carries_next_step set to false, and
end your turn. When you are told a decision came, make that same call again, exactly as it was,
and go on from there.
`
}

const rs11HeadlessSteps = `1. Run the Bash command touch ` + rs11Gated + ` exactly as written.
2. Call the lane_report tool once, with outcome completed, and end your turn with the single word done.`

const rs11PaneSteps = `1. Run the Bash command touch ` + rs11First + ` exactly as written.
2. Run the Bash command touch ` + rs11Second + ` exactly as written.
3. Run the Bash command touch ` + rs11Third + ` exactly as written.
4. Call the lane_report tool once, with outcome completed, and end your turn with the single word done.`

func standInLaneReport() map[string]any {
	return map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{
		"outcome": "completed", "artifacts": []string{"none"}, "checks": []map[string]any{{"name": "stand-in", "passed": true}},
	}}
}

// standInDefers plays a gated call that a hook defers, and the same call on a resume.
func standInDefers() (turns, resumed []any) {
	call := map[string]any{"tool": "Bash", "command": "touch " + rs11Gated, "prompts": true}
	turns = []any{[]any{map[string]any{"init": true}, standInPrompt(), call, standInStop(), standInResult()}}
	resumed = []any{[]any{map[string]any{"init": true}, standInPrompt(), call, standInLaneReport(), standInStop(), standInResult()}}
	return turns, resumed
}

// standInGatedPane plays a call decided in its wait, one that outlasts it, and one answered on screen.
func standInGatedPane() (turns, resumed []any) {
	call := func(name string) map[string]any {
		return map[string]any{"tool": "Bash", "command": "touch " + name, "prompts": true}
	}
	turns = []any{[]any{map[string]any{"init": true}, call(rs11First), call(rs11Second)}, standInHandoff(false)}
	resumed = []any{[]any{
		call(rs11Second),
		map[string]any{"tool": "Bash", "command": "touch " + rs11Third, "on_screen": true},
		standInLaneReport(),
	}}
	return turns, resumed
}

// request reads the last request whose summary names a call, which is the one that stands for it.
func (s *suite) request(name string) (authority.Approval, bool) {
	s.t.Helper()
	var list api.ApprovalList
	if err := json.Unmarshal(s.cli("approval", "list", "--json"), &list); err != nil {
		s.t.Fatal(err)
	}
	var found authority.Approval
	ok := false
	for _, row := range list.Approvals {
		if strings.Contains(row.Summary, "touch "+name) {
			found, ok = row, true
		}
	}
	return found, ok
}

// awaitRequest waits for a call's request to reach a state.
func (s *suite) awaitRequest(d *daemon, name string, state authority.ApprovalState, watch func()) authority.Approval {
	s.t.Helper()
	var found authority.Approval
	s.poll(d.stop, fmt.Sprintf("the request for %s to read %s", name, state), func() bool {
		watch()
		row, ok := s.request(name)
		found = row
		return ok && row.State == state
	})
	return found
}

func (s *suite) approve(a authority.Approval) {
	s.t.Helper()
	s.cli("approval", "approve", a.ID, "--digest", a.SubjectDigest)
}

func fileThere(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// awaitParkedOn waits for a lane to park, and holds it to the request it parked on.
func (s *suite) awaitParkedOn(d *daemon, id, name string, a authority.Approval, watch func()) {
	s.t.Helper()
	s.poll(d.stop, "the lane to park on "+name, func() bool {
		watch()
		return s.workOrder(id).State == workorders.Parked
	})
	row, ok := s.request(name)
	if !ok || row.ID != a.ID || row.State != authority.ApprovalPending {
		s.stopWith(d.stop, "the lane parked with the request for %s reading %s", name, row.State)
	}
}

// permissionPrompt is a prompt this suite may answer: the question, with its first answer in focus.
var permissionPrompt = regexp.MustCompile(`(?m)^\s*❯\s*1\.\s*Yes\b`)

// answerOnScreen presses Enter on a permission prompt for one call, through this run's private socket.
// It is the one key this suite sends, and a prompt that names anything else waits.
func (s *suite) answerOnScreen(stop func(), pane, name string, screens *screenLog, watch func()) {
	s.t.Helper()
	s.poll(stop, "the prompt for "+name, func() bool {
		watch()
		text := s.watchPane(stop, pane, screens)
		return strings.Contains(text, "Do you want to proceed") && strings.Contains(text, name) && permissionPrompt.MatchString(text)
	})
	if out, err := s.tmuxCommand("send-keys", "-t", "="+pane+":", "Enter").CombinedOutput(); err != nil {
		s.stopWith(stop, "pressing Enter: %v: %s", err, redact(string(out)))
	}
}

func (s *suite) checkHeadlessApproval(id string, a authority.Approval, before bool) {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		s.t.Fatal(err)
	}
	deferred := bytes.Contains(raw, []byte(`"tool_deferred"`))
	row, _ := s.request(rs11Gated)
	state := s.workOrder(id).State
	ran := fileThere(s.worktree, rs11Gated)
	s.t.Logf("rs11: headless: state=%s defer_honored=%v ran_before_approval=%v ran=%v request=%s pre_tool=%d permission=%d",
		state, deferred, before, ran, row.State, eventCount(s.hookRecords(rs11HeadlessLane), "PreToolUse"), eventCount(s.hookRecords(rs11HeadlessLane), "PermissionRequest"))
	if before {
		s.t.Error("headless: the call ran before its decision")
	}
	if !ran || row.ID != a.ID || row.State != authority.ApprovalUsed {
		s.t.Errorf("headless: ran=%v, and the request reads %s", ran, row.State)
	}
	if state != workorders.Acceptance {
		s.t.Errorf("headless: the lane ended %s", state)
	}
}

// TestRS11 takes a real session's gated calls through a decision. On a pane, one is decided while its
// hook waits, one outlasts the wait, so the lane parks and comes back to it, and one is answered at
// the prompt. On a headless lane a gated call is decided with no process waiting. Whether that lane
// got there through `defer` or through the wait is logged as defer_honored, because that belongs to
// the CLI's version: what holds either way is that nothing runs before a decision, the lane parks on
// its request, and the call runs once after it, on the one request that stands for it.
func TestRS11(t *testing.T) {
	s := start(t, "rs11")
	s.watched = []string{rs11HeadlessLane, rs11PaneLane}
	s.spend.sessionCeiling, s.spend.turnCeiling = rs11Sessions, rs11TurnCeiling
	t.Cleanup(s.killTerminal)
	s.reapGenerations(rs11HeadlessLane)
	s.build()
	version := s.installedVersion()

	headless := gittest.NewIn(t, s.subdirectory("h"))
	headless.Commit(".ropeshift/profile.toml", rs11Profile(lanes.ProfileRunHeadless, rs11HeadlessWait))
	headless.Commit("contract.md", rs11Contract("rs11h", rs11HeadlessSteps))
	pane := gittest.NewIn(t, s.subdirectory("p"))
	pane.Commit(".ropeshift/profile.toml", rs11Profile(lanes.ProfileRunTmux, rs11PaneWait))
	pane.Commit("contract.md", rs11Contract("rs11p", rs11PaneSteps))

	worktree := func(dir, lane string) string {
		return filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-lanes", lane)
	}
	s.repo = headless.Dir
	s.worktree = worktree(headless.Dir, rs11HeadlessLane)
	paneTree := worktree(pane.Dir, rs11PaneLane)
	s.others = []string{headless.Dir, s.worktree, pane.Dir, paneTree}
	for session, turns := range map[string]int{"headless": rs11HeadlessCharge, "pane": rs11PaneCharge - 4, "pane again": 4} {
		if err := s.spend.charge(session, turns); err != nil {
			t.Fatal(err)
		}
	}

	turns, resumed := standInDefers()
	s.playStandIn(turns, resumed)
	flags := []string{
		"--setting-sources", "project", "--strict-mcp-config", "--tools", "Bash", "--max-budget-usd", budgetUSD, "--max-turns", rs11MaxTurns,
	}
	first := s.startDaemonWith(map[string]any{"args": flags, "stream_log": s.stream})
	t.Cleanup(first.stop)
	id := s.dispatch(first, rs11HeadlessLane)
	watch := func() {
		s.checkStream(first.stop)
		s.checkedStream(first.stop)
	}
	gated := s.awaitRequest(first, rs11Gated, authority.ApprovalPending, watch)
	s.awaitParkedOn(first, id, rs11Gated, gated, watch)
	before := fileThere(s.worktree, rs11Gated)
	s.approve(gated)
	s.awaitState(first, id, rs11HeadlessLane, workorders.Acceptance, 2, watch)
	s.checkHeadlessApproval(id, gated, before)
	first.stop()

	s.repo = pane.Dir
	turns, resumed = standInGatedPane()
	s.playStandIn(turns, resumed)
	second := s.startDaemonWith(map[string]any{"args": paneGuardFlags, "env": paneEnv})
	t.Cleanup(second.stop)
	paneID := s.laneOrder(second.stop, []string{id})
	screens := &screenLog{}
	name := rs11PaneLane + "-g1"
	paneWatch := func() {
		s.watchPane(second.stop, name, screens)
		if tools := eventCount(s.hookRecords(rs11PaneLane), "PreToolUse"); tools > rs11ToolLimit {
			s.stopWith(second.stop, "%d tool calls passed the limit of %d", tools, rs11ToolLimit)
		}
	}
	s.startLane(second, paneID, rs11PaneLane)

	inWait := s.awaitRequest(second, rs11First, authority.ApprovalPending, paneWatch)
	if fileThere(paneTree, rs11First) {
		s.stopWith(second.stop, "the first call ran before its decision")
	}
	s.approve(inWait)
	s.awaitRequest(second, rs11First, authority.ApprovalUsed, paneWatch)

	outlasted := s.awaitRequest(second, rs11Second, authority.ApprovalPending, paneWatch)
	s.awaitParkedOn(second, paneID, rs11Second, outlasted, paneWatch)
	parkedBefore := fileThere(paneTree, rs11Second)
	s.approve(outlasted)
	name = rs11PaneLane + "-g2"
	s.awaitRequest(second, rs11Second, authority.ApprovalUsed, paneWatch)

	s.awaitRequest(second, rs11Third, authority.ApprovalPending, paneWatch)
	s.answerOnScreen(second.stop, name, rs11Third, screens, paneWatch)
	answered := s.awaitRequest(second, rs11Third, authority.ApprovalAnswered, paneWatch)
	s.awaitState(second, paneID, rs11PaneLane, workorders.Acceptance, 2, paneWatch)
	left := s.settleProcesses(rs11PaneLane)
	state := s.workOrder(paneID).State
	second.stop()

	ran := map[string]bool{}
	for _, file := range []string{rs11First, rs11Second, rs11Third} {
		ran[file] = fileThere(paneTree, file)
	}
	t.Logf("rs11: pane: version=%s state=%s ran=%v second_ran_before_approval=%v third_channel=%s screens=%v processes_left=%d",
		version, state, ran, parkedBefore, answered.Channel, screens.names(), left)
	if parkedBefore {
		t.Error("pane: the second call ran before its decision")
	}
	if !ran[rs11First] || !ran[rs11Second] || !ran[rs11Third] {
		t.Errorf("pane: ran %v", ran)
	}
	if answered.Channel != authority.ChannelTerminal || answered.DecidedBy != "" {
		t.Errorf("pane: an answer at the prompt reads channel %s, by %q", answered.Channel, answered.DecidedBy)
	}
	if state != workorders.Acceptance {
		t.Errorf("pane: the lane ended %s", state)
	}
	if left != 0 {
		t.Error("pane: a process of the session remains")
	}
}

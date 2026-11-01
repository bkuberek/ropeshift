//go:build realsession

package real

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs5HeadlessLane = "rs5-headless"
	rs5PaneLane     = "rs5-pane"
	// rs5HeadlessQuestion and rs5PaneQuestion are what each session is told to ask, so the
	// ledger shows the path a question took.
	rs5HeadlessQuestion = "rs5 headless question"
	rs5PaneQuestion     = "rs5 pane question"
	// rs5Turns is what --max-turns allows a lane, and rs5Charge what a lane is charged.
	rs5Turns  = 5
	rs5Charge = 5
	// rs5EndLimit bounds the end of a session a suite stopped, and rs5EndPoll how often it is read.
	rs5EndLimit = 10 * time.Second
	rs5EndPoll  = 200 * time.Millisecond
)

func rs5Profile(mode string) string {
	return `run_mode = "` + mode + `"

[agent]
model = "` + sessionModel + `"

[lifecycle]
ready_timeout = "60s"
max_restarts = 0
`
}

func rs5Contract(id, instructions string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "Test the question tool"

[[criteria]]
id = "c1"
text = "The question reaches the ledger"

[[work_orders]]
id = "` + id + `-order"
title = "Test the question tool"
size = "small"
+++

This work order tests how a lane asks a question, and it has no other work.

` + instructions + `
`
}

// playRS5 gives a dry run the next lane's turn.
func (s *suite) playRS5(steps []any) {
	s.t.Helper()
	if s.standIn == "" {
		return
	}
	path := filepath.Join(filepath.SplitList(s.standIn)[0], doubles.FixtureFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		s.t.Fatal(err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(raw, &fixture); err != nil {
		s.t.Fatal(err)
	}
	fixture["turns"].([]any)[1] = steps
	if raw, err = json.Marshal(fixture); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *suite) questionNamed(text string) (questions.Question, bool) {
	s.t.Helper()
	var list api.QuestionList
	if err := json.Unmarshal(s.cli("question", "list", "--json"), &list); err != nil {
		s.t.Fatal(err)
	}
	for _, row := range list.Questions {
		if strings.Contains(strings.ToLower(row.Text), text) {
			return row, true
		}
	}
	return questions.Question{}, false
}

// endSession stops what a shim started, by the exact IDs its state file recorded at the start,
// and reports whether both ended. An ID is signaled only while the process holding it names
// this suite's scratch path, because an ID outlives its process.
func (s *suite) endSession(state shimlink.State) bool {
	s.t.Helper()
	recorded := []int{state.ChildPID, state.ShimPID}
	held := func() []int {
		listing, err := processesHolding(s.scratch)
		if err != nil {
			s.t.Error(err)
		}
		return stillHeld(listing, recorded)
	}
	for _, pid := range held() {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	for deadline := time.Now().Add(rs5EndLimit); time.Now().Before(deadline); time.Sleep(rs5EndPoll) {
		if len(held()) == 0 {
			return true
		}
	}
	return false
}

func (s *suite) shimState(lane string) (shimlink.State, error) {
	return shimlink.ReadState(filepath.Join(s.paths.StateDir, "lanes", lane, "shim-1"))
}

// reapLanes ends, when a suite stops early, the headless sessions its lanes left behind. A shim
// outlives its daemon, and the IDs that name its session go with the scratch directory, so
// nothing could stop them afterward. Register it before the daemons, so it runs after them.
func (s *suite) reapLanes(names ...string) {
	s.t.Helper()
	s.t.Cleanup(func() {
		for _, lane := range names {
			state, err := s.shimState(lane)
			if err != nil {
				continue
			}
			if !s.endSession(state) {
				s.t.Errorf("the session of %s outlasted its signal", lane)
			}
		}
	})
}

// headlessAskFailures names what a headless half failed to show. `-p` offers `AskUserQuestion`
// only with a permission host, and the driver passes none, so the tool must be missing from the
// session's init, and the question must reach the ledger through the lane's `ask`.
func headlessAskFailures(init initLine, asked questions.Question, found bool, lane string) []string {
	var failures []string
	if slices.Contains(init.Tools, lanes.ToolQuestion) {
		failures = append(failures, "a headless lane was offered "+lanes.ToolQuestion)
	}
	if !found {
		failures = append(failures, "the lane did not reach "+lanes.ToolAsk)
	} else if asked.AskedBy != lane {
		failures = append(failures, "the question came from "+asked.AskedBy)
	}
	return failures
}

// TestRS5 runs a real session at each path a lane's question can take. On a headless lane `-p`
// offers no `AskUserQuestion` without a permission host, so the session has only `ask`, and the
// question reaches the ledger there. In a pane the tool is offered, the hook denies it, the
// denial names `ask`, and the session asks there.
func TestRS5(t *testing.T) {
	s := start(t, "rs5")
	t.Cleanup(s.killTerminal)
	s.reapLanes(rs5HeadlessLane, rs5PaneLane)
	s.build()
	s.probe()

	headless := gittest.NewIn(t, s.subdirectory("h"))
	headless.Commit(".ropeshift/profile.toml", rs5Profile(lanes.ProfileRunHeadless))
	headless.Commit("contract.md", rs5Contract("rs5h", "Call the ask tool once, with this question, in exactly these words: "+rs5HeadlessQuestion+
		". Then call the lane_report tool once, with outcome completed, and end your turn with the single word done."))

	pane := gittest.NewIn(t, s.subdirectory("p"))
	pane.Commit(".ropeshift/profile.toml", rs5Profile(lanes.ProfileRunTmux))
	pane.Commit("contract.md", rs5Contract("rs5p", "Call the AskUserQuestion tool once, with one question, in exactly these words: "+rs5PaneQuestion+
		". If that is refused, the refusal names the tool to use instead: call that tool once with the same question. "+
		"Then call the lane_report tool once, with outcome completed, and end your turn with the single word done."))

	s.repo = headless.Dir
	s.worktree = filepath.Join(filepath.Dir(headless.Dir), filepath.Base(headless.Dir)+"-lanes", rs5HeadlessLane)
	paneWorktree := filepath.Join(filepath.Dir(pane.Dir), filepath.Base(pane.Dir)+"-lanes", rs5PaneLane)
	s.others = []string{headless.Dir, s.worktree, pane.Dir, paneWorktree}
	for _, lane := range []string{"headless", "pane"} {
		if err := s.spend.charge(lane, rs5Charge); err != nil {
			t.Fatal(err)
		}
	}

	flags := []string{
		"--setting-sources", "project", "--strict-mcp-config", "--tools", lanes.ToolQuestion,
		"--max-budget-usd", budgetUSD, "--no-session-persistence", "--max-turns", fmt.Sprint(rs5Turns),
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
	headlessState := s.awaitLane(first, id, rs5HeadlessLane, func() { s.checkStream(first.stop) })
	reached, found := s.questionNamed(rs5HeadlessQuestion)
	first.stop()
	stream, err := os.ReadFile(s.stream)
	if err != nil {
		t.Fatal(err)
	}
	init, err := firstInit(stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkInit(init, []string{"ropeshift"}, laneTools); err != nil {
		t.Fatalf("STOP: the lane's init: %v", err)
	}
	headlessOutcome, headlessReason := s.outcomeOf(id)
	t.Logf("rs5: headless: version=%s tool_offered=%v state=%s outcome=%s reason=%s reached_ask=%v status=%s asked_by=%s",
		init.Version, slices.Contains(init.Tools, lanes.ToolQuestion), headlessState, headlessOutcome, headlessReason, found, reached.Status, reached.AskedBy)
	// A lane that shows neither has still left the pane its own half to run.
	for _, failure := range headlessAskFailures(init, reached, found, rs5HeadlessLane) {
		t.Error(failure)
	}

	s.repo = pane.Dir
	s.playRS5([]any{
		map[string]any{"init": true},
		map[string]any{"hook": "PreToolUse", "input": map[string]any{"tool_name": lanes.ToolQuestion, "tool_use_id": "toolu_pane"}},
		map[string]any{"mcp": lanes.ToolAsk, "arguments": map[string]any{"text": rs5PaneQuestion}},
		map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{
			"outcome": "completed", "artifacts": []string{"none"}, "checks": []map[string]any{{"name": "stand-in", "passed": true}},
		}},
	})
	second := s.startDaemonWith(map[string]any{
		"args": []string{"--setting-sources", "project", "--strict-mcp-config", "--tools", lanes.ToolQuestion}, "env": paneEnv,
	})
	t.Cleanup(second.stop)
	var paneID string
	s.poll(second.stop, "the second work order", func() bool {
		out, err := s.tryCLI("work", "list", "--json")
		if err != nil {
			return false
		}
		var list api.WorkOrderList
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
	screens := &screenLog{}
	state := s.awaitLane(second, paneID, rs5PaneLane, func() { s.watchPane(second.stop, rs5PaneLane+"-g1", screens) })
	asked, found := s.questionNamed(rs5PaneQuestion)
	second.stop()
	outcome, reason := s.outcomeOf(paneID)
	records := s.hookRecords(rs5PaneLane)
	t.Logf("rs5: pane: state=%s outcome=%s reason=%s screens=%v asked=%v status=%s blocking=%v tools=%d post_tools=%d",
		state, outcome, reason, screens.names(), found, asked.Status, asked.Blocking, eventCount(records, "PreToolUse"), eventCount(records, "PostToolUse"))
	if !found {
		t.Error("the denial did not lead the session to ask")
	} else if asked.AskedBy != rs5PaneLane {
		t.Errorf("the question reads %+v", asked)
	}
	if state != workorders.Acceptance {
		t.Fatalf("the pane lane ended in %s\n%s", state, second.stderr.String())
	}
}

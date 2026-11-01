//go:build realsession

package real

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs7HeadlessLane = "rs7-headless"
	rs7PaneLane     = "rs7-pane"
	// rs7MaxTurns caps each input of a headless lane.
	rs7MaxTurns = "3"
	// A headless lane takes five inputs: a brief and a narrative, then the resume's prompt and
	// another narrative in the same session, then a fresh brief in a new one.
	rs7ResumedCharge = 12
	rs7FreshCharge   = 3
	// A pane takes three inputs, and its tool limit holds it to this.
	rs7PaneCharge = 8
	rs7ToolLimit  = 4
	// rs7TurnCeiling is the charge of this run. RS6 charges 13, so the two stay under 40.
	rs7TurnCeiling = rs7ResumedCharge + rs7FreshCharge + rs7PaneCharge
	// rs7Generations is the most a lane reaches here.
	rs7Generations = 3

	rs7FirstQuestion  = "rs7 first question"
	rs7SecondQuestion = "rs7 second question"
)

func rs7Profile(mode string) string {
	return `run_mode = "` + mode + `"

[agent]
model = "` + sessionModel + `"
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
handoff_timeout = "90s"
ask_wait = "1s"
max_restarts = 0
`
}

func rs7Contract(id, instructions string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "Come back from parking"

[[criteria]]
id = "c1"
text = "The lane reports its outcome after it comes back"

[[work_orders]]
id = "` + id + `-order"
title = "Come back from parking"
size = "small"
+++

This work order tests how a lane comes back from parking, and it has no other work. Change no
file, and call no tool these steps do not name. Follow the first of these that applies.

` + instructions + `
`
}

// rs7ThreeStops walks one session through a native resume and then a fresh generation.
const rs7ThreeStops = `1. If you were told you take over from an earlier generation: call the lane_report tool once, with outcome completed, and end your turn with the single word done.
2. If a message asks you to hand off and you asked the second question: call the lane_handoff tool once, with done set to ["asked the second question"], next_steps set to ["report the outcome"], and carries_next_step set to true. End your turn with the single word waiting.
3. If a message asks you to hand off: call the lane_handoff tool once, with done set to ["asked the first question"], next_steps set to ["ask the second question"], and carries_next_step set to false. End your turn with the single word waiting.
4. If a message says this lane parked: call the ask tool once, with blocking set to true and this exact question: ` + rs7SecondQuestion + `. End your turn with the single word waiting.
5. Otherwise call the ask tool once, with blocking set to true and this exact question: ` + rs7FirstQuestion + `. End your turn with the single word waiting.`

// rs7OneStop walks a pane through a native resume.
const rs7OneStop = `1. If a message says this lane parked: call the lane_report tool once, with outcome completed, and end your turn with the single word done.
2. If a message asks you to hand off: call the lane_handoff tool once, with done set to ["asked the question"], next_steps set to ["report the outcome"], and carries_next_step set to false. End your turn with the single word waiting.
3. Otherwise call the ask tool once, with blocking set to true and this exact question: ` + rs7FirstQuestion + `. End your turn with the single word waiting.`

// allInits returns every system/init of a stream, oldest first.
func allInits(stream []byte) []initLine {
	var out []initLine
	for line := range bytes.SplitSeq(stream, []byte("\n")) {
		var event initLine
		if json.Unmarshal(line, &event) == nil && event.Type == "system" && event.Subtype == "init" {
			out = append(out, event)
		}
	}
	return out
}

// sameStart says two sessions started from the same flags: the same model, mode, servers, and
// tools, which is what a flag a resume dropped would change.
func sameStart(a, b initLine) bool {
	names := func(init initLine) []string {
		var out []string
		for _, server := range init.Servers {
			out = append(out, server.Name)
		}
		return out
	}
	return a.Model == b.Model && a.Permission == b.Permission && slices.Equal(names(a), names(b)) && slices.Equal(a.Tools, b.Tools)
}

// generationsOf reads a lane from the ledger behind a stopped daemon.
func (s *suite) generationsOf(lane string) []ledger.Generation {
	s.t.Helper()
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer store.Close()
	var out []ledger.Generation
	err = store.Read(s.t.Context(), rs6Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Generations(lane)
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return out
}

// reapGenerations ends, when a suite stops early, what any of a lane's generations left.
func (s *suite) reapGenerations(names ...string) {
	s.t.Helper()
	s.t.Cleanup(func() {
		for _, lane := range names {
			for generation := 1; generation <= rs7Generations; generation++ {
				state, err := shimlink.ReadState(filepath.Join(s.paths.StateDir, "lanes", lane, fmt.Sprintf("shim-%d", generation)))
				if err != nil {
					continue
				}
				if !s.endSession(state) {
					s.t.Errorf("a session of %s outlasted its signal", lane)
				}
			}
		}
	})
}

// playStandIn gives a dry run what the next session plays, because a fixture cannot tell one
// lane from another.
func (s *suite) playStandIn(turns, resumed []any) {
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
	fixture["turns"], fixture["resume_turns"] = turns, resumed
	if raw, err = json.Marshal(fixture); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func standInResult() map[string]any {
	return map[string]any{"emit": map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.01,
		"usage": map[string]any{"input_tokens": 3, "output_tokens": 5},
	}}
}

func standInAsk(text string) []any {
	return []any{
		map[string]any{"init": true},
		map[string]any{"mcp": lanes.ToolAsk, "arguments": map[string]any{"text": text, "blocking": true}},
		standInResult(),
	}
}

func standInHandoff(carries bool) []any {
	return []any{
		map[string]any{"mcp": lanes.ToolLaneHandoff, "arguments": map[string]any{
			"done": []string{"asked"}, "in_progress": []string{}, "next_steps": []string{"go on"}, "risks": []string{}, "carries_next_step": carries,
		}},
		standInResult(),
	}
}

func standInReport() []any {
	return []any{
		map[string]any{"init": true},
		map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{
			"outcome": "completed", "artifacts": []string{"none"}, "checks": []map[string]any{{"name": "stand-in", "passed": true}},
		}},
		standInResult(),
	}
}

// awaitState waits for a work order to reach a state, at a generation.
func (s *suite) awaitState(d *daemon, id, lane string, state workorders.State, generation int64, watch func()) {
	s.t.Helper()
	failed := map[workorders.State]bool{workorders.Blocked: true, workorders.Failed: true}
	s.poll(d.stop, fmt.Sprintf("lane %s to read %s in generation %d", lane, state, generation), func() bool {
		watch()
		row := s.workOrder(id)
		if failed[row.State] {
			s.stopWith(d.stop, "lane %s ended %s", lane, row.State)
		}
		return row.State == state && s.laneView(lane).Generation == generation
	})
}

// checkedStream reads a lane's stream, and stops the run at its first init if user configuration
// loaded. This suite's six-session budget leaves no room for a probe of its own, so the first
// lane's init stands for one, on the flags RS6's probe passed.
func (s *suite) checkedStream(stop func()) []initLine {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.t.Fatal(err)
	}
	inits := allInits(raw)
	for _, init := range inits {
		if err := checkInit(init, []string{"ropeshift"}, laneTools); err != nil {
			s.stopWith(stop, "a lane's init: %v", err)
		}
	}
	return inits
}

// TestRS7 brings real sessions back in each run mode. Only a real session can show a native
// resume that keeps its conversation with every start flag passed again, and a fresh generation
// that goes on from a handoff.
func TestRS7(t *testing.T) {
	s := start(t, "rs7")
	s.spend.turnCeiling = rs7TurnCeiling
	t.Cleanup(s.killTerminal)
	s.reapGenerations(rs7HeadlessLane)
	s.build()

	headless := gittest.NewIn(t, s.subdirectory("h"))
	headless.Commit(".ropeshift/profile.toml", rs7Profile(lanes.ProfileRunHeadless))
	headless.Commit("contract.md", rs7Contract("rs7h", rs7ThreeStops))
	pane := gittest.NewIn(t, s.subdirectory("p"))
	pane.Commit(".ropeshift/profile.toml", rs7Profile(lanes.ProfileRunTmux))
	pane.Commit("contract.md", rs7Contract("rs7p", rs7OneStop))

	s.repo = headless.Dir
	s.worktree = filepath.Join(filepath.Dir(headless.Dir), filepath.Base(headless.Dir)+"-lanes", rs7HeadlessLane)
	paneWorktree := filepath.Join(filepath.Dir(pane.Dir), filepath.Base(pane.Dir)+"-lanes", rs7PaneLane)
	s.others = []string{headless.Dir, s.worktree, pane.Dir, paneWorktree}
	for session, turns := range map[string]int{"resumed": rs7ResumedCharge, "fresh": rs7FreshCharge, "pane": rs7PaneCharge} {
		if err := s.spend.charge(session, turns); err != nil {
			t.Fatal(err)
		}
	}
	// A native resume needs a transcript, so this suite's sessions keep one, and finish takes
	// away only the ones this run's ledger names.
	flags := []string{"--setting-sources", "project", "--strict-mcp-config", "--tools", "", "--max-budget-usd", budgetUSD, "--max-turns", rs7MaxTurns}
	s.playStandIn([]any{standInAsk(rs7FirstQuestion), standInHandoff(false)}, []any{standInAsk(rs7SecondQuestion), standInHandoff(true)})

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
	watch := func(stop func()) func() {
		return func() {
			s.checkStream(stop)
			s.checkedStream(stop)
		}
	}
	if state := s.awaitParked(first, id, rs7HeadlessLane, watch(first.stop)); state != workorders.Parked {
		s.stopWith(first.stop, "the lane did not park: %s", state)
	}
	s.cli("lane", "resume", id)
	s.awaitState(first, id, rs7HeadlessLane, workorders.Parked, 2, watch(first.stop))
	first.stop()

	s.playStandIn([]any{standInReport()}, nil)
	second := s.startDaemonWith(map[string]any{"args": flags, "stream_log": s.stream, "expired_cache": true})
	t.Cleanup(second.stop)
	s.poll(second.stop, "the second daemon", func() bool {
		_, err := s.tryCLI("work", "show", id, "--json")
		return err == nil
	})
	s.cli("lane", "resume", id)
	s.awaitState(second, id, rs7HeadlessLane, workorders.Acceptance, 3, watch(second.stop))
	second.stop()

	s.reportHeadless()

	s.repo = pane.Dir
	s.playStandIn([]any{standInAsk(rs7FirstQuestion), standInHandoff(false)}, []any{standInReport()})
	third := s.startDaemonWith(map[string]any{"args": paneGuardFlags})
	t.Cleanup(third.stop)
	var paneID string
	s.poll(third.stop, "the pane's work order", func() bool {
		out, err := s.tryCLI("work", "list", "--json")
		if err != nil {
			return false
		}
		var list api.WorkOrderList
		if err := json.Unmarshal(out, &list); err != nil {
			t.Fatal(err)
		}
		others := slices.DeleteFunc(slices.Clone(list.WorkOrders), func(row workorders.WorkOrder) bool { return row.ID == id })
		if len(others) == 1 {
			paneID = others[0].ID
		}
		return paneID != ""
	})
	screens := &screenLog{}
	paneWatch := func() {
		s.watchPane(third.stop, rs7PaneLane+"-g1", screens)
		s.watchPane(third.stop, rs7PaneLane+"-g2", screens)
		if tools := eventCount(s.hookRecords(rs7PaneLane), "PreToolUse"); tools > rs7ToolLimit {
			s.stopWith(third.stop, "%d tool calls passed the limit of %d", tools, rs7ToolLimit)
		}
	}
	if state := s.awaitParked(third, paneID, rs7PaneLane, paneWatch); state != workorders.Parked {
		s.stopWith(third.stop, "the pane did not park: %s", state)
	}
	s.cli("lane", "resume", paneID)
	s.awaitState(third, paneID, rs7PaneLane, workorders.Acceptance, 2, paneWatch)
	left := s.settleProcesses(rs7PaneLane)
	third.stop()
	s.reportPane(screens, left)
}

// reportHeadless checks the lane that walked both paths, and keeps no ID in what it logs.
func (s *suite) reportHeadless() {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		s.t.Fatal(err)
	}
	inits := allInits(raw)
	generations := s.generationsOf(rs7HeadlessLane)
	if len(generations) != rs7Generations {
		s.t.Fatalf("generations %d", len(generations))
	}
	same := true
	for _, init := range inits {
		same = same && sameStart(inits[0], init)
	}
	native := generations[0].SessionID == generations[1].SessionID
	fresh := generations[2].SessionID != generations[1].SessionID
	s.t.Logf("rs7: headless: version=%s reasons=%s,%s,%s same_session_on_resume=%v new_session_on_handoff=%v inits=%d same_start=%v handoff_in=%v responded=%v,%v",
		inits[0].Version, generations[0].StartReason, generations[1].StartReason, generations[2].StartReason, native, fresh,
		len(inits), same, generations[2].HandoffIn != "", generations[0].LastResponseAt != 0, generations[1].LastResponseAt != 0)
	if generations[1].StartReason != ledger.StartResume || generations[2].StartReason != ledger.StartHandoff {
		s.t.Errorf("reasons %s and %s", generations[1].StartReason, generations[2].StartReason)
	}
	if !native {
		s.t.Error("a native resume changed its session")
	}
	// A dry run's scripted CLI has one session ID, so only a real run can show a new one.
	if (!fresh && s.standIn == "") || generations[2].HandoffIn == "" {
		s.t.Error("the fresh generation did not go on from its handoff")
	}
	if !same {
		s.t.Error("a resume started from other flags")
	}
	if generations[0].LastResponseAt == 0 {
		s.t.Error("the cache clock never started")
	}
}

// reportPane checks the lane that resumed in a pane. A pane has no stream, so its hooks and its
// MCP tool are what show `--settings` and `--mcp-config` reached the resumed process.
func (s *suite) reportPane(screens *screenLog, left int) {
	s.t.Helper()
	generations := s.generationsOf(rs7PaneLane)
	if len(generations) != 2 {
		s.t.Fatalf("generations %d", len(generations))
	}
	records := s.hookRecords(rs7PaneLane)
	second := map[string]int64{}
	for _, record := range records {
		if record.Generation == 2 {
			second[record.Event] += record.Count
		}
	}
	native := generations[0].SessionID == generations[1].SessionID
	s.t.Logf("rs7: pane: screens=%v reason=%s same_session_on_resume=%v resumed_session_start=%d resumed_tools=%d resumed_prompts=%d processes_left=%d",
		screens.names(), generations[1].StartReason, native, second["SessionStart"], second["PreToolUse"], second["UserPromptSubmit"], left)
	if generations[1].StartReason != ledger.StartResume {
		s.t.Errorf("reason %s", generations[1].StartReason)
	}
	if !native {
		s.t.Error("a native resume changed its session")
	}
	if second["SessionStart"] == 0 || second["PreToolUse"] == 0 {
		s.t.Error("the resumed process ran no generated hook, so a start flag was lost")
	}
	if left != 0 {
		s.t.Error("a process of the session remains")
	}
}

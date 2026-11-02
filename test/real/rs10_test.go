//go:build realsession

package real

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs10HeadlessLane = "rs10-headless"
	rs10PaneLane     = "rs10-pane"
	rs10Question     = "rs10 question"
	// rs10MaxTurns caps each input of a headless lane.
	rs10MaxTurns = "3"
	// A headless lane takes five inputs: its brief, the compaction, the turn after it, a handoff, and
	// a next generation. A pane's five are its reads, the compaction, the turn after it, and the
	// brief a clear owes it.
	rs10HeadlessCharge = 13
	rs10PaneCharge     = 11
	// rs10Sessions are the first probe, two generations of a headless lane, and a pane with the
	// session its clear begins.
	rs10Sessions    = 5
	rs10TurnCeiling = rs10HeadlessCharge + rs10PaneCharge + 1
	// rs10ToolLimit ends a pane that keeps calling tools past that charge.
	rs10ToolLimit = 7
	// rs10HeadlessCompact is under anything a call can report, so no compaction can pass.
	rs10HeadlessCompact = 1
	// rs10PaneCompact sits above where a pane starts and below where its reads take it, so only a
	// compaction that ran can bring the next call under it.
	rs10PaneCompact = 30000
	// rs10Reads and rs10ReadBytes are what a pane reads to get there.
	rs10Reads     = 3
	rs10ReadBytes = 30000
	// rs10Keyed is how long a pane may take to show what was typed.
	rs10Keyed = 5 * time.Second
)

func rs10Profile(mode string, compact int) string {
	return fmt.Sprintf(`run_mode = %q

[agent]
model = %q
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
ask_wait = "10m"
max_restarts = 0

[context.roles.writer]
compact_tokens = %d
handoff_tokens = 180000
`, mode, sessionModel, compact)
}

// rs10Steps names the one thing that differs between the two lanes: what they read before they rest.
func rs10Contract(id, reads string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "Hold a lane's context"

[[criteria]]
id = "c1"
text = "The lane reports the generation it is in"

[[work_orders]]
id = "` + id + `-order"
title = "Hold a lane's context"
size = "small"
+++

This work order tests how a lane's context is held, and it has no other work. Change no file, read
no file, and call no tool these steps do not name. Read the latest message you received, and follow
the first of these that applies to it.

1. If you are in generation 2 or later of this lane, call the lane_report tool once, with outcome completed and reason set to the word generation, one space, and your generation's number. End your turn with the single word done.
2. If the message says your context was compacted, call the ask tool once, with blocking set to true and this exact question: ` + rs10Question + `. End your turn with the single word waiting.
3. If the message tells you to write a handoff, do what it says, and end your turn.
4. Otherwise ` + reads + ` Make one call at a time, you need nothing from what they answer, and end your turn with the single word waiting. Do not call lane_report here: a later step of this lane does that.
`
}

const rs10HeadlessReads = `call the decision_search tool once with the query rs10, then the question_search tool once with the query rs10.`

func rs10PaneReads() string {
	keys := make([]string, 0, rs10Reads)
	for i := 1; i <= rs10Reads; i++ {
		keys = append(keys, fmt.Sprintf("RS10-%d", i))
	}
	return "call the requirement_get tool once for each of these keys, in order: " + strings.Join(keys, ", ") + "."
}

// rs10Requirements are long, and made of words nothing can shorten, so that reading them moves a
// session's context by a known amount and a compaction takes it back.
func rs10Requirements(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	out.WriteString("+++\nkind = \"requirements\"\nid = \"rs10-reqs\"\ntitle = \"Requirements\"\n\n")
	for i := 1; i <= rs10Reads; i++ {
		raw := make([]byte, rs10ReadBytes/2)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		text := hex.EncodeToString(raw)
		var words strings.Builder
		for len(text) > 8 {
			words.WriteString(text[:8] + " ")
			text = text[8:]
		}
		fmt.Fprintf(&out, "[[requirement]]\nid = \"RS10-%d\"\ntext = %q\n\n", i, words.String())
	}
	out.WriteString("+++\n")
	return out.String()
}

func standInUsage(tokens int) map[string]any {
	return map[string]any{"emit": map[string]any{"type": "stream_event", "parent_tool_use_id": nil, "event": map[string]any{
		"type": "message_start", "message": map[string]any{"usage": map[string]any{
			"input_tokens": tokens, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0, "output_tokens": 0,
		}},
	}}}
}

func standInStop() map[string]any { return map[string]any{"hook": "Stop", "input": map[string]any{}} }

func standInPrompt() map[string]any {
	return map[string]any{"hook": "UserPromptSubmit", "input": map[string]any{"prompt": "an input"}}
}

func standInGenerationReport() map[string]any {
	return map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{
		"outcome": "completed", "reason": "generation 2", "artifacts": []string{"none"},
		"checks": []map[string]any{{"name": "stand-in", "passed": true}},
	}}
}

// standInHeadless plays a lane whose compaction cannot pass, and the next generation it hands off to.
func standInHeadless() []any {
	return []any{
		[]any{
			map[string]any{"init": true},
			standInPrompt(),
			map[string]any{"after_first": "rs10-handed-off", "then": []any{standInGenerationReport()}},
			map[string]any{"first_time": "rs10-handed-off", "then": []any{
				standInUsage(9000),
				map[string]any{"mcp": lanes.ToolDecisionSearch, "arguments": map[string]any{"query": "rs10"}},
				map[string]any{"mcp": lanes.ToolQuestionSearch, "arguments": map[string]any{"query": "rs10"}},
			}},
			standInStop(),
			standInResult(),
		},
		[]any{
			standInPrompt(),
			standInUsage(8000),
			map[string]any{"mcp": lanes.ToolAsk, "arguments": map[string]any{"text": rs10Question, "blocking": true}},
			standInStop(),
			standInResult(),
		},
		[]any{
			standInPrompt(),
			map[string]any{"mcp": lanes.ToolLaneHandoff, "arguments": map[string]any{
				"done": []string{"searched"}, "in_progress": []string{}, "next_steps": []string{"report"}, "risks": []string{}, "carries_next_step": true,
			}},
			standInStop(),
			standInResult(),
		},
	}
}

// standInPane plays a pane whose reads pass its threshold and whose compaction brings it back.
func standInPane() []any {
	status := func(tokens int) map[string]any {
		return map[string]any{"status": map[string]any{"context_window": map[string]any{"current_usage": map[string]any{"input_tokens": tokens}}}}
	}
	reads := []any{map[string]any{"init": true}, status(12000)}
	for i := 1; i <= rs10Reads; i++ {
		reads = append(reads, map[string]any{"mcp": lanes.ToolRequirementGet, "arguments": map[string]any{"key": fmt.Sprintf("RS10-%d", i)}})
	}
	reads = append(reads, status(60000))
	return []any{
		reads,
		[]any{status(13000), map[string]any{"mcp": lanes.ToolAsk, "arguments": map[string]any{"text": rs10Question, "blocking": true}}},
		[]any{status(9000), standInGenerationReport()},
	}
}

// typeOnPane types one command at a prompt this run recognized, through this run's private socket,
// and sends Enter only once the prompt shows what was typed. It is the one key this suite sends, and
// a screen it does not recognize ends the run with nothing pressed.
func (s *suite) typeOnPane(stop func(), pane, command string, screens *screenLog) {
	s.t.Helper()
	text := s.watchPane(stop, pane, screens)
	for _, other := range []string{"Do you want to proceed", "Accessing workspace:", "Enter to confirm", "Esc to cancel"} {
		if strings.Contains(text, other) {
			s.stopWith(stop, "the pane is not at a prompt, so nothing was typed")
		}
	}
	if !strings.Contains(text, "❯") {
		s.stopWith(stop, "no prompt on the pane, so nothing was typed")
	}
	if out, err := s.tmuxCommand("send-keys", "-t", "="+pane+":", "-l", "--", command).CombinedOutput(); err != nil {
		s.stopWith(stop, "typing: %v: %s", err, redact(string(out)))
	}
	typed := regexp.MustCompile(`❯\s*` + regexp.QuoteMeta(command))
	for deadline := time.Now().Add(rs10Keyed); ; time.Sleep(100 * time.Millisecond) {
		if typed.MatchString(s.capture(pane)) {
			break
		}
		if time.Now().After(deadline) {
			s.stopWith(stop, "the prompt never showed what was typed, so no Enter was sent")
		}
	}
	if out, err := s.tmuxCommand("send-keys", "-t", "="+pane+":", "Enter").CombinedOutput(); err != nil {
		s.stopWith(stop, "pressing Enter: %v: %s", err, redact(string(out)))
	}
}

// journalRecord reads where the worker holds a lane, for a figure no CLI command prints.
func (s *suite) journalRecord(lane string) lanes.Record {
	s.t.Helper()
	journal, err := sqlite.OpenJournal(filepath.Join(s.paths.StateDir, api.JournalFileName))
	if err != nil {
		s.t.Fatal(err)
	}
	defer journal.Close()
	record, err := journal.Lane(context.Background(), lane)
	if err != nil {
		s.t.Fatal(err)
	}
	return record
}

func (s *suite) checkHeadlessContext(id string, state workorders.State) {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		s.t.Fatal(err)
	}
	boundaries := bytes.Count(raw, []byte(`"compact_boundary"`))
	generations := s.generationsOf(rs10HeadlessLane)
	reason := s.reportedReason(id)
	records := s.hookRecords(rs10HeadlessLane)
	s.t.Logf("rs10: headless: state=%s boundaries=%d post_compact=%d generations=%d reason_says_two=%v",
		state, boundaries, eventCount(records, "PostCompact"), len(generations), reason == "generation 2")
	if boundaries == 0 {
		s.t.Error("headless: no compaction ran")
	}
	if len(generations) != 2 {
		s.t.Fatalf("headless: %d generations", len(generations))
	}
	if generations[1].StartReason != ledger.StartHandoff || generations[0].HandoffOut == "" {
		s.t.Errorf("headless: the next generation began %s, with handoff=%v", generations[1].StartReason, generations[0].HandoffOut != "")
	}
	if state != workorders.Acceptance {
		s.t.Errorf("headless: the lane ended %s", state)
	}
	if reason != "generation 2" {
		s.t.Error("headless: the next generation did not name itself")
	}
}

func (s *suite) checkPaneContext(id string, state workorders.State, compacted lanes.Record, screens *screenLog, left int) {
	s.t.Helper()
	generations := s.generationsOf(rs10PaneLane)
	reason := s.reportedReason(id)
	s.t.Logf("rs10: pane: state=%s compactions=%d pending=%v tokens_after=%d generations=%d reason_says_two=%v screens=%v processes_left=%d",
		state, compacted.Progress.Compactions, compacted.Progress.CompactPending, compacted.Progress.ContextTokens, len(generations), reason == "generation 2", screens.names(), left)
	if compacted.Progress.Compactions == 0 || compacted.Progress.CompactPending {
		s.t.Errorf("pane: no compaction closed: %d, pending %v", compacted.Progress.Compactions, compacted.Progress.CompactPending)
	}
	if tokens := compacted.Progress.ContextTokens; tokens == 0 || tokens >= rs10PaneCompact {
		s.t.Errorf("pane: the call after a compaction reported %d", tokens)
	}
	if len(generations) != 2 {
		s.t.Fatalf("pane: %d generations", len(generations))
	}
	if generations[1].StartReason != ledger.StartClear {
		s.t.Errorf("pane: the second generation began %s", generations[1].StartReason)
	}
	if generations[1].SessionID == "" || generations[0].SessionID == generations[1].SessionID {
		s.t.Error("pane: a clear left the session it found")
	}
	if state != workorders.Acceptance {
		s.t.Errorf("pane: the lane ended %s", state)
	}
	if reason != "generation 2" {
		s.t.Error("pane: the cleared session did not read its generation, which only additionalContext tells it")
	}
	if left != 0 {
		s.t.Error("pane: a process of the session remains")
	}
}

// TestRS10 takes real sessions through their context. A headless lane's compaction cannot fall under
// its threshold, so the next call's usage fails it and the lane hands off. A pane's does, and a clear
// typed at its prompt then begins a generation in the same process. Only a real session can show
// `/compact` through each path, the usage a call reports after it, and `additionalContext` reaching
// a cleared session: the generation it reports is written nowhere else it can read.
func TestRS10(t *testing.T) {
	s := start(t, "rs10")
	s.watched = []string{rs10HeadlessLane, rs10PaneLane}
	s.spend.sessionCeiling, s.spend.turnCeiling = rs10Sessions, rs10TurnCeiling
	t.Cleanup(s.killTerminal)
	s.reapGenerations(rs10HeadlessLane)
	s.build()
	s.probe()

	headless := gittest.NewIn(t, s.subdirectory("h"))
	headless.Commit(".ropeshift/profile.toml", rs10Profile(lanes.ProfileRunHeadless, rs10HeadlessCompact))
	headless.Commit("contract.md", rs10Contract("rs10h", rs10HeadlessReads))
	pane := gittest.NewIn(t, s.subdirectory("p"))
	pane.Commit(".ropeshift/profile.toml", rs10Profile(lanes.ProfileRunTmux, rs10PaneCompact))
	pane.Commit("requirements.md", rs10Requirements(t))
	pane.Commit("contract.md", rs10Contract("rs10p", rs10PaneReads()))

	worktree := func(dir, lane string) string {
		return filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-lanes", lane)
	}
	s.repo = headless.Dir
	s.worktree = worktree(headless.Dir, rs10HeadlessLane)
	s.others = []string{headless.Dir, s.worktree, pane.Dir, worktree(pane.Dir, rs10PaneLane)}
	for session, turns := range map[string]int{"headless": rs10HeadlessCharge - 3, "next": 3, "pane": rs10PaneCharge - 3, "cleared": 3} {
		if err := s.spend.charge(session, turns); err != nil {
			t.Fatal(err)
		}
	}

	s.playStandIn(standInHeadless(), nil)
	flags := append(append([]string{}, guardFlags...), "--max-turns", rs10MaxTurns)
	first := s.startDaemonWith(map[string]any{"args": flags, "stream_log": s.stream})
	t.Cleanup(first.stop)
	id := s.dispatch(first, rs10HeadlessLane)
	watch := func() {
		s.checkStream(first.stop)
		s.checkedStream(first.stop)
	}
	s.poll(first.stop, "the headless lane's compaction", func() bool {
		watch()
		return eventCount(s.hookRecords(rs10HeadlessLane), "PostCompact") != 0
	})
	s.poll(first.stop, "the headless lane's handoff", func() bool {
		watch()
		return s.laneView(rs10HeadlessLane).Generation == 2
	})
	s.awaitState(first, id, rs10HeadlessLane, workorders.Acceptance, 2, watch)
	headlessState := s.workOrder(id).State
	first.stop()
	s.checkHeadlessContext(id, headlessState)

	s.repo = pane.Dir
	s.playStandIn(standInPane(), nil)
	second := s.startDaemonWith(map[string]any{"args": paneGuardFlags, "env": paneEnv})
	t.Cleanup(second.stop)
	paneID := s.laneOrder(second.stop, []string{id})
	screens := &screenLog{}
	name := rs10PaneLane + "-g1"
	paneWatch := func() {
		s.watchPane(second.stop, name, screens)
		if tools := eventCount(s.hookRecords(rs10PaneLane), "PreToolUse"); tools > rs10ToolLimit {
			s.stopWith(second.stop, "%d tool calls passed the limit of %d", tools, rs10ToolLimit)
		}
	}
	s.startLane(second, paneID, rs10PaneLane)
	s.poll(second.stop, "the pane's compaction", func() bool {
		paneWatch()
		return eventCount(s.hookRecords(rs10PaneLane), "PostCompact") != 0
	})
	var compacted lanes.Record
	s.poll(second.stop, "the pane at rest after its compaction", func() bool {
		paneWatch()
		compacted = s.journalRecord(rs10PaneLane)
		if compacted.Generation != 1 {
			s.stopWith(second.stop, "the pane left generation 1 before its clear, so its compaction did not hold")
		}
		return !compacted.Progress.CompactPending && len(compacted.Blocking) != 0 &&
			s.laneView(rs10PaneLane).ActualState == string(lanes.Idle)
	})
	s.typeOnPane(second.stop, name, "/clear", screens)
	s.awaitState(second, paneID, rs10PaneLane, workorders.Acceptance, 2, paneWatch)
	left := s.settleProcesses(rs10PaneLane)
	paneState := s.workOrder(paneID).State
	second.stop()
	s.checkPaneContext(paneID, paneState, compacted, screens, left)
}

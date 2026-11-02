//go:build realsession

package real

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs8HeadlessLane = "rs8-headless"
	rs8PaneLane     = "rs8-pane"
	rs8RefusedLane  = "rs8-refused"
	rs8Question     = "rs8 question"
	// rs8MaxTurns caps each input of a headless lane, and a lane takes two: its brief and a message.
	rs8MaxTurns = "3"
	// A lane that replies is charged for both inputs, and one that refuses for its brief alone.
	rs8Charge        = 6
	rs8RefusedCharge = 3
	// rs8Sessions are the first probe and three lanes.
	rs8Sessions    = 4
	rs8TurnCeiling = 2*rs8Charge + rs8RefusedCharge + 1
	// rs8ToolLimit ends a pane that keeps calling tools past that charge.
	rs8ToolLimit = 4
	// rs8Body is what a message says. It is plain work, so a session has no reason to weigh it.
	rs8Body = "Answer this message as your work order says."
)

func rs8Profile(mode string) string {
	return `run_mode = "` + mode + `"

[agent]
model = "` + sessionModel + `"
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
ask_wait = "10m"
max_restarts = 0
`
}

// rs8Steps are what a session does with its brief and with a message. The session names the message
// by the ID in its first line, so a reply proves that line reached it.
const rs8Steps = `1. If your latest message opens with a line that starts with [ropeshift message, take the ID that follows those words, and call the reply tool once, with reply_to set to that ID and body set to the single word received. Then call the lane_report tool once, with outcome completed, and end your turn with the single word done.
2. Otherwise call the ask tool once, with blocking set to true and this exact question: ` + rs8Question + `. End your turn with the single word waiting.`

func rs8Contract(id string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "Answer a message"

[[criteria]]
id = "c1"
text = "The lane replies to the message it receives"

[[work_orders]]
id = "` + id + `-order"
title = "Answer a message"
size = "small"
+++

This work order tests how a lane takes a message, and it has no other work. Change no file, and
call no tool these steps do not name. Read only the latest message you received, and follow the
first of these that applies to it.

` + rs8Steps + `
`
}

func rs8Message(lane string) string { return "m-" + strings.TrimPrefix(lane, "rs8-") }

// standInBrief asks as standInAsk does, with the hook the real CLI runs for a brief on stdin, which
// a scripted session does not run by itself.
func standInBrief(text string) []any {
	hook := map[string]any{"hook": "UserPromptSubmit", "input": map[string]any{"prompt": "the brief"}}
	return append([]any{hook}, standInAsk(text)...)
}

func standInAnswers(lane string) []any {
	return []any{
		map[string]any{"init": true},
		map[string]any{"mcp": lanes.ToolReply, "arguments": map[string]any{"reply_to": rs8Message(lane), "body": "received"}},
		map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{
			"outcome": "completed", "artifacts": []string{"none"}, "checks": []map[string]any{{"name": "stand-in", "passed": true}},
		}},
		standInResult(),
	}
}

// requireQualified stops before a session starts. A socket surface runs only on a version the matrix
// pins, so on any other this suite could spend turns and prove nothing. `--version` starts no session.
func (s *suite) requireQualified() string {
	s.t.Helper()
	env := s.environment()
	path, err := claudeOn(env)
	if err != nil {
		s.t.Fatal(err)
	}
	version := exec.Command(path, "--version")
	version.Env = env
	out, err := version.Output()
	if err != nil {
		s.t.Fatalf("the version: %v", err)
	}
	word, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	if !claudecode.Qualified(word) {
		s.t.Fatalf("STOP: version %s passed no check of the socket, and this suite would prove nothing on it", word)
	}
	return word
}

// laneOrder waits for the work order this run has not seen yet, because a later daemon reads the
// same ledger.
func (s *suite) laneOrder(stop func(), known []string) string {
	s.t.Helper()
	var id string
	s.poll(stop, "the next work order", func() bool {
		out, err := s.tryCLI("work", "list", "--json")
		if err != nil {
			return false
		}
		var list api.WorkOrderList
		if err := json.Unmarshal(out, &list); err != nil {
			s.t.Fatal(err)
		}
		for _, row := range list.WorkOrders {
			if !slices.Contains(known, row.ID) {
				id = row.ID
			}
		}
		return id != ""
	})
	return id
}

// atRest starts a lane and waits for it to ask and go idle, where a message finds it.
func (s *suite) atRest(d *daemon, id, lane string, watch func()) {
	s.t.Helper()
	s.poll(d.stop, "the worker's session", func() bool {
		var workers api.WorkerList
		if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
			s.t.Fatal(err)
		}
		return len(workers.Workers) == 1 && workers.Workers[0].Epoch >= 1
	})
	var workers api.WorkerList
	if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
		s.t.Fatal(err)
	}
	s.cli("work", "review", id)
	s.cli("work", "claim", id, "--worker", workers.Workers[0].ID)
	s.cli("lane", "start", id, "--lane", lane)
	s.poll(d.stop, "lane "+lane+" at rest", func() bool {
		watch()
		return s.laneView(lane).ActualState == string(lanes.Idle)
	})
}

func (s *suite) messageRow(id string) messages.Message {
	s.t.Helper()
	var out messages.Message
	if err := json.Unmarshal(s.cli("message", "show", id, "--json"), &out); err != nil {
		s.t.Fatal(err)
	}
	return out
}

// awaitMessage waits for a message to settle, and stops the run if it settles somewhere else.
func (s *suite) awaitMessage(d *daemon, id string, want messages.State, watch func()) messages.Message {
	s.t.Helper()
	var row messages.Message
	s.poll(d.stop, "message "+id+" to read "+string(want), func() bool {
		watch()
		row = s.messageRow(id)
		return row.State == want
	})
	return row
}

// messageStates reads what the log holds of one message, in order, behind a stopped daemon.
func (s *suite) messageStates(id string) []messages.State {
	s.t.Helper()
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer store.Close()
	var out []messages.State
	err = store.Read(s.t.Context(), rs6Scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Type != messages.EventChanged || e.Subject.ID != id {
				continue
			}
			var row messages.Message
			if err := json.Unmarshal(e.Data, &row); err != nil {
				return err
			}
			out = append(out, row.State)
		}
		return nil
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return out
}

// replayOf reads the stream for what only a headless session says of a message: the sender's own
// `uuid`, with a peer's origin.
func replayOf(stream []byte, id string) (found, peer bool) {
	var line struct {
		Type     string `json:"type"`
		IsReplay bool   `json:"isReplay"`
		UUID     string `json:"uuid"`
		Origin   struct {
			Kind string `json:"kind"`
		} `json:"origin"`
	}
	for raw := range bytes.SplitSeq(stream, []byte("\n")) {
		line.Type, line.IsReplay, line.UUID, line.Origin.Kind = "", false, "", ""
		if json.Unmarshal(raw, &line) != nil || line.Type != "user" || !line.IsReplay || line.UUID != messages.InputID(id) {
			continue
		}
		return true, line.Origin.Kind == "peer"
	}
	return false, false
}

// reportReply holds what this suite set out to show for one lane that took a message.
func (s *suite) reportReply(mode, lane string, wantReplay bool) {
	s.t.Helper()
	id := rs8Message(lane)
	states := s.messageStates(id)
	prompts := eventCount(s.hookRecords(lane), "UserPromptSubmit")
	replayed, peer := false, false
	if wantReplay {
		raw, err := os.ReadFile(s.stream)
		if err != nil {
			s.t.Fatal(err)
		}
		replayed, peer = replayOf(raw, id)
	}
	s.t.Logf("rs8: %s: states=%v prompts=%d replay=%v peer=%v", mode, states, prompts, replayed, peer)
	want := []messages.State{messages.Queued, messages.AtWorker, messages.HandedOff, messages.Admitted, messages.Replied}
	if !slices.Equal(states, want) {
		s.t.Errorf("%s: a message walked %v, want %v", mode, states, want)
	}
	if prompts < 2 {
		s.t.Errorf("%s: %d prompts on record, want the brief and the message", mode, prompts)
	}
	if wantReplay && (!replayed || !peer) {
		s.t.Errorf("%s: replay=%v peer=%v", mode, replayed, peer)
	}
}

// TestRS8 sends real sessions a message in each run mode, and to one that refuses it. Only a real
// session can show `UserPromptSubmit` and the replay as admission, a reply by `reply_to`, and a
// receipt reaching a reply address.
func TestRS8(t *testing.T) {
	s := start(t, "rs8")
	s.watched = []string{rs8HeadlessLane, rs8PaneLane, rs8RefusedLane}
	s.spend.sessionCeiling, s.spend.turnCeiling = rs8Sessions, rs8TurnCeiling
	t.Cleanup(s.killTerminal)
	s.reapGenerations(rs8HeadlessLane, rs8RefusedLane)
	s.build()
	s.probe()
	version := s.requireQualified()

	headless := gittest.NewIn(t, s.subdirectory("h"))
	headless.Commit(".ropeshift/profile.toml", rs8Profile(lanes.ProfileRunHeadless))
	headless.Commit("contract.md", rs8Contract("rs8h"))
	pane := gittest.NewIn(t, s.subdirectory("p"))
	pane.Commit(".ropeshift/profile.toml", rs8Profile(lanes.ProfileRunTmux))
	pane.Commit("contract.md", rs8Contract("rs8p"))
	refusing := gittest.NewIn(t, s.subdirectory("r"))
	refusing.Commit(".ropeshift/profile.toml", rs8Profile(lanes.ProfileRunHeadless))
	refusing.Commit("contract.md", rs8Contract("rs8r"))

	s.repo = headless.Dir
	worktree := func(dir, lane string) string {
		return filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-lanes", lane)
	}
	s.worktree = worktree(headless.Dir, rs8HeadlessLane)
	s.others = []string{
		headless.Dir, s.worktree, pane.Dir, worktree(pane.Dir, rs8PaneLane), refusing.Dir, worktree(refusing.Dir, rs8RefusedLane),
	}
	for session, turns := range map[string]int{"headless": rs8Charge, "pane": rs8Charge, "refused": rs8RefusedCharge} {
		if err := s.spend.charge(session, turns); err != nil {
			t.Fatal(err)
		}
	}
	flags := append(append([]string{}, guardFlags...), "--max-turns", rs8MaxTurns)
	watch := func(stop func()) func() {
		return func() {
			s.checkStream(stop)
			s.checkedStream(stop)
		}
	}
	send := func(lane string) {
		s.cli("message", "send", lane, "--id", rs8Message(lane), "--body", rs8Body)
	}

	s.playStandIn([]any{standInBrief(rs8Question), standInAnswers(rs8HeadlessLane)}, nil)
	first := s.startDaemonWith(map[string]any{"args": flags, "stream_log": s.stream})
	t.Cleanup(first.stop)
	id := s.waitForWorkOrder(first.stop)
	s.atRest(first, id, rs8HeadlessLane, watch(first.stop))
	send(rs8HeadlessLane)
	s.awaitMessage(first, rs8Message(rs8HeadlessLane), messages.Replied, watch(first.stop))
	s.awaitState(first, id, rs8HeadlessLane, workorders.Acceptance, 1, watch(first.stop))
	first.stop()
	s.reportReply("headless", rs8HeadlessLane, true)

	s.repo = pane.Dir
	s.playStandIn([]any{standInBrief(rs8Question), standInAnswers(rs8PaneLane)}, nil)
	second := s.startDaemonWith(map[string]any{"args": paneGuardFlags, "env": paneEnv})
	t.Cleanup(second.stop)
	paneID := s.laneOrder(second.stop, []string{id})
	screens := &screenLog{}
	paneWatch := func() {
		s.watchPane(second.stop, rs8PaneLane+"-g1", screens)
		if tools := eventCount(s.hookRecords(rs8PaneLane), "PreToolUse"); tools > rs8ToolLimit {
			s.stopWith(second.stop, "%d tool calls passed the limit of %d", tools, rs8ToolLimit)
		}
	}
	s.atRest(second, paneID, rs8PaneLane, paneWatch)
	send(rs8PaneLane)
	s.awaitMessage(second, rs8Message(rs8PaneLane), messages.Replied, paneWatch)
	s.awaitState(second, paneID, rs8PaneLane, workorders.Acceptance, 1, paneWatch)
	paneLeft := s.settleProcesses(rs8PaneLane)
	second.stop()
	s.reportReply("pane", rs8PaneLane, false)
	t.Logf("rs8: pane: screens=%v processes_left=%d", screens.names(), paneLeft)
	if paneLeft != 0 {
		t.Error("pane: a process of the session remains")
	}

	s.repo = refusing.Dir
	s.playStandIn([]any{standInBrief(rs8Question), standInAnswers(rs8RefusedLane)}, nil)
	third := s.startDaemonWith(map[string]any{"args": flags, "stream_log": s.stream, "inbound": "refuse"})
	t.Cleanup(third.stop)
	refusedID := s.laneOrder(third.stop, []string{id, paneID})
	s.atRest(third, refusedID, rs8RefusedLane, watch(third.stop))
	send(rs8RefusedLane)
	row := s.awaitMessage(third, rs8Message(rs8RefusedLane), messages.Refused, watch(third.stop))
	third.stop()
	s.reportRefused(row, version)
}

// reportRefused holds a receipt to what it proves: a message the session never took, read as
// refused only because a receipt reached a reply address.
func (s *suite) reportRefused(row messages.Message, version string) {
	s.t.Helper()
	states := s.messageStates(row.ID)
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		s.t.Fatal(err)
	}
	replayed, _ := replayOf(raw, row.ID)
	prompts := eventCount(s.hookRecords(rs8RefusedLane), "UserPromptSubmit")
	s.t.Logf("rs8: refused: version=%s states=%v prompts=%d replay=%v", version, states, prompts, replayed)
	want := []messages.State{messages.Queued, messages.AtWorker, messages.HandedOff, messages.Refused}
	if !slices.Equal(states, want) {
		s.t.Errorf("a refused message walked %v, want %v", states, want)
	}
	if replayed {
		s.t.Error("a refused message came back as a replay")
	}
	if prompts > 1 {
		s.t.Error(fmt.Sprintf("%d prompts on record, and a refused message is none of them", prompts))
	}
}

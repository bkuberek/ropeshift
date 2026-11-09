//go:build realsession

package real

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/gittest"
)

const (
	rs6HeadlessLane = "rs6-headless"
	rs6PaneLane     = "rs6-pane"
	// rs6Scope is the scope of the solo daemon's ledger.
	rs6Scope = ledger.WorkspaceID("default")
	// rs6MaxTurns caps each input of a headless lane, and a lane takes two: its brief and the
	// instruction that asks for its narrative. rs6Charge is what that can reach, and what a pane
	// is charged too, because a pane's tool limit holds it to the same.
	rs6MaxTurns = "3"
	rs6Charge   = 6
	// rs6ToolLimit ends a pane that keeps calling tools past that charge.
	rs6ToolLimit = 4
	// rs6Instructions say what a lane does with a question nobody will answer here.
	rs6Instructions = "Change no file. Call the ask tool once, with blocking set to true, and this exact question: " +
		"what should this lane do next. Then end your turn with the single word waiting, and call no other tool. " +
		"If a later message asks you to hand off, do exactly what it says."
)

func rs6Profile(mode string) string {
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

func rs6Contract(id string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "Park on a question"

[[criteria]]
id = "c1"
text = "The lane parks with a narrative"

[[work_orders]]
id = "` + id + `-order"
title = "Park on a question"
size = "small"
+++

This work order tests how a lane parks, and it has no other work.

` + rs6Instructions + `
`
}

// parkedLane is what one lane left in the ledger, with no text of a session's in it.
type parkedLane struct {
	wake       parking.Wake
	checkpoint ledger.Checkpoint
	handoff    lanes.Handoff
	degraded   bool
	generation ledger.Generation
}

// readParked reads the ledger behind a stopped daemon, because a handoff has no route yet.
func (s *suite) readParked(lane string, generation int64) parkedLane {
	s.t.Helper()
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer store.Close()
	var out parkedLane
	err = store.Read(s.t.Context(), rs6Scope, func(tx ports.ReadTx) error {
		var err error
		if out.wake, err = tx.Wakes().Get(lanes.WakeID(lane, generation)); err != nil {
			return err
		}
		if out.checkpoint, err = tx.Lanes().Checkpoint(lane, generation); err != nil {
			return err
		}
		record, err := tx.Lanes().Handoff(lane, generation)
		if err != nil {
			return err
		}
		out.degraded = record.Degraded
		if out.handoff, err = lanes.ReadHandoff(record.Body); err != nil {
			return err
		}
		generations, err := tx.Lanes().Generations(lane)
		if err != nil {
			return err
		}
		for _, candidate := range generations {
			if candidate.Number == generation {
				out.generation = candidate
			}
		}
		return nil
	})
	if err != nil {
		s.t.Fatalf("the ledger holds no parked lane: %v", err)
	}
	return out
}

// awaitParked starts one lane and waits for its work order to park.
func (s *suite) awaitParked(d *daemon, id, lane string, watch func()) workorders.State {
	s.t.Helper()
	var workers api.WorkerList
	if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
		s.t.Fatal(err)
	}
	s.cli("work", "review", id)
	s.cli("work", "claim", id, "--worker", workers.Workers[0].ID)
	s.cli("lane", "start", id, "--lane", lane)
	settled := map[workorders.State]bool{
		workorders.Parked: true, workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true,
	}
	var row workorders.WorkOrder
	s.poll(d.stop, "lane "+lane+" to park", func() bool {
		watch()
		row = s.workOrder(id)
		return settled[row.State]
	})
	return row.State
}

// laneProcesses counts what still names a lane's generated files, which every process of its
// session does, and the shim that held it.
func (s *suite) laneProcesses(lane string) int {
	s.t.Helper()
	held, err := processesHolding(filepath.Join(s.paths.StateDir, "lanes", lane))
	if err != nil {
		s.t.Fatal(err)
	}
	return len(held)
}

// checkParked names what a parked lane failed to show.
func (s *suite) checkParked(lane string, state workorders.State, left int) []string {
	s.t.Helper()
	var failures []string
	fail := func(on bool, text string) {
		if on {
			failures = append(failures, text)
		}
	}
	fail(state != workorders.Parked, "the work order is "+string(state))
	fail(left != 0, "a process of the session remains")
	if state != workorders.Parked {
		return failures
	}
	parked := s.readParked(lane, 1)
	fail(!parked.wake.Open(), "the wake closed with no answer")
	fail(parked.degraded || parked.handoff.Narrative == nil, "the session wrote no narrative")
	fail(parked.generation.EndReason != workorders.EndParked, "the generation ended "+parked.generation.EndReason)
	fail(parked.handoff.Reason != string(lanes.ParkBlockingQuestion), "the document says "+parked.handoff.Reason)
	return append(failures, s.sequenceGaps(lane, 1)...)
}

// sequenceGaps reads the worker's own record of a parking, because a narrative alone does not
// show it was asked for, and a process that is gone does not show that the worker stopped it.
func (s *suite) sequenceGaps(lane string, generation int64) []string {
	s.t.Helper()
	journal, err := sqlite.OpenJournal(filepath.Join(s.paths.StateDir, api.JournalFileName))
	if err != nil {
		s.t.Fatal(err)
	}
	defer journal.Close()
	effects, err := journal.Effects(s.t.Context())
	if err != nil {
		s.t.Fatal(err)
	}
	return parkingGaps(effects, lane, generation)
}

// parkingGaps names what an effect log lacks of one generation's parking.
func parkingGaps(effects []lanes.Effect, lane string, generation int64) []string {
	asked, stopped := false, false
	for _, effect := range effects {
		if effect.Lane != lane || effect.Generation != generation || effect.State != lanes.EffectCompleted {
			continue
		}
		asked = asked || (effect.Kind == lanes.EffectInput && strings.HasSuffix(effect.Key, "/handoff"))
		stopped = stopped || effect.Kind == lanes.EffectStop
	}
	var gaps []string
	if !asked {
		gaps = append(gaps, fmt.Sprintf("generation %d was never asked for its narrative", generation))
	}
	if !stopped {
		gaps = append(gaps, fmt.Sprintf("the worker has no stop of generation %d on record", generation))
	}
	return gaps
}

func (s *suite) reportParked(mode, lane string, state workorders.State, left int) {
	s.t.Helper()
	if state != workorders.Parked {
		s.t.Logf("rs6: %s: state=%s", mode, state)
		return
	}
	parked := s.readParked(lane, 1)
	carries := parked.handoff.Narrative != nil && parked.handoff.Narrative.CarriesNextStep
	s.t.Logf("rs6: %s: state=%s wake_open=%v narrative=%v carries_next_step=%v generation_end=%s pushed=%v processes_left=%d",
		mode, state, parked.wake.Open(), parked.handoff.Narrative != nil, carries, parked.generation.EndReason, parked.checkpoint.Pushed, left)
}

// settleProcesses gives a stopped session's shim its linger, and counts what stays.
func (s *suite) settleProcesses(lane string) int {
	s.t.Helper()
	for deadline := time.Now().Add(rs5EndLimit); time.Now().Before(deadline); time.Sleep(rs5EndPoll) {
		if s.laneProcesses(lane) == 0 {
			return 0
		}
	}
	return s.laneProcesses(lane)
}

// TestRS6 parks a real session in each run mode. Only a real session can show a narrative
// request answered through `lane_handoff`, a stop that SIGINT ends, and no process left.
func TestRS6(t *testing.T) {
	s := start(t, "rs6")
	s.watched = []string{rs6HeadlessLane, rs6PaneLane}
	t.Cleanup(s.killTerminal)
	s.reapLanes(rs6HeadlessLane)
	s.build()
	s.probe()

	headless := gittest.NewIn(t, s.subdirectory("h"))
	headless.Commit(".ropeshift/profile.toml", rs6Profile(lanes.ProfileRunHeadless))
	headless.Commit("contract.md", rs6Contract("rs6h"))
	pane := gittest.NewIn(t, s.subdirectory("p"))
	pane.Commit(".ropeshift/profile.toml", rs6Profile(lanes.ProfileRunTmux))
	pane.Commit("contract.md", rs6Contract("rs6p"))

	s.repo = headless.Dir
	s.worktree = filepath.Join(filepath.Dir(headless.Dir), filepath.Base(headless.Dir)+"-lanes", rs6HeadlessLane)
	paneWorktree := filepath.Join(filepath.Dir(pane.Dir), filepath.Base(pane.Dir)+"-lanes", rs6PaneLane)
	s.others = []string{headless.Dir, s.worktree, pane.Dir, paneWorktree}
	for _, lane := range []string{"headless", "pane"} {
		if err := s.spend.charge(lane, rs6Charge); err != nil {
			t.Fatal(err)
		}
	}

	first := s.startDaemonWith(map[string]any{
		"args": append(append([]string{}, guardFlags...), "--max-turns", rs6MaxTurns), "stream_log": s.stream,
	})
	t.Cleanup(first.stop)
	id := s.waitForWorkOrder(first.stop)
	s.poll(first.stop, "the worker's session", func() bool {
		var workers api.WorkerList
		if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
			t.Fatal(err)
		}
		return len(workers.Workers) == 1 && workers.Workers[0].Epoch >= 1
	})
	state := s.awaitParked(first, id, rs6HeadlessLane, func() {
		s.checkStream(first.stop)
		s.checkedStream(first.stop)
	})
	left := s.settleProcesses(rs6HeadlessLane)
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
	s.reportParked("headless", rs6HeadlessLane, state, left)
	for _, failure := range s.checkParked(rs6HeadlessLane, state, left) {
		t.Error("headless: " + failure)
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
	state = s.awaitParked(second, paneID, rs6PaneLane, func() {
		s.watchPane(second.stop, rs6PaneLane+"-g1", screens)
		if tools := eventCount(s.hookRecords(rs6PaneLane), "PreToolUse"); tools > rs6ToolLimit {
			s.stopWith(second.stop, "%d tool calls passed the limit of %d", tools, rs6ToolLimit)
		}
	})
	left = s.settleProcesses(rs6PaneLane)
	shown := s.capture(rs6PaneLane + "-g1")
	second.stop()
	s.reportParked("pane", rs6PaneLane, state, left)
	t.Logf("rs6: pane: screens=%v pane_left=%v", screens.names(), shown != "")
	for _, failure := range s.checkParked(rs6PaneLane, state, left) {
		t.Error("pane: " + failure)
	}
	if shown != "" {
		t.Error("pane: the terminal still shows the session")
	}
}

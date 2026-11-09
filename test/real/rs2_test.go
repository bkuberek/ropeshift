//go:build realsession

package real

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/shimlink"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/gittest"
)

const (
	rs2Lane = "rs2-lane"
	// rs2Turns charges the worst case, because a session may run to its limit on each of
	// its two inputs, and the ceiling refuses a run before it spends.
	rs2Turns    = 12
	rs2Contract = `+++
kind = "contract"
id = "rs2"
title = "Survive a worker kill"

[[criteria]]
id = "c1"
text = "The lane reports its outcome after its worker restarts"

[[work_orders]]
id = "rs2-order"
title = "Survive a worker kill"
size = "small"
+++

This work order tests that a session outlives its worker, and it has no other work.
Change no file. Your first reply is the single word ready, and it calls no tool. When
your next message arrives, call the lane_report tool once, with outcome completed.
Then end your turn with the single word done.
`
)

func exists(pid int) bool {
	return pid > 0 && !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// shimProcesses are what this suite may signal if it must stop, by exact ID.
type shimProcesses struct {
	suite *suite
	state shimlink.State
}

// stop ends a session the suite started, after its daemon is gone: an interrupt, then the
// end of the shim that holds it. It never signals by name.
func (p *shimProcesses) stop() {
	if exists(p.state.ChildPID) {
		_ = syscall.Kill(p.state.ChildPID, syscall.SIGINT)
		for deadline := time.Now().Add(10 * time.Second); exists(p.state.ChildPID) && time.Now().Before(deadline); {
			time.Sleep(50 * time.Millisecond)
		}
		if exists(p.state.ChildPID) {
			_ = syscall.Kill(p.state.ChildPID, syscall.SIGKILL)
		}
	}
	if exists(p.state.ShimPID) {
		_ = syscall.Kill(p.state.ShimPID, syscall.SIGKILL)
	}
}

func (s *suite) shimDir() string {
	return filepath.Join(s.paths.StateDir, "lanes", rs2Lane, "shim-1")
}

// journalLines reads what the session wrote, from the journal no worker needs, so the
// stop conditions hold while the worker is dead.
func (s *suite) journalLines() []byte {
	s.t.Helper()
	records, err := shimlink.ReadJournal(s.shimDir(), 0)
	if err != nil {
		s.t.Fatal(err)
	}
	var out bytes.Buffer
	for _, record := range records {
		if record.Line != "" {
			out.WriteString(record.Line + "\n")
		}
	}
	return out.Bytes()
}

func (s *suite) checkJournal(stop func()) {
	s.t.Helper()
	if signals := stopSignals(s.journalLines()); len(signals) != 0 {
		s.stopWith(stop, "%v", signals)
	}
}

// TestRS2 runs a real session under the production shim through the one thing only a real
// session can show: the worker dies by SIGKILL mid-turn, the session finishes that turn
// unattended, and a new worker reattaches and reads the lane to its outcome through
// `ropeshift mcp`, on a socket the dead worker never saw.
func TestRS2(t *testing.T) {
	s := start(t, "rs2")
	s.build()
	s.probe()

	repo := gittest.NewIn(t, s.scratch)
	repo.Commit(".ropeshift/profile.toml", profileText)
	repo.Commit("contract.md", rs2Contract)
	s.repo = repo.Dir
	s.worktree = filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", rs2Lane)
	if err := s.spend.charge("lane", rs2Turns); err != nil {
		t.Fatal(err)
	}

	first := s.startDaemon()
	t.Cleanup(first.stop)
	id := s.waitForWorkOrder(first.stop)
	s.cli("work", "review", id)
	var workers api.WorkerList
	s.poll(first.stop, "the worker's session", func() bool {
		if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
			t.Fatal(err)
		}
		return len(workers.Workers) == 1 && workers.Workers[0].Epoch >= 1
	})
	s.cli("work", "claim", id, "--worker", workers.Workers[0].ID)
	s.cli("lane", "start", id, "--lane", rs2Lane)

	processes := &shimProcesses{suite: s}
	t.Cleanup(processes.stop)
	s.poll(first.stop, "the session's system/init", func() bool {
		s.checkStream(first.stop)
		raw, err := os.ReadFile(s.stream)
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		_, err = firstInit(raw)
		return err == nil
	})
	state, err := shimlink.ReadState(s.shimDir())
	if err != nil {
		t.Fatal(err)
	}
	processes.state = state
	init, err := firstInit(s.journalLines())
	if err != nil {
		t.Fatal(err)
	}
	if err := checkInit(init, []string{"ropeshift"}, laneTools); err != nil {
		s.stopWith(first.stop, "the lane's init: %v", err)
	}

	finishedBefore := bytes.Count(s.journalLines(), []byte(`"type":"result"`))
	first.kill()
	killed := time.Now()
	if !exists(state.ShimPID) || !exists(state.ChildPID) {
		t.Fatal("STOP: the session died with its worker")
	}
	s.poll(processes.stop, "the turn's end with no worker", func() bool {
		s.checkJournal(processes.stop)
		return bytes.Count(s.journalLines(), []byte(`"type":"result"`)) > finishedBefore || finishedBefore > 0
	})
	gap := time.Since(killed)
	if !exists(state.ShimPID) || !exists(state.ChildPID) {
		t.Fatal("STOP: the session ended with no worker, before anything asked it to")
	}
	records := len(bytes.Split(bytes.TrimSpace(s.journalLines()), []byte("\n")))
	t.Logf("rs2: survived: mid_turn_at_kill=%v gap_ms=%d lines=%d shim_alive=%v session_alive=%v",
		finishedBefore == 0, gap.Milliseconds(), records, exists(state.ShimPID), exists(state.ChildPID))

	second := s.startDaemon()
	t.Cleanup(second.stop)
	stopBoth := func() {
		second.stop()
		processes.stop()
	}
	settled := map[workorders.State]bool{
		workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true,
	}
	var row workorders.WorkOrder
	s.poll(stopBoth, "the lane's outcome after the reattach", func() bool {
		s.checkJournal(stopBoth)
		out, err := s.tryCLI("work", "show", id, "--json")
		if err != nil {
			return false
		}
		if err := json.Unmarshal(out, &row); err != nil {
			t.Fatal(err)
		}
		return settled[row.State]
	})
	s.poll(stopBoth, "the shim's end", func() bool { return !exists(state.ShimPID) && !exists(state.ChildPID) })
	second.stop()

	raw := s.journalLines()
	t.Logf("rs2: reattached: lines_total=%d results=%d session_ids=%d lane_state=%s",
		bytes.Count(raw, []byte("\n")), bytes.Count(raw, []byte(`"type":"result"`)), distinctSessions(raw), row.State)
	if row.State != workorders.Acceptance {
		t.Fatalf("the lane ended in %s\n%s", row.State, second.stderr.String())
	}
	s.report(id, init, raw)
}

// distinctSessions counts session IDs and keeps none, because evidence holds no ID.
func distinctSessions(raw []byte) int {
	seen := map[string]bool{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		var event struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.SessionID != "" {
			seen[event.SessionID] = true
		}
	}
	return len(seen)
}

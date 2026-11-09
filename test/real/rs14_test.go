//go:build realsession

package real

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/gittest"
)

const (
	rs14Lane = "rs14-lane"
	// rs14Sessions is the guard's probe, the qualification probe's three headless sessions and its
	// pane, and one lane.
	rs14Sessions    = 6
	rs14TurnCeiling = 40
	rs14MaxTurns    = "3"
	rs14Profile     = `run_mode = "headless"

[agent]
model = "` + sessionModel + `"
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
max_restarts = 0
`
	rs14Contract = `+++
kind = "contract"
id = "rs14"
title = "Run on a qualified version"

[[criteria]]
id = "c1"
text = "The lane reports its outcome"

[[work_orders]]
id = "rs14-order"
title = "Run on a qualified version"
size = "small"
+++

This work order runs once the worker has qualified the installed CLI, and it has no other work.
Change no file, read no file, and run no command. Call the lane_report tool once, with outcome
completed. Then end your turn with the single word done.
`
)

// rs14Charges are what each session may spend, in the order they run. The probe's accept session can
// take a second turn when its socket message is never taken, its refuse session one when the socket
// answers nothing before a first turn, and its hold session one before its message. Its pane takes
// none.
var rs14Charges = []struct {
	session string
	turns   int
}{{"probe-accept", 2}, {"probe-refuse", 1}, {"probe-hold", 1}, {"probe-screens", 0}, {"lane", 3}}

// rs14Wait bounds the lane's outcome: the probe's own waits, then the lane's turns.
const rs14Wait = 2*turnLimit + 5*time.Minute

// rs14Row is the matrix row a run leaves: versions, surfaces, and results, and nothing else.
type rs14Row struct {
	Version string                              `json:"version"`
	Checks  map[lanes.Surface]lanes.CheckResult `json:"checks"`
}

// ledgerEvents reads every event a stopped daemon left.
func (s *suite) ledgerEvents() []ledger.Event {
	s.t.Helper()
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer store.Close()
	var events []ledger.Event
	err = store.Read(context.Background(), "default", func(tx ports.ReadTx) error {
		var err error
		events, err = control.ReadAllEvents(tx.Events)
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return events
}

// TestRS14 qualifies the installed version from what it does, through the production probe on a real
// upgrade, in both run modes. The daemon starts on a version its journal has never probed, so its
// first lane waits while the probe runs every check of the qualification matrix on this machine, and
// the lane then runs on what the probe found. The run ends with that version's matrix row.
func TestRS14(t *testing.T) {
	s := start(t, "rs14")
	s.watched = []string{rs14Lane}
	s.spend.sessionCeiling, s.spend.turnCeiling = rs14Sessions, rs14TurnCeiling
	t.Cleanup(s.killTerminal)
	s.reapLanes(rs14Lane)
	s.build()
	s.probe()

	repo := gittest.NewIn(t, s.scratch)
	repo.Commit(".ropeshift/profile.toml", rs14Profile)
	repo.Commit("contract.md", rs14Contract)
	s.repo = repo.Dir
	s.worktree = filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", rs14Lane)
	probeDir := filepath.Join(s.paths.StateDir, "probe")
	for _, name := range []string{"accept", "refuse", "hold", "screens"} {
		s.others = append(s.others, filepath.Join(probeDir, name))
	}
	for _, charge := range rs14Charges {
		if err := s.spend.charge(charge.session, charge.turns); err != nil {
			t.Fatal(err)
		}
	}
	s.playStandIn([]any{standInTelemetryReport("done", false)}, nil)

	d := s.startDaemonWith(map[string]any{
		"args": append(append([]string{}, guardFlags...), "--max-turns", rs14MaxTurns), "pane_args": paneGuardFlags,
		"env": paneEnv, "stream_log": s.stream,
	})
	t.Cleanup(d.stop)
	id := s.dispatch(d, rs14Lane)
	settled := map[workorders.State]bool{workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true}
	screens := &screenLog{}
	s.pollFor(rs14Wait, d.stop, "the lane's outcome", func() bool {
		s.checkStream(d.stop)
		s.watchPane(d.stop, "probe-screens", screens)
		return settled[s.workOrder(id).State]
	})
	s.checkStream(d.stop)
	state := s.workOrder(id).State
	left := s.settleProcesses(rs14Lane)
	d.stop()

	raw, err := os.ReadFile(s.stream)
	if err != nil {
		t.Fatal(err)
	}
	inits := 0
	for _, init := range everyInit(raw) {
		inits++
		servers := []string{}
		if len(init.Servers) != 0 {
			servers = []string{"ropeshift"}
		}
		if err := checkInit(init, servers, laneTools); err != nil {
			t.Fatalf("STOP: an init: %v", err)
		}
	}
	events := s.ledgerEvents()
	replayed, err := lanes.ReplayQualifications(events)
	if err != nil {
		t.Fatal(err)
	}
	var q lanes.Qualification
	var qualifiedAt, boundAt int64
	for _, event := range events {
		switch {
		case event.Type == ledger.EventDriverQualified && qualifiedAt == 0:
			qualifiedAt = event.OccurredAt
		case event.Type == workorders.EventGenerationBound && boundAt == 0:
			boundAt = event.OccurredAt
		}
	}
	for _, found := range replayed {
		q = found
	}
	row, err := json.Marshal(rs14Row{Version: q.Version, Checks: q.Checks})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rs14: version=%s source=%s sign_in=%s degraded=%v inits=%d lane=%s processes_left=%d screens=%v",
		q.Version, q.Source, q.SignIn, q.Degraded, inits, state, left, screens.names())
	t.Logf("rs14: row %s", row)

	if len(replayed) != 1 || q.Source != lanes.QualifiedByProbe || q.Driver != claudecode.DriverName {
		t.Fatalf("the ledger replays to %+v", replayed)
	}
	for _, surface := range claudecode.Surfaces {
		if _, ok := q.Checks[surface]; !ok {
			t.Errorf("no result for %s", surface)
		}
	}
	if qualifiedAt == 0 || boundAt == 0 || qualifiedAt > boundAt {
		t.Errorf("the probe's result at %d, and the lane's session at %d: dispatch did not wait for it", qualifiedAt, boundAt)
	}
	if state != workorders.Acceptance {
		t.Errorf("the lane ended in %s\n%s", state, d.reported())
	}
	if left != 0 {
		t.Errorf("%d processes of the lane remain", left)
	}
}

// everyInit reads each session's init from a stream that holds several sessions.
func everyInit(stream []byte) []initLine {
	var out []initLine
	for _, line := range splitLines(stream) {
		var init initLine
		if json.Unmarshal(line, &init) == nil && init.Type == "system" && init.Subtype == "init" {
			out = append(out, init)
		}
	}
	return out
}

func splitLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			out = append(out, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, raw[start:])
	}
	return out
}

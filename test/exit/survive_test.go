package exit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

func exists(pid int) bool { return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

// TestLaneSurvivesAWorkerKill is FR-LANE-17 on real processes: the daemon dies by
// SIGKILL mid-turn, the session finishes that turn with nobody watching, a new daemon
// adopts it, and the lane reports through the new worker's socket. RS2 walks this with
// a real session.
func TestLaneSurvivesAWorkerKill(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-survives")
	flags := []string{"--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir}
	first := in.start(flags...)
	in.waitReady(first)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	config := filepath.Join(in.paths().StateDir, "lanes", "lane-1")
	doubles.ReapShims(t, config)
	shimDir := filepath.Join(config, "shim-1")
	var state shimlink.State
	in.waitFor("the session's start", func() bool {
		var err error
		state, err = shimlink.ReadState(shimDir)
		return err == nil && state.ChildPID != 0
	})
	in.waitFor("the generation's binding", func() bool {
		return slices.Contains(eventTypes(in.events(t, id)), workorders.EventGenerationBound)
	})

	first.kill()
	if !exists(state.ShimPID) || !exists(state.ChildPID) {
		t.Fatal("the session died with its worker")
	}

	if err := os.WriteFile(filepath.Join(doubles.BinDir(in.path), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	in.waitFor("the turn's end in the journal, with no worker", func() bool {
		records, err := shimlink.ReadJournal(shimDir, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if strings.Contains(record.Line, `"type":"result"`) {
				return true
			}
		}
		return false
	})

	second := in.start(flags...)
	in.waitReady(second)
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	in.waitFor("the shim's end", func() bool { return !exists(state.ShimPID) && !exists(state.ChildPID) })
	second.kill()

	starts, listings := 0, 0
	for _, seen := range doubles.SeenBy(t, doubles.BinDir(in.path)) {
		switch {
		case len(seen.Args) != 0 && seen.Args[0] == "agents":
			listings++
		default:
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("%d sessions started for one lane", starts)
	}
	if listings != 1 {
		t.Errorf("%d reads of the listing", listings)
	}
	events := eventTypes(in.events(t, id))
	for _, once := range []string{
		workorders.EventType(workorders.TriggerLaneStarted), workorders.EventGenerationBound,
		workorders.EventType(workorders.TriggerLaneCompleted),
	} {
		found := 0
		for _, event := range events {
			if event == once {
				found++
			}
		}
		if found != 1 {
			t.Errorf("%d %s events in %v", found, once, events)
		}
	}
}

// TestLaneReadCarriesFreshness is FR-LANE-6 and FR-UI-5 on the real binary: a client reads
// a lane's state with its freshness, and the ledger's own record of its end. In solo mode
// nothing answers while the daemon is down, so the contract suite proves `last_reported`.
func TestLaneReadCarriesFreshness(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-survives")
	first := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(first)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", "lane-1"))
	read := func() ledger.LaneView {
		var view ledger.LaneView
		code, stdout, _ := in.run("lane", "show", "lane-1", "--json")
		if code != 0 {
			return view
		}
		if err := json.Unmarshal([]byte(stdout), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	in.waitFor("a turn in progress, observed", func() bool {
		view := read()
		return view.ActualState == "working" && view.Freshness == ledger.FreshnessObserved && view.AsOf != 0 && view.ReportedByEpoch >= 1
	})
	if text := in.must("lane", "list"); !strings.Contains(text, "lane-1\tworking") || !strings.Contains(text, ledger.FreshnessObserved) {
		t.Errorf("lane list: %q", text)
	}
	release := filepath.Join(doubles.BinDir(in.path), "release")
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	view := read()
	if view.ActualState != ledger.StateEnded || view.Freshness != ledger.FreshnessObserved {
		t.Errorf("an ended lane reads %+v", view)
	}
	code, _, _ := in.run("lane", "show", "no-such-lane")
	if code == 0 {
		t.Error("a lane that does not exist answered")
	}
}

// paneProfile runs a lane in a pane.
const paneProfile = "run_mode = \"tmux\"\n"

// TestPaneLaneSurvivesAWorkerKill is FR-LANE-17 in the interactive run mode: the daemon dies
// by SIGKILL mid-turn, the pane stays, the turn ends with nobody listening, and a new daemon
// reads that end from the spool, nudges the session on its socket, and takes its report.
func TestPaneLaneSurvivesAWorkerKill(t *testing.T) {
	doubles.RequireTmux(t)
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", paneProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "pane-survives")
	terminal := doubles.TmuxOn(t, in.paths().TerminalSocket())
	flags := []string{"--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir}
	first := in.start(flags...)
	in.waitReady(first)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	in.waitFor("the generation's binding", func() bool {
		return slices.Contains(eventTypes(in.events(t, id)), workorders.EventGenerationBound)
	})
	const name = "lane-1-g1"
	pane, err := terminal.Pane(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}

	first.kill()
	after, err := terminal.Pane(t.Context(), name)
	if err != nil {
		t.Fatalf("the pane went with its worker: %v", err)
	}
	if after.Dead || after.PID != pane.PID || !exists(pane.PID) {
		t.Fatalf("the session did not survive: %+v", after)
	}

	if err := os.WriteFile(filepath.Join(doubles.BinDir(in.path), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	in.waitFor("the turn's end in the spool, with no worker", func() bool {
		raw, err := os.ReadFile(in.paths().Spool())
		return err == nil && strings.Contains(string(raw), `"event":"Stop"`)
	})

	second := in.start(flags...)
	in.waitReady(second)
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	in.waitFor("the pane's end", func() bool { return !exists(pane.PID) })
	second.kill()

	starts, listings := 0, 0
	for _, seen := range doubles.SeenBy(t, doubles.BinDir(in.path)) {
		switch {
		case len(seen.Args) != 0 && seen.Args[0] == "agents":
			listings++
		default:
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("%d sessions started for one lane", starts)
	}
	if listings != 1 {
		t.Errorf("%d reads of the listing", listings)
	}
}

// TestDenialWithWorkerDown is FR-AUTH-8 on real processes: the policy a generation compiled
// stops a force push, a push to the default branch, and writes outside the worktree while
// no worker is running, and a call the policy allows still runs.
func TestDenialWithWorkerDown(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "policy-denials")
	flags := []string{"--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir}
	daemon := in.start(flags...)
	in.waitReady(daemon)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	config := filepath.Join(in.paths().StateDir, "lanes", "lane-1")
	doubles.ReapShims(t, config)
	in.waitFor("the generation's binding", func() bool {
		return slices.Contains(eventTypes(in.events(t, id)), workorders.EventGenerationBound)
	})
	daemon.kill()

	bin := doubles.BinDir(in.path)
	if err := os.WriteFile(filepath.Join(bin, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	denied := filepath.Join(bin, "denied.log")
	in.waitFor("the session's last call", func() bool {
		_, err := os.Stat(filepath.Join(bin, "ran.log"))
		return err == nil
	})
	raw, err := os.ReadFile(denied)
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{lanes.ReasonForcePush, lanes.ReasonDefaultBranch, lanes.ReasonOutside} {
		if !strings.Contains(string(raw), reason) {
			t.Errorf("no denial for %q in %s", reason, raw)
		}
	}
	if count := strings.Count(string(raw), lanes.ReasonOutside); count != 2 {
		t.Errorf("%d writes outside were denied, of 2", count)
	}
	if _, err := os.Stat("/tmp/rs-denied-by-policy"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a denied write ran: %v", err)
	}
	if ran, err := os.ReadFile(filepath.Join(bin, "ran.log")); err != nil || strings.Count(string(ran), "\n") != 1 {
		t.Errorf("the allowed call: %q, %v", ran, err)
	}
}

// restartProfile restarts a crashed session at once, so a test need not wait.
const restartProfile = "[lifecycle]\nrestart_backoff = \"10ms\"\n"

// TestAWorkerRestartsASessionAfterItsShimDies is FR-LANE-12 on real processes: SIGKILL to
// the shim ends its session, because its stdin closes, and the worker that reads that end
// resumes the same session as a new generation, which reports the lane's outcome.
func TestAWorkerRestartsASessionAfterItsShimDies(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", restartProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "shim-dies")
	daemon := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(daemon)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	config := filepath.Join(in.paths().StateDir, "lanes", "lane-1")
	doubles.ReapShims(t, config)
	var state shimlink.State
	in.waitFor("the session's start", func() bool {
		var err error
		state, err = shimlink.ReadState(filepath.Join(config, "shim-1"))
		return err == nil && state.ChildPID != 0
	})
	in.waitFor("the generation's binding", func() bool {
		return slices.Contains(eventTypes(in.events(t, id)), workorders.EventGenerationBound)
	})

	if err := syscall.Kill(state.ShimPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(doubles.BinDir(in.path), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	daemon.kill()

	var sessions [][]string
	for _, seen := range doubles.SeenBy(t, doubles.BinDir(in.path)) {
		if len(seen.Args) != 0 && seen.Args[0] == "-p" {
			sessions = append(sessions, seen.Args)
		}
	}
	if len(sessions) != 2 {
		t.Fatalf("%d sessions, want the first and its resume", len(sessions))
	}
	resumed := 0
	for _, args := range sessions {
		if slices.Contains(args, "--resume") {
			resumed++
		}
	}
	if resumed != 1 {
		t.Errorf("%d resumes", resumed)
	}
	events := eventTypes(in.events(t, id))
	if !slices.Contains(events, workorders.EventLaneRestarted) {
		t.Errorf("no restart on record: %v", events)
	}
}

// TestAWorkerRestartsAPaneThatDied is the same promise in the other run mode: the pane's
// process dies by SIGKILL, and the next generation resumes in a new pane, through the
// checked start.
func TestAWorkerRestartsAPaneThatDied(t *testing.T) {
	doubles.RequireTmux(t)
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", paneProfile+restartProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "pane-dies")
	terminal := doubles.TmuxOn(t, in.paths().TerminalSocket())
	daemon := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(daemon)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	in.waitFor("the generation's binding", func() bool {
		return slices.Contains(eventTypes(in.events(t, id)), workorders.EventGenerationBound)
	})
	if err := terminal.Signal(t.Context(), "lane-1-g1", ports.SignalKill); err != nil {
		t.Fatal(err)
	}
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	daemon.kill()
	if events := eventTypes(in.events(t, id)); !slices.Contains(events, workorders.EventLaneRestarted) {
		t.Errorf("no restart on record: %v", events)
	}
}

// TestALaneResetReachesTheCLI holds the one command a tripped lane waits for.
func TestALaneResetReachesTheCLI(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-completed")
	daemon := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(daemon)
	if code, _, _ := in.run("lane", "reset", "no-such-work-order"); code == 0 {
		t.Error("a reset of nothing answered")
	}
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", "lane-1"))
	in.waitFor("the lane on record", func() bool {
		code, _, _ := in.run("lane", "show", "lane-1")
		return code == 0
	})
	if text := in.must("lane", "reset", id); !strings.Contains(text, "reset the lane of "+id) {
		t.Errorf("reset said %q", text)
	}
}

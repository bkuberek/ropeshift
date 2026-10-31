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
	"ropeshift.dev/ropeshift/internal/ledger"
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

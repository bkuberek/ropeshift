package worker_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/workorders"
)

func (r *laneRig) setFleet(mode ledger.FleetMode) {
	r.t.Helper()
	if err := control.NewFleet(r.store).SetMode(r.t.Context(), scope, mode, member); err != nil {
		r.t.Fatal(err)
	}
	if err := r.subject.Sync(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
}

// heldNotes lists what the worker told the ledger about a lane it would not start.
func (r *laneRig) heldNotes() []string {
	r.t.Helper()
	if err := r.subject.Upload(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	var notes []string
	for _, event := range r.events() {
		if event.Type == workorders.EventAdmissionHeld {
			var change workorders.Change
			if err := json.Unmarshal(event.Data, &change); err != nil {
				r.t.Fatal(err)
			}
			notes = append(notes, string(change.Payload))
		}
	}
	return notes
}

func TestPauseHoldsANewLaneUntilTheFleetRuns(t *testing.T) {
	r := laneRigWith(t, "")
	r.setFleet(ledger.FleetPause)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.driver.Specs()) != 0 {
		t.Fatal("a paused fleet started a lane")
	}
	if notes := r.heldNotes(); len(notes) != 1 {
		t.Errorf("held notes %v, want one", notes)
	}
	r.setFleet(ledger.FleetRun)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if len(r.driver.Specs()) != 1 {
		t.Errorf("%d sessions after the fleet ran again", len(r.driver.Specs()))
	}
}

func TestDrainHoldsANewLane(t *testing.T) {
	r := laneRigWith(t, "")
	r.setFleet(ledger.FleetDrain)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.driver.Specs()) != 0 {
		t.Error("a draining fleet started a lane")
	}
}

func TestARunningLaneContinuesUnderPauseAndDrain(t *testing.T) {
	for _, mode := range []ledger.FleetMode{ledger.FleetPause, ledger.FleetDrain} {
		t.Run(string(mode), func(t *testing.T) {
			r := laneRigWith(t, "")
			g := newGate()
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), r.reports(workorders.OutcomeCompleted), completed(0.1)}))
			r.atGate(g)
			r.setFleet(mode)
			close(g.release)
			r.subject.Wait()
			if err := r.subject.Upload(t.Context()); err != nil {
				t.Fatal(err)
			}
			if row := r.row(); row.State != workorders.Acceptance {
				t.Errorf("row %+v", row)
			}
		})
	}
}

func TestPauseStillAllowsACrashRestart(t *testing.T) {
	r := laneRigWith(t, restarts)
	g := newGate()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), {Crash: true}}))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	r.atGate(g)
	r.setFleet(ledger.FleetPause)
	close(g.release)
	r.awaitCharge(t)
	r.clock.Advance(time.Minute)
	r.subject.Wait()
	if len(r.driver.Specs()) != 2 {
		t.Errorf("%d sessions", len(r.driver.Specs()))
	}
}

func TestDrainStopsANewGeneration(t *testing.T) {
	r := laneRigWith(t, restarts)
	g := newGate()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), {Crash: true}}))
	r.atGate(g)
	r.setFleet(ledger.FleetDrain)
	close(g.release)
	r.subject.Wait()
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.driver.Specs()) != 1 {
		t.Errorf("%d sessions under a drain", len(r.driver.Specs()))
	}
	if change := r.outcome(); change.Reason != workorders.ReasonCrashed {
		t.Errorf("outcome %+v", change)
	}
}

func TestDrainDoesNotContinueALaneThatNeverStarted(t *testing.T) {
	r := laneRigWith(t, "")
	r.setFleet(ledger.FleetDrain)
	record := lanes.Record{ID: "lane-1", WorkOrder: r.id, ClaimID: r.grant.ClaimID, Fence: r.grant.Fence, Generation: 1, State: lanes.StateRunning}
	event := ledger.UploadEvent{
		Epoch: r.subject.Session().Epoch, Type: ledger.UploadLaneStarted, WorkOrder: r.id, ClaimID: r.grant.ClaimID,
		Fence: r.grant.Fence, Lane: "lane-1", Generation: 1, OccurredAt: 1,
	}
	if _, err := r.journal.BeginLane(t.Context(), record, event); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.driver.Specs()) != 0 {
		t.Error("a drain started a session")
	}
}

// secondLane dispatches another work order to this worker.
func (r *laneRig) secondLane(id, lane string) {
	r.t.Helper()
	r.claimFor(id, r.subject.Auth().WorkerID)
	if _, err := control.NewLeases(r.store).Dispatch(r.t.Context(), scope, id, lane, member); err != nil {
		r.t.Fatal(err)
	}
	if err := r.subject.Sync(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
}

func TestStartHoldsWhatOnlyTheMachineSees(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		machine lanes.MachineReading
		want    string
	}{
		{"lanes per worker", "[admission]\nmax_lanes = 1\nmax_lanes_per_sign_in = 0\n", plentyOf(), lanes.HeldWorkerFull},
		{"lanes per sign-in", "[admission]\nmax_lanes = 0\nmax_lanes_per_sign_in = 1\n", plentyOf(), lanes.HeldSignInFull},
		{"the memory floor", "[admission]\nmax_lanes = 0\nmin_free_memory_mb = 512\n", lanes.MachineReading{Known: true, FreeMemoryMB: 100, FreeCPUPercent: 90}, lanes.HeldMemory},
		{"the CPU floor", "[admission]\nmax_lanes = 0\nmin_free_cpu_percent = 20\n", lanes.MachineReading{Known: true, FreeMemoryMB: 4096, FreeCPUPercent: 5}, lanes.HeldCPU},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newLaneRig(t)
			r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(tt.profile)
			r.config.Machine = fake.NewMachine(tt.machine)
			r.dispatched()
			holdFirst := tt.want == lanes.HeldMemory || tt.want == lanes.HeldCPU
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
			r.start()
			if holdFirst {
				if len(r.driver.Specs()) != 0 {
					t.Fatal("a lane started under the floor")
				}
			} else {
				r.awaitGeneration(t, 1)
				r.secondLane("wo-2", "lane-2")
				if _, err := r.subject.Reconcile(t.Context()); err != nil {
					t.Fatal(err)
				}
				if len(r.driver.Specs()) != 1 {
					t.Errorf("%d sessions past the cap", len(r.driver.Specs()))
				}
				r.driver.StopEverySession()
				r.subject.Wait()
			}
			if notes := r.heldNotes(); !slices.ContainsFunc(notes, func(note string) bool { return strings.Contains(note, tt.want) }) {
				t.Errorf("held notes %v", notes)
			}
		})
	}
}

func plentyOf() lanes.MachineReading {
	return lanes.MachineReading{Known: true, FreeMemoryMB: 4096, FreeCPUPercent: 90}
}

func TestAMachineThatCannotBeReadStartsTheLane(t *testing.T) {
	r := newLaneRig(t)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser("[admission]\nmin_free_memory_mb = 512\n")
	machine := fake.NewMachine(lanes.MachineReading{})
	machine.Fail(errors.New("no reading"))
	r.config.Machine = machine
	r.dispatched()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if len(r.driver.Specs()) != 1 {
		t.Errorf("%d sessions", len(r.driver.Specs()))
	}
}

func TestTheDailyCapChargesGenerations(t *testing.T) {
	r := laneRigWith(t, "[lifecycle]\nmax_restarts = 5\nrestart_window = \"1m\"\nmax_generations_per_day = 2\nrestart_backoff = \"1s\"\n")
	r.driver.Play(script(crashed()))
	r.driver.PlayAfter(script(crashed()))
	r.start()
	r.awaitCharge(t)
	r.clock.Advance(time.Minute)
	r.laneState(lanes.Tripped)
	if len(r.driver.Specs()) != 2 {
		t.Errorf("%d sessions under a cap of 2", len(r.driver.Specs()))
	}
	r.stopWorker()
	r.subject.Wait()
}

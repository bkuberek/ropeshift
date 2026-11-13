package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestTheDefaultRunModeFollowsTheSignIn(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		kind    string
		profile string
		want    string
	}{
		{"a subscription runs in a terminal", ports.SignInSubscription, "", ports.RunModeInteractive},
		{"an API key runs headless", ports.SignInAPIKey, "", ports.RunModeHeadless},
		{"a profile that says so wins", ports.SignInSubscription, "run_mode = \"headless\"\n", ports.RunModeHeadless},
		{"a profile wins the other way", ports.SignInAPIKey, "run_mode = \"tmux\"\n", ports.RunModeInteractive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newLaneRigOn(t, func(driver *fake.AgentDriver) { driver.SetSignIn(tt.kind) })
			if tt.profile != "" {
				r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", tt.profile)
			}
			r.dispatched()
			r.runWith()
			if got := r.driver.Specs()[0].RunMode; got != tt.want {
				t.Errorf("run mode %q, want %q", got, tt.want)
			}
		})
	}
}

func TestALanesOwnLayerReachesItsSession(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	id := "wo-2"
	r.claimFor(id, r.subject.Auth().WorkerID)
	if _, err := control.NewLeases(r.store).DispatchWith(t.Context(), scope, id, "lane-2", "[agent]\nmodel = \"from-lane\"\n", member); err != nil {
		t.Fatal(err)
	}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	var models []string
	for _, spec := range r.driver.Specs() {
		models = append(models, spec.Lane+"="+spec.Model)
	}
	if !slices.Contains(models, "lane-2=from-lane") {
		t.Errorf("models %v", models)
	}
}

func TestWorkIsOfferedOnlyToAWorkerThatCanRunIt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		needs map[string]string
		setup func(*fake.AgentDriver)
		want  bool
	}{
		{name: "no needs", want: true},
		{name: "the driver it has", needs: map[string]string{ledger.NeedDriver: fake.FakeDriverName}, want: true},
		{name: "a driver it lacks", needs: map[string]string{ledger.NeedDriver: "codex"}},
		{
			name: "a CLI that is not there", needs: map[string]string{ledger.NeedDriver: fake.FakeDriverName},
			setup: func(driver *fake.AgentDriver) { driver.SetAbsent() },
		},
		{name: "the sign-in it has", needs: map[string]string{ledger.NeedSignIn: ledger.SignInAPIKey}, want: true},
		{name: "a sign-in it lacks", needs: map[string]string{ledger.NeedSignIn: ledger.SignInSubscription}},
		{name: "a label it lacks", needs: map[string]string{"os": "plan9"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newLaneRigOn(t, func(driver *fake.AgentDriver) {
				if tt.setup != nil {
					tt.setup(driver)
				}
			})
			ctx := t.Context()
			_, err := control.NewWorkOrders(r.store).Record(ctx, scope, control.LedgerWork{
				Order:    workorders.WorkOrder{ID: "needs", Kind: workorders.KindChange, Title: "T", Needs: tt.needs},
				Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
			}, member)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.NewReviews(r.store).Pass(ctx, scope, "needs", member); err != nil {
				t.Fatal(err)
			}
			worker := r.subject.Auth().WorkerID
			offered, err := r.plane.Offered(ctx, worker)
			if err != nil {
				t.Fatal(err)
			}
			listed := slices.ContainsFunc(offered, func(row workorders.WorkOrder) bool { return row.ID == "needs" })
			if listed != tt.want {
				t.Errorf("offered %v, want %v", listed, tt.want)
			}
			_, err = control.NewLeases(r.store).Claim(ctx, scope, workorders.ClaimRequest{
				WorkOrder: "needs", ClaimID: "claim-needs", Worker: worker, TTLMs: 60_000,
			}, member)
			if claimed := err == nil; claimed != tt.want {
				t.Errorf("claim: %v, want claimed=%v", err, tt.want)
			}
			if !tt.want && !errors.Is(err, workorders.ErrIneligible) {
				t.Errorf("a refused claim said %v", err)
			}
		})
	}
}

func TestACapacityAdvertisedHoldsAtClaim(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	driver := fake.NewAgentDriver()
	config := &worker.LaneRuntime{Driver: driver}
	subject := worker.New(base.link, base.journal, base.clock, worker.Config{
		Name: "test", Lanes: config, Manifest: ledger.Manifest{Capacity: ledger.Capacity{MaxLanes: 1}},
		JoinToken: func(ctx context.Context) (string, error) { return base.plane.CreateJoinToken(ctx, member) },
	})
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	base.claimFor("one", subject.Auth().WorkerID)
	ctx := t.Context()
	_, err := control.NewWorkOrders(base.store).Record(ctx, scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: "two", Kind: workorders.KindChange, Title: "T"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewReviews(base.store).Pass(ctx, scope, "two", member); err != nil {
		t.Fatal(err)
	}
	_, err = control.NewLeases(base.store).Claim(ctx, scope, workorders.ClaimRequest{
		WorkOrder: "two", ClaimID: "claim-two", Worker: subject.Auth().WorkerID, TTLMs: 60_000,
	}, member)
	if !errors.Is(err, workorders.ErrIneligible) {
		t.Errorf("a full worker took a claim: %v", err)
	}
}

// sized is a machine with this many cores and plenty of memory, whose recommendation, at the default
// estimates and reserve, is one lane fewer than its cores.
func sized(cores int64) *fake.Machine {
	machine := fake.NewMachine(plentyOf())
	memory := int64(256) << 30
	machine.SetSample(lanes.MachineSample{Cores: cores, MemoryTotalBytes: &memory, MemoryAvailableBytes: &memory})
	return machine
}

// TestAnUnsetWorkerCapFollowsTheRecommendation holds FR-PROJ-7's default: with no max_lanes in the user's
// profile, the worker's recommendation caps its lanes.
func TestAnUnsetWorkerCapFollowsTheRecommendation(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.config.Machine = sized(2)
	r.dispatched()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	r.start()
	r.awaitGeneration(t, 1)
	r.secondLane("wo-2", "lane-2")
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.driver.Specs()) != 1 {
		t.Errorf("%d sessions past a recommendation of one", len(r.driver.Specs()))
	}
	r.driver.StopEverySession()
	r.subject.Wait()
	if notes := r.heldNotes(); !slices.ContainsFunc(notes, func(note string) bool { return strings.Contains(note, lanes.HeldWorkerFull) }) {
		t.Errorf("held notes %v", notes)
	}
}

// TestTheManifestAdvertisesTheCapInForce is what a claim is held to before the worker first reports: the
// user's cap, or the recommendation where the user set none, or no cap where the user chose none.
func TestTheManifestAdvertisesTheCapInForce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		profile string
		want    int64
	}{
		{"unset follows the recommendation", "", 4},
		{"a value over the recommendation", "[admission]\nmax_lanes = 9\n", 9},
		{"no cap", "[admission]\nmax_lanes = 0\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newRig(t)
			profiles := fake.NewProfileSource()
			profiles.PutUser(tt.profile)
			config := &worker.LaneRuntime{Driver: fake.NewAgentDriver(), Machine: sized(5), Provisioner: worker.Provisioner{Profiles: profiles}}
			subject := worker.New(base.link, base.journal, base.clock, worker.Config{
				Name: "test", Lanes: config,
				JoinToken: func(ctx context.Context) (string, error) { return base.plane.CreateJoinToken(ctx, member) },
			})
			if err := subject.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			listed, err := base.plane.List(t.Context())
			if err != nil || len(listed) != 1 {
				t.Fatalf("workers %+v: %v", listed, err)
			}
			if got := listed[0].Manifest.Capacity.MaxLanes; got != tt.want {
				t.Errorf("advertised %d, want %d", got, tt.want)
			}
		})
	}
}

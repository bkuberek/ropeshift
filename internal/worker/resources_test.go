package worker_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// measured is a machine of eight cores and 32 GiB, half of it available, whose state directory is on one
// volume, with the lane rig's first session holding a process tree.
func measured(r *laneRig) (*fake.Machine, lanes.MachineSample) {
	total, available := int64(32)<<30, int64(16)<<30
	sample := lanes.MachineSample{
		Cores: 8, CPU: &lanes.CPUTicks{Busy: 100, Total: 1000}, Load: &lanes.LoadAverage{One: 1, Five: 2, Fifteen: 3},
		MemoryTotalBytes: &total, MemoryAvailableBytes: &available,
		Volumes: []lanes.VolumeSample{{Volume: lanes.Volume{Role: lanes.VolumeState, Path: r.dir}, Device: 1, FreeBytes: 50 << 30, TotalBytes: 100 << 30}},
	}
	machine := fake.NewMachine(plentyOf())
	machine.SetSample(sample)
	machine.SetTree(1, lanes.ProcessUse{CPUSeconds: 10, RSSBytes: 300 << 20, Processes: 3})
	r.config.Machine = machine
	return machine, sample
}

// running starts the rig's lane and holds it, until the test ends.
func (r *laneRig) running() {
	r.t.Helper()
	r.dispatched()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	r.start()
	r.awaitGeneration(r.t, 1)
	r.t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
}

func (r *laneRig) measure() ledger.ResourceReport {
	r.t.Helper()
	report, err := r.subject.Measure(r.t.Context())
	if err != nil {
		r.t.Fatal(err)
	}
	return report
}

// reported is what the control plane holds of this rig's worker.
func (r *laneRig) reported() ledger.Worker {
	r.t.Helper()
	workers, err := r.plane.List(r.t.Context())
	if err != nil {
		r.t.Fatal(err)
	}
	for _, worker := range workers {
		if worker.ID == r.subject.Auth().WorkerID {
			return worker
		}
	}
	r.t.Fatal("the worker is not listed")
	return ledger.Worker{}
}

func (r *laneRig) heartbeat() {
	r.t.Helper()
	if _, err := r.subject.Beat(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
}

// TestTheWorkerReportsItsMachineAndSessions holds FR-OBS-8 and FR-OBS-9 end to end: the machine, each
// session's process tree, the recommendation, and the caps in force reach the control plane on the next
// heartbeat, dated by its clock.
func TestTheWorkerReportsItsMachineAndSessions(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	machine, sample := measured(r)
	r.running()
	r.heartbeat()
	if worker := r.reported(); worker.Report != nil {
		t.Fatalf("a heartbeat before any measure carried %+v", worker.Report)
	}
	first := r.measure()
	if first.Machine.CPUPercent != nil {
		t.Errorf("a first measure has no CPU share to tell, and gave %v", *first.Machine.CPUPercent)
	}
	if len(first.Sessions) != 1 || first.Sessions[0].CPUCores != nil || first.Sessions[0].RSSBytes == nil {
		t.Errorf("first sessions %+v", first.Sessions)
	}
	sample.CPU = &lanes.CPUTicks{Busy: 400, Total: 2000}
	machine.SetSample(sample)
	machine.SetTree(1, lanes.ProcessUse{CPUSeconds: 25, RSSBytes: 400 << 20, Processes: 4})
	r.clock.Advance(15 * time.Second)
	report := r.measure()
	if report.IntervalMs != 15_000 || report.Machine.Cores != 8 || report.Machine.CPUPercent == nil || *report.Machine.CPUPercent != 30 ||
		*report.Machine.MemoryUsedBytes != 16<<30 || *report.Machine.Load5 != 2 {
		t.Errorf("machine %+v", report.Machine)
	}
	if len(report.Machine.Volumes) != 1 || !slices.Equal(report.Machine.Volumes[0].Roles, []string{lanes.VolumeState}) {
		t.Errorf("volumes %+v", report.Machine.Volumes)
	}
	asked := machine.Asked()
	if !slices.Contains(asked, lanes.Volume{Role: lanes.VolumeState, Path: r.dir}) ||
		!slices.ContainsFunc(asked, func(v lanes.Volume) bool { return v.Role == lanes.VolumeWorktrees && v.Path != "" }) {
		t.Errorf("asked for %+v", asked)
	}
	if len(report.Sessions) != 1 {
		t.Fatalf("sessions %+v", report.Sessions)
	}
	lane := report.Sessions[0]
	if lane.Kind != ledger.SessionLane || lane.Lane != "lane-1" || lane.CPUCores == nil || *lane.CPUCores != 1 || *lane.RSSBytes != 400<<20 || lane.Processes != 4 {
		t.Errorf("lane %+v", lane)
	}
	if report.Recommendation == nil || report.Recommendation.MaxLanes != 7 || report.Recommendation.Basis != ledger.BasisEstimates {
		t.Errorf("recommendation %+v", report.Recommendation)
	}
	if report.Caps != (ledger.LaneCaps{MaxLanes: 7}) {
		t.Errorf("caps %+v", report.Caps)
	}
	if len(report.Warnings) != 0 {
		t.Errorf("warnings %+v", report.Warnings)
	}
	r.heartbeat()
	worker := r.reported()
	if worker.Report == nil || worker.Report.Caps.MaxLanes != 7 || len(worker.Report.Sessions) != 1 || worker.ReportedAt == 0 {
		t.Errorf("the control plane holds %+v at %d", worker.Report, worker.ReportedAt)
	}
}

// TestAMeasureTheMachineCannotProvideIsAbsent never reports an unread measure as zero.
func TestAMeasureTheMachineCannotProvideIsAbsent(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	machine := fake.NewMachine(plentyOf())
	machine.SetSample(lanes.MachineSample{Cores: 4})
	machine.NoProcesses()
	r.config.Machine = machine
	r.running()
	report := r.measure()
	m := report.Machine
	if m.CPUPercent != nil || m.Load1 != nil || m.MemoryTotalBytes != nil || m.MemoryUsedBytes != nil || m.MemoryAvailableBytes != nil || m.MemoryPressureSome != nil || len(m.Volumes) != 0 {
		t.Errorf("machine %+v", m)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].CPUCores != nil || report.Sessions[0].RSSBytes != nil {
		t.Errorf("sessions %+v", report.Sessions)
	}
	if report.Recommendation == nil || report.Recommendation.MaxLanes != 3 || report.Recommendation.ByMemory != nil {
		t.Errorf("with no memory read, the cores decide: %+v", report.Recommendation)
	}
	machine.Fail(errors.New("no sample"))
	report = r.measure()
	if report.Machine.Cores != 0 || report.Recommendation != nil || report.Caps.MaxLanes != 0 {
		t.Errorf("a machine that cannot be read gave %+v", report)
	}
}

// TestAChangedCapReachesTheNextReport is how a member's change takes hold with no restart: the worker
// reads the user's layer at every measure.
func TestAChangedCapReachesTheNextReport(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	measured(r)
	profiles := r.config.Provisioner.Profiles.(*fake.ProfileSource)
	for _, step := range []struct {
		profile string
		want    ledger.LaneCaps
	}{
		{"", ledger.LaneCaps{MaxLanes: 7}},
		{"[admission]\nmax_lanes = 12\nmax_lanes_per_sign_in = 2\n", ledger.LaneCaps{MaxLanes: 12, MaxLanesSet: true, MaxLanesPerSignIn: 2}},
		{"[admission]\nmax_lanes = 0\n", ledger.LaneCaps{MaxLanes: 0, MaxLanesSet: true}},
	} {
		profiles.PutUser(step.profile)
		if got := r.measure().Caps; got != step.want {
			t.Errorf("%q: caps %+v, want %+v", step.profile, got, step.want)
		}
	}
}

// TestThresholdsWarnAndStopNothing holds FR-UI-10: a value past a threshold is a warning in the report, and
// the lane keeps running.
func TestThresholdsWarnAndStopNothing(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	machine, sample := measured(r)
	sample.Volumes[0].FreeBytes = 1 << 30
	machine.SetSample(sample)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser("[resources]\nreport_interval = \"30s\"\ncpu_load_warn = 0.1\n")
	r.running()
	report := r.measure()
	measures := []string{}
	for _, warning := range report.Warnings {
		measures = append(measures, warning.Measure)
	}
	slices.Sort(measures)
	if !slices.Equal(measures, []string{ledger.WarnCPULoad, ledger.WarnDiskFree}) || report.IntervalMs != 30_000 {
		t.Errorf("warnings %+v every %d", report.Warnings, report.IntervalMs)
	}
	if state := r.view().ActualState; state == ledger.StateParked || state == ledger.StateEnded {
		t.Errorf("a warning left the lane %s", state)
	}
}

// TestALanesPeakReplacesTheEstimates holds the rest of FR-OBS-9: once enough lanes have run, what one lane
// takes is the peak they reached, which the journal keeps past a restart.
func TestALanesPeakReplacesTheEstimates(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	machine, _ := measured(r)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser("[admission]\nobserved_min = 1\nobserved_window = 5\n")
	r.dispatched()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	r.start()
	r.awaitGeneration(t, 1)
	r.measure()
	machine.SetTree(1, lanes.ProcessUse{CPUSeconds: 70, RSSBytes: 3 << 30, Processes: 3})
	r.clock.Advance(time.Minute)
	if report := r.measure(); report.Recommendation.Basis != ledger.BasisEstimates {
		t.Errorf("a running lane's peak counted before it ended: %+v", report.Recommendation)
	}
	r.driver.StopEverySession()
	r.subject.Wait()
	r.clock.Advance(15 * time.Second)
	report := r.measure()
	got := report.Recommendation
	if got == nil || got.Basis != ledger.BasisObserved || got.ObservedLanes != 1 || got.LaneCPU != 1 || got.LaneMemoryBytes != 3<<30 || got.MaxLanes != 7 {
		t.Errorf("recommendation %+v", got)
	}
	peaks, err := r.journal.LanePeaks(t.Context())
	if err != nil || len(peaks) != 1 || peaks[0] != (lanes.LanePeak{CPU: 1, MemoryBytes: 3 << 30}) {
		t.Errorf("the journal holds %+v, %v", peaks, err)
	}
}

// TestAReportedCapHoldsAtClaim is the claim side of a change: the control plane holds a claim to the cap
// the worker last reported, which follows the user's layer with no new session.
func TestAReportedCapHoldsAtClaim(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	measured(r)
	profiles := r.config.Provisioner.Profiles.(*fake.ProfileSource)
	claim := func(id string) error {
		ctx := t.Context()
		_, err := control.NewWorkOrders(r.store).Record(ctx, scope, control.LedgerWork{
			Order: workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "T"}, Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
		}, member)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := control.NewReviews(r.store).Pass(ctx, scope, id, member); err != nil {
			t.Fatal(err)
		}
		_, err = control.NewLeases(r.store).Claim(ctx, scope, workorders.ClaimRequest{WorkOrder: id, ClaimID: "claim-" + id, Worker: r.subject.Auth().WorkerID, TTLMs: 60_000}, member)
		return err
	}
	profiles.PutUser("[admission]\nmax_lanes = 1\n")
	r.measure()
	r.heartbeat()
	if err := claim("two"); !errors.Is(err, workorders.ErrIneligible) {
		t.Errorf("a claim past the reported cap: %v", err)
	}
	profiles.PutUser("[admission]\nmax_lanes = 0\n")
	r.measure()
	r.heartbeat()
	if err := claim("three"); err != nil {
		t.Errorf("a claim under no cap: %v", err)
	}
}

// TestAReadThatLosesTheMemoryKeepsTheLastTotal holds a recommendation to the memory an earlier read
// counted, because a machine's memory does not change while it runs and a read that loses it, as a failed
// sysctl does on macOS, would leave the cores alone to decide.
func TestAReadThatLosesTheMemoryKeepsTheLastTotal(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	machine, sample := measured(r)
	if got := r.measure().Recommendation; got == nil || got.ByMemory == nil || *got.ByMemory != 14 || got.MaxLanes != 7 {
		t.Fatalf("first %+v", got)
	}
	sample.MemoryTotalBytes = nil
	machine.SetSample(sample)
	report := r.measure()
	if got := report.Recommendation; got == nil || got.ByMemory == nil || *got.ByMemory != 14 || got.MaxLanes != 7 {
		t.Errorf("after a read that lost the memory: %+v", got)
	}
	if total := report.Machine.MemoryTotalBytes; total == nil || *total != 32<<30 {
		t.Errorf("total %v", total)
	}
}

// TestAnAdmissionReadsNoMachineItAlreadyKnows keeps a start from spawning the reads a Mac runs: what the
// machine has does not change, so a start reuses what a read found, and with no floor set it reads nothing.
func TestAnAdmissionReadsNoMachineItAlreadyKnows(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	machine := sized(8)
	r.config.Machine = machine
	r.running()
	samples, readings := machine.Calls()
	if samples != 1 || readings != 0 {
		t.Errorf("the first start sampled %d times and read %d", samples, readings)
	}
	r.secondLane("wo-2", "lane-2")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.awaitGeneration(t, 2)
	if samples, readings := machine.Calls(); samples != 1 || readings != 0 {
		t.Errorf("a second start sampled %d times and read %d in all", samples, readings)
	}
}

package worker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// errNoMachine says a worker has no machine to measure, as a worker with no lanes has.
var errNoMachine = errors.New("this worker measures no machine")

// resources is what a worker measures of its machine and its sessions (FR-OBS-8): the report it last made
// and whether the control plane has it, the CPU ticks before, a meter for each session, and the peaks of
// the lanes it ran, which its recommendation reads (FR-OBS-9).
type resources struct {
	// measuring holds to one measure at a time, and lock guards the rest, so a heartbeat that reads the
	// report never waits on a read of the machine.
	measuring sync.Mutex
	lock      sync.Mutex
	report    *ledger.ResourceReport
	at        time.Time
	seq, sent int64
	ticks     *lanes.CPUTicks
	meters    map[string]*sessionMeter
	peaks     []lanes.LanePeak
}

// sessionMeter follows one session, by its lane's ID.
type sessionMeter struct {
	kind string
	lanes.LaneMeter
}

// session is one session the worker runs now, as a measure finds it.
type session struct {
	kind, project, lane string
	pid                 int
	worktree            string
}

// loadPeaks reads the peaks the journal kept, so a restart does not start the count again.
func (w *Worker) loadPeaks(ctx context.Context) error {
	peaks, err := w.journal.LanePeaks(ctx)
	if err != nil {
		return err
	}
	w.resources.lock.Lock()
	defer w.resources.lock.Unlock()
	w.resources.peaks = peaks
	return nil
}

// lanePeaks is the peaks of the lanes this worker ran, oldest first.
func (w *Worker) lanePeaks() []lanes.LanePeak {
	w.resources.lock.Lock()
	defer w.resources.lock.Unlock()
	return slices.Clone(w.resources.peaks)
}

// sessions lists what runs now that has a process to measure.
func (w *Worker) sessions() []session {
	w.mu.Lock()
	runs := make([]*laneRun, 0, len(w.running))
	for _, run := range w.running {
		runs = append(runs, run)
	}
	w.mu.Unlock()
	out := make([]session, 0, len(runs))
	for _, run := range runs {
		run.handleLock.Lock()
		pid, worktree := run.handle.PID, run.worktree
		run.handleLock.Unlock()
		if pid <= 0 {
			continue
		}
		kind := ledger.SessionLane
		if run.orchestrating() {
			kind = ledger.SessionOrchestrator
		}
		out = append(out, session{kind: kind, project: run.lease.Project, lane: run.lease.Lane, pid: pid, worktree: worktree})
	}
	slices.SortFunc(out, func(a, b session) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.project, b.project), cmp.Compare(a.lane, b.lane))
	})
	return out
}

// Measure reads the machine and each session's process tree, works out the recommendation and the caps
// in force from the user's layer as it stands now, and keeps the report for the next heartbeat. A measure
// the machine cannot provide is absent from the report, never zero, and a warning stops nothing.
func (w *Worker) Measure(ctx context.Context) (ledger.ResourceReport, error) {
	rt := w.config.Lanes
	if rt == nil || rt.Machine == nil {
		return ledger.ResourceReport{}, errNoMachine
	}
	w.resources.measuring.Lock()
	defer w.resources.measuring.Unlock()
	var profile lanes.Profile
	if rt.Provisioner.Profiles != nil {
		loaded, err := rt.Provisioner.Profiles.Load(ctx, "", "")
		if err != nil {
			w.config.Report(fmt.Errorf("measuring the machine: %w", err))
		} else {
			profile = loaded
		}
	}
	interval, thresholds, err := profile.Resources.Reporting()
	if err != nil {
		interval, thresholds, _ = lanes.ResourceSettings{}.Reporting()
	}
	settings, err := profile.Admission.Recommending()
	if err != nil {
		settings, _ = lanes.Admission{}.Recommending()
	}
	running := w.sessions()
	volumes := []lanes.Volume{{Role: lanes.VolumeState, Path: rt.StateDir}}
	roots := make([]int, 0, len(running))
	for _, s := range running {
		roots = append(roots, s.pid)
		if s.worktree != "" && !slices.Contains(volumes, lanes.Volume{Role: lanes.VolumeWorktrees, Path: s.worktree}) {
			volumes = append(volumes, lanes.Volume{Role: lanes.VolumeWorktrees, Path: s.worktree})
		}
	}
	sample, sampleErr := rt.Machine.Sample(ctx, volumes)
	if sampleErr != nil {
		w.config.Report(fmt.Errorf("sampling the machine: %w", sampleErr))
		sample = lanes.MachineSample{}
	}
	trees, treesErr := rt.Machine.ProcessTrees(ctx, roots)
	now := w.clock.Now()

	w.resources.lock.Lock()
	defer w.resources.lock.Unlock()
	report := ledger.ResourceReport{IntervalMs: interval.Milliseconds(), Machine: lanes.MachineResources(sample, w.resources.ticks), Sessions: []ledger.SessionResources{}}
	if sampleErr == nil {
		w.resources.ticks = sample.CPU
	}
	report.Sessions = w.meterSessions(running, trees, treesErr == nil, now)
	if ended := w.endSessions(running, settings.ObservedWindow); ended {
		if err := w.journal.SaveLanePeaks(ctx, w.resources.peaks); err != nil {
			w.config.Report(fmt.Errorf("keeping the lanes' peaks: %w", err))
		}
	}
	var recommended int64
	if recommendation, ok := lanes.Recommend(lanes.RecommendInput{
		Cores: sample.Cores, MemoryTotalBytes: sample.MemoryTotalBytes, Settings: settings, Peaks: w.resources.peaks,
	}); ok {
		report.Recommendation, recommended = &recommendation, recommendation.MaxLanes
	}
	user := profile.Admission
	report.Caps = ledger.LaneCaps{
		MaxLanes: lanes.WorkerCap(user.MaxLanes, user.MaxLanes, recommended), MaxLanesSet: user.MaxLanes != nil, MaxLanesPerSignIn: user.MaxLanesPerSignIn,
	}
	report.Warnings = lanes.ResourceWarnings(report.Machine, thresholds)
	w.resources.seq++
	w.resources.report, w.resources.at = &report, now
	return report, nil
}

// meterSessions measures each session's process tree. A tree it could not read, or a session whose
// process is gone, has no measure.
func (w *Worker) meterSessions(running []session, trees map[int]lanes.ProcessUse, read bool, now time.Time) []ledger.SessionResources {
	if w.resources.meters == nil {
		w.resources.meters = map[string]*sessionMeter{}
	}
	out := make([]ledger.SessionResources, 0, len(running))
	for _, s := range running {
		line := ledger.SessionResources{Kind: s.kind, Project: s.project, Lane: s.lane}
		use, found := trees[s.pid]
		if read && found {
			meter := w.resources.meters[s.lane]
			if meter == nil {
				meter = &sessionMeter{kind: s.kind}
				w.resources.meters[s.lane] = meter
			}
			rss := use.RSSBytes
			line.RSSBytes, line.Processes = &rss, use.Processes
			if cores, ok := meter.Add(now, use.CPUSeconds, use.RSSBytes); ok {
				line.CPUCores = &cores
			}
		}
		out = append(out, line)
	}
	return out
}

// endSessions keeps the peak of each lane that no longer runs, and forgets its meter. It reports whether
// a peak was kept. An orchestrator's peak is not a lane's, and a lane never measured has none.
func (w *Worker) endSessions(running []session, window int64) bool {
	kept := false
	for lane, meter := range w.resources.meters {
		if slices.ContainsFunc(running, func(s session) bool { return s.lane == lane }) {
			continue
		}
		delete(w.resources.meters, lane)
		peak := meter.Peak()
		if meter.kind != ledger.SessionLane || peak == (lanes.LanePeak{}) {
			continue
		}
		w.resources.peaks, kept = lanes.KeepPeaks(w.resources.peaks, peak, window), true
	}
	return kept
}

// pendingReport is the report the control plane has not been sent, with how long ago it was measured, and
// the number that marks it sent.
func (w *Worker) pendingReport() (*ledger.ResourceReport, int64) {
	w.resources.lock.Lock()
	defer w.resources.lock.Unlock()
	if w.resources.report == nil || w.resources.sent == w.resources.seq {
		return nil, 0
	}
	report := *w.resources.report
	report.AgeMs = max(0, w.clock.Now().Sub(w.resources.at).Milliseconds())
	return &report, w.resources.seq
}

// reportSent marks a report the control plane answered for, unless a newer one was sent since.
func (w *Worker) reportSent(seq int64) {
	w.resources.lock.Lock()
	defer w.resources.lock.Unlock()
	w.resources.sent = max(w.resources.sent, seq)
}

// measureEvery measures at the interval the user's layer sets, read again at every measure, until the
// context ends. A worker with no machine measures nothing.
func (w *Worker) measureEvery(ctx context.Context) {
	for ctx.Err() == nil {
		report, err := w.Measure(ctx)
		if errors.Is(err, errNoMachine) {
			return
		}
		every := lanes.DefaultReportInterval
		if err == nil && report.IntervalMs > 0 {
			every = time.Duration(report.IntervalMs) * time.Millisecond
		}
		wait(ctx, every)
	}
}

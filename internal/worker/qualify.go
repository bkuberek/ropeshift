package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const (
	// probeModel is what a probe's sessions ask for. The surfaces it checks belong to the CLI and
	// not to a model, so the smallest model's turns are enough.
	probeModel = "haiku"
	// probeLimit bounds one probe, whose own waits end well within it.
	probeLimit = 10 * time.Minute
	// versionCheckEvery is how often a running worker reads the installed version, so a CLI that
	// updated itself is probed before a lane asks.
	versionCheckEvery = time.Minute
	// requalifyFirst is how long after a probe of a version whose result has a check that did not pass the next probe of
	// it waits, and each wait after that doubles, up to requalifyCap. Each probe spends real CLI sessions.
	requalifyFirst = time.Hour
	requalifyCap   = 24 * time.Hour
	// evidenceDirName is where a worker's probes keep what they saw of the checks that did not pass, within its state
	// directory and beside the probe's own, which each probe clears.
	evidenceDirName = "probe-evidence"
	// restartProbeDelay is how long after the last probe began a worker that starts holds back its own, so a worker
	// that keeps restarting does not open a probe's sessions on each start.
	restartProbeDelay = 10 * time.Minute
)

// errNoVersion is a driver that reports no installed version.
var errNoVersion = errors.New("the driver reports no version")

// probeRun is the one probe a worker runs at a time.
type probeRun struct {
	version string
	began   time.Time
}

// probeBackoff is the last probe of one version that ended, whatever it ended with, and when the next may start. A
// worker keeps one: a probe of another version, or one that passed every check it runs, starts it over. The journal
// keeps it with the record of the last probe, so a restart goes on from it.
type probeBackoff struct {
	version string
	wait    time.Duration
	next    time.Time
}

// loadQualification hands the driver what this worker's last probe observed, so a restart on the
// same version starts work on that result, and a version that moved since reads unqualified. A result that passed
// every check a probe runs now holds, and any other is probed again as the worker starts.
func (w *Worker) loadQualification(ctx context.Context) error {
	if w.config.Lanes == nil {
		return nil
	}
	q, err := w.journal.Qualification(ctx)
	if err != nil {
		return err
	}
	attempt, err := w.journal.ProbeAttempt(ctx)
	if err != nil {
		return err
	}
	w.qualLock.Lock()
	defer w.qualLock.Unlock()
	w.life = ctx
	if q.Version != "" {
		w.qual = q
	}
	if attempt.Version != "" {
		// An attempt dated after now is one a clock that moved back left, which holds the probe back for the delay and no more.
		now := w.clock.Now()
		w.probeAfter = attempt.BeganAt.Add(restartProbeDelay)
		if latest := now.Add(restartProbeDelay); w.probeAfter.After(latest) {
			w.probeAfter = latest
		}
		// The wait the version's probes had grown to goes on, so a restart probes a version that keeps failing when that wait
		// ends. One a clock that moved back left is held to the wait it names and no more.
		if attempt.Wait > 0 {
			next := attempt.Next
			if latest := now.Add(attempt.Wait); next.After(latest) {
				next = latest
			}
			w.backoff = probeBackoff{version: attempt.Version, wait: attempt.Wait, next: next}
		}
	}
	w.handOverLocked()
	return nil
}

// lookAtVersion reads the installed version and starts its probe when it is due, with no lane asking (FR-DRV-2). A
// version nobody can read is the doctor's to report, and the next look reads it again.
func (w *Worker) lookAtVersion(ctx context.Context) {
	if w.config.Lanes == nil {
		return
	}
	w.qualLock.Lock()
	w.lookedAt = w.clock.Now()
	w.qualLock.Unlock()
	_ = w.qualify(ctx)
}

// tendVersion looks at the installed version once in each versionCheckEvery, so a CLI that updated
// itself under a running worker is probed before its next lane.
func (w *Worker) tendVersion(ctx context.Context) {
	w.qualLock.Lock()
	due := w.clock.Now().Sub(w.lookedAt) >= versionCheckEvery
	w.qualLock.Unlock()
	if due {
		w.lookAtVersion(ctx)
	}
}

// qualifyLane is a lane's look at the installed version before its next process. It starts the probe
// of a version no probe's result holds for, and never waits for it: the lane starts on what the driver
// holds meanwhile.
func (w *Worker) qualifyLane(ctx context.Context, lease ledger.DesiredLease) {
	if err := w.qualify(ctx); err != nil {
		// A version nobody can read fails the start where the driver reads it again.
		w.config.Report(fmt.Errorf("lane %s: reading the CLI's version: %w", lease.Lane, err))
	}
}

// qualify starts the probe of the installed version when one is due and no probe runs (probeDueLocked). One probe
// runs at a time, and a probe of another version that ends leaves the next look to start this one. No result holds
// for an empty version, so a driver that read none is not probed: each probe would end only for the next to start. A
// worker told not to probe starts none, and none starts while the sign-in is at a usage limit or signed out.
func (w *Worker) qualify(ctx context.Context) error {
	if w.config.Lanes.NoProbe {
		return nil
	}
	// A probe's sessions would meet the limit or the sign-in that is out, and its failed or unobserved checks would
	// replace passes. The first look after it ends probes.
	if sign := w.signIn(); sign.Limited || sign.SignedOut {
		return nil
	}
	capabilities, err := w.config.Lanes.Driver.Capabilities(ctx)
	if err != nil {
		return err
	}
	if capabilities.Version == "" {
		return errNoVersion
	}
	checks := w.config.Lanes.Driver.ProbeChecks(w.probeSignIn(capabilities.Version))
	w.qualLock.Lock()
	if w.probe != nil || !w.probeDueLocked(capabilities.Version, checks, w.clock.Now()) {
		w.qualLock.Unlock()
		return nil
	}
	life := w.life
	if life == nil {
		life = ctx
	}
	p := &probeRun{version: capabilities.Version, began: w.clock.Now()}
	w.probe = p
	attempt := w.attemptLocked(p)
	w.qualLock.Unlock()
	// The attempt is journaled before any session starts, so a worker that restarts knows when this one began, and keeps the
	// wait the version had. A journal that cannot keep it starts no probe, and the wait before the next try begins.
	if err := w.journal.SaveProbeAttempt(life, attempt); err != nil {
		w.qualLock.Lock()
		w.probe = nil
		w.noteProbeLocked(p.version, false)
		w.qualLock.Unlock()
		w.laneMoved()
		w.config.Report(fmt.Errorf("recording the probe of %s: %w", p.version, err))
		return nil
	}
	w.probes.Go(func() { w.runProbe(life, p) })
	return nil
}

// probeSignIn is the kind of sign-in a probe of this version is judged and run for. A sign-in that could not be read
// reads as none, which a probe takes for a subscription, so while it is unread the kind the result in force for this
// version was probed on stands in: a failed read neither probes the version again nor replaces the result with one
// probed on another kind.
func (w *Worker) probeSignIn(version string) string {
	if kind := w.signInKind(); kind != "" {
		return kind
	}
	w.qualLock.Lock()
	defer w.qualLock.Unlock()
	if w.qual.Holds(version) {
		return w.qual.SignIn
	}
	return ""
}

// probeDueLocked says a probe of the installed version is due. A result that holds for the version and passed every
// check a probe runs now, which the driver names for this sign-in, is never probed again. No probe starts before
// probeAfter, which a restart sets from the journal's record of the last attempt. Any other version is, unless the last probe of it ended less than its wait ago,
// whether it left a result with a check that did not pass, left none because it could not run or its result could not
// be kept, or ended because the version moved under it. So no path probes a version in a loop.
func (w *Worker) probeDueLocked(version string, checks []lanes.Surface, now time.Time) bool {
	if w.qual.Holds(version) && w.qual.PassedAll(checks) {
		return false
	}
	if now.Before(w.probeAfter) {
		return false
	}
	return w.backoff.version != version || !now.Before(w.backoff.next)
}

// noteProbeLocked starts the wait after a probe of a version that has ended. A probe that passed every check it runs,
// and whose result was kept, ends the backoff, and any other starts it at requalifyFirst for a version the last
// probe did not name, or doubles it, up to requalifyCap.
func (w *Worker) noteProbeLocked(version string, passed bool) {
	if passed {
		w.backoff = probeBackoff{}
		return
	}
	wait := requalifyFirst
	if w.backoff.version == version {
		wait = min(2*w.backoff.wait, requalifyCap)
	}
	w.backoff = probeBackoff{version: version, wait: wait, next: w.clock.Now().Add(wait)}
}

// attemptLocked is what the journal keeps of a probe: when it began, and the wait the worker holds for its version, which
// is none for a version no wait stands for. The caller holds qualLock.
func (w *Worker) attemptLocked(p *probeRun) lanes.ProbeAttempt {
	attempt := lanes.ProbeAttempt{Version: p.version, BeganAt: p.began}
	if w.backoff.version == p.version {
		attempt.Wait, attempt.Next = w.backoff.wait, w.backoff.next
	}
	return attempt
}

// handOverLocked hands the driver the last probe's result. The driver qualifies the version that result
// names and no other, so a version no result names, such as one that is being probed, runs on documented
// surfaces until its own result arrives. With no result before, the driver keeps its own answer for the
// version, which uses only the surfaces the shipped matrix passed.
func (w *Worker) handOverLocked() {
	if w.qual.Version != "" {
		w.config.Lanes.Driver.Qualify(w.qual)
	}
}

// runProbe runs one probe and keeps its result: in the journal with the event that records it, and
// in the driver, which from then on qualifies that version alone. While it runs the last result stays in force, and
// so it does when the probe cannot run. A probe that could not run, for a version no result holds for, leaves every
// surface unobserved, so the version runs degraded.
func (w *Worker) runProbe(life context.Context, p *probeRun) {
	// A probe's end moves the status of every lane on its version, so the loop publishes at once. A probe
	// that ends with no result of its own, because the version moved under it, leaves the driver what the
	// worker holds. Whatever it ends with, the wait before the next probe of the version starts.
	ownResult, passed := false, false
	defer func() {
		w.qualLock.Lock()
		w.probe = nil
		w.noteProbeLocked(p.version, passed)
		attempt := w.attemptLocked(p)
		if !ownResult {
			// Hands the driver the last result the worker holds, which names no version this probe checked.
			w.handOverLocked()
		}
		w.qualLock.Unlock()
		// The wait goes in the journal with the attempt, so a restart goes on from it. A probe the worker's own stop cut short
		// proved nothing of its version, so a restart does not wait on it.
		if life.Err() == nil {
			if err := w.journal.SaveProbeAttempt(life, attempt); err != nil {
				w.config.Report(fmt.Errorf("recording the wait after the probe of %s: %w", p.version, err))
			}
		}
		w.laneMoved()
	}()
	ctx, cancel := context.WithTimeout(life, probeLimit)
	defer cancel()
	rt := w.config.Lanes
	spec := ports.ProbeSpec{
		Dir: filepath.Join(rt.StateDir, "probe"), TempDir: rt.probeTempDir(), EvidenceDir: filepath.Join(rt.StateDir, evidenceDirName), Model: probeModel,
		Binary: rt.Binary, SignIn: w.probeSignIn(p.version), Override: rt.Override,
	}
	checks := rt.Driver.ProbeChecks(spec.SignIn)
	q, err := rt.Driver.Probe(ctx, spec)
	switch {
	case life.Err() != nil:
		return
	case errors.Is(err, ports.ErrVersionMoved):
		w.config.Report(fmt.Errorf("the probe of %s: %w", p.version, err))
		return
	case err != nil:
		w.config.Report(fmt.Errorf("the probe of %s could not run: %w", p.version, err))
		// A probe that could not run proved nothing against a result that holds for its version, so that result stays in
		// force and only the wait begins. With no result for the version, every surface reads unobserved.
		if w.holdsFor(p.version) {
			return
		}
		q = lanes.Unprobed(w.driverName(), p.version, spec.SignIn, nil)
	}
	if err := w.keepQualification(life, q); err != nil {
		w.config.Report(fmt.Errorf("keeping the probe of %s: %w", q.Version, err))
		return
	}
	// A result for a version other than the one this probe was started for leaves that version to be probed again.
	ownResult, passed = true, q.PassedAll(checks) && q.Version == p.version
}

// holdsFor says the worker holds a probe's result for this version.
func (w *Worker) holdsFor(version string) bool {
	w.qualLock.Lock()
	defer w.qualLock.Unlock()
	return w.qual.Holds(version)
}

// keepQualification stores a result and its event in one write, then hands it to the driver, so the
// driver never runs on a result the ledger will not hear of.
func (w *Worker) keepQualification(ctx context.Context, q lanes.Qualification) error {
	payload, err := json.Marshal(q)
	if err != nil {
		return err
	}
	if err := w.journal.SaveQualification(ctx, q, w.event(ledger.UploadDriverQualified, lanes.Record{}, payload)); err != nil {
		return err
	}
	w.qualLock.Lock()
	defer w.qualLock.Unlock()
	w.qual = q
	w.config.Lanes.Driver.Qualify(q)
	return nil
}

// qualificationOf is what a lane whose process runs on a version reads of its worker's probes: the
// result that holds for that version, when one does, and otherwise the check of it that runs now, which
// the lane names, with the source of the surfaces its process started on.
func (w *Worker) qualificationOf(version, source string) (check *ledger.CLICheck, held lanes.Qualification, holds bool) {
	if version == "" {
		return nil, lanes.Qualification{}, false
	}
	driver := w.driverName()
	w.qualLock.Lock()
	defer w.qualLock.Unlock()
	switch {
	case w.qual.Holds(version):
		return nil, w.qual, true
	case w.probe != nil && w.probe.version == version:
		return ledger.CheckingCLI(driver, version, source), lanes.Qualification{}, false
	}
	return nil, lanes.Qualification{}, false
}

// noteAdoptedSurfaces drops the shipped matrix as the source of the surfaces an adopted session runs on when the
// worker holds a probe's result, because the driver's adoption then read that result and not the matrix.
func (r *laneRun) noteAdoptedSurfaces() {
	r.w.qualLock.Lock()
	probed := r.w.qual.Version != ""
	r.w.qualLock.Unlock()
	if !probed {
		return
	}
	r.handleLock.Lock()
	r.source = ""
	r.handleLock.Unlock()
}

// runVersion is the version the lane's process runs, and empty before its first process starts. The source
// is where the surfaces that process started on come from.
func (r *laneRun) runVersion() (version, source string) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	if r.source != ledger.SurfacesShipped {
		return r.version, ""
	}
	return r.version, r.source
}

// driverName is the driver this epoch advertised.
func (w *Worker) driverName() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.advertised.Drivers) == 0 {
		return ""
	}
	return w.advertised.Drivers[0].Driver
}

// probeTempDir is a short private directory for a probe's sockets, beside each lane's own.
func (rt *LaneRuntime) probeTempDir() string {
	dir := rt.RunDir
	if dir == "" {
		dir = rt.StateDir
	}
	return filepath.Join(dir, "q")
}

// noteProcess keeps what the session's own process started on, which can differ from what the
// worker last read: a version that changed between the look and the launch runs degraded, and the
// lane shows it.
func (r *laneRun) noteProcess(handle ports.SessionHandle) {
	if handle.Version == "" {
		return
	}
	r.handleLock.Lock()
	r.version, r.degraded, r.source = handle.Version, handle.Degraded, handle.Source
	r.handleLock.Unlock()
	r.state.mu.Lock()
	r.state.degraded = handle.Degraded
	r.state.probeDegraded = false
	r.state.mu.Unlock()
}

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
)

// errNoVersion is a driver that reports no installed version.
var errNoVersion = errors.New("the driver reports no version")

// probeRun is the one probe a worker runs at a time.
type probeRun struct {
	version string
}

// loadQualification hands the driver what this worker's last probe observed, so a restart on the
// same version starts work with no second probe, and a version that moved since reads unqualified.
func (w *Worker) loadQualification(ctx context.Context) error {
	if w.config.Lanes == nil {
		return nil
	}
	q, err := w.journal.Qualification(ctx)
	if err != nil {
		return err
	}
	w.qualLock.Lock()
	defer w.qualLock.Unlock()
	w.life = ctx
	if q.Version != "" {
		w.qual = q
	}
	w.handOverLocked()
	return nil
}

// lookAtVersion reads the installed version and starts its probe when no probe's result holds for it,
// with no lane asking (FR-DRV-2). A version nobody can read is the doctor's to report, and the next
// look reads it again.
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

// qualify starts the probe of the installed version when no probe's result holds for it and no probe
// runs. One probe runs at a time, and a probe of another version that ends leaves the next look to
// start this one. No result holds for an empty version, so a driver that read none is not probed:
// each probe would end only for the next to start.
func (w *Worker) qualify(ctx context.Context) error {
	capabilities, err := w.config.Lanes.Driver.Capabilities(ctx)
	if err != nil {
		return err
	}
	if capabilities.Version == "" {
		return errNoVersion
	}
	w.qualLock.Lock()
	defer w.qualLock.Unlock()
	if w.qual.Holds(capabilities.Version) || w.probe != nil {
		return nil
	}
	life := w.life
	if life == nil {
		life = ctx
	}
	p := &probeRun{version: capabilities.Version}
	w.probe = p
	w.probes.Go(func() { w.runProbe(life, p) })
	return nil
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
// in the driver, which from then on qualifies that version alone. A probe that could not run leaves
// every surface unobserved, so the version runs degraded.
func (w *Worker) runProbe(life context.Context, p *probeRun) {
	// A probe that ends with no result of its own, because the version moved under it, leaves the driver
	// what the worker holds.
	ownResult := false
	defer func() {
		w.qualLock.Lock()
		w.probe = nil
		if !ownResult {
			// Hands the driver the last result the worker holds, which names no version this probe checked.
			w.handOverLocked()
		}
		w.qualLock.Unlock()
	}()
	ctx, cancel := context.WithTimeout(life, probeLimit)
	defer cancel()
	rt := w.config.Lanes
	spec := ports.ProbeSpec{
		Dir: filepath.Join(rt.StateDir, "probe"), TempDir: rt.probeTempDir(), Model: probeModel,
		Binary: rt.Binary, SignIn: w.signInKind(), Override: rt.Override,
	}
	q, err := rt.Driver.Probe(ctx, spec)
	switch {
	case life.Err() != nil:
		return
	case errors.Is(err, ports.ErrVersionMoved):
		w.config.Report(fmt.Errorf("the probe of %s: %w", p.version, err))
		return
	case err != nil:
		w.config.Report(fmt.Errorf("the probe of %s could not run: %w", p.version, err))
		q = lanes.Unprobed(w.driverName(), p.version, spec.SignIn, nil)
	}
	if err := w.keepQualification(life, q); err != nil {
		w.config.Report(fmt.Errorf("keeping the probe of %s: %w", q.Version, err))
		return
	}
	ownResult = true
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
	r.version, r.degraded = handle.Version, handle.Degraded
	r.handleLock.Unlock()
	r.state.mu.Lock()
	r.state.degraded = handle.Degraded
	r.state.mu.Unlock()
}

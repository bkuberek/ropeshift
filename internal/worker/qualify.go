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
	// qualifyNoteAfter is how long a lane waits on a probe before a person hears why.
	qualifyNoteAfter = time.Second
)

// errNoVersion is a driver that reports no installed version.
var errNoVersion = errors.New("the driver reports no version")

// probeRun is the one probe a worker runs at a time. Every lane that reaches a version with no
// result waits on it.
type probeRun struct {
	version string
	done    chan struct{}
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
		w.config.Lanes.Driver.Qualify(q)
	}
	return nil
}

// awaitQualified holds a lane's next process until the installed version has a probe's result on
// this machine (FR-DRV-2): a version is probed before any work runs on it, and a CLI that updated
// itself under a running worker is probed again before its first new process. The first lane to see
// a version starts the probe, and every lane waits on the same one. A wait that lasts reaches a
// person as a held lane.
func (w *Worker) awaitQualified(ctx context.Context, lease ledger.DesiredLease) error {
	noted := false
	for {
		done, err := w.qualify(ctx)
		if err != nil {
			// A version nobody can read fails the start where the driver reads it again.
			w.config.Report(fmt.Errorf("lane %s: reading the CLI's version: %w", lease.Lane, err))
			return nil
		}
		if done == nil {
			return nil
		}
		if !noted {
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(qualifyNoteAfter):
			}
			noted = true
			if err := w.hold(ctx, lease, lanes.HeldQualifying); err != nil {
				w.config.Report(fmt.Errorf("lane %s: %w", lease.Lane, err))
			}
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// qualify returns nil when a probe's result holds for the installed version, and otherwise what
// closes when the running probe ends, starting one when none runs. A probe of another version that
// ends leaves its waiters to look again. No result holds for an empty version, so a driver that read
// none is not probed: each probe would end only for the next to start.
func (w *Worker) qualify(ctx context.Context) (<-chan struct{}, error) {
	capabilities, err := w.config.Lanes.Driver.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	if capabilities.Version == "" {
		return nil, errNoVersion
	}
	w.qualLock.Lock()
	defer w.qualLock.Unlock()
	if w.qual.Holds(capabilities.Version) {
		return nil, nil
	}
	if w.probe != nil {
		return w.probe.done, nil
	}
	life := w.life
	if life == nil {
		life = ctx
	}
	p := &probeRun{version: capabilities.Version, done: make(chan struct{})}
	w.probe = p
	w.probes.Go(func() { w.runProbe(life, p) })
	return p.done, nil
}

// runProbe runs one probe and keeps its result: in the journal with the event that records it, and
// in the driver, which from then on qualifies that version alone. A probe that could not run leaves
// every surface unobserved, so the version runs degraded and nothing waits on it forever.
func (w *Worker) runProbe(life context.Context, p *probeRun) {
	defer func() {
		w.qualLock.Lock()
		w.probe = nil
		w.qualLock.Unlock()
		close(p.done)
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
	}
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

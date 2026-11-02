package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
)

// signInPollBeats is how many heartbeats pass between two reads of the doctor while a sign-in is
// out. Each one starts a process, and a person may take hours.
const signInPollBeats = 3

// signInKey names this worker's sign-in. Phase 1 runs one driver, so that is one key (D10).
func (w *Worker) signInKey() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	driver := ""
	if len(w.advertised.Drivers) != 0 {
		driver = w.advertised.Drivers[0].Driver
	}
	return parking.SignInKey(w.identity.WorkerID, driver)
}

// signIn reads the last state this worker kept.
func (w *Worker) signIn() lanes.SignInState {
	w.signLock.Lock()
	defer w.signLock.Unlock()
	return w.sign
}

// loadSignIn reads what an earlier process kept.
func (w *Worker) loadSignIn(ctx context.Context) error {
	state, err := w.journal.SignIn(ctx)
	if err != nil {
		return err
	}
	w.signLock.Lock()
	defer w.signLock.Unlock()
	w.sign = state
	return nil
}

// moveSignIn takes one step, and keeps it with what follows from it before anything acts on it, so
// the journal is what a restart finds. A change that says nothing writes nothing.
func (w *Worker) moveSignIn(ctx context.Context, change func(*lanes.SignInState) ([]ledger.UploadEvent, bool)) error {
	w.signLock.Lock()
	defer w.signLock.Unlock()
	next := w.sign
	events, changed := change(&next)
	if !changed {
		return nil
	}
	w.sign = next
	if forgetsLimit() {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	if !splitsSignal() || len(events) == 0 {
		return w.journal.SaveSignIn(ctx, next, events...)
	}
	if err := w.journal.SaveSignIn(ctx, next); err != nil {
		return err
	}
	crashpoint.Hit("worker.limit", "split")
	return w.journal.SaveSignIn(ctx, next, events...)
}

// readingOf merges what a session said into what was known. A reading can leave anything out, and
// what stops a turn carries no time, so a time stays until another replaces it.
func readingOf(last, heard lanes.UsageReading) lanes.UsageReading {
	if heard.Status != "" {
		last.Status = heard.Status
	}
	if heard.ResetsAtMs != 0 {
		last.ResetsAtMs = heard.ResetsAtMs
	}
	if heard.Window != "" {
		last.Window = heard.Window
	}
	if heard.UsedKnown {
		last.UsedKnown, last.UsedPercent = true, heard.UsedPercent
	}
	last.ReadAtMs = heard.ReadAtMs
	return last
}

// noteUsage keeps what a session read. Rejected reaches a limit at once, and an `allowed` from the
// lane sent to find out ends one.
func (r *laneRun) noteUsage(ctx context.Context, heard lanes.UsageReading) {
	heard.ReadAtMs = r.w.clock.Now().UnixMilli()
	err := r.w.moveSignIn(ctx, func(state *lanes.SignInState) ([]ledger.UploadEvent, bool) {
		next := readingOf(state.Last, heard)
		changed := next != state.Last
		state.Last = next
		return nil, changed
	})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
	switch heard.Status {
	case lanes.UsageRejected:
		r.reachedLimit(ctx)
	case lanes.UsageAllowed:
		r.limitPassed(ctx)
	}
}

// noteFailure reads what a failed turn or a retry says of a sign-in, and says whether it did. A
// category nobody named is a lane's own.
func (r *laneRun) noteFailure(ctx context.Context, category string) bool {
	switch category {
	case lanes.FailureUsageLimit:
		r.reachedLimit(ctx)
	case lanes.FailureSignIn:
		r.signedOut(ctx)
	default:
		return false
	}
	return true
}

// noteRetry reads one retry. A session retries a 429 by itself, so only one that keeps at it counts
// as a limit. An expired sign-in does not pass by itself, so one retry says enough.
func (r *laneRun) noteRetry(ctx context.Context, event ports.DriverEvent) {
	if event.Detail == lanes.FailureUsageLimit && event.Attempt < lanes.RetriesBeforeLimit {
		return
	}
	r.noteFailure(ctx, event.Detail)
}

// reachedLimit starts the wait for this sign-in, or moves it on, then parks what it can. With no
// reset to go by, this lane is the one that tries again. A probe that finds the limit still there
// waits longer.
func (r *laneRun) reachedLimit(ctx context.Context) {
	lane, now := r.lease.Lane, r.w.clock.Now().UnixMilli()
	base := r.state.usageBackoff()
	err := r.w.moveSignIn(ctx, func(state *lanes.SignInState) ([]ledger.UploadEvent, bool) {
		return nil, state.Reach(lane, now, base)
	})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", lane, err))
		return
	}
	crashpoint.Hit("worker.limit", "noted")
	r.w.parkOnSignIn(ctx)
}

// signedOut keeps the fact, then parks what it can.
func (r *laneRun) signedOut(ctx context.Context) {
	err := r.w.moveSignIn(ctx, func(state *lanes.SignInState) ([]ledger.UploadEvent, bool) {
		changed := !state.SignedOut
		state.SignedOut = true
		return nil, changed
	})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	crashpoint.Hit("worker.limit", "noted")
	r.w.parkOnSignIn(ctx)
}

// limitPassed is the probe's answer: a response came back, so the limit is over for every lane.
func (r *laneRun) limitPassed(ctx context.Context) {
	if !r.w.signIn().Passed(r.lease.Lane, r.w.clock.Now().UnixMilli()) {
		return
	}
	if err := r.w.endLimit(ctx); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}

// signalEvent is a worker's word on its own sign-in. It belongs to no lane.
func (w *Worker) signalEvent(kind parking.Kind) (ledger.UploadEvent, error) {
	payload, err := json.Marshal(parking.Signal{Kind: kind, Key: w.signInKey()})
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return w.event(ledger.UploadWakeSignal, lanes.Record{}, payload), nil
}

// endLimit clears a limit and says so in one journal write, because a state that moved first would
// leave a kill with every lane waiting on a word no worker would ever send.
func (w *Worker) endLimit(ctx context.Context) error {
	event, err := w.signalEvent(parking.UsageReset)
	if err != nil {
		return err
	}
	err = w.moveSignIn(ctx, func(state *lanes.SignInState) ([]ledger.UploadEvent, bool) {
		if !state.Limited {
			return nil, false
		}
		*state = lanes.SignInState{SignedOut: state.SignedOut}
		return []ledger.UploadEvent{event}, true
	})
	if err != nil {
		return err
	}
	crashpoint.Hit("worker.limit", "signaled")
	return w.Upload(ctx)
}

// endSignOut is the same for a sign-in that came back.
func (w *Worker) endSignOut(ctx context.Context) error {
	event, err := w.signalEvent(parking.SigninRestored)
	if err != nil {
		return err
	}
	err = w.moveSignIn(ctx, func(state *lanes.SignInState) ([]ledger.UploadEvent, bool) {
		if !state.SignedOut {
			return nil, false
		}
		state.SignedOut = false
		return []ledger.UploadEvent{event}, true
	})
	if err != nil {
		return err
	}
	crashpoint.Hit("worker.limit", "signaled")
	return w.Upload(ctx)
}

// parkOnSignIn parks every lane this worker runs that can park. One that cannot right now, such as
// one still starting, comes up on the next pass, and so does every lane after a restart. It holds
// the state through each lane's journal write: a wake journaled after the signal that ends a limit
// would wait on that signal for good, because a signal counts only for wakes stored before it.
func (w *Worker) parkOnSignIn(ctx context.Context) {
	key, runs, now := w.signInKey(), w.runs(), w.clock.Now().UnixMilli()
	w.signLock.Lock()
	defer w.signLock.Unlock()
	if !w.sign.Limited && !w.sign.SignedOut {
		return
	}
	for _, run := range runs {
		if request, ok := parkRequestOf(w.sign, key, run.lease.Lane, now); ok {
			run.parkReported(ctx, request)
		}
	}
}

// parkRequestOf says what one lane waits on. A probe whose time came is out finding the answer.
func parkRequestOf(state lanes.SignInState, key, lane string, now int64) (lanes.ParkRequest, bool) {
	switch {
	case state.SignedOut:
		return lanes.ParkRequest{Trigger: lanes.ParkSignIn, Expr: lanes.SignInWake(key), SkipNarrative: true}, true
	case lane == state.Probe && now >= state.ProbeAtMs:
		return lanes.ParkRequest{}, false
	case lane == state.Probe:
		return lanes.ParkRequest{Trigger: lanes.ParkUsageLimit, Expr: lanes.UsageWake(key, state.ProbeAtMs), SkipNarrative: true}, true
	}
	return lanes.ParkRequest{Trigger: lanes.ParkUsageLimit, Expr: lanes.UsageWake(key, state.ResetAtMs), SkipNarrative: true}, true
}

// tendSignIn is one pass over a sign-in that is out of service: it parks what came up since, and
// decides whether the wait is over. A worker that lost its control plane still keeps its state.
func (w *Worker) tendSignIn(ctx context.Context) {
	state := w.signIn()
	if !state.Limited && !state.SignedOut {
		return
	}
	w.parkOnSignIn(ctx)
	if state.Limited {
		if err := w.tendLimit(ctx, state); err != nil {
			w.config.Report(err)
		}
	}
	if state.SignedOut {
		if err := w.pollDoctor(ctx); err != nil {
			w.config.Report(err)
		}
	}
}

// tendLimit ends a limit whose time passed, or one whose probe will never answer, because nothing
// else wakes the lanes that wait on the signal alone. With no time to go by, it reads `/usage` once.
func (w *Worker) tendLimit(ctx context.Context, state lanes.SignInState) error {
	if state.ResetAtMs != 0 && w.clock.Now().UnixMilli() >= state.ResetAtMs {
		return w.endLimit(ctx)
	}
	if state.Probe == "" {
		return nil
	}
	record, err := w.journal.Lane(ctx, state.Probe)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && record.State == lanes.StateEnded) {
		return w.endLimit(ctx)
	}
	if err != nil {
		return err
	}
	if state.HintRead {
		return nil
	}
	return w.readHint(ctx, record)
}

// readHint keeps `/usage` on this machine. It is free text that can be out of date and can name
// local plugins, so nothing reads a time from it and it never reaches the ledger.
func (w *Worker) readHint(ctx context.Context, record lanes.Record) error {
	text, err := w.config.Lanes.Driver.ReadUsage(ctx, ports.SessionSpec{RunMode: record.RunMode})
	if err != nil && !errors.Is(err, ports.ErrUnsupported) {
		w.config.Report(fmt.Errorf("reading usage: %w", err))
	}
	return w.moveSignIn(ctx, func(state *lanes.SignInState) ([]ledger.UploadEvent, bool) {
		state.Hint, state.HintRead = text, true
		return nil, true
	})
}

// pollDoctor asks whether a sign-in came back. Only `signed_in` says so: a read that could not tell
// is no evidence of anything.
func (w *Worker) pollDoctor(ctx context.Context) error {
	now := w.clock.Now()
	w.signLock.Lock()
	due := now.Sub(w.polledAt) >= signInPollBeats*w.pause()
	if due {
		w.polledAt = now
	}
	w.signLock.Unlock()
	if !due {
		return nil
	}
	report, err := w.config.Lanes.Driver.Doctor(ctx)
	if err != nil {
		return err
	}
	if report.SignInStatus != lanes.SignedIn {
		return nil
	}
	return w.endSignOut(ctx)
}

// usageFacts is what admission reads of a sign-in.
func (w *Worker) usageFacts(lane string, woken bool) lanes.AdmissionFacts {
	state := w.signIn()
	return lanes.AdmissionFacts{
		SignedOut: state.SignedOut, Limited: state.Limited, NearLimit: state.Last.Near(w.clock.Now().UnixMilli()),
		Woken: woken, Probe: state.Limited && lane != "" && lane == state.Probe && w.clock.Now().UnixMilli() >= state.ProbeAtMs,
	}
}

package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// heldToken is the token of a session that outlived its worker.
const heldToken = "held-token"

// holdsToken gives the adopted session the token its hooks speak with.
func (w *worldRig) holdsToken(t *testing.T) {
	t.Helper()
	record := w.lane("lane-1")
	record.TokenDigest = lanes.TokenDigest(heldToken)
	if err := w.journal.SaveLane(t.Context(), record); err != nil {
		t.Fatal(err)
	}
}

// hookOnSocket is one hook reaching the worker's socket.
func (w *worldRig) hookOnSocket(t *testing.T, event lanes.HookEvent) {
	t.Helper()
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: heldToken, Hook: &event})
	if err != nil {
		t.Error(err)
		return
	}
	if reply := w.subject.AnswerLane(context.Background(), line); !reply.OK {
		t.Errorf("the hook was refused: %+v", reply)
	}
}

// spooledWhileDown writes what hooks spooled with no worker up, and starts the worker on it.
func (w *worldRig) spooledWhileDown(t *testing.T, events ...lanes.HookEvent) {
	t.Helper()
	var spool bytes.Buffer
	for _, event := range events {
		event.Lane, event.Generation, event.Subject = "lane-1", 1, "s"
		spool.Write(sealedLine(t, heldToken, event))
		spool.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(w.dir, "spool"), spool.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// signInWrites counts what a worker writes of its sign-in.
type signInWrites struct {
	ports.Journal
	writes atomic.Int64
}

func (j *signInWrites) SaveSignIn(ctx context.Context, state lanes.SignInState, events ...ledger.UploadEvent) error {
	j.writes.Add(1)
	return j.Journal.SaveSignIn(ctx, state, events...)
}

// failedHook lays down one StopFailure as a hook left it in the journal while no worker listened.
func (w *worldRig) failedHook(t *testing.T, detail string, atMs int64) {
	t.Helper()
	err := w.journal.MergeHook(t.Context(), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "StopFailure", Subject: "s", Detail: detail, AtMs: atMs})
	if err != nil {
		t.Fatal(err)
	}
}

// A StopFailure in the journal is a failure of a past turn. Reading it again when a worker adopts the
// session says nothing of the sign-in now, so it starts no limit and parks no lane.
func TestAReplayedStopFailureChangesNoSignIn(t *testing.T) {
	t.Parallel()
	for _, detail := range []string{lanes.FailureUsageLimit, lanes.FailureSignIn} {
		t.Run(detail, func(t *testing.T) {
			t.Parallel()
			base := newRig(t)
			journal := &signInWrites{Journal: base.journal}
			base.workerJournal = journal
			w := newWorldRigOn(t, base)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.failedHook(t, detail, 10)
			w.passesOnceAdopted(t)
			// A limit a lane found ends with that lane, so the state at the end does not show one that was started.
			if got := journal.writes.Load(); got != 0 {
				t.Errorf("an old failure wrote the sign-in %d times", got)
			}
			state, err := w.journal.SignIn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if state.Limited || state.SignedOut || state.Probe != "" {
				t.Errorf("an old failure moved the sign-in: %+v", state)
			}
		})
	}
}

// A StopFailure that came while the worker was down is unread. A headless turn's result names no reason, so
// the hook is all that says the sign-in failed, and the lane parks on it instead of ending failed.
func TestAFailureSpooledWhileDownIsNoted(t *testing.T) {
	t.Parallel()
	for _, detail := range []string{lanes.FailureUsageLimit, lanes.FailureSignIn} {
		t.Run(detail, func(t *testing.T) {
			t.Parallel()
			w := newWorldRig(t)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			w.desire("lane-1")
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.holdsToken(t)
			w.spooledWhileDown(t, lanes.HookEvent{Event: "StopFailure", Detail: detail, AtMs: 10})
			state, err := w.journal.SignIn(t.Context())
			if err != nil || state.Limited != (detail == lanes.FailureUsageLimit) || state.SignedOut != (detail == lanes.FailureSignIn) {
				t.Fatalf("a failure spooled while down left the sign-in %+v (%v)", state, err)
			}
			// Passes run while the driver takes the session over, and a stop sent then finds no session.
			w.config.Driver = &adoptingDriver{AgentDriver: w.driver, during: func() { time.Sleep(50 * time.Millisecond) }}
			w.driver.Play(script([]fake.DriverStep{{Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: "success", TotalUSD: 0.01}}}, []fake.DriverStep{{Hold: true}}))
			var record lanes.Record
			w.passUntil(t, "the lane never settled", func() bool {
				record = w.lane("lane-1")
				return record.State != lanes.StateRunning
			})
			// This rig has no work order, so the ledger can end the lane once it parked.
			if record.State != lanes.StateParked && record.Actual != lanes.Parked {
				t.Errorf("a failure spooled while down left the lane %s", record.State)
			}
		})
	}
}

// A Stop after the failure says the session went on, whichever the spool holds first.
func TestASpooledFailureWithALaterStopIsNotNoted(t *testing.T) {
	t.Parallel()
	failure := lanes.HookEvent{Event: "StopFailure", Detail: lanes.FailureSignIn, AtMs: 10}
	stop := lanes.HookEvent{Event: "Stop", AtMs: 20}
	for name, events := range map[string][]lanes.HookEvent{"failure first": {failure, stop}, "stop first": {stop, failure}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorldRig(t)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.holdsToken(t)
			w.spooledWhileDown(t, events...)
			if state, err := w.journal.SignIn(t.Context()); err != nil || state.SignedOut {
				t.Errorf("a failure the session got past left the sign-in %+v (%v)", state, err)
			}
		})
	}
}

// The socket serves before a worker's first pass adopts, so a failure can arrive that no run hears.
func TestAFailureBeforeTheAdoptionIsNoted(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	w.adoptedAt(t, lanes.Working, lanes.Idle)
	w.holdsToken(t)
	w.hookOnSocket(t, lanes.HookEvent{Event: "StopFailure", Subject: "s", Detail: lanes.FailureSignIn, AtMs: 10})
	if state, err := w.journal.SignIn(t.Context()); err != nil || !state.SignedOut {
		t.Errorf("a failure no run heard left the sign-in %+v (%v)", state, err)
	}
}

// A clear while no worker listened is a generation, though the cleared session compacted after it.
func TestAClearBeforeACompactionSurvivesAReplay(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	w.adoptedAt(t, lanes.Idle, lanes.Working)
	for at, detail := range []string{lanes.SourceClear, "compact"} {
		event := lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "SessionStart", Subject: "s2", SessionID: "s2", Detail: detail, AtMs: int64(10 + at)}
		if err := w.journal.MergeHook(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	w.passesOnceAdopted(t)
	if got := w.lane("lane-1").Generation; got != 2 {
		t.Errorf("the lane reads generation %d after a clear", got)
	}
}

// failingAdoption is a driver that cannot take a session over, and says so after a moment.
type failingAdoption struct {
	*fake.AgentDriver
	asked atomic.Bool
}

func (d *failingAdoption) Adopt(context.Context, ports.SessionHandle, ports.AdoptFrom) error {
	d.asked.Store(true)
	time.Sleep(50 * time.Millisecond)
	return ports.ErrNoSession
}

// A session that cannot be taken over while the sign-in is out parks on it, as one that can does, and is not
// lost as a restart's failure.
func TestAnAdoptionThatFailsWhileTheSignInIsOutParks(t *testing.T) {
	t.Parallel()
	tests := map[string]lanes.SignInState{
		"a usage limit": {Limited: true, ResetAtMs: time.UnixMilli(1_000_000).Add(time.Hour).UnixMilli()},
		"a sign-out":    {SignedOut: true},
	}
	for name, out := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorldRig(t)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			w.desire("lane-1")
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			if err := w.journal.SaveSignIn(t.Context(), out); err != nil {
				t.Fatal(err)
			}
			if err := w.subject.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			driver := &failingAdoption{AgentDriver: w.driver}
			w.config.Driver = driver
			var record lanes.Record
			w.passUntil(t, "the lane never settled", func() bool {
				record = w.lane("lane-1")
				return driver.asked.Load() && record.State != lanes.StateRunning
			})
			// This rig has no work order, so the ledger can end the lane once it parked.
			if record.Parking == nil || (record.State != lanes.StateParked && record.Actual != lanes.Parked) {
				t.Errorf("a failed adoption while the sign-in is out left the lane %s, parking %+v", record.State, record.Parking)
			}
		})
	}
}

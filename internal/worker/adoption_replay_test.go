package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
			// A limit has to hold still to be noted, and a sign-out does not pass by time.
			atMs := int64(10)
			if detail == lanes.FailureUsageLimit {
				atMs = w.clock.Now().Add(-time.Minute).UnixMilli()
			}
			w.spooledWhileDown(t, lanes.HookEvent{Event: "StopFailure", Detail: detail, AtMs: atMs})
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

// A failure spooled while the worker was down is as old as its hook, so a limit that passed since parks no
// lane, and one that still holds ends when it would have. A sign-out does not pass by time.
func TestASpooledFailureIsDatedAtItsHook(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		detail  string
		ago     time.Duration
		profile string
		// resetIn is when the last reading said the limit ends, from now, and zero where none said.
		resetIn time.Duration
		// waitFor is how long after the hook the limit's own wait ends.
		waitFor time.Duration
		holds   bool
	}{
		{name: "a limit whose backoff ended", detail: lanes.FailureUsageLimit, ago: 6 * time.Minute},
		{name: "a limit whose backoff ended this moment", detail: lanes.FailureUsageLimit, ago: lanes.DefaultUsageBackoff},
		{name: "a limit whose backoff holds", detail: lanes.FailureUsageLimit, ago: 2 * time.Minute, waitFor: lanes.DefaultUsageBackoff, holds: true},
		{
			name: "a limit whose profile's backoff holds", detail: lanes.FailureUsageLimit, ago: 8 * time.Minute,
			profile: profileAt + "[lifecycle]\nusage_backoff = \"15m\"\n", waitFor: 15 * time.Minute, holds: true,
		},
		{name: "a limit whose reset passed", detail: lanes.FailureUsageLimit, ago: 2 * time.Minute, resetIn: -time.Minute},
		{name: "a limit whose reset is ahead", detail: lanes.FailureUsageLimit, ago: 10 * time.Minute, resetIn: time.Hour, holds: true},
		{name: "a sign-out", detail: lanes.FailureSignIn, ago: 15 * time.Minute, holds: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			w := newWorldRig(t)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			if test.profile != "" {
				w.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", test.profile)
			}
			w.desire("lane-1")
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.holdsToken(t)
			now := w.clock.Now()
			at := now.Add(-test.ago)
			if test.resetIn != 0 {
				last := lanes.UsageReading{ResetsAtMs: now.Add(test.resetIn).UnixMilli(), ReadAtMs: at.UnixMilli()}
				if err := w.journal.SaveSignIn(t.Context(), lanes.SignInState{Last: last}); err != nil {
					t.Fatal(err)
				}
			}
			w.spooledWhileDown(t, lanes.HookEvent{Event: "StopFailure", Detail: test.detail, AtMs: at.UnixMilli()})
			state, err := w.journal.SignIn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got := state.Limited || state.SignedOut; got != test.holds {
				t.Fatalf("a failure %s old left the sign-in %+v", test.ago, state)
			}
			switch {
			case !test.holds:
				w.passesOnceAdopted(t)
				if record := w.lane("lane-1"); record.Parking != nil {
					t.Errorf("a limit that passed parked the lane: %+v", record.Parking)
				}
			case test.waitFor != 0:
				if want := at.Add(test.waitFor).UnixMilli(); state.ProbeAtMs != want {
					t.Errorf("the limit's wait ends at %d, want %d", state.ProbeAtMs, want)
				}
			case test.resetIn != 0:
				if want := now.Add(test.resetIn).UnixMilli(); state.ResetAtMs != want {
					t.Errorf("the limit ends at %d, want %d", state.ResetAtMs, want)
				}
			}
		})
	}
}

// What an adoption reads again of the turn that failed on the limit is as old as that turn's hook, so it starts
// no limit either, and the lane goes on from where the limit stopped it.
func TestALimitThatPassedWhileDownStopsNoLane(t *testing.T) {
	t.Parallel()
	failed := func(reason string) fake.DriverStep {
		return fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: reason, TotalUSD: 0.01}}
	}
	tests := map[string][]fake.DriverStep{
		"an end that names the limit": {failed(lanes.FailureUsageLimit)},
		"an end that names no reason": {failed("success")},
		"retries read again":          {{Retry: lanes.FailureUsageLimit, Attempt: lanes.RetriesBeforeLimit}, failed("success")},
		"a rejected reading read again": {
			{Limit: &lanes.UsageReading{Status: lanes.UsageRejected}}, failed("success"),
		},
	}
	for name, steps := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorldRig(t)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			w.desire("lane-1")
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.holdsToken(t)
			w.spooledWhileDown(t, lanes.HookEvent{
				Event: "StopFailure", Detail: lanes.FailureUsageLimit, AtMs: w.clock.Now().Add(-10 * time.Minute).UnixMilli(),
			})
			w.driver.Play(script(steps, []fake.DriverStep{{Hold: true}}))
			var record lanes.Record
			w.passUntil(t, "the lane neither went on nor settled", func() bool {
				record = w.lane("lane-1")
				return len(w.driver.Sent()) != 0 || record.State != lanes.StateRunning || record.Parking != nil
			})
			state, err := w.journal.SignIn(t.Context())
			if err != nil || state.Limited || state.SignedOut {
				t.Errorf("a limit that passed was dated now: %+v (%v)", state, err)
			}
			if record.State != lanes.StateRunning || record.Parking != nil {
				t.Fatalf("a limit that passed left the lane %s, parking %+v", record.State, record.Parking)
			}
			if sent := w.driver.Sent(); sent[0].Text != lanes.AfterLimit {
				t.Errorf("the lane was told %q", sent[0].Text)
			}
		})
	}
}

// The limit's failure need not be the last thing that happened while no worker listened. Its turn's end is
// still as old as its hook, and only a failure nothing followed leaves the lane to be told to go on.
func TestALimitAmongTurnsReadAgainIsDatedAtItsHook(t *testing.T) {
	t.Parallel()
	limited := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: lanes.FailureUsageLimit, TotalUSD: 0.02}}
	tests := []struct {
		name string
		// rested is an adoption of a lane at rest, where a turn a person typed leaves it.
		rested bool
		// around is the hook beside the failure, and after how long after the failure it fired.
		around string
		after  time.Duration
		steps  []fake.DriverStep
		sent   []string
	}{
		{name: "a turn typed at rest", rested: true, steps: []fake.DriverStep{limited}, sent: []string{lanes.AfterLimit}},
		{
			name: "a prompt since", around: lanes.EventUserPromptSubmit, after: time.Minute,
			steps: []fake.DriverStep{limited, {Hold: true}},
		},
		{
			name: "a Stop before it", around: lanes.EventStop, after: -time.Minute,
			steps: []fake.DriverStep{limited, {Hold: true}}, sent: []string{lanes.AfterLimit},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			base := newRig(t)
			journal := &signInWrites{Journal: base.journal}
			base.workerJournal = journal
			w := newWorldRigOn(t, base)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			w.desire("lane-1")
			state, from := lanes.Working, lanes.Idle
			if test.rested {
				state, from = lanes.Idle, lanes.Working
			}
			w.adoptedAt(t, state, from)
			w.holdsToken(t)
			failedAt := w.clock.Now().Add(-10 * time.Minute)
			events := []lanes.HookEvent{{Event: "StopFailure", Detail: lanes.FailureUsageLimit, AtMs: failedAt.UnixMilli()}}
			if test.around != "" {
				events = append(events, lanes.HookEvent{Event: test.around, AtMs: failedAt.Add(test.after).UnixMilli()})
			}
			w.spooledWhileDown(t, events...)
			w.driver.Play(script(test.steps, []fake.DriverStep{{Hold: true}}))
			w.passesOnceAdopted(t)
			for range 100 {
				time.Sleep(time.Millisecond)
				if _, err := w.subject.Reconcile(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if got := journal.writes.Load(); got != 0 {
				t.Errorf("a limit that passed wrote the sign-in %d times", got)
			}
			if record := w.lane("lane-1"); record.State != lanes.StateRunning || record.Parking != nil {
				t.Errorf("a limit that passed left the lane %s, parking %+v", record.State, record.Parking)
			}
			var sent []string
			for _, input := range w.driver.Sent() {
				sent = append(sent, input.Text)
			}
			if !slices.Equal(sent, test.sent) {
				t.Errorf("the lane was told %q, want %q", sent, test.sent)
			}
		})
	}
}

// A failure of an earlier generation ended another session's turn, and excuses no failure of this one's.
func TestAnEarlierGenerationsLimitExcusesNoFailure(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	w.desire("lane-1")
	w.adoptedAt(t, lanes.Working, lanes.Idle)
	record := w.lane("lane-1")
	record.Generation = 2
	if err := w.journal.SaveLane(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	w.failedHook(t, lanes.FailureUsageLimit, w.clock.Now().Add(-10*time.Minute).UnixMilli())
	w.driver.Leave("lane-1", 2, sessionAlive)
	effect := lanes.NewEffect(lanes.EffectSession, "lane-1", 2, "/worktrees/lane-1", "")
	if _, err := w.journal.BeginEffect(t.Context(), effect); err != nil {
		t.Fatal(err)
	}
	if err := w.journal.FinishEffect(t.Context(), effect.Key, lanes.EffectCompleted, ""); err != nil {
		t.Fatal(err)
	}
	w.driver.Play(script([]fake.DriverStep{{Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: "error_max_turns", TotalUSD: 0.01}}}, []fake.DriverStep{{Hold: true}}))
	w.passUntil(t, "a failed turn never ended the lane", func() bool { return w.lane("lane-1").State == lanes.StateEnded })
	if sent := w.driver.Sent(); len(sent) != 0 {
		t.Errorf("another generation's limit sent %+v", sent)
	}
}

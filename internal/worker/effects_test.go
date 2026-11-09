package worker_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/worker"
)

// unknownInput lays out a running lane with an input a crash left unknown.
func unknownInput(t *testing.T) (*worldRig, lanes.Effect) {
	t.Helper()
	w := newWorldRig(t)
	w.record("lane-1", lanes.StateRunning)
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	effect := w.effect(lanes.EffectInput, "lane-1", lanes.EffectStarted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{completed(0.25)}))
	return w, effect
}

// pass is one turn of a worker's loop: it converges, and the ledger hears what it did.
func (w *worldRig) pass(t *testing.T, subject *worker.Worker) {
	t.Helper()
	if err := subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (w *worldRig) raised(t *testing.T) []ledger.UnknownEffect {
	t.Helper()
	rows, err := control.NewEffects(w.store).List(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (w *worldRig) decide(t *testing.T, id string, outcome ledger.EffectOutcome) {
	t.Helper()
	if _, err := control.NewEffects(w.store).Decide(t.Context(), scope, ledger.EffectDecision{ID: id, Outcome: outcome}, member); err != nil {
		t.Fatal(err)
	}
}

func TestAnEffectNothingCanObserveReachesAPerson(t *testing.T) {
	w, effect := unknownInput(t)
	w.pass(t, w.subject)
	w.pass(t, w.subject)
	rows := w.raised(t)
	if len(rows) != 1 {
		t.Fatalf("%d effects raised, want one", len(rows))
	}
	got := rows[0]
	if got.Key != effect.Key || got.Kind != string(lanes.EffectInput) || got.Lane != "lane-1" || got.Attempt != 1 || !got.Waiting() {
		t.Errorf("raised %+v", got)
	}
}

func TestAnEffectThatCanBeObservedTroublesNobody(t *testing.T) {
	w := newWorldRig(t)
	w.record("lane-1", lanes.StateRunning)
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectStarted)
	w.driver.FailFind(errors.New("nothing to read yet"))
	w.pass(t, w.subject)
	if rows := w.raised(t); len(rows) != 0 {
		t.Errorf("raised %+v", rows)
	}
}

func TestAPersonsWordSettlesAnEffect(t *testing.T) {
	cases := []struct {
		name    string
		outcome ledger.EffectOutcome
		want    lanes.Effect
	}{
		{"it happened", ledger.EffectHappened, lanes.Effect{State: lanes.EffectCompleted, Detail: lanes.DetailSaidHappened, Attempts: 1}},
		{"it did not happen", ledger.EffectNotHappened, lanes.Effect{State: lanes.EffectFailed, Detail: lanes.DetailSaidNotHappened, Attempts: 1}},
		{"it runs again", ledger.EffectRetry, lanes.Effect{State: lanes.EffectStarted, Attempts: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, effect := unknownInput(t)
			w.pass(t, w.subject)
			id := w.raised(t)[0].ID
			w.decide(t, id, tc.outcome)
			w.pass(t, w.subject)
			got := w.state(effect)
			if got.State != tc.want.State || got.Detail != tc.want.Detail || got.Attempts != tc.want.Attempts {
				t.Errorf("effect %+v, want %+v", got, tc.want)
			}
			if err := w.subject.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
			rows := w.raised(t)
			if len(rows) != 1 || rows[0].SettledAt == 0 {
				t.Errorf("the ledger did not hear the worker acted: %+v", rows)
			}
			state, err := w.journal.Desired(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Effects) != 0 {
				t.Errorf("still offered: %+v", state.Effects)
			}
		})
	}
}

// A restart reads the same word from desired state until the ledger hears the worker acted. Run twice,
// a retry would count two attempts and could send the input twice.
func TestAWordAppliesOnceAcrossARestart(t *testing.T) {
	w, effect := unknownInput(t)
	w.pass(t, w.subject)
	w.decide(t, w.raised(t)[0].ID, ledger.EffectRetry)
	if err := w.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted := worker.New(w.link, w.journal, w.clock, worker.Config{
		Name: "test", Lanes: w.config, Spool: w.config.Spool,
	})
	if err := restarted.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted.Wait()
	if got := w.state(effect); got.Attempts != 2 {
		t.Errorf("attempts %d, want 2", got.Attempts)
	}
}

// A retry that a crash cuts short is unknown again, and only a person can say what happened to it.
func TestARetryACrashCutShortAsksAgain(t *testing.T) {
	w, effect := unknownInput(t)
	w.pass(t, w.subject)
	w.decide(t, w.raised(t)[0].ID, ledger.EffectRetry)
	w.pass(t, w.subject)
	if _, err := w.journal.MarkUnknown(t.Context()); err != nil {
		t.Fatal(err)
	}
	w.pass(t, w.subject)
	rows := w.raised(t)
	if len(rows) != 2 {
		t.Fatalf("%d effects raised, want two: %+v", len(rows), rows)
	}
	if rows[1].Attempt != 2 || rows[1].Key != effect.Key || !rows[1].Waiting() {
		t.Errorf("second attempt %+v", rows[1])
	}
}

// unknownMessage is a lane at work with a message a crash left half written: the journal holds the
// write as begun, and nothing shows whether the session took it.
func unknownMessage(t *testing.T) (*handoffRig, string) {
	t.Helper()
	r := heldBack(t)
	effect := lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", worker.MessageLabel+"m-1")
	if _, err := r.journal.BeginEffect(t.Context(), effect); err != nil {
		t.Fatal(err)
	}
	if _, err := r.journal.MarkUnknown(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.send(t, "m-1", messages.KindInstruction, nil)
	r.beat(t)
	if inputs := r.peerInputs(); len(inputs) != 0 {
		t.Fatalf("an unknown write repeated with nobody deciding: %+v", inputs)
	}
	rows, err := control.NewEffects(r.store).List(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d effects raised, want one", len(rows))
	}
	return r, rows[0].ID
}

func (r *handoffRig) decideEffect(t *testing.T, id string, outcome ledger.EffectOutcome) {
	t.Helper()
	if _, err := control.NewEffects(r.store).Decide(t.Context(), scope, ledger.EffectDecision{ID: id, Outcome: outcome}, member); err != nil {
		t.Fatal(err)
	}
	r.beat(t)
	r.beat(t)
}

func TestARetriedMessageIsWrittenAgain(t *testing.T) {
	r, id := unknownMessage(t)
	r.decideEffect(t, id, ledger.EffectRetry)
	if inputs := r.peerInputs(); len(inputs) != 1 {
		t.Fatalf("%d writes after a retry: %+v", len(inputs), inputs)
	}
	if got := r.message(t, "m-1"); got.State != messages.HandedOff {
		t.Errorf("state %s", got.State)
	}
	r.letGo()
}

func TestAMessageAPersonSawArrivedIsHandedOff(t *testing.T) {
	r, id := unknownMessage(t)
	r.decideEffect(t, id, ledger.EffectHappened)
	if inputs := r.peerInputs(); len(inputs) != 0 {
		t.Fatalf("a write a person saw arrive repeated: %+v", inputs)
	}
	if got := r.message(t, "m-1"); got.State != messages.HandedOff {
		t.Errorf("state %s, and the sender would read it as never delivered", got.State)
	}
	r.letGo()
}

func TestAMessageAPersonSaysNeverArrivedStaysUnwritten(t *testing.T) {
	r, id := unknownMessage(t)
	r.decideEffect(t, id, ledger.EffectNotHappened)
	if inputs := r.peerInputs(); len(inputs) != 0 {
		t.Fatalf("a person's no sent it anyway: %+v", inputs)
	}
	if got := r.message(t, "m-1"); got.State == messages.HandedOff {
		t.Errorf("state %s", got.State)
	}
	r.letGo()
}

package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// errWentAway is what a worker that died between two writes would have said of the second, had it lived.
var errWentAway = errors.New("the worker went away")

// reportLost is a journal that loses the move of a message to expired, which is the write that follows a failed
// delivery. A worker that reads its first failed delivery through it dies where a crash between the two would.
type reportLost struct {
	ports.Journal
}

func (j reportLost) MoveMessage(ctx context.Context, id string, from, to messages.State, event ledger.UploadEvent) (bool, error) {
	if to == messages.Expired {
		return false, errWentAway
	}
	return j.Journal.MoveMessage(ctx, id, from, to, event)
}

// restingAsk is a profile whose lane rests on its question for longer than a test lasts.
const restingAsk = profileAt + "[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"3m\"\nmax_restarts = 0\n"

// restsOnAQuestion is a lane at rest on one blocking question, on a version with no path for a peer, whose worker can
// not report a message it found undeliverable. It returns the question's ID.
func restsOnAQuestion(t *testing.T) (*laneRig, string) {
	t.Helper()
	base := newRig(t)
	base.workerJournal = reportLost{Journal: base.journal}
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", restingAsk)
	r.dispatched()
	r.driver.SetPeerChannel(false)
	r.driver.Play(r.asksThenNarrates("Which retry budget?", notCarried))
	r.start()
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.awaitAskWait()
	asked := r.questionList(t)
	if len(asked) != 1 {
		t.Fatalf("questions %+v", asked)
	}
	return r, asked[0].ID
}

// restartedOnItsSession stops the worker, which leaves its session running, and starts the next on the same journal.
// That worker finds the session the lane had, and has no path for a peer into it either. The lane's handoff
// narrative, if it is asked for, is the one the first session was told to give.
func (r *laneRig) restartedOnItsSession(t *testing.T) {
	t.Helper()
	spec := r.driver.Specs()[0]
	narrate := fake.DriverStep{Do: func(ports.SessionSpec) {
		r.callAs(spec, lanes.Request{Handoff: json.RawMessage(notCarried)})
	}}
	r.stopWorker()
	driver := fake.NewAgentDriver()
	t.Cleanup(func() { endSessions(t, driver) })
	driver.SetPeerChannel(false)
	driver.Leave("lane-1", 1, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	driver.Play(script([]fake.DriverStep{completed(0.01)}, []fake.DriverStep{narrate, completed(0.02)}))
	config := *r.config
	config.Driver = driver
	next := worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: &config, Report: func(err error) { t.Log(err) }})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject, r.driver = next, driver
	t.Cleanup(func() {
		driver.StopEverySession()
		next.Wait()
	})
}

// A worker that failed to write a message for want of a channel, and died before it reported that, settles the message
// when it starts again: the failed write is the journal's record that nothing was written, and no later attempt can
// write it. Without that the message waits at the worker until its window ends, and its sender reads nothing of why.
func TestARestartSettlesAMessageWhoseWriteFailedForWantOfAChannel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		kind   messages.Kind
		reason string
	}{
		{"an instruction", messages.KindInstruction, messages.ReasonUndeliverable},
		{"a decision the lane waits on", messages.KindAnswer, messages.ReasonLedgerFact},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, question := restsOnAQuestion(t)
			var id string
			if tt.kind == messages.KindAnswer {
				r.answerQuestion(t, question, "Three retries.")
				for _, m := range r.laneMessages(t) {
					if m.Kind == messages.KindAnswer {
						id = m.ID
					}
				}
			} else {
				id = r.send(t, "m-1", tt.kind, nil).ID
			}
			if id == "" {
				t.Fatal("no message was sent to the lane")
			}
			r.beat(t)
			failed, err := r.journal.Effect(t.Context(), lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", "message/"+id).Key)
			if err != nil || failed.State != lanes.EffectFailed {
				t.Fatalf("the write reads %+v, %v: the worker was to die after it failed", failed, err)
			}
			if got := r.message(t, id); got.State != messages.AtWorker {
				t.Fatalf("the message reads %s: the worker was to die before it reported it", got.State)
			}

			r.restartedOnItsSession(t)
			for range 3 {
				r.beat(t)
			}
			if got := r.message(t, id); got.State != messages.Expired || got.Reason != tt.reason {
				t.Errorf("the message reads %s (%s) after the restart, want expired (%s)", got.State, got.Reason, tt.reason)
			}
			waiting, err := r.journal.WaitingMessages(t.Context())
			if err != nil || len(waiting) != 0 {
				t.Errorf("the worker still holds %+v: %v", waiting, err)
			}
			for _, input := range r.driver.Sent() {
				if input.Peer || strings.Contains(input.Text, r.message(t, id).Body) {
					t.Errorf("the message was written after the restart: %+v", input)
				}
			}
			if tt.kind != messages.KindAnswer {
				return
			}
			r.awaitWoken(t)
			r.subject.Wait()
			if wake := r.wakeOf(t, 1); wake.Expr != lanes.QuestionWake([]string{question}) || wake.Open() {
				t.Errorf("the lane parked on %+v, want a fired wake on its answered question", wake)
			}
			if parked := r.parkingOf(t); parked == nil || parked.Trigger != lanes.ParkBlockingQuestion {
				t.Errorf("the lane parked with %+v", parked)
			}
		})
	}
}

// A restart reads the marker the worker wrote with a failed write, and never the words of an error: a write that failed
// another way, whose text only names the error, may have reached the session, and it waits as it did.
func TestARestartLeavesAFailedWriteThatOnlyNamesTheErrorAlone(t *testing.T) {
	t.Parallel()
	r, _ := restsOnAQuestion(t)
	r.restartedOnItsSession(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	effect := lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", "message/"+m.ID)
	if begun, err := r.journal.BeginEffect(t.Context(), effect); err != nil || !begun {
		t.Fatalf("begun %v: %v", begun, err)
	}
	if err := r.journal.FinishEffect(t.Context(), effect.Key, lanes.EffectFailed, "the pane said: "+ports.ErrNoChannel.Error()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		r.beat(t)
	}
	if got := r.message(t, m.ID); got.State != messages.AtWorker {
		t.Errorf("the message reads %s (%s), want it left at the worker", got.State, got.Reason)
	}
}

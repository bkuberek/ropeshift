package worker_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// briefAnsweredAtOnce puts the end of the turn a cleared session's brief began ahead of the record of that brief's
// delivery, as a session that answers at once does. It holds the delivery's record until the stream has read the end and
// reached its response write, and holds the stream there until the pass that delivered the brief has returned.
type briefAnsweredAtOnce struct {
	ports.Journal
	responses atomic.Int32
	answered  chan struct{}
	passed    chan struct{}
}

func newBriefAnsweredAtOnce(journal ports.Journal) *briefAnsweredAtOnce {
	return &briefAnsweredAtOnce{Journal: journal, answered: make(chan struct{}), passed: make(chan struct{})}
}

func (j *briefAnsweredAtOnce) hold(until <-chan struct{}) {
	select {
	case <-until:
	case <-time.After(20 * time.Second):
	}
}

// Append holds the response to the second turn the lane ends: the first is the one the clear ended.
func (j *briefAnsweredAtOnce) Append(ctx context.Context, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	if event.Type == ledger.UploadGenerationResponded && j.responses.Add(1) == 2 {
		close(j.answered)
		j.hold(j.passed)
	}
	return j.Journal.Append(ctx, event)
}

func (j *briefAnsweredAtOnce) FinishEffect(ctx context.Context, key string, state lanes.EffectState, detail string) error {
	if strings.HasSuffix(key, "/input/"+worker.BriefLabel(1)) {
		j.hold(j.answered)
	}
	return j.Journal.FinishEffect(ctx, key, state, detail)
}

// The end of the turn a cleared session's brief began can be read before the pass that sent the brief has recorded it. That
// end still ends the turn: a lane that reported stops, and one that did not is nudged, as it is when the end is read after
// the hand-off. A lane that took the hand-off after the end waited for a turn that had no end left to come.
func TestTheEndOfABriefsTurnReadBeforeItsHandOffIsDecided(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{"a session that reported": true, "a session that did not": false}
	for name, reports := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := newRig(t)
			journal := newBriefAnsweredAtOnce(base.journal)
			base.workerJournal = journal
			r := newLaneRigFor(t, base).dispatched()
			g := newGate()
			r.finishLater(g)
			finish := []fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.03)}
			turns := [][]fake.DriverStep{{{Bound: true}, g.step(), completed(0.01)}, finish}
			nudges := 0
			if !reports {
				turns, nudges = [][]fake.DriverStep{turns[0], {completed(0.02)}, finish}, 1
			}
			r.driver.Play(script(turns...))
			r.start()
			r.reach(t, g)
			r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
			r.cleared(t, clearedSession)
			g.release <- struct{}{}
			r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
			if _, err := r.subject.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			close(journal.passed)
			r.awaitOrderState(t, workorders.Acceptance)
			sent := 0
			for _, input := range r.driver.Sent() {
				if input.Text == lanes.Nudge {
					sent++
				}
			}
			if told := r.briefs(); told != 1 || sent != nudges {
				t.Errorf("%d briefs and %d nudges, want 1 and %d: %+v", told, sent, nudges, r.driver.Sent())
			}
		})
	}
}

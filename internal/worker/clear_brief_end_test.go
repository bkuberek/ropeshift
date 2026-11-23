package worker_test

import (
	"context"
	"strings"
	"sync"
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

// briefIsKey says an effect is the delivery of a cleared session's brief.
func briefIsKey(key string) bool { return strings.HasSuffix(key, "/input/"+worker.BriefLabel(1)) }

// briefAnsweredAtOnce puts the end of the turn a cleared session's brief began ahead of the record of that brief's
// delivery, as a session that answers at once does. It holds the delivery's record until the stream has reached a point in
// its read of that end, and holds the stream there until the pass that delivered the brief has returned. The point is one
// of two: the write of the turn's response, which follows the end's move of the lane, or the read of the digest, which
// follows the stream's read of what the generation owes and precedes the move.
type briefAnsweredAtOnce struct {
	ports.Journal
	beforeMove bool
	armed      atomic.Bool
	responses  atomic.Int32
	reached    chan struct{}
	passed     chan struct{}
	once       sync.Once
}

func newBriefAnsweredAtOnce(journal ports.Journal, beforeMove bool) *briefAnsweredAtOnce {
	return &briefAnsweredAtOnce{Journal: journal, beforeMove: beforeMove, reached: make(chan struct{}), passed: make(chan struct{})}
}

func (j *briefAnsweredAtOnce) hold(until <-chan struct{}) {
	select {
	case <-until:
	case <-time.After(20 * time.Second):
	}
}

// stopStream holds the stream at the point it has reached.
func (j *briefAnsweredAtOnce) stopStream() {
	j.once.Do(func() { close(j.reached) })
	j.hold(j.passed)
}

// Append holds the response to the second turn the lane ends: the first is the one the clear ended.
func (j *briefAnsweredAtOnce) Append(ctx context.Context, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	if !j.beforeMove && event.Type == ledger.UploadGenerationResponded && j.responses.Add(1) == 2 {
		j.stopStream()
	}
	return j.Journal.Append(ctx, event)
}

// Digest holds the first read of a digest once armed, which a run reads for the end of a turn when it holds none open.
func (j *briefAnsweredAtOnce) Digest(ctx context.Context, lane string, generation int64) (lanes.DigestRecord, error) {
	if j.beforeMove && j.armed.CompareAndSwap(true, false) {
		j.stopStream()
	}
	return j.Journal.Digest(ctx, lane, generation)
}

func (j *briefAnsweredAtOnce) FinishEffect(ctx context.Context, key string, state lanes.EffectState, detail string) error {
	if briefIsKey(key) {
		j.hold(j.reached)
	}
	return j.Journal.FinishEffect(ctx, key, state, detail)
}

// The end of the turn a cleared session's brief began can be read before the pass that sent the brief has recorded it. That
// end still ends the turn: a lane that reported stops, and one that did not is nudged, as it is when the end is read after
// the hand-off. A lane that took the hand-off after the end waited for a turn that had no end left to come, and one the
// stream read the debt for before the pass settled it, and counted the end after the pass recorded the hand-off, was
// never nudged.
func TestTheEndOfABriefsTurnReadBeforeItsHandOffIsDecided(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ reports, beforeMove bool }{
		"a session that reported":                       {reports: true},
		"a session that did not":                        {},
		"a session that reported, read before the move": {reports: true, beforeMove: true},
		"a session that did not, read before the move":  {beforeMove: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := newRig(t)
			journal := newBriefAnsweredAtOnce(base.journal, c.beforeMove)
			base.workerJournal = journal
			r := newLaneRigFor(t, base).dispatched()
			g := newGate()
			r.finishLater(g)
			finish := []fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.03)}
			turns := [][]fake.DriverStep{{{Bound: true}, g.step(), completed(0.01)}, finish}
			nudges := 0
			if !c.reports {
				turns, nudges = [][]fake.DriverStep{turns[0], {completed(0.02)}, finish}, 1
			}
			r.driver.Play(script(turns...))
			r.start()
			r.reach(t, g)
			r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
			r.cleared(t, clearedSession)
			g.release <- struct{}{}
			r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
			// The next end the run folds reads the digest from the journal.
			r.subject.ForgetDigest("lane-1")
			journal.armed.Store(true)
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

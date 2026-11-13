package worker

import (
	"context"
	"sync"

	"ropeshift.com/ropeshift/internal/ports"
)

// MessageLabel starts the effect key of a message's one write.
const MessageLabel = messageLabel

// BriefLabel names the effect of a cleared session's nth delivery of its brief, and MaxBriefAttempts is
// how many deliveries a generation's brief gets.
func BriefLabel(attempt int) string { return briefLabel(attempt) }

const MaxBriefAttempts = maxBriefAttempts

// WakeWaiting counts what a worker read for its orchestrator and has not told it. A debounce starts
// where a worker reads an item, so a test has to see that before it moves its clock.
func (w *Worker) WakeWaiting() int {
	w.wakeLock.Lock()
	defer w.wakeLock.Unlock()
	return len(w.wakes.Batch())
}

// Respawn is a pass that read a request for a lane's next generation, with the count it read. A pass reads
// the journal and then acts, so a count can arrive after the run has acted on it.
func (w *Worker) Respawn(ctx context.Context, lane string, asked int64) {
	if run := w.runOf(lane); run != nil {
		run.respawn(ctx, asked)
	}
}

// ReportPending says the worker holds a report the control plane has not kept.
func ReportPending(w *Worker) bool {
	report, _ := w.pendingReport()
	return report != nil
}

// Lost is a pass that found a lane's lease gone.
func (w *Worker) Lost(ctx context.Context, lane string) {
	if run := w.runOf(lane); run != nil {
		run.lost(ctx)
	}
}

// Crossing holds a run's generation still, as a run is for a caller that reads the generation in
// pieces and stalls between two of them. It then shows the readers the next generation, as a run that
// began one meanwhile would.
type Crossing struct {
	run        *laneRun
	generation int64
	handle     ports.SessionHandle
	once       *sync.Once
	reached    bool
	over       bool
	held       bool
}

// HoldGeneration takes the lock the generation's session and stop are read under, so a reader of them
// waits until the crossing lets go.
func (w *Worker) HoldGeneration(lane string) *Crossing {
	run := w.runOf(lane)
	run.handleLock.Lock()
	return &Crossing{
		run: run, generation: run.generation, handle: run.handle, once: run.stopOnce,
		reached: run.reached.Load(), over: run.sessionOver.Load(), held: true,
	}
}

// MoveOn leaves the run as enterGeneration leaves it: the next generation, with no session, a stop of its own,
// and nothing reached. It is called while the crossing holds the lock.
func (c *Crossing) MoveOn() {
	c.run.generation, c.run.handle, c.run.stopOnce = c.generation+1, ports.SessionHandle{}, newOnce()
	c.run.reached.Store(false)
	c.run.sessionOver.Store(false)
}

// Let lets readers go on, and does nothing once it has.
func (c *Crossing) Let() {
	if c.held {
		c.held = false
		c.run.handleLock.Unlock()
	}
}

// NextStopSpent waits for the stops the run asked for to finish, and reports whether the stop of the generation
// it moved on to was spent.
func (c *Crossing) NextStopSpent() bool {
	c.run.stopped.Wait()
	spent := true
	c.run.generationStop().Do(func() { spent = false })
	return spent
}

// Restore gives the run the generation it had, so it ends as it would have.
func (c *Crossing) Restore() {
	c.run.handleLock.Lock()
	defer c.run.handleLock.Unlock()
	c.run.generation, c.run.handle, c.run.stopOnce = c.generation, c.handle, c.once
	c.run.reached.Store(c.reached)
	c.run.sessionOver.Store(c.over)
}

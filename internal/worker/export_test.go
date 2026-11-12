package worker

import "context"

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

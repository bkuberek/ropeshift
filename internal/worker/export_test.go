package worker

// MessageLabel starts the effect key of a message's one write.
const MessageLabel = messageLabel

// WakeWaiting counts what a worker read for its orchestrator and has not told it. A debounce starts
// where a worker reads an item, so a test has to see that before it moves its clock.
func (w *Worker) WakeWaiting() int {
	w.wakeLock.Lock()
	defer w.wakeLock.Unlock()
	return len(w.wakes.Batch())
}

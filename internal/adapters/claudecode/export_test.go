package claudecode

import "time"

// EventBuffer is how many events a session's stream holds that nobody has read.
const EventBuffer = eventBuffer

// KillWait is how long a stop waits for a session's end after its last signal.
const KillWait = killWait

// ShortenProbeWaits bounds every wait of a probe to d, so a stand-in that never answers fails a
// check in a test's time, and returns what restores them.
func ShortenProbeWaits(d time.Duration) (restore func()) {
	saved := []time.Duration{probeSocketWait, probeTurnWait, probeReceiptWait, probeHoldWait, probeExitWait, probeScreenWait}
	probeSocketWait, probeTurnWait, probeReceiptWait, probeHoldWait, probeExitWait, probeScreenWait = d, d, d, d, d, d
	return func() {
		probeSocketWait, probeTurnWait, probeReceiptWait, probeHoldWait, probeExitWait, probeScreenWait =
			saved[0], saved[1], saved[2], saved[3], saved[4], saved[5]
	}
}

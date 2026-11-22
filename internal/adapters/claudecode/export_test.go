package claudecode

import "time"

// EventBuffer is how many events a session's stream holds that nobody has read.
const EventBuffer = eventBuffer

// KillWait is how long a stop waits for a session's end after its last signal.
const KillWait = killWait

// ProbePane names the probe's pane.
const ProbePane = probePane

// ShortenProbeWaits bounds every wait of a probe to d, so a stand-in that never answers fails a
// check in a test's time, and returns what restores them. The screen wait is never under 10 s,
// because the trust dialog alone takes some 2 s of settles and resends.
func ShortenProbeWaits(d time.Duration) (restore func()) {
	saved := []time.Duration{probeSocketWait, probeTurnWait, probeReceiptWait, probeHoldWait, probeExitWait, probeCloseWait, probeStopGrace, probeScreenWait}
	probeSocketWait, probeTurnWait, probeReceiptWait, probeHoldWait, probeExitWait, probeCloseWait, probeStopGrace, probeScreenWait = d, d, d, d, d, d, d, max(d, 10*time.Second)
	return func() {
		probeSocketWait, probeTurnWait, probeReceiptWait, probeHoldWait, probeExitWait, probeCloseWait, probeStopGrace, probeScreenWait =
			saved[0], saved[1], saved[2], saved[3], saved[4], saved[5], saved[6], saved[7]
	}
}

// ShortenEvidence bounds each file of a probe's evidence to n bytes, and returns what restores the bound.
func ShortenEvidence(n int) (restore func()) {
	saved := evidenceLimit
	evidenceLimit = n
	return func() { evidenceLimit = saved }
}

// EvidenceCut is the line that ends a file of evidence that was cut at its bound.
const EvidenceCut = evidenceCut

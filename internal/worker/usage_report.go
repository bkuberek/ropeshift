package worker

import (
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// signInUsage is what a report says of this worker's sign-in limit, so a member reads why its orchestrator does not run
// and when it could. A limit holds until its reset, as this worker's lanes wait, or with no reset until a probe finds
// it gone. At a limit, the reset is the one the worker waits on, and otherwise the last any session named.
func signInUsage(state lanes.SignInState, nowMs int64) *ledger.SignInUsage {
	last := state.Last
	holds := state.Limited && (state.ResetAtMs == 0 || nowMs < state.ResetAtMs)
	usage := &ledger.SignInUsage{Limited: holds, Window: last.Window, ResetsAtMs: last.ResetsAtMs, Used: last.UsedPercent, UsedKnown: last.UsedKnown}
	if holds {
		usage.ResetsAtMs = state.ResetAtMs
	}
	if last.ReadAtMs != 0 {
		usage.ReadAgeMs = max(nowMs-last.ReadAtMs, 0)
	}
	return usage
}

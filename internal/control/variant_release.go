//go:build !chaos

package control

// skipsParkedEvaluation is false: a normal build evaluates a wake in the parked commit.
func skipsParkedEvaluation() bool { return false }

// firesBeforeParked is false: a normal build fires only for a parked work order.
func firesBeforeParked() bool { return false }

// evaluatedAt is the time as the database read it.
func evaluatedAt(now int64) int64 { return now }

// firesFirstOnly is false: one signal reaches every lane that names it.
func firesFirstOnly() bool { return false }

// leavesApprovalWakes is false: a decision reaches whatever waits on it, in its own write.
func leavesApprovalWakes() bool { return false }

// stopsOnUnreadable is false: what a build cannot read stops nothing.
func stopsOnUnreadable() bool { return false }

// keepsNextEval is false: a false evaluation moves `next_eval_at` on.
func keepsNextEval() bool { return false }

// keepsWakesOpen is false: ending a lane retires its wake.
func keepsWakesOpen() bool { return false }

// resumesAnyClaim is false: a lane resumes only under its own worker's claim on it.
func resumesAnyClaim() bool { return false }

// refusesResumeRetry is false: a retried resume returns the generation it started.
func refusesResumeRetry() bool { return false }

// refusesParkedRepeat is false: a repeated parked commit is done.
func refusesParkedRepeat() bool { return false }

// readsOnePage is false: a wake reads on past one read's bound.
func readsOnePage() bool { return false }

// leavesMessages is false: the end of a generation decides what it left.
func leavesMessages() bool { return false }

// expiresSettled is false: a clock never moves a message a signal settled.
func expiresSettled() bool { return false }

// readsAnyLane is false: a worker speaks only for messages to the lane it names.
func readsAnyLane() bool { return false }

// leavesAnswers is false: the write that closes a question tells its lanes.
func leavesAnswers() bool { return false }

// signalsAnyKey is false: a worker speaks only for its own sign-in.
func signalsAnyKey() bool { return false }

// signalsAnyKind is false: a worker says only what a sign-in can say.
func signalsAnyKind() bool { return false }

// dropsPackEvent is false: every pack is on the log.
func dropsPackEvent() bool { return false }

// ignoresAutonomy is false: a claim no member made reads the level and the window.
func ignoresAutonomy() bool { return false }

// dropsAutonomyEvent is false: every change of autonomy is on the log.
func dropsAutonomyEvent() bool { return false }

// passesAnyVersion is false: a review passes only the versions it read.
func passesAnyVersion() bool { return false }

// releasesReviews is false: what a review asks waits for it to end.
func releasesReviews() bool { return false }

// takesInUnaccepted is false: nothing a lane wrote is a record until someone accepts it.
func takesInUnaccepted() bool { return false }

// movesPlanCursor is false: a commit a person accepted is not where a source stands.
func movesPlanCursor() bool { return false }

// keepsHoldTime is false: a hold that ends has ended, so a wake reads its question now.
func keepsHoldTime() bool { return false }

// rebuildsPack is false: a lane keeps its pack.
func rebuildsPack() bool { return false }

// keepsParkWanted is false: a parked commit answers what a policy asked.
func keepsParkWanted() bool { return false }

// ignoresPins is false: a notice finds every lane whose pack holds what changed.
func ignoresPins() bool { return false }

// leavesParkedLanes is false: a cancel ends a lane with no process, which no worker will report.
func leavesParkedLanes() bool { return false }

// endsAnyCanceledLane is false: only the worker that ran a lane says it ended.
func endsAnyCanceledLane() bool { return false }

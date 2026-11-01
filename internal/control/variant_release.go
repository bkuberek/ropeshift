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

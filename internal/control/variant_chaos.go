//go:build chaos

package control

import (
	"sync/atomic"
)

// Sabotage is a broken variant of parking and waking, used only to prove that a check can
// fail. The chaos build holds these seams, and a normal build has none.
type Sabotage struct {
	// NoParkedEvaluation commits `parked` and evaluates nothing.
	NoParkedEvaluation bool
	// FiresBeforeParked fires with the work order in any state, which spends a wake during
	// parking.
	FiresBeforeParked bool
	// EarlyMs evaluates ahead of the database's time.
	EarlyMs int64
	// FirstOnly stops at the first wake an event fires.
	FirstOnly bool
	// UnreadableStops turns an expression this build cannot read into a failure.
	UnreadableStops bool
	// KeepsNextEval never moves `next_eval_at`.
	KeepsNextEval bool
	// KeepsWakesOpen retires nothing when a lane ends.
	KeepsWakesOpen bool
}

var sabotage atomic.Pointer[Sabotage]

// Sabotaged installs a variant for this process.
func Sabotaged(s Sabotage) { sabotage.Store(&s) }

func current() Sabotage {
	if s := sabotage.Load(); s != nil {
		return *s
	}
	return Sabotage{}
}

func skipsParkedEvaluation() bool { return current().NoParkedEvaluation }

func firesBeforeParked() bool { return current().FiresBeforeParked }

func evaluatedAt(now int64) int64 { return now + current().EarlyMs }

func firesFirstOnly() bool { return current().FirstOnly }

func stopsOnUnreadable() bool { return current().UnreadableStops }

func keepsNextEval() bool { return current().KeepsNextEval }

func keepsWakesOpen() bool { return current().KeepsWakesOpen }

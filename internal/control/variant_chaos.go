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
	// ResumesAnyClaim starts a lane under a claim that is another worker's, or another lane's.
	ResumesAnyClaim bool
	// RefusesResumeRetry answers a retried resume as a stranger's, which a lost answer needs.
	RefusesResumeRetry bool
	// RefusesParkedRepeat answers a repeated parked commit as a stranger's.
	RefusesParkedRepeat bool
	// LeavesApprovalWakes decides a request and tells nothing that waits on it.
	LeavesApprovalWakes bool
	// ReadsOnePage stops a wake's read of a scope at its first page.
	ReadsOnePage bool
	// LeavesMessages ends a generation and decides nothing about what it left.
	LeavesMessages bool
	// ExpiresSettled lets a stale tick move a message a signal settled.
	ExpiresSettled bool
	// ReadsAnyLane lets a worker speak for a message to a lane it does not name.
	ReadsAnyLane bool
	// LeavesAnswers closes a question and tells no lane.
	LeavesAnswers bool
	// SignalsAnyKey takes a worker's word on any sign-in, and SignalsAnyKind any word at all.
	SignalsAnyKey, SignalsAnyKind bool
	// DropsPackEvent stores a pack the log never hears of.
	DropsPackEvent bool
	// IgnoresAutonomy grants a claim whatever a worker's level and window say.
	IgnoresAutonomy bool
	// DropsAutonomyEvent keeps a change of autonomy the log never hears of.
	DropsAutonomyEvent bool
	// PassesAnyVersion lets a review pass a contract version it never read.
	PassesAnyVersion bool
	// ReleasesReviews sends what a review asks up the ladder while it still reads.
	ReleasesReviews bool
	// TakesInUnaccepted reads a plan's commit before anyone accepted it.
	TakesInUnaccepted bool
	// MovesPlanCursor moves a source to the commit of an accepted plan.
	MovesPlanCursor bool
	// KeepsHoldTime names no holder and leaves the time a hold ran to, which no wake reads until then.
	KeepsHoldTime bool
	// RebuildsPack gives a claim that brings a lane back a new pack.
	RebuildsPack bool
	// KeepsParkWanted leaves what a policy asked of a lane that parked.
	KeepsParkWanted bool
	// IgnoresPins tells only the lanes a decision's scope reaches, and renews no pack.
	IgnoresPins bool
	// LeavesParkedLanes cancels work and leaves its parked lane reading parked for good.
	LeavesParkedLanes bool
	// EndsAnyCanceledLane takes any worker's word that a canceled lane ended.
	EndsAnyCanceledLane bool
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

func leavesApprovalWakes() bool { return current().LeavesApprovalWakes }

func stopsOnUnreadable() bool { return current().UnreadableStops }

func keepsNextEval() bool { return current().KeepsNextEval }

func keepsWakesOpen() bool { return current().KeepsWakesOpen }

func resumesAnyClaim() bool { return current().ResumesAnyClaim }

func refusesResumeRetry() bool { return current().RefusesResumeRetry }

func refusesParkedRepeat() bool { return current().RefusesParkedRepeat }

func readsOnePage() bool { return current().ReadsOnePage }

func leavesMessages() bool { return current().LeavesMessages }

func expiresSettled() bool { return current().ExpiresSettled }

func readsAnyLane() bool { return current().ReadsAnyLane }

func leavesAnswers() bool { return current().LeavesAnswers }

func signalsAnyKey() bool { return current().SignalsAnyKey }

func signalsAnyKind() bool { return current().SignalsAnyKind }

func dropsPackEvent() bool { return current().DropsPackEvent }

func ignoresAutonomy() bool { return current().IgnoresAutonomy }

func dropsAutonomyEvent() bool { return current().DropsAutonomyEvent }

func passesAnyVersion() bool { return current().PassesAnyVersion }

func releasesReviews() bool { return current().ReleasesReviews }

func takesInUnaccepted() bool { return current().TakesInUnaccepted }

func movesPlanCursor() bool { return current().MovesPlanCursor }

func keepsHoldTime() bool { return current().KeepsHoldTime }

func rebuildsPack() bool { return current().RebuildsPack }

func keepsParkWanted() bool { return current().KeepsParkWanted }

func ignoresPins() bool { return current().IgnoresPins }

func leavesParkedLanes() bool { return current().LeavesParkedLanes }

func endsAnyCanceledLane() bool { return current().EndsAnyCanceledLane }

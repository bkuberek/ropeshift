package messages

import "time"

// State says how far a message got. Each state claims only what its signal can prove: a write to
// a session proves that bytes were handed over, and nothing more.
type State string

// The delivery states.
const (
	// Queued is recorded in the ledger.
	Queued State = "queued"
	// AtWorker says the receiving worker holds it, which is where transport acknowledgement stops.
	AtWorker State = "at_worker"
	// HandedOff says it was written to the session's channel, and nothing confirms that.
	HandedOff State = "handed_off"
	// Admitted says the session took it into a turn, by the driver's signal. It does not say the
	// model acted on it.
	Admitted State = "admitted"
	// Replied says a reply correlated by `reply_to` arrived, the only semantic confirmation.
	Replied State = "replied"
	// Refused says the driver reported that the session refused it.
	Refused State = "refused"
	// Expired says it was not injected in time, or its generation ended.
	Expired State = "expired"
)

// States lists every state.
var States = []State{Queued, AtWorker, HandedOff, Admitted, Replied, Refused, Expired}

// The reasons a sender reads.
const (
	// ReasonTimedOut is a message that outlived its window.
	ReasonTimedOut = "timed_out"
	// ReasonGenerationEnded is a message whose session is gone. The sender decides whether to send
	// it again, because a message that has not entered a turn dies with its process.
	ReasonGenerationEnded = "generation_ended"
	// ReasonLedgerFact is a decision-bearing message that was never injected. Its fact reaches the
	// next generation in that generation's first prompt.
	ReasonLedgerFact = "ledger_fact"
)

// moves is the delivery table. An admission can pass `handed_off`, because a worker can die
// between its write and the record of it. An expired message can move on, because expiry is a
// clock's word and a signal that arrives late is a fact: that corrects what a sender reads.
var moves = map[State][]State{
	Queued:    {AtWorker, Expired},
	AtWorker:  {HandedOff, Admitted, Replied, Expired},
	HandedOff: {Admitted, Replied, Refused, Expired},
	Admitted:  {Replied},
	Expired:   {Admitted, Replied},
}

// CanMove reads the delivery table.
func CanMove(from, to State) bool {
	for _, next := range moves[from] {
		if next == to {
			return true
		}
	}
	return false
}

// Settled says no clock and no rollover moves this message: only a late signal can.
func (s State) Settled() bool { return s != Queued && s != AtWorker && s != HandedOff }

// Rolled is what the end of a generation does to one message.
type Rolled string

// The rows of the rollover table.
const (
	// RollNothing leaves a message as it stands.
	RollNothing Rolled = "nothing"
	// RollFact expires a decision-bearing message, whose fact the next generation is told.
	RollFact Rolled = "fact"
	// RollExpire expires a message with a reason its sender can see.
	RollExpire Rolled = "expire"
	// RollList leaves a message the next generation's brief lists as possibly unanswered.
	RollList Rolled = "list"
)

// Rollover is the design's table (FR-MSG-5). A message that was handed off and not admitted is
// handled as not yet injected, because a message that has not entered a turn dies with its process.
// Nothing is ever retargeted to the new generation, because it was written for a session with a
// different context. A lane with no next generation has nobody to tell.
func Rollover(state State, kind Kind, next bool) Rolled {
	switch {
	case state == Admitted && next:
		return RollList
	case state.Settled():
		return RollNothing
	case kind.DecisionBearing() && next:
		return RollFact
	}
	return RollExpire
}

// Window is how long a message stays useful, because a stale instruction is worse than none.
type Window struct {
	StaleAfter  time.Duration
	ExpireAfter time.Duration
}

// DefaultWindow is the design's table.
var DefaultWindow = Window{StaleAfter: 2 * time.Minute, ExpireAfter: 10 * time.Minute}

// Windows holds what a kind sets for itself (FR-MSG-4).
type Windows map[Kind]Window

// For reads a kind's window. A kind nobody set takes the default.
func (w Windows) For(kind Kind) Window {
	if window, ok := w[kind]; ok {
		return window
	}
	return DefaultWindow
}

// Tier says how old a message reads.
type Tier string

// The tiers.
const (
	TierFresh   Tier = "fresh"
	TierStale   Tier = "stale"
	TierExpired Tier = "expired"
)

// Tier reads an age. The last moment of a window is still inside it.
func (w Window) Tier(age time.Duration) Tier {
	switch {
	case age > w.ExpireAfter:
		return TierExpired
	case age >= w.StaleAfter:
		return TierStale
	}
	return TierFresh
}

package parking

// The causes a firing records.
const (
	// CauseParked is the transaction that commits `parked`.
	CauseParked = "parked"
	// CauseTick is the tick that reads what came due.
	CauseTick = "tick"
)

// The values of `fired_by` that close a wake with no firing, which appends no event. A
// wake left open would wake the next owner's work order, or stay in every later evaluation.
const (
	RetiredFinished     = "retired:finished"
	RetiredReassignment = "retired:reassignment"
	RetiredCanceled     = "retired:canceled"
)

// EventSignal records a fact only a signal can establish, such as a member resuming a lane.
const EventSignal = "wake.signal"

// SubjectSignal is what a signal's event is about.
const SubjectSignal = "wake_signal"

// Wake is one stored wake condition. A lane parks many times, so it has many, with at most
// one open. Expr stays text, because a newer build's expression must survive this one.
type Wake struct {
	ID   string
	Lane string
	Expr string
	// NextEvalAt is when the tick next reads this wake. Zero means only an event can change
	// its answer.
	NextEvalAt int64
	// FiredAt is when it closed, because it fired or because it was retired.
	FiredAt int64
	FiredBy string
	// StoredRev is the revision that stored it. A signal counts only after this, because a
	// person's resume or a reset from an earlier park says nothing about this one.
	StoredRev int64
}

// Open says the wake has neither fired nor been retired.
func (w Wake) Open() bool { return w.FiredAt == 0 }

// Draft is a wake to store. A client generates the ID, so a retry stores it once.
type Draft struct {
	ID   string
	Expr string
}

// Signal is a fact that has no record of its own.
type Signal struct {
	Kind Kind   `json:"kind"`
	Key  string `json:"key"`
}

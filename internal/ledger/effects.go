package ledger

import (
	"encoding/json"
	"errors"
	"slices"
)

// SubjectEffect names an effect in the log.
const SubjectEffect = "effect"

// What the ledger hears of an effect a crash left unknown and nothing can observe. The worker raises
// and settles one, and only a member decides it.
const (
	EventEffectUnknown = "effect.unknown"
	EventEffectDecided = "effect.decided"
	EventEffectSettled = "effect.settled"
)

// The upload types of those events.
const (
	// UploadEffectUnknown raises an effect for a person. It passes no fence, because a worker that
	// lost its lease still owes a person the truth about what it may have done.
	UploadEffectUnknown = "effect_unknown"
	// UploadEffectSettled says a worker acted on a person's word.
	UploadEffectSettled = "effect_settled"
)

// EffectOutcome is what a person says of an effect nothing can observe.
type EffectOutcome string

// The outcomes. EffectRetry gives at-least-once: the effect may have happened already.
const (
	EffectHappened    EffectOutcome = "happened"
	EffectNotHappened EffectOutcome = "not_happened"
	EffectRetry       EffectOutcome = "retry"
)

// The refusals of a decision. Each is a different instruction to a person.
var (
	// ErrEffectOutcome means a decision says nothing a person can say of an effect.
	ErrEffectOutcome = errors.New("an effect happened, did not happen, or runs again")
	// ErrEffectDecided means another decision came first.
	ErrEffectDecided = errors.New("this effect is already decided")
	// ErrEffectCannotRepeat means nothing is left that could run it again.
	ErrEffectCannotRepeat = errors.New("this effect's lane ended, so nothing can run it again")
)

// UnknownEffect is one attempt at an effect that waits for a person. A worker's journal holds the
// effect, and this is what a member can read and answer. ID stays the same while Attempt names which
// try it was, so a retry a crash cut short asks again.
type UnknownEffect struct {
	ID         string        `json:"id"`
	WorkerID   string        `json:"worker_id"`
	Key        string        `json:"key"`
	Kind       string        `json:"kind"`
	Lane       string        `json:"lane_id"`
	WorkOrder  string        `json:"work_order_id,omitempty"`
	Generation int64         `json:"generation"`
	Target     string        `json:"target,omitempty"`
	Attempt    int64         `json:"attempt"`
	RaisedAt   int64         `json:"raised_at"`
	Outcome    EffectOutcome `json:"outcome,omitempty"`
	Reason     string        `json:"reason,omitempty"`
	DecidedBy  string        `json:"decided_by,omitempty"`
	DecidedAt  int64         `json:"decided_at,omitempty"`
	SettledAt  int64         `json:"settled_at,omitempty"`
}

// Waiting says it still needs a person.
func (e UnknownEffect) Waiting() bool { return e.Outcome == "" }

// Offers lists what a person can say. An effect of a lane that ended has nothing left to run it again.
func (e UnknownEffect) Offers(laneEnded bool) []EffectOutcome {
	if !e.Waiting() {
		return nil
	}
	if laneEnded {
		return []EffectOutcome{EffectHappened, EffectNotHappened}
	}
	return []EffectOutcome{EffectHappened, EffectNotHappened, EffectRetry}
}

// EffectDecision is a person's word on one effect.
type EffectDecision struct {
	ID      string
	Outcome EffectOutcome
	Reason  string
}

// Replayed says this decision is the one on record, so a retry after a lost answer hears it again.
func (e UnknownEffect) Replayed(decision EffectDecision) bool {
	return !e.Waiting() && e.Outcome == decision.Outcome && e.Reason == decision.Reason
}

// Check says whether a person can say this now.
func (e UnknownEffect) Check(decision EffectDecision, laneEnded bool) error {
	if !slices.Contains([]EffectOutcome{EffectHappened, EffectNotHappened, EffectRetry}, decision.Outcome) {
		return ErrEffectOutcome
	}
	if !e.Waiting() {
		return ErrEffectDecided
	}
	if !slices.Contains(e.Offers(laneEnded), decision.Outcome) {
		return ErrEffectCannotRepeat
	}
	return nil
}

// EffectReport is what a worker says when it raises an effect.
type EffectReport struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Target  string `json:"target,omitempty"`
	Attempt int64  `json:"attempt"`
}

// EffectSettlement is what a worker says once it acted on a person's word.
type EffectSettlement struct {
	ID string `json:"id"`
}

// DesiredEffect is a person's word on its way to the worker that holds the effect. The control plane
// offers it until the worker says it acted.
type DesiredEffect struct {
	Project string        `json:"project,omitempty"`
	ID      string        `json:"id"`
	Key     string        `json:"key"`
	Attempt int64         `json:"attempt"`
	Outcome EffectOutcome `json:"outcome"`
}

// EffectDecided is what a decision's event holds.
type EffectDecided struct {
	Outcome   EffectOutcome `json:"outcome"`
	Reason    string        `json:"reason,omitempty"`
	DecidedBy string        `json:"decided_by"`
	DecidedAt int64         `json:"decided_at"`
}

// EffectSettled is what a settlement's event holds.
type EffectSettled struct {
	SettledAt int64 `json:"settled_at"`
}

// ReplayEffects rebuilds the projection from its events, so a store can be held to what it was told.
func ReplayEffects(events []Event) (map[string]UnknownEffect, error) {
	out := map[string]UnknownEffect{}
	for _, event := range events {
		switch event.Type {
		case EventEffectUnknown:
			var row UnknownEffect
			if err := json.Unmarshal(event.Data, &row); err != nil {
				return nil, err
			}
			out[row.ID] = row
		case EventEffectDecided:
			var decided EffectDecided
			if err := json.Unmarshal(event.Data, &decided); err != nil {
				return nil, err
			}
			row := out[event.Subject.ID]
			row.Outcome, row.Reason, row.DecidedBy, row.DecidedAt = decided.Outcome, decided.Reason, decided.DecidedBy, decided.DecidedAt
			out[event.Subject.ID] = row
		case EventEffectSettled:
			var settled EffectSettled
			if err := json.Unmarshal(event.Data, &settled); err != nil {
				return nil, err
			}
			row := out[event.Subject.ID]
			row.SettledAt = settled.SettledAt
			out[event.Subject.ID] = row
		}
	}
	return out, nil
}

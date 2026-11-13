package ledger

import (
	"encoding/json"
	"errors"
)

// ActorKind names who caused a change.
type ActorKind string

// The kinds of actor.
const (
	ActorMember       ActorKind = "member"
	ActorWorker       ActorKind = "worker"
	ActorLane         ActorKind = "lane"
	ActorOrchestrator ActorKind = "orchestrator"
	ActorSystem       ActorKind = "system"
	// ActorDelegate is a credential a member made for an agent outside ropeshift. Its ID is the member's ID,
	// a slash, and the credential's ID, so every event it causes names the member it acts for.
	ActorDelegate ActorKind = "delegate"
)

// Actor names the cause of a state change (FR-LEDGER-4).
type Actor struct {
	Kind ActorKind
	ID   string
}

// Subject names the record an event is about.
type Subject struct {
	Kind string
	ID   string
}

// EventDraft is an event before the Store gives it a revision and a time.
type EventDraft struct {
	ID          string
	Type        string
	Actor       Actor
	Subject     Subject
	Data        json.RawMessage
	OccurredAt  int64
	Traceparent string
}

// Event is one entry of the append-only log. Every state change traces to one.
// OccurredAt is reported data, and RecordedAt is database time.
type Event struct {
	Rev         int64
	ID          string
	Type        string
	Actor       Actor
	Subject     Subject
	Data        json.RawMessage
	OccurredAt  int64
	RecordedAt  int64
	Traceparent string
}

// ErrInvalidEvent marks a draft the log must not accept.
var ErrInvalidEvent = errors.New("invalid event")

// Validate refuses a draft that cannot trace a change to its cause.
func (d EventDraft) Validate() error {
	switch {
	case d.Type == "":
		return errors.Join(ErrInvalidEvent, errors.New("no type"))
	case d.Actor.Kind == "" || d.Actor.ID == "":
		return errors.Join(ErrInvalidEvent, errors.New("no actor"))
	case d.Subject.Kind == "" || d.Subject.ID == "":
		return errors.Join(ErrInvalidEvent, errors.New("no subject"))
	}
	return nil
}

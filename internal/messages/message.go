// Package messages is the narrow path for sending something to a lane: an answer, a notice, or an
// instruction. Most facts reach a lane through the ledger, and a message only tells it to look. A
// message never counts as a permission decision or as consent (FR-MSG-6), so this package holds
// no path to either.
package messages

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/ledger"
)

// EnvelopeVersion is the envelope this build writes (FR-MSG-1).
const EnvelopeVersion = 1

// MaxBody bounds a message, which only tells a lane to look.
const MaxBody = 32 << 10

// derivedBytes is how much of a hash names a message the ledger derives.
const derivedBytes = 16

// ErrInvalid means a message cannot be sent as it stands.
var ErrInvalid = errors.New("invalid message")

// ErrForeign means a report names a message addressed to another lane.
var ErrForeign = errors.New("this message is not this lane's")

// Kind says what a message is.
type Kind string

// The kinds of message.
const (
	KindAnswer         Kind = "answer"
	KindDecisionNotice Kind = "decision_notice"
	KindContractNotice Kind = "contract_notice"
	KindInstruction    Kind = "instruction"
	KindNotice         Kind = "notice"
)

var kinds = map[Kind]bool{KindAnswer: true, KindDecisionNotice: true, KindContractNotice: true, KindInstruction: true, KindNotice: true}

// DecisionBearing says a message carries a fact the ledger holds, so that fact reaches the next
// generation whatever happens to the message.
func (k Kind) DecisionBearing() bool {
	return k == KindAnswer || k == KindDecisionNotice || k == KindContractNotice
}

// SenderWorker is the kind of sender a worker is, on the one line it writes for a running turn.
const SenderWorker = "worker"

// Target is who a message is for. Generation is part of the address, because a message is written
// for a session that holds one context.
type Target struct {
	Lane       string `json:"lane"`
	Generation int64  `json:"generation"`
}

// Sender is who wrote a message. Seq counts what this sender sent (FR-MSG-1).
type Sender struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Seq  int64  `json:"seq"`
}

// Message is the envelope and what became of it. A worker's copy holds the same fields.
type Message struct {
	Envelope int    `json:"envelope"`
	ID       string `json:"id"`
	To       Target `json:"to"`
	From     Sender `json:"from"`
	// ReplyTo names what this message answers. An answer names its question.
	ReplyTo     string `json:"reply_to,omitempty"`
	Kind        Kind   `json:"kind"`
	Body        string `json:"body"`
	CreatedAt   int64  `json:"created_at"`
	StaleAt     int64  `json:"stale_at"`
	ExpiresAt   int64  `json:"expires_at"`
	Traceparent string `json:"traceparent,omitempty"`
	State       State  `json:"state"`
	// Reason says why a message ended where it did, so a sender can decide what to do.
	Reason string `json:"reason,omitempty"`
	// Reply is what a lane answered, and the only semantic confirmation there is (FR-MSG-3).
	Reply string `json:"reply,omitempty"`
	// CarriedIn is the generation that was told of this message in its first prompt.
	CarriedIn int64 `json:"carried_in,omitempty"`
}

// Draft is a message before the ledger stamps it. The client generates its ID, so a retry sends
// it once. A zero generation addresses the lane's current one.
type Draft struct {
	ID          string `json:"id"`
	To          Target `json:"to"`
	ReplyTo     string `json:"reply_to,omitempty"`
	Kind        Kind   `json:"kind"`
	Body        string `json:"body"`
	Traceparent string `json:"traceparent,omitempty"`
}

// New stamps a draft at a time and under a window.
func New(draft Draft, from Sender, now int64, window Window) (Message, error) {
	switch {
	case draft.ID == "":
		return Message{}, fmt.Errorf("%w: a message needs an ID", ErrInvalid)
	case !isLineID(draft.ID):
		return Message{}, fmt.Errorf("%w: an ID holds letters, digits, and %s", ErrInvalid, lineIDMarks)
	case draft.To.Lane == "":
		return Message{}, fmt.Errorf("%w: a message needs a lane", ErrInvalid)
	case !kinds[draft.Kind]:
		return Message{}, fmt.Errorf("%w: no kind %q", ErrInvalid, draft.Kind)
	case strings.TrimSpace(draft.Body) == "":
		return Message{}, fmt.Errorf("%w: a message needs a body", ErrInvalid)
	case len(draft.Body) > MaxBody:
		return Message{}, fmt.Errorf("%w: %d bytes", ErrInvalid, len(draft.Body))
	}
	return Message{
		Envelope: EnvelopeVersion, ID: draft.ID, To: draft.To, From: from, ReplyTo: draft.ReplyTo, Kind: draft.Kind, Body: draft.Body,
		CreatedAt: now, StaleAt: now + window.StaleAfter.Milliseconds(), ExpiresAt: now + window.ExpireAfter.Milliseconds(),
		Traceparent: draft.Traceparent, State: Queued,
	}, nil
}

// DerivedID names a message the ledger sends itself, from what it is about, so one fact sends one
// message, and a first line can carry the ID whatever those names hold.
func DerivedID(kind string, about ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(append([]string{kind}, about...), "\x00")))
	return kind + "-" + hex.EncodeToString(sum[:derivedBytes])
}

// Report is a worker's word on one message.
type Report struct {
	ID    string `json:"id"`
	State State  `json:"state"`
	// Reply is what a lane wrote, on a report that says `replied`.
	Reply string `json:"reply,omitempty"`
}

// Held is a worker's copy of a message. Age and the windows count from when the ledger read it,
// plus what the worker's own clock saw since, because the two clocks need not agree.
type Held struct {
	Message
	// InputID is what the session calls this message.
	InputID string `json:"input_id"`
	// AgeMs is how old it was at TakenMs, on the worker's clock.
	AgeMs         int64 `json:"age_ms"`
	TakenMs       int64 `json:"taken_ms"`
	StaleAfterMs  int64 `json:"stale_after_ms"`
	ExpireAfterMs int64 `json:"expire_after_ms"`
}

// Window is what this message's sender was given.
func (h Held) Window() Window {
	return Window{StaleAfter: time.Duration(h.StaleAfterMs) * time.Millisecond, ExpireAfter: time.Duration(h.ExpireAfterMs) * time.Millisecond}
}

// Age is how old a message is at a time on the worker's clock.
func (h Held) Age(nowMs int64) time.Duration {
	return time.Duration(h.AgeMs+max(nowMs-h.TakenMs, 0)) * time.Millisecond
}

// InputID is the form a session's input takes. A session names its own inputs with UUIDs, so a
// message's ID maps to one, and the same message always to the same one.
func InputID(id string) string {
	sum := sha256.Sum256([]byte(id))
	raw := sum[:16]
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	text := hex.EncodeToString(raw)
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

// The event a message's every change appends, and what it is about.
const (
	EventChanged   = "message.changed"
	SubjectMessage = "message"
)

// RecordEvent is one change of a message. It holds the whole message, as a question's does, so a
// replay takes the last word.
func RecordEvent(m Message, actor ledger.Actor) (ledger.EventDraft, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{
		Type: EventChanged, Actor: actor, Subject: ledger.Subject{Kind: SubjectMessage, ID: m.ID}, Data: raw, Traceparent: m.Traceparent,
	}, nil
}

// Replay rebuilds messages from events, in revision order.
func Replay(events []ledger.Event) (map[string]Message, error) {
	book := map[string]Message{}
	for _, e := range events {
		if e.Type != EventChanged {
			continue
		}
		var m Message
		if err := json.Unmarshal(e.Data, &m); err != nil {
			return nil, fmt.Errorf("event %d: %w", e.Rev, err)
		}
		book[m.ID] = m
	}
	return book, nil
}

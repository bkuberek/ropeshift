// Package conversations is the assistant's side of the conversation surface (ADR-0024): a member's
// conversation with the assistant that acts for them in one project, and what it holds. A conversation
// belongs to its member, at most one of a member's conversations in a project runs at a time, and what
// anyone wrote in one lives only in its entries and messages, so deleting the conversation removes it.
package conversations

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/ledger"
)

// ErrInvalid means a conversation or an entry cannot be recorded as it stands.
var ErrInvalid = errors.New("invalid conversation")

// ErrNotRunning means a conversation's session is not running, so it cannot compact or clear.
var ErrNotRunning = errors.New("the target is not running")

// ErrNoWorker means no worker can run the project's assistant.
var ErrNoWorker = errors.New("no worker can run the assistant")

// MaxID bounds a conversation's ID, which its client generates.
const MaxID = 64

// CheckID refuses an ID a client cannot have generated: letters, digits, '-', and '_', up to 64.
func CheckID(id string) error {
	if id == "" || len(id) > MaxID {
		return fmt.Errorf("%w: a conversation's ID is 1 to %d characters", ErrInvalid, MaxID)
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-' || r == '_') {
			return fmt.Errorf("%w: a conversation's ID holds letters, digits, '-', and '_'", ErrInvalid)
		}
	}
	return nil
}

// DefaultName is what a conversation is called until its first member message names it.
const DefaultName = "New conversation"

// MaxName bounds a name a member gives, in characters, and NameFromLength the name a first message gives.
const (
	MaxName        = 80
	NameFromLength = 60
)

// HistoryEntries is how many of a conversation's newest entries a fresh generation's first prompt holds.
const HistoryEntries = 50

// DesiredState is what the member asked of a conversation's session.
type DesiredState string

// The desired states.
const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
)

// ActualState is where a conversation's session stands, as its worker last reported it.
type ActualState string

// The actual states.
const (
	// Starting is a session asked for that its worker has not reported running.
	Starting ActualState = "starting"
	// Running is a session waiting for the member.
	Running ActualState = "running"
	// Working is a session in a turn.
	Working ActualState = "working"
	// Stopping is a session asked to stop that its worker has not reported stopped.
	Stopping ActualState = "stopping"
	// Stopped is a conversation with no session.
	Stopped ActualState = "stopped"
	// Failed is a session that could not start, with the reason in StoppedReason.
	Failed ActualState = "failed"
)

// ActualStates lists every actual state, in the order the API's state table names them.
var ActualStates = []ActualState{Starting, Running, Working, Stopping, Stopped, Failed}

// The reasons a conversation stopped.
const (
	ReasonMember     = "member"
	ReasonSwitched   = "another_conversation"
	ReasonIdle       = "idle"
	ReasonSignedOut  = "signed_out"
	ReasonDeleted    = "deleted"
	ReasonNoWorker   = "no_worker"
	ReasonFailed     = "could_not_start"
	ReasonSessionEnd = "session_ended"
)

// Conversation is one of a member's conversations with the assistant. Worker is empty until it is placed,
// RunLane until its first start, and each start names a run lane of its own.
type Conversation struct {
	ID            string       `json:"id"`
	Member        string       `json:"member_id"`
	Name          string       `json:"name"`
	Worker        string       `json:"worker_id,omitempty"`
	Desired       DesiredState `json:"desired_state"`
	Actual        ActualState  `json:"actual_state"`
	RunLane       string       `json:"run_lane,omitempty"`
	Generation    int64        `json:"current_generation"`
	StoppedReason string       `json:"stopped_reason,omitempty"`
	CreatedAt     int64        `json:"created_at"`
	LastActiveAt  int64        `json:"last_active_at"`
}

// Live says the conversation's session runs and can take a request: compact, clear, or a credential's call.
func (c Conversation) Live() bool {
	return c.Desired == DesiredRunning && (c.Actual == Running || c.Actual == Working)
}

// Running says the member asked for its session.
func (c Conversation) Running() bool { return c.Desired == DesiredRunning }

// CheckName reads a name a member gave: 1 to 80 characters once trimmed.
func CheckName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxName {
		return "", fmt.Errorf("%w: a conversation's name is 1 to %d characters", ErrInvalid, MaxName)
	}
	return name, nil
}

// NameFrom names a conversation by its first member message: its first 60 characters, on one line, cut
// at a word when the message runs longer.
func NameFrom(body string) string {
	text := strings.Join(strings.Fields(body), " ")
	runes := []rune(text)
	if len(runes) <= NameFromLength {
		return text
	}
	cut := string(runes[:NameFromLength])
	if runes[NameFromLength] != ' ' {
		if at := strings.LastIndexByte(cut, ' '); at > 0 {
			cut = cut[:at]
		}
	}
	return strings.TrimSpace(cut)
}

// assentWords are the messages that read as a person saying yes to a card.
var assentWords = map[string]bool{
	"yes": true, "y": true, "yep": true, "yeah": true, "ok": true, "okay": true, "sure": true, "do it": true, "go ahead": true,
	"approve": true, "approved": true, "confirm": true, "confirmed": true, "lgtm": true,
}

// Assent says a member's message reads as a yes while a card waits. It decides nothing: the message is
// still sent, and the conversation shows that a message never confirms a card (FR-MSG-6).
func Assent(body string, cardPending bool) bool {
	if !cardPending {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(body))
	text = strings.TrimRightFunc(text, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	return assentWords[text] || strings.HasPrefix(text, "yes ") || strings.HasPrefix(text, "ok ")
}

// The events of a conversation. None holds a body or a name, because a name comes from a message:
// bodies live only in entries and messages, which deleting a conversation removes (FR-LEDGER-4, ADR-0024).
const (
	EventChanged        = "conversation.changed"
	EventEntryAdded     = "conversation.entry_added"
	EventDeleted        = "conversation.deleted"
	SubjectConversation = "conversation"
)

// changedData is what a conversation's change records: everything but its name.
type changedData struct {
	ID            string       `json:"id"`
	Member        string       `json:"member_id"`
	Worker        string       `json:"worker_id,omitempty"`
	Desired       DesiredState `json:"desired_state"`
	Actual        ActualState  `json:"actual_state"`
	RunLane       string       `json:"run_lane,omitempty"`
	Generation    int64        `json:"current_generation"`
	StoppedReason string       `json:"stopped_reason,omitempty"`
}

func event(eventType, conversation string, data any, actor ledger.Actor) (ledger.EventDraft, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: SubjectConversation, ID: conversation}, Data: raw}, nil
}

// ChangedEvent records a conversation's change, with no name.
func ChangedEvent(c Conversation, actor ledger.Actor) (ledger.EventDraft, error) {
	return event(EventChanged, c.ID, changedData{
		ID: c.ID, Member: c.Member, Worker: c.Worker, Desired: c.Desired, Actual: c.Actual, RunLane: c.RunLane,
		Generation: c.Generation, StoppedReason: c.StoppedReason,
	}, actor)
}

// entryData is what an entry's event records: where it is, and nothing it says.
type entryData struct {
	Conversation string `json:"conversation"`
	ID           string `json:"id"`
	Seq          int64  `json:"seq"`
	Kind         Kind   `json:"kind"`
}

// EntryEvent records an entry's addition: its conversation, ID, seq, and kind.
func EntryEvent(e Entry, actor ledger.Actor) (ledger.EventDraft, error) {
	return event(EventEntryAdded, e.Conversation, entryData{Conversation: e.Conversation, ID: e.ID, Seq: e.Seq, Kind: e.Kind}, actor)
}

// DeletedEvent records a conversation's deletion.
func DeletedEvent(c Conversation, actor ledger.Actor) (ledger.EventDraft, error) {
	return event(EventDeleted, c.ID, struct {
		ID     string `json:"id"`
		Member string `json:"member_id"`
	}{ID: c.ID, Member: c.Member}, actor)
}

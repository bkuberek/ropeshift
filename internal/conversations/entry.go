package conversations

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Kind says what an entry is.
type Kind string

// The kinds of entry.
const (
	// KindMember is the member's message.
	KindMember Kind = "member"
	// KindReply is the assistant's text.
	KindReply Kind = "reply"
	// KindAction is an operation the assistant ran or prepared.
	KindAction Kind = "action"
	// KindMarker is a turn in the conversation's life: a start, a stop, a compaction, or a clear.
	KindMarker Kind = "marker"
)

var kinds = map[Kind]bool{KindMember: true, KindReply: true, KindAction: true, KindMarker: true}

// The markers.
const (
	MarkerStarted     = "started"
	MarkerStopped     = "stopped"
	MarkerCompacted   = "compacted"
	MarkerCleared     = "cleared"
	MarkerIdleStopped = "idle_stopped"
)

var markers = map[string]bool{MarkerStarted: true, MarkerStopped: true, MarkerCompacted: true, MarkerCleared: true, MarkerIdleStopped: true}

// The outcomes of an action.
const (
	// OutcomeRan is an operation the API ran as the assistant.
	OutcomeRan = "ran"
	// OutcomeCard is a consent operation the API recorded as a card for the member to confirm.
	OutcomeCard = "card"
)

// MaxBody bounds what one entry holds.
const MaxBody = 64 << 10

// Entry is one thing in a conversation, in order. Body is the text of a member's message or a reply, and
// Data what each kind needs beside it. BeforeClear marks what came before the last clear.
type Entry struct {
	Conversation string          `json:"conversation"`
	ID           string          `json:"id"`
	Seq          int64           `json:"seq"`
	Kind         Kind            `json:"kind"`
	Body         string          `json:"body,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
	BeforeClear  bool            `json:"before_clear,omitempty"`
	RecordedAt   int64           `json:"recorded_at"`
}

// MemberData is a member entry's data: the message it was sent as, and whether it read as assent.
type MemberData struct {
	Message string `json:"message"`
	Assent  bool   `json:"assent,omitempty"`
}

// ActionData is an action entry's data. Approval names the card a consent operation became, Rev the newest
// project event the call made, and PlanningLane the lane a start of a planning or adopt work order made.
type ActionData struct {
	Operation    string `json:"operation"`
	Label        string `json:"label"`
	Subject      string `json:"subject,omitempty"`
	Outcome      string `json:"outcome"`
	Approval     string `json:"approval,omitempty"`
	Rev          int64  `json:"rev,omitempty"`
	PlanningLane string `json:"planning_lane,omitempty"`
}

// MarkerData is a marker entry's data. A start and a clear name the run lane and the conversation's
// generation the session begins at.
type MarkerData struct {
	Marker     string `json:"marker"`
	RunLane    string `json:"run_lane,omitempty"`
	Generation int64  `json:"generation,omitempty"`
}

// Boundary says a marker begins a run: a start or a clear.
func (d MarkerData) Boundary() bool { return d.Marker == MarkerStarted || d.Marker == MarkerCleared }

// ReplyData is a reply entry's data: the vendor message it came from.
type ReplyData struct {
	Source string `json:"source,omitempty"`
}

// NewEntry builds an entry with no position: the ledger gives its seq and time.
func NewEntry(conversation, id string, kind Kind, body string, data any) (Entry, error) {
	switch {
	case conversation == "" || id == "":
		return Entry{}, fmt.Errorf("%w: an entry names its conversation and its ID", ErrInvalid)
	case !kinds[kind]:
		return Entry{}, fmt.Errorf("%w: no entry kind %q", ErrInvalid, kind)
	case (kind == KindMember || kind == KindReply) && strings.TrimSpace(body) == "":
		return Entry{}, fmt.Errorf("%w: a %s entry needs a body", ErrInvalid, kind)
	case len(body) > MaxBody:
		return Entry{}, fmt.Errorf("%w: an entry holds at most %d bytes", ErrInvalid, MaxBody)
	}
	switch d := data.(type) {
	case MarkerData:
		if kind != KindMarker || !markers[d.Marker] {
			return Entry{}, fmt.Errorf("%w: no marker %q", ErrInvalid, d.Marker)
		}
	case ActionData:
		if kind != KindAction || d.Operation == "" || (d.Outcome != OutcomeRan && d.Outcome != OutcomeCard) || (d.Outcome == OutcomeCard && d.Approval == "") {
			return Entry{}, fmt.Errorf("%w: an action names its operation, and a card its approval", ErrInvalid)
		}
	case nil:
		if kind == KindMarker || kind == KindAction {
			return Entry{}, fmt.Errorf("%w: a %s entry needs its data", ErrInvalid, kind)
		}
	}
	entry := Entry{Conversation: conversation, ID: id, Kind: kind, Body: body}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return Entry{}, err
		}
		entry.Data = raw
	}
	return entry, nil
}

// Marker reads a marker entry's marker.
func (e Entry) Marker() (string, error) {
	data, err := e.MarkerData()
	return data.Marker, err
}

// MarkerData reads a marker entry's data.
func (e Entry) MarkerData() (MarkerData, error) {
	var data MarkerData
	if e.Kind != KindMarker {
		return data, fmt.Errorf("%w: entry %s is a %s", ErrInvalid, e.ID, e.Kind)
	}
	err := json.Unmarshal(e.Data, &data)
	return data, err
}

// Action reads an action entry's data.
func (e Entry) Action() (ActionData, error) {
	var data ActionData
	err := json.Unmarshal(e.Data, &data)
	return data, err
}

// Member reads a member entry's data.
func (e Entry) Member() (MemberData, error) {
	var data MemberData
	if len(e.Data) == 0 {
		return data, nil
	}
	err := json.Unmarshal(e.Data, &data)
	return data, err
}

// ActiveAt says the entry counts as activity for the idle stop: a member's message, a reply, or a start.
func (e Entry) ActiveAt() bool {
	if e.Kind == KindMember || e.Kind == KindReply {
		return true
	}
	marker, err := e.Marker()
	return e.Kind == KindMarker && err == nil && marker == MarkerStarted
}

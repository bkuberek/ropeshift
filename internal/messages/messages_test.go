package messages_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
)

// TestDeliveryTable holds every pair of states to the design's table. A signal that arrives late
// corrects what a sender reads, so a state with no way forward is a state nothing can correct.
func TestDeliveryTable(t *testing.T) {
	legal := map[messages.State][]messages.State{
		messages.Queued:    {messages.AtWorker, messages.Expired},
		messages.AtWorker:  {messages.HandedOff, messages.Admitted, messages.Replied, messages.Expired},
		messages.HandedOff: {messages.Admitted, messages.Replied, messages.Refused, messages.Expired},
		messages.Admitted:  {messages.Replied},
		messages.Expired:   {messages.Admitted, messages.Replied},
		messages.Refused:   {},
		messages.Replied:   {},
	}
	if len(legal) != len(messages.States) {
		t.Fatalf("%d states in the table and %d in the package", len(legal), len(messages.States))
	}
	for _, from := range messages.States {
		for _, to := range messages.States {
			want := false
			for _, allowed := range legal[from] {
				want = want || allowed == to
			}
			if got := messages.CanMove(from, to); got != want {
				t.Errorf("%s to %s: %v, want %v", from, to, got, want)
			}
		}
	}
}

// TestSettledStates are what no tick and no rollover touches again.
func TestSettledStates(t *testing.T) {
	for state, want := range map[messages.State]bool{
		messages.Queued: false, messages.AtWorker: false, messages.HandedOff: false,
		messages.Admitted: true, messages.Replied: true, messages.Refused: true, messages.Expired: true,
	} {
		if got := state.Settled(); got != want {
			t.Errorf("%s: %v", state, got)
		}
	}
}

// TestRolloverTable is the design's table, row by row, with a lane that has a next generation and
// one that has none.
func TestRolloverTable(t *testing.T) {
	tests := []struct {
		name  string
		state messages.State
		kind  messages.Kind
		next  bool
		want  messages.Rolled
	}{
		{"an answer not yet injected reaches the next generation", messages.Queued, messages.KindAnswer, true, messages.RollFact},
		{"a notice not yet injected reaches the next generation", messages.AtWorker, messages.KindDecisionNotice, true, messages.RollFact},
		{"an instruction not yet injected expires", messages.Queued, messages.KindInstruction, true, messages.RollExpire},
		{"an instruction at the worker expires", messages.AtWorker, messages.KindNotice, true, messages.RollExpire},
		{"an answer handed off and not admitted died with the process", messages.HandedOff, messages.KindAnswer, true, messages.RollFact},
		{"an instruction handed off and not admitted expires", messages.HandedOff, messages.KindInstruction, true, messages.RollExpire},
		{"an admitted message with no reply is listed", messages.Admitted, messages.KindInstruction, true, messages.RollList},
		{"an admitted answer with no reply is listed", messages.Admitted, messages.KindAnswer, true, messages.RollList},
		{"a replied message needs nothing", messages.Replied, messages.KindInstruction, true, messages.RollNothing},
		{"a refused message needs nothing", messages.Refused, messages.KindAnswer, true, messages.RollNothing},
		{"an expired message needs nothing", messages.Expired, messages.KindAnswer, true, messages.RollNothing},
		{"an answer with no generation to reach expires", messages.Queued, messages.KindAnswer, false, messages.RollExpire},
		{"an admitted message with no generation to read a list expires nothing", messages.Admitted, messages.KindNotice, false, messages.RollNothing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := messages.Rollover(tt.state, tt.kind, tt.next); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDecisionBearingKinds(t *testing.T) {
	for kind, want := range map[messages.Kind]bool{
		messages.KindAnswer: true, messages.KindDecisionNotice: true, messages.KindInstruction: false, messages.KindNotice: false,
	} {
		if got := kind.DecisionBearing(); got != want {
			t.Errorf("%s: %v", kind, got)
		}
	}
}

// TestTiers are the design's table at its edges: 2 minutes is stale, and 10 is the last moment a
// message may be injected.
func TestTiers(t *testing.T) {
	window := messages.DefaultWindow
	for _, tt := range []struct {
		age  time.Duration
		want messages.Tier
	}{
		{0, messages.TierFresh},
		{2*time.Minute - time.Millisecond, messages.TierFresh},
		{2 * time.Minute, messages.TierStale},
		{10 * time.Minute, messages.TierStale},
		{10*time.Minute + time.Millisecond, messages.TierExpired},
	} {
		if got := window.Tier(tt.age); got != tt.want {
			t.Errorf("%v: %s, want %s", tt.age, got, tt.want)
		}
	}
}

// TestWindowsPerKind says a window is configured per kind, and a kind nobody set takes the default.
func TestWindowsPerKind(t *testing.T) {
	short := messages.Window{StaleAfter: time.Minute, ExpireAfter: 3 * time.Minute}
	windows := messages.Windows{messages.KindInstruction: short}
	if got := windows.For(messages.KindInstruction); got != short {
		t.Errorf("instruction %+v", got)
	}
	if got := windows.For(messages.KindAnswer); got != messages.DefaultWindow {
		t.Errorf("answer %+v", got)
	}
}

func message() messages.Message {
	return messages.Message{
		ID: "01JCB3", Kind: messages.KindAnswer, ReplyTo: "01JC9Z", Body: "Twice.",
		From: messages.Sender{Kind: "orchestrator", ID: "01JC7R", Seq: 118},
	}
}

// TestFirstLine is the design's line, byte for byte, and a message with no `reply_to` leaves the
// field out.
func TestFirstLine(t *testing.T) {
	window := messages.DefaultWindow
	got := messages.FirstLine(message(), 3*time.Minute+12*time.Second, window)
	want := "[ropeshift message 01JCB3 kind=answer age=3m12s tier=stale from=orchestrator reply_to=01JC9Z]"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	fresh := message()
	fresh.ReplyTo = ""
	got = messages.FirstLine(fresh, 2*time.Second, window)
	want = "[ropeshift message 01JCB3 kind=answer age=0m02s tier=fresh from=orchestrator]"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestRenderStatesTheAgeFirst holds the first visible line, and says what a message is not.
func TestRenderStatesTheAgeFirst(t *testing.T) {
	text := messages.Render(message(), time.Second, messages.DefaultWindow)
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || !strings.HasPrefix(first, "[ropeshift message 01JCB3 ") {
		t.Fatalf("first line %q", first)
	}
	if !strings.Contains(rest, "Twice.") {
		t.Errorf("no body in %q", rest)
	}
	if !strings.Contains(rest, "not a permission decision") {
		t.Errorf("a message does not say what it is not: %q", rest)
	}
}

// TestClip keeps a message whole in every case: inside the limit it changes nothing, and past it
// the text stays valid and says it was cut.
func TestClip(t *testing.T) {
	short := strings.Repeat("a", messages.MaxBody)
	if got := messages.Clip(short); got != short {
		t.Errorf("a body inside the limit changed: %d bytes", len(got))
	}
	for name, body := range map[string]string{
		"one byte over": short + "a",
		"on a boundary": strings.Repeat("é", messages.MaxBody),
		"three bytes":   strings.Repeat("€", messages.MaxBody),
	} {
		got := messages.Clip(body)
		if len(got) > messages.MaxBody || !utf8.ValidString(got) || !strings.HasSuffix(got, messages.Clipped) {
			t.Errorf("%s: %d bytes, ends %q", name, len(got), got[max(0, len(got)-30):])
		}
	}
}

// TestAMessageNeedsAnIDItsLineCanName refuses what a first line could not carry: a hook could never
// say such a message entered a turn, and a pane has no other signal.
func TestAMessageNeedsAnIDItsLineCanName(t *testing.T) {
	draft := messages.Draft{ID: "a.b", To: messages.Target{Lane: "lane-1"}, Kind: messages.KindNotice, Body: "Look."}
	if _, err := messages.New(draft, messages.Sender{}, 0, messages.DefaultWindow); !errors.Is(err, messages.ErrInvalid) {
		t.Errorf("an ID no line can name: %v", err)
	}
}

// TestDerivedIDIsOneAMessageCanCarry holds an ID the ledger makes to what a first line can name,
// whatever it was derived from, and to one ID for one pair.
func TestDerivedIDIsOneAMessageCanCarry(t *testing.T) {
	first := messages.DerivedID("answer", "question.one", "lane:1")
	if first != messages.DerivedID("answer", "question.one", "lane:1") {
		t.Error("the same pair gave two IDs")
	}
	if first == messages.DerivedID("answer", "question.one", "lane:2") || first == messages.DerivedID("notice", "question.one", "lane:1") {
		t.Error("two pairs gave one ID")
	}
	line := messages.FirstLine(messages.Message{ID: first, Kind: messages.KindAnswer, From: messages.Sender{Kind: "member"}}, 0, messages.DefaultWindow)
	if got := messages.ReadFirstLine(line); got != first {
		t.Errorf("a line named %q", got)
	}
}

// TestReadFirstLine is the one thing a hook takes from a prompt. Only a line the worker wrote names
// a message, and text further down a prompt names nothing.
func TestReadFirstLine(t *testing.T) {
	line := "[ropeshift message 01JCB3 kind=answer age=0m02s tier=fresh from=orchestrator reply_to=01JC9Z]"
	for _, tt := range []struct {
		name, prompt, want string
	}{
		{"a message", line + "\nTwice.", "01JCB3"},
		{"a line alone", line, "01JCB3"},
		{"a typed prompt", "Go on with the retry.", ""},
		{"an empty prompt", "", ""},
		{"a line further down", "Look at this.\n" + line, ""},
		{"a line with no end", "[ropeshift message 01JCB3 kind=answer", ""},
		{"a line with no ID", "[ropeshift message ]", ""},
		{"a line with a mark in its ID", "[ropeshift message 01/../CB3 kind=answer]", ""},
		{"text after the line's end", line + " and more\nTwice.", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := messages.ReadFirstLine(tt.prompt); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestInputIDIsAStableUUID gives a socket line the form a session's own IDs take, and the same
// message the same ID, so a replay finds it after a restart.
func TestInputIDIsAStableUUID(t *testing.T) {
	first, second := messages.InputID("01JCB3"), messages.InputID("01JCB3")
	if first != second {
		t.Errorf("%s and %s", first, second)
	}
	if first == messages.InputID("01JCB4") {
		t.Error("two messages share an input")
	}
	parts := strings.Split(first, "-")
	if len(parts) != 5 || len(first) != 36 || parts[2][0] != '4' {
		t.Errorf("%s is not a version 4 form", first)
	}
}

func TestNewRefuses(t *testing.T) {
	good := messages.Draft{ID: "m", To: messages.Target{Lane: "lane"}, Kind: messages.KindInstruction, Body: "Stop."}
	if _, err := messages.New(good, messages.Sender{Kind: "member", ID: "m"}, 1000, messages.DefaultWindow); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*messages.Draft){
		"no ID":     func(d *messages.Draft) { d.ID = "" },
		"no lane":   func(d *messages.Draft) { d.To.Lane = "" },
		"no body":   func(d *messages.Draft) { d.Body = "  " },
		"bad kind":  func(d *messages.Draft) { d.Kind = "chat" },
		"long body": func(d *messages.Draft) { d.Body = strings.Repeat("x", messages.MaxBody+1) },
	} {
		t.Run(name, func(t *testing.T) {
			draft := good
			change(&draft)
			if _, err := messages.New(draft, messages.Sender{Kind: "member", ID: "m"}, 1000, messages.DefaultWindow); !errors.Is(err, messages.ErrInvalid) {
				t.Errorf("error %v", err)
			}
		})
	}
}

// TestNewStampsItsWindow carries the window on the message, so a worker reads no configuration.
func TestNewStampsItsWindow(t *testing.T) {
	draft := messages.Draft{ID: "m", To: messages.Target{Lane: "lane", Generation: 4}, Kind: messages.KindInstruction, Body: "Stop."}
	got, err := messages.New(draft, messages.Sender{Kind: "member", ID: "m", Seq: 3}, 1000, messages.DefaultWindow)
	if err != nil {
		t.Fatal(err)
	}
	if got.Envelope != messages.EnvelopeVersion || got.State != messages.Queued || got.CreatedAt != 1000 {
		t.Errorf("%+v", got)
	}
	if got.StaleAt != 1000+(2*time.Minute).Milliseconds() || got.ExpiresAt != 1000+(10*time.Minute).Milliseconds() {
		t.Errorf("%+v", got)
	}
}

// TestReplayKeepsTheLastWord rebuilds a message from its events, and reads past what it does not
// know, because an event log holds every kind of record.
func TestReplayKeepsTheLastWord(t *testing.T) {
	first := message()
	first.State = messages.Queued
	second := first
	second.State, second.Reason = messages.Expired, messages.ReasonGenerationEnded
	event := func(rev int64, m messages.Message) ledger.Event {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return ledger.Event{Rev: rev, Type: messages.EventChanged, Subject: ledger.Subject{Kind: messages.SubjectMessage, ID: m.ID}, Data: raw}
	}
	book, err := messages.Replay([]ledger.Event{
		event(1, first), {Rev: 2, Type: "note.added"}, event(3, second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := book[first.ID]; got.State != messages.Expired || got.Reason != messages.ReasonGenerationEnded {
		t.Errorf("%+v", got)
	}
	if _, err := messages.Replay([]ledger.Event{{Type: messages.EventChanged, Data: []byte("{")}}); err == nil {
		t.Error("a broken event replayed")
	}
}

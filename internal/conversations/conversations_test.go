package conversations_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/ledger"
)

func TestAssentIsAShortYesWhileACardWaits(t *testing.T) {
	for _, body := range []string{
		"yes", "Y", "yep", "yeah", "ok", "okay", "sure", "do it", "go ahead", "approve", "approved", "confirm",
		"confirmed", "LGTM", "  Yes!  ", "ok.", "yes ship it", "ok then", "Go ahead!!", "sure?",
	} {
		if !conversations.Assent(body, true) {
			t.Errorf("%q with a card waiting reads as no assent", body)
		}
	}
	for _, body := range []string{"no", "yesterday", "okay so what changed", "not yet", "maybe", "yeses", "oki", "do it later please", "yes, but why"} {
		if conversations.Assent(body, true) {
			t.Errorf("%q reads as assent", body)
		}
	}
	if conversations.Assent("yes", false) {
		t.Error("assent with no card waiting")
	}
}

func TestTheFirstMessageNamesAConversation(t *testing.T) {
	cases := map[string]string{
		"Why did the harbor-web merge fail?": "Why did the harbor-web merge fail?",
		"  Line one\n\nline two  ":           "Line one line two",
		strings.Repeat("word ", 20):          strings.TrimSpace(strings.Repeat("word ", 12)),
		strings.Repeat("x", 70):              strings.Repeat("x", 60),
	}
	for body, want := range cases {
		if got := conversations.NameFrom(body); got != want {
			t.Errorf("NameFrom(%q) = %q, want %q", body, got, want)
		}
		if got := conversations.NameFrom(body); len([]rune(got)) > conversations.NameFromLength {
			t.Errorf("NameFrom(%q) is %d characters", body, len([]rune(got)))
		}
	}
}

func TestANameIsOneToEightyCharacters(t *testing.T) {
	for _, name := range []string{"", "   ", strings.Repeat("a", 81)} {
		if _, err := conversations.CheckName(name); !errors.Is(err, conversations.ErrInvalid) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	got, err := conversations.CheckName("  Merges  ")
	if err != nil || got != "Merges" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := conversations.CheckName(strings.Repeat("é", 80)); err != nil {
		t.Errorf("eighty characters: %v", err)
	}
}

func TestOnlyARunningSessionCompactsOrClears(t *testing.T) {
	for _, actual := range []conversations.ActualState{conversations.Running, conversations.Working} {
		if !(conversations.Conversation{Desired: conversations.DesiredRunning, Actual: actual}).Live() {
			t.Errorf("%s does not read as live", actual)
		}
	}
	for _, actual := range []conversations.ActualState{conversations.Starting, conversations.Stopping, conversations.Stopped, conversations.Failed} {
		if (conversations.Conversation{Desired: conversations.DesiredRunning, Actual: actual}).Live() {
			t.Errorf("%s reads as live", actual)
		}
	}
	if (conversations.Conversation{Desired: conversations.DesiredStopped, Actual: conversations.Running}).Live() {
		t.Error("a stopped conversation reads as live")
	}
}

// An event about a conversation names it and never holds what anyone wrote in it, so deleting the
// conversation removes every body (ADR-0024).
func TestNoEventHoldsABodyOrAName(t *testing.T) {
	record := conversations.Conversation{
		ID: "01JC", Member: "m1", Name: "Secret harbor plans", Desired: conversations.DesiredRunning, Actual: conversations.Starting,
		Worker: "w1", RunLane: "01JD", Generation: 1, LastActiveAt: 5,
	}
	actor := ledger.Actor{Kind: ledger.ActorMember, ID: "m1"}
	changed, err := conversations.ChangedEvent(record, actor)
	if err != nil {
		t.Fatal(err)
	}
	entry := conversations.Entry{Conversation: "01JC", ID: "e1", Seq: 1, Kind: conversations.KindMember, Body: "the body text",
		Data: json.RawMessage(`{"message":"m-1","assent":true}`)}
	added, err := conversations.EntryEvent(entry, actor)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := conversations.DeletedEvent(record, actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []ledger.EventDraft{changed, added, deleted} {
		if err := event.Validate(); err != nil {
			t.Errorf("%s: %v", event.Type, err)
		}
		data := string(event.Data)
		if strings.Contains(data, "Secret") || strings.Contains(data, "body text") {
			t.Errorf("%s holds %s", event.Type, data)
		}
		if event.Subject.Kind != conversations.SubjectConversation || event.Subject.ID != "01JC" {
			t.Errorf("%s is about %+v", event.Type, event.Subject)
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(added.Data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"conversation", "id", "seq", "kind"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("the entry's event lacks %s: %s", key, added.Data)
		}
	}
	if len(fields) != 4 {
		t.Errorf("the entry's event holds more than it names: %s", added.Data)
	}
}

func TestAnEntryHoldsWhatItsKindNeeds(t *testing.T) {
	if _, err := conversations.NewEntry("c", "e", conversations.KindMember, "", nil); !errors.Is(err, conversations.ErrInvalid) {
		t.Errorf("a member entry with no body: %v", err)
	}
	if _, err := conversations.NewEntry("c", "e", "chatter", "hi", nil); !errors.Is(err, conversations.ErrInvalid) {
		t.Errorf("an unknown kind: %v", err)
	}
	if _, err := conversations.NewEntry("c", "e", conversations.KindMarker, "", conversations.MarkerData{Marker: "nap"}); !errors.Is(err, conversations.ErrInvalid) {
		t.Errorf("an unknown marker: %v", err)
	}
	marker, err := conversations.NewEntry("c", "e", conversations.KindMarker, "", conversations.MarkerData{Marker: conversations.MarkerCleared})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := marker.Marker(); got != conversations.MarkerCleared {
		t.Errorf("marker %q", got)
	}
	action, err := conversations.NewEntry("c", "e", conversations.KindAction, "", conversations.ActionData{
		Operation: "lane.start", Label: "Start a lane", Subject: "work_order:WO-1", Outcome: conversations.OutcomeRan, Rev: 9, PlanningLane: "L-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := action.Action()
	if err != nil || got.PlanningLane != "L-1" || got.Outcome != conversations.OutcomeRan {
		t.Errorf("action %+v, %v", got, err)
	}
	if _, err := conversations.NewEntry("c", "e", conversations.KindAction, "", conversations.ActionData{Operation: "x", Outcome: conversations.OutcomeCard}); !errors.Is(err, conversations.ErrInvalid) {
		t.Errorf("a card with no approval: %v", err)
	}
}

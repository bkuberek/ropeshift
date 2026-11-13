package contract

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

func heldMessage(id string) messages.Held {
	return messages.Held{
		Message: messages.Message{
			ID: id, To: messages.Target{Lane: "lane-1", Generation: 2}, From: messages.Sender{Kind: "member"}, Kind: messages.KindAnswer,
			ReplyTo: "q-1", Body: "Twice.", State: messages.AtWorker,
		},
		InputID: messages.InputID(id), AgeMs: 7, TakenMs: 100, StaleAfterMs: 120_000, ExpireAfterMs: 600_000,
	}
}

func eventOf(kind string) ledger.UploadEvent { return ledger.UploadEvent{Type: kind, OccurredAt: 1} }

func pendingCount(t *testing.T, j ports.Journal) int {
	t.Helper()
	pending, err := j.Pending(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(pending)
}

// TestJournalTakesAMessageOnce is worker deduplication by ID: a message offered again changes
// nothing and stores no second word to the ledger.
func TestJournalTakesAMessageOnce(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	message := heldMessage("m-1")
	first, err := j.TakeMessage(ctx, message, eventOf(ledger.UploadMessage))
	if err != nil || !first {
		t.Fatalf("first %v: %v", first, err)
	}
	offered := message
	offered.Body = "Another body under the same ID."
	again, err := j.TakeMessage(ctx, offered, eventOf(ledger.UploadMessage))
	if err != nil || again {
		t.Fatalf("a repeat %v: %v", again, err)
	}
	if got := pendingCount(t, j); got != 1 {
		t.Errorf("%d events for one message", got)
	}
	got, err := j.Message(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != message {
		t.Errorf("got  %+v\nwant %+v", got, message)
	}
	byInput, err := j.MessageByInput(ctx, message.InputID)
	if err != nil || byInput.ID != message.ID {
		t.Errorf("by input %+v: %v", byInput, err)
	}
	if _, err := j.Message(ctx, "no-such-message"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a missing message: %v", err)
	}
	if _, err := j.MessageByInput(ctx, "no-such-input"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a missing input: %v", err)
	}
}

// TestJournalListsWhatWaits leaves out what was written or closed, and keeps arrival order.
func TestJournalListsWhatWaits(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	for _, id := range []string{"z-first", "a-second", "m-third", "b-fourth"} {
		if _, err := j.TakeMessage(ctx, heldMessage(id), eventOf(ledger.UploadMessage)); err != nil {
			t.Fatal(err)
		}
	}
	effect := lanes.NewEffect(lanes.EffectInput, "lane-1", 2, "", "message/a-second")
	if _, err := j.BeginEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if _, err := j.HandMessage(ctx, effect.Key, "a-second", eventOf(ledger.UploadMessage)); err != nil {
		t.Fatal(err)
	}
	if err := j.CloseMessage(ctx, "m-third"); err != nil {
		t.Fatal(err)
	}
	waiting, err := j.WaitingMessages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 2 || waiting[0].ID != "z-first" || waiting[1].ID != "b-fourth" {
		t.Errorf("waiting %+v", waiting)
	}
}

// TestJournalHandsAMessageInOneWrite keeps what a crash must not separate.
func TestJournalHandsAMessageInOneWrite(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	message := heldMessage("m-1")
	if _, err := j.TakeMessage(ctx, message, eventOf(ledger.UploadMessage)); err != nil {
		t.Fatal(err)
	}
	effect := lanes.NewEffect(lanes.EffectInput, "lane-1", 2, "", "message/m-1")
	if _, err := j.BeginEffect(ctx, effect); err != nil {
		t.Fatal(err)
	}
	if _, err := j.HandMessage(ctx, effect.Key, message.ID, eventOf(ledger.UploadMessage)); err != nil {
		t.Fatal(err)
	}
	got, err := j.Message(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != messages.HandedOff {
		t.Errorf("state %s", got.State)
	}
	settled, err := j.Effect(ctx, effect.Key)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != lanes.EffectCompleted {
		t.Errorf("effect %s", settled.State)
	}
	if got := pendingCount(t, j); got != 2 {
		t.Errorf("%d events, want the take and the hand", got)
	}
}

// TestJournalMovesAMessageFromWhatItRead is the compare-and-set: a move that lost stores no event.
func TestJournalMovesAMessageFromWhatItRead(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	message := heldMessage("m-1")
	if _, err := j.TakeMessage(ctx, message, eventOf(ledger.UploadMessage)); err != nil {
		t.Fatal(err)
	}
	if moved, err := j.MoveMessage(ctx, message.ID, messages.HandedOff, messages.Admitted, eventOf(ledger.UploadMessage)); err != nil || moved {
		t.Fatalf("a stale move %v: %v", moved, err)
	}
	if got := pendingCount(t, j); got != 1 {
		t.Errorf("%d events after a move that lost", got)
	}
	if moved, err := j.MoveMessage(ctx, message.ID, messages.AtWorker, messages.Admitted, eventOf(ledger.UploadMessage)); err != nil || !moved {
		t.Fatalf("a move %v: %v", moved, err)
	}
	got, err := j.Message(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != messages.Admitted {
		t.Errorf("state %s", got.State)
	}
	if got := pendingCount(t, j); got != 2 {
		t.Errorf("%d events", got)
	}
}

// TestJournalClosesOnlyWhatWaits leaves a message that was written as it stands, because a late
// admission must still find it.
func TestJournalClosesOnlyWhatWaits(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	message := heldMessage("m-1")
	if _, err := j.TakeMessage(ctx, message, eventOf(ledger.UploadMessage)); err != nil {
		t.Fatal(err)
	}
	if moved, err := j.MoveMessage(ctx, message.ID, messages.AtWorker, messages.HandedOff, eventOf(ledger.UploadMessage)); err != nil || !moved {
		t.Fatalf("%v: %v", moved, err)
	}
	if err := j.CloseMessage(ctx, message.ID); err != nil {
		t.Fatal(err)
	}
	got, err := j.Message(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != messages.HandedOff {
		t.Errorf("closing moved a written message to %s", got.State)
	}
}

// TestJournalForgetsALaneForGood removes what the journal holds of a lane, its record and the messages to
// it, with an event, and leaves every other lane's as it was.
func TestJournalForgetsALaneForGood(t *testing.T) {
	t.Parallel()
	j := openTestJournal(t)
	ctx := t.Context()
	other := heldMessage("m-2")
	other.To.Lane = "lane-2"
	for _, message := range []messages.Held{heldMessage("m-1"), other} {
		if _, err := j.TakeMessage(ctx, message, eventOf(ledger.UploadMessage)); err != nil {
			t.Fatal(err)
		}
	}
	for _, lane := range []string{"lane-1", "lane-2"} {
		if err := j.SaveLane(ctx, lanes.Record{ID: lane, State: lanes.StateEnded}); err != nil {
			t.Fatal(err)
		}
	}
	before := pendingCount(t, j)
	if _, err := j.ForgetLanes(ctx, []string{"lane-1", "lane-gone"}, eventOf(ledger.UploadAssistantForgotten)); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Message(ctx, "m-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a forgotten lane's message: %v", err)
	}
	if _, err := j.Lane(ctx, "lane-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a forgotten lane's record: %v", err)
	}
	if _, err := j.Message(ctx, "m-2"); err != nil {
		t.Errorf("another lane's message: %v", err)
	}
	if _, err := j.Lane(ctx, "lane-2"); err != nil {
		t.Errorf("another lane's record: %v", err)
	}
	if got := pendingCount(t, j); got != before+1 {
		t.Errorf("%d events after forgetting, from %d", got, before)
	}
}

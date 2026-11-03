package questions

import (
	"errors"
	"testing"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/measures"
)

func experienceEvent(t *testing.T, rev int64, eventType, kind, subject string, record any) ledger.Event {
	t.Helper()
	draft, err := RecordEvent(eventType, kind, subject, record, ledger.Actor{Kind: ledger.ActorWorker, ID: "w"})
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Event{Rev: rev, Type: draft.Type, Actor: draft.Actor, Subject: draft.Subject, Data: draft.Data}
}

// TestAnExperienceReplaysOnceAndFillsOnce holds a record's log to what FR-EXP-1 allows: written once,
// with its outcome filled once. A log that holds a second of either does not replay.
func TestAnExperienceReplaysOnceAndFillsOnce(t *testing.T) {
	record := measures.Experience{
		ID: "x-1", SubjectKind: measures.SubjectLane, SubjectID: "lane-1", PolicyVersion: 2, Chosen: "change",
		Settings: measures.Settings{Driver: "claude-code", Effort: "high", WorkOrderSize: "small"},
	}
	outcome := measures.Outcome{Status: "completed", CostUSD: 0.25}
	written := experienceEvent(t, 1, EventExperienceRecorded, measures.SubjectLane, "lane-1", record)
	filled := experienceEvent(t, 2, EventOutcomeFilled, measures.SubjectLane, "lane-1", outcome)
	book, err := Replay([]ledger.Event{written, filled})
	if err != nil {
		t.Fatal(err)
	}
	if got := book.Experiences[ExperienceKey(measures.SubjectLane, "lane-1")]; got.ID != "x-1" || got.Settings != record.Settings {
		t.Errorf("record %+v", got)
	}
	if got := book.Outcomes["lane-1"]; got.Status != outcome.Status || got.CostUSD != outcome.CostUSD {
		t.Errorf("outcome %+v", got)
	}
	again := experienceEvent(t, 3, EventOutcomeFilled, measures.SubjectLane, "lane-1", measures.Outcome{Status: "failed"})
	if _, err := Replay([]ledger.Event{written, filled, again}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a second fill replayed: %v", err)
	}
	rewritten := experienceEvent(t, 3, EventExperienceRecorded, measures.SubjectLane, "lane-1", record)
	if _, err := Replay([]ledger.Event{written, rewritten}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a second record replayed: %v", err)
	}
}

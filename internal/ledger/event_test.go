package ledger

import (
	"errors"
	"testing"
)

func TestDraftMustNameItsCause(t *testing.T) {
	good := EventDraft{
		Type:    "work_order.created",
		Actor:   Actor{Kind: ActorMember, ID: "m1"},
		Subject: Subject{Kind: "work_order", ID: "w1"},
	}
	tests := []struct {
		name   string
		change func(*EventDraft)
		ok     bool
	}{
		{"complete", func(*EventDraft) {}, true},
		{"no type", func(d *EventDraft) { d.Type = "" }, false},
		{"no actor kind", func(d *EventDraft) { d.Actor.Kind = "" }, false},
		{"no actor id", func(d *EventDraft) { d.Actor.ID = "" }, false},
		{"no subject kind", func(d *EventDraft) { d.Subject.Kind = "" }, false},
		{"no subject id", func(d *EventDraft) { d.Subject.ID = "" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := good
			tt.change(&draft)
			err := draft.Validate()
			if tt.ok && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidEvent) {
				t.Errorf("got %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestEmptyScopeIsRefused(t *testing.T) {
	if err := WorkspaceID("").Validate(); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("got %v", err)
	}
	if err := WorkspaceID("w").Validate(); err != nil {
		t.Errorf("got %v", err)
	}
}

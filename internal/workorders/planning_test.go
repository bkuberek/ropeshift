package workorders

import (
	"encoding/json"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// changeOf is one event of a work order's history.
func changeOf(t *testing.T, rev int64, eventType string, change Change) ledger.Event {
	t.Helper()
	data, err := json.Marshal(change)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Event{
		Rev: rev, Type: eventType, Subject: ledger.Subject{Kind: SubjectKind, ID: "wo"}, Data: data,
		Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member"},
	}
}

func created(t *testing.T) ledger.Event {
	t.Helper()
	data, err := json.Marshal(WorkOrder{ID: "wo", Kind: KindChange, State: Intake, ContractID: "c", ContractVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Event{Rev: 1, Type: EventCreated, Subject: ledger.Subject{Kind: SubjectKind, ID: "wo"}, Data: data}
}

// replayed walks triggers from a new work order and returns where it ends.
func replayed(t *testing.T, triggers ...Trigger) WorkOrder {
	t.Helper()
	events := []ledger.Event{created(t)}
	for i, trigger := range triggers {
		events = append(events, changeOf(t, int64(i+2), EventType(trigger), Change{Trigger: trigger}))
	}
	book, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	return book["wo"]
}

// TestPlanningTransitions is FR-PLAN-1, one row per planning edge of the diagram: each
// is an unleased edge a use case can take, from exactly one state.
func TestPlanningTransitions(t *testing.T) {
	tests := []struct {
		name    string
		from    State
		trigger Trigger
		to      State
	}{
		{"intake is accepted", Intake, TriggerAccepted, Planning},
		{"a plan and contract are submitted", Planning, TriggerContractSubmitted, ContractReview},
		{"a contract must change", ContractReview, TriggerContractChange, Planning},
		{"the definition of ready passes", ContractReview, TriggerReadyPassed, Ready},
		{"a question or a new version moves ready work back", Ready, TriggerBackToReview, ContractReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edge, err := Move(WorkOrder{State: tt.from}, tt.trigger)
			if err != nil {
				t.Fatal(err)
			}
			if edge.To != tt.to || edge.Leased {
				t.Errorf("edge %+v", edge)
			}
			for _, state := range States() {
				if state == tt.from {
					continue
				}
				if _, err := Move(WorkOrder{State: state}, tt.trigger); !errors.Is(err, ErrIllegalMove) {
					t.Errorf("%s from %s: %v", tt.trigger, state, err)
				}
			}
		})
	}
}

func TestTheWayToExecutionPassesEveryPlanningState(t *testing.T) {
	path := []Trigger{TriggerAccepted, TriggerContractSubmitted, TriggerReadyPassed}
	if got := replayed(t, path...); got.State != Ready {
		t.Fatalf("state %s", got.State)
	}
	if _, err := Move(WorkOrder{State: Intake}, TriggerReadyPassed); !errors.Is(err, ErrIllegalMove) {
		t.Errorf("intake skipped to ready: %v", err)
	}
}

// TestAContractThatMustChangeVoidsItsReview: the contract a person reviewed will not be the
// one that runs, so their review cannot carry over to the next version.
func TestAContractThatMustChangeVoidsItsReview(t *testing.T) {
	events := []ledger.Event{
		created(t),
		changeOf(t, 2, EventType(TriggerAccepted), Change{Trigger: TriggerAccepted}),
		changeOf(t, 3, EventType(TriggerContractSubmitted), Change{Trigger: TriggerContractSubmitted}),
		changeOf(t, 4, EventReviewPassed, Change{}),
		changeOf(t, 5, EventType(TriggerContractChange), Change{Trigger: TriggerContractChange}),
	}
	book, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if got := book["wo"]; got.State != Planning || got.ReviewedBy != "" {
		t.Errorf("work order %+v", got)
	}
}

// TestANewVersionPinsAndVoidsTheReview: a review covers one version, and the pin says which
// version the work will run.
func TestANewVersionPinsAndVoidsTheReview(t *testing.T) {
	events := []ledger.Event{
		created(t),
		changeOf(t, 2, EventType(TriggerAccepted), Change{Trigger: TriggerAccepted}),
		changeOf(t, 3, EventType(TriggerContractSubmitted), Change{Trigger: TriggerContractSubmitted}),
		changeOf(t, 4, EventReviewPassed, Change{}),
		changeOf(t, 5, EventType(TriggerReadyPassed), Change{Trigger: TriggerReadyPassed}),
		changeOf(t, 6, EventContractPinned, Change{ContractVersion: 2}),
		changeOf(t, 7, EventType(TriggerBackToReview), Change{Trigger: TriggerBackToReview}),
	}
	book, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if got := book["wo"]; got.State != ContractReview || got.ContractVersion != 2 || got.ReviewedBy != "" {
		t.Errorf("work order %+v", got)
	}
}

func TestOnlyUnstartedWorkTakesANewVersion(t *testing.T) {
	unstarted := map[State]bool{Intake: true, Planning: true, ContractReview: true, Ready: true}
	for _, state := range States() {
		if got := TakesNewVersion(state); got != unstarted[state] {
			t.Errorf("%s: %v", state, got)
		}
	}
}

func TestAPinOnAnUnknownWorkOrderDoesNotReplay(t *testing.T) {
	_, err := Replay([]ledger.Event{changeOf(t, 1, EventContractPinned, Change{ContractVersion: 2})})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("replay: %v", err)
	}
}

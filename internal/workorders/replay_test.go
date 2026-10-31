package workorders

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"testing"

	"ropeshift.dev/ropeshift/internal/ledger"
)

const testID = "wo-1"

func changeEvent(t *testing.T, rev int64, change Change) ledger.Event {
	t.Helper()
	data, err := json.Marshal(change)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Event{
		Rev:     rev,
		Type:    EventType(change.Trigger),
		Subject: ledger.Subject{Kind: SubjectKind, ID: testID},
		Data:    data,
	}
}

func createdEvent(t *testing.T) ledger.Event {
	t.Helper()
	data, err := json.Marshal(WorkOrder{ID: testID, Kind: KindChange, State: Intake})
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Event{Rev: 1, Type: EventCreated, Subject: ledger.Subject{Kind: SubjectKind, ID: testID}, Data: data}
}

// TestGeneratedEventSequences turns walks over the table into event logs. A replay
// must end where the walk ended, with the fence it tracked.
func TestGeneratedEventSequences(t *testing.T) {
	legal := legalTriggers()
	rng := rand.New(rand.NewPCG(20261031, 7))
	for range 2000 {
		events := []ledger.Event{createdEvent(t)}
		state := Intake
		var fence int64
		for rev := int64(2); rev < 60; rev++ {
			options := legal[state]
			if len(options) == 0 {
				break
			}
			trigger := options[rng.IntN(len(options))]
			edge, _ := Next(state, trigger)
			change := Change{Trigger: trigger, Fence: fence}
			switch trigger {
			case TriggerClaimGranted, TriggerClaimReleased, TriggerTakeover, TriggerLaneCompleted,
				TriggerLaneBlocked, TriggerLaneFailedRetry, TriggerAttemptsExhausted:
				fence++
				change.Fence = fence
			}
			events = append(events, changeEvent(t, rev, change))
			state = edge.To
		}
		book, err := Replay(events)
		if err != nil {
			t.Fatalf("a legal sequence did not replay: %v", err)
		}
		if got := book[testID]; got.State != state || got.Fence != fence {
			t.Fatalf("replayed %s fence %d, the walk ended at %s fence %d", got.State, got.Fence, state, fence)
		}
	}
}

func TestReplayRefusesWhatTheMachineForbids(t *testing.T) {
	tests := []struct {
		name   string
		events func(t *testing.T) []ledger.Event
	}{
		{
			name: "an event before creation",
			events: func(t *testing.T) []ledger.Event {
				return []ledger.Event{changeEvent(t, 1, Change{Trigger: TriggerAccepted})}
			},
		},
		{
			name: "creation twice",
			events: func(t *testing.T) []ledger.Event {
				return []ledger.Event{createdEvent(t), createdEvent(t)}
			},
		},
		{
			name: "an edge the table does not hold",
			events: func(t *testing.T) []ledger.Event {
				return []ledger.Event{createdEvent(t), changeEvent(t, 2, Change{Trigger: TriggerClaimGranted, Fence: 1})}
			},
		},
		{
			name: "a grant that does not raise the fence",
			events: func(t *testing.T) []ledger.Event {
				return append(toReady(t), changeEvent(t, 5, Change{Trigger: TriggerClaimGranted, Fence: 0}))
			},
		},
		{
			name: "a write with a stale fence",
			events: func(t *testing.T) []ledger.Event {
				return append(toReady(t),
					changeEvent(t, 5, Change{Trigger: TriggerClaimGranted, Fence: 1}),
					changeEvent(t, 6, Change{Trigger: TriggerClaimReleased, Fence: 2}),
					changeEvent(t, 7, Change{Guarded: true, Fence: 1}))
			},
		},
		{
			name: "an event that changes nothing",
			events: func(t *testing.T) []ledger.Event {
				return []ledger.Event{createdEvent(t), changeEvent(t, 2, Change{})}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Replay(tt.events(t)); !errors.Is(err, ErrInvalid) {
				t.Errorf("got %v, want ErrInvalid", err)
			}
		})
	}
}

func toReady(t *testing.T) []ledger.Event {
	t.Helper()
	return []ledger.Event{
		createdEvent(t),
		changeEvent(t, 2, Change{Trigger: TriggerAccepted}),
		changeEvent(t, 3, Change{Trigger: TriggerContractSubmitted}),
		changeEvent(t, 4, Change{Trigger: TriggerReadyPassed}),
	}
}

func TestRejectTellsStaleFromLapsed(t *testing.T) {
	lease := Lease{WorkOrder: testID, ClaimID: "c1", Fence: 7}
	tests := []struct {
		name    string
		current WorkOrder
		lapsed  bool
	}{
		{"lapsed with the same claim", WorkOrder{State: Lapsed, Fence: 7, ClaimID: "c1"}, true},
		{"lapsed under a new claim", WorkOrder{State: Lapsed, Fence: 9, ClaimID: "c2"}, false},
		{"another holder", WorkOrder{State: Running, Fence: 9, ClaimID: "c2"}, false},
		{"back in ready", WorkOrder{State: Ready, Fence: 8}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Reject(tt.current, lease)
			if tt.lapsed {
				if !errors.Is(err, ErrLapsed) {
					t.Errorf("got %v, want ErrLapsed", err)
				}
				return
			}
			var fenced *FencedError
			if !errors.As(err, &fenced) || fenced.Current != tt.current.Fence {
				t.Errorf("got %v, want the current fence %d", err, tt.current.Fence)
			}
		})
	}
}

func TestRejectRenewalTellsExpiredFromFenced(t *testing.T) {
	lease := Lease{WorkOrder: testID, ClaimID: "c1", Fence: 7}
	var fenced *FencedError
	tests := []struct {
		name    string
		current WorkOrder
		check   func(error) bool
	}{
		{"claimed past the deadline", WorkOrder{State: Claimed, Fence: 7, ClaimID: "c1"}, func(err error) bool { return errors.Is(err, ErrExpired) }},
		{"running past the deadline", WorkOrder{State: Running, Fence: 7, ClaimID: "c1"}, func(err error) bool { return errors.Is(err, ErrExpired) }},
		{"lapsed", WorkOrder{State: Lapsed, Fence: 7, ClaimID: "c1"}, func(err error) bool { return errors.Is(err, ErrLapsed) }},
		{"another holder", WorkOrder{State: Running, Fence: 9, ClaimID: "c2"}, func(err error) bool { return errors.As(err, &fenced) }},
		{"finished under the same fence", WorkOrder{State: Done, Fence: 7, ClaimID: "c1"}, func(err error) bool { return errors.As(err, &fenced) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := RejectRenewal(tt.current, lease); !tt.check(err) {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestMoveRefusesALeasedEdge(t *testing.T) {
	if _, err := Move(WorkOrder{State: Ready}, TriggerClaimGranted); !errors.Is(err, ErrLeasedEdge) {
		t.Errorf("got %v", err)
	}
	if _, err := Move(WorkOrder{State: Done}, TriggerRework); !errors.Is(err, ErrIllegalMove) {
		t.Errorf("got %v", err)
	}
	if edge, err := Move(WorkOrder{State: Intake}, TriggerAccepted); err != nil || edge.To != Planning {
		t.Errorf("got %+v, %v", edge, err)
	}
}

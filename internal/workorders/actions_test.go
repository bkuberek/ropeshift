package workorders

import (
	"slices"
	"testing"
)

// allowedIn is what a member may do to a work order in each state, written out apart from the code that
// computes it, so a changed edge of the machine has to change this table too.
var allowedIn = map[State][]Action{
	Intake:         {ActionCancel},
	Planning:       {ActionCancel},
	ContractReview: {ActionSendBack, ActionCancel},
	Ready:          {ActionClaim, ActionCancel},
	Claimed:        {ActionStartLane, ActionRelease, ActionCancel},
	Running:        {ActionCancel},
	Parked:         {ActionCancel},
	Lapsed:         {ActionReassign, ActionCancel},
	Acceptance:     {ActionCancel},
	Integrating:    {ActionCancel},
	Blocked:        {ActionResolve, ActionReplan, ActionCancel},
	Done:           nil,
	Failed:         {ActionRetry},
	Canceled:       nil,
}

func TestEveryStateSaysWhatAMemberMayDoToIt(t *testing.T) {
	for _, state := range States() {
		want, ok := allowedIn[state]
		if !ok {
			t.Fatalf("the table above says nothing of %s", state)
		}
		if got := Allowed(state, Facts{}); !slices.Equal(got, want) {
			t.Errorf("%s allows %v, want %v", state, got, want)
		}
	}
}

// An action is allowed where the machine has an edge for its trigger, so no action stands on a rule of its own.
func TestAnActionIsAllowedWhereTheMachineHasItsEdge(t *testing.T) {
	triggers := map[Action]Trigger{
		ActionClaim: TriggerClaimGranted, ActionStartLane: TriggerLaneStarted, ActionCancel: TriggerCanceled, ActionReassign: TriggerTakeover,
		ActionReplan: TriggerReplan, ActionResolve: TriggerResolved, ActionRetry: TriggerRetried, ActionRelease: TriggerClaimReleased,
		ActionSendBack: TriggerContractChange,
	}
	for _, state := range States() {
		allowed := Allowed(state, Facts{})
		for action, trigger := range triggers {
			_, edge := Next(state, trigger)
			if got := slices.Contains(allowed, action); got != edge {
				t.Errorf("%s allows %s: %v, and the machine has an edge for %s there: %v", state, action, got, trigger, edge)
			}
		}
	}
	for _, action := range Actions() {
		if _, named := triggers[action]; !named && action != ActionArchiveLane {
			t.Errorf("%s names no trigger and is not the archive of a lane", action)
		}
	}
}

func TestAClaimThatAskedForALaneStartsNoSecond(t *testing.T) {
	if got := Allowed(Claimed, Facts{Dispatched: true}); !slices.Equal(got, []Action{ActionRelease, ActionCancel}) {
		t.Errorf("a claim that asked for its lane allows %v", got)
	}
	if got := Allowed(Ready, Facts{Dispatched: true}); slices.Contains(got, ActionStartLane) {
		t.Errorf("ready work allows %v", got)
	}
}

// A lane's worktree goes by what the lane and its attempt did, whatever state the work order stands in.
func TestAWorktreeCanGoInAnyState(t *testing.T) {
	for _, state := range States() {
		without := Allowed(state, Facts{})
		with := Allowed(state, Facts{Removable: true})
		if slices.Contains(without, ActionArchiveLane) {
			t.Errorf("%s allows an archive with no lane to remove", state)
		}
		if !slices.Equal(with, append(slices.Clone(without), ActionArchiveLane)) {
			t.Errorf("%s with a lane to remove allows %v, want %v and the archive last", state, with, without)
		}
	}
}

// TestActionsAreListedInTheOrderAViewOffersThem holds the order, which a view reads off the list.
func TestActionsAreListedInTheOrderAViewOffersThem(t *testing.T) {
	want := []Action{
		ActionClaim, ActionStartLane, ActionRetry, ActionRelease, ActionSendBack, ActionReassign, ActionResolve, ActionReplan, ActionCancel, ActionArchiveLane,
	}
	if got := Actions(); !slices.Equal(got, want) {
		t.Errorf("Actions are %v, want %v", got, want)
	}
}

// A lane runs while it has not ended and the attempt it started under is the work order's current one.
func TestALaneRunsUntilItEndsOrItsAttemptDoes(t *testing.T) {
	order := WorkOrder{Fence: 3}
	for _, c := range []struct {
		name    string
		started int64
		ended   bool
		running bool
	}{
		{"a lane working on the current attempt", 3, false, true},
		{"a lane that ended", 3, true, false},
		{"a lane of an earlier attempt that was never reported ended", 2, false, false},
		{"a lane of an earlier attempt that ended", 2, true, false},
	} {
		if got := order.LaneRuns(c.started, c.ended); got != c.running {
			t.Errorf("%s: runs %v, want %v", c.name, got, c.running)
		}
	}
}

package lanes_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
)

// designEdges is the state diagram, row by row, written out a second time so a change to
// the table has to be made twice.
var designEdges = []struct {
	from lanes.ActualState
	on   lanes.Signal
	to   lanes.ActualState
}{
	{lanes.Pending, lanes.OnClaimGranted, lanes.Provisioning},
	{lanes.Provisioning, lanes.OnResourcesReady, lanes.Starting},
	{lanes.Starting, lanes.OnDriverReady, lanes.Briefing},
	{lanes.Starting, lanes.OnResumeNotReady, lanes.Retiring},
	{lanes.Starting, lanes.OnUnknownScreen, lanes.Parking},
	{lanes.Briefing, lanes.OnActivity, lanes.Working},
	{lanes.Briefing, lanes.OnBriefGrace, lanes.Idle},
	{lanes.Working, lanes.OnTurnFinished, lanes.Idle},
	{lanes.Idle, lanes.OnInputHanded, lanes.Working},
	{lanes.Working, lanes.OnDecisionPending, lanes.NeedsDecision},
	{lanes.NeedsDecision, lanes.OnDecisionReturned, lanes.Working},
	{lanes.NeedsDecision, lanes.OnWaitBound, lanes.Parking},
	{lanes.Working, lanes.OnCompactionStarted, lanes.Compacting},
	{lanes.Idle, lanes.OnCompactionStarted, lanes.Compacting},
	{lanes.Compacting, lanes.OnFinishedInTurn, lanes.Working},
	{lanes.Compacting, lanes.OnFinishedAtRest, lanes.Idle},
	{lanes.Working, lanes.OnHandoffThreshold, lanes.HandingOff},
	{lanes.Idle, lanes.OnHandoffThreshold, lanes.HandingOff},
	{lanes.HandingOff, lanes.OnHandoffWritten, lanes.Retiring},
	{lanes.Retiring, lanes.OnNextGeneration, lanes.Starting},
	{lanes.Working, lanes.OnCannotProceed, lanes.Parking},
	{lanes.Idle, lanes.OnParkRequested, lanes.Parking},
	{lanes.Parking, lanes.OnProcessGone, lanes.Parked},
	{lanes.Parked, lanes.OnWakeFired, lanes.Provisioning},
	{lanes.Working, lanes.OnSessionGone, lanes.Backoff},
	{lanes.Idle, lanes.OnSessionGone, lanes.Backoff},
	{lanes.NeedsDecision, lanes.OnSessionGone, lanes.Backoff},
	{lanes.Backoff, lanes.OnDelayElapsed, lanes.Starting},
	{lanes.Backoff, lanes.OnRestartLimit, lanes.Tripped},
	{lanes.Tripped, lanes.OnHumanReset, lanes.Starting},
	{lanes.Idle, lanes.OnOutcomeReported, lanes.Retiring},
	{lanes.Working, lanes.OnStopRequested, lanes.Retiring},
	{lanes.Retiring, lanes.OnSessionStopped, lanes.Ended},
}

func TestEveryDesignEdge(t *testing.T) {
	for _, edge := range designEdges {
		t.Run(fmt.Sprintf("%s on %s", edge.from, edge.on), func(t *testing.T) {
			got, moved := lanes.Next(edge.from, edge.on)
			if !moved || got != edge.to {
				t.Errorf("got %s, moved %v, want %s", got, moved, edge.to)
			}
		})
	}
}

func TestEveryStateIsReachable(t *testing.T) {
	seen := map[lanes.ActualState]bool{lanes.Pending: true}
	frontier := []lanes.ActualState{lanes.Pending}
	for len(frontier) != 0 {
		state := frontier[0]
		frontier = frontier[1:]
		for _, signal := range lanes.Signals {
			if next, moved := lanes.Next(state, signal); moved && !seen[next] {
				seen[next] = true
				frontier = append(frontier, next)
			}
		}
	}
	for _, state := range lanes.ActualStates {
		if !seen[state] {
			t.Errorf("%s cannot be reached", state)
		}
	}
	if len(lanes.ActualStates) != 15 {
		t.Errorf("%d states, and the design names 15", len(lanes.ActualStates))
	}
}

// TestGeneratedSequencesStayOnTheTable walks random signals through the table. An event
// can arrive late or twice, so a signal with no edge moves nothing, and nothing leaves
// `ended`.
func TestGeneratedSequencesStayOnTheTable(t *testing.T) {
	declared := map[[2]string]lanes.ActualState{}
	for _, edge := range designEdges {
		declared[[2]string{string(edge.from), string(edge.on)}] = edge.to
	}
	for seed := range uint64(500) {
		rng := rand.New(rand.NewPCG(seed, 2))
		tracker := lanes.NewTracker(lanes.Pending)
		for step := range 300 {
			before := tracker.State()
			signal := lanes.Signals[rng.IntN(len(lanes.Signals))]
			moved := tracker.Apply(signal)
			after := tracker.State()
			if !slices.Contains(lanes.ActualStates, after) {
				t.Fatalf("seed %d step %d: unknown state %s", seed, step, after)
			}
			if before == lanes.Ended && after != lanes.Ended {
				t.Fatalf("seed %d step %d: %s left ended", seed, step, after)
			}
			if moved != (before != after) {
				t.Fatalf("seed %d step %d: %s on %s moved=%v to %s", seed, step, before, signal, moved, after)
			}
			if !moved {
				continue
			}
			if want, ok := declared[[2]string{string(before), string(signal)}]; ok && want != after {
				t.Fatalf("seed %d step %d: %s on %s went to %s, want %s", seed, step, before, signal, after, want)
			}
		}
	}
}

// A compaction returns to the state it left, whoever started it.
func TestCompactionReturnsToTheStateItLeft(t *testing.T) {
	for _, from := range []lanes.ActualState{lanes.Working, lanes.Idle} {
		tracker := lanes.NewTracker(from)
		if !tracker.Apply(lanes.OnCompactionStarted) || tracker.State() != lanes.Compacting {
			t.Fatalf("from %s: %s", from, tracker.State())
		}
		if !tracker.Apply(lanes.OnCompactionFinished) || tracker.State() != from {
			t.Errorf("returned to %s, want %s", tracker.State(), from)
		}
	}
}

// A lane moves only on what happened: no signal is a person saying it is ready.
func TestNoSignalMarksALaneReadyOrBriefed(t *testing.T) {
	for _, signal := range lanes.Signals {
		for _, word := range []string{"ready_mark", "mark", "briefed", "set_state"} {
			if string(signal) == word {
				t.Errorf("signal %s", signal)
			}
		}
	}
	if _, moved := lanes.Next(lanes.Starting, lanes.OnActivity); moved {
		t.Error("activity moved a lane past its driver's ready signal")
	}
}

func TestConditions(t *testing.T) {
	tests := []struct {
		name  string
		facts lanes.Facts
		want  []lanes.Condition
	}{
		{name: "a lane with nothing wrong", facts: lanes.Facts{State: lanes.Working}},
		{
			name:  "working with no progress",
			facts: lanes.Facts{State: lanes.Working, SinceProgress: 30, StallAfter: 20},
			want:  []lanes.Condition{lanes.Stalled},
		},
		{
			name:  "a quiet idle lane is not stalled",
			facts: lanes.Facts{State: lanes.Idle, SinceProgress: 30, StallAfter: 20},
		},
		{
			name:  "no threshold set",
			facts: lanes.Facts{State: lanes.Working, SinceProgress: 30},
		},
		{
			name:  "a declared wait suppresses stalled",
			facts: lanes.Facts{State: lanes.Working, SinceProgress: 30, StallAfter: 20, WaitDeclared: true},
			want:  []lanes.Condition{lanes.WaitingExternal},
		},
		{
			name:  "a long tool call suppresses stalled",
			facts: lanes.Facts{State: lanes.Working, SinceProgress: 30, StallAfter: 20, ToolInFlight: true},
			want:  []lanes.Condition{lanes.WaitingExternal},
		},
		{
			name:  "a wait does not suppress overdue",
			facts: lanes.Facts{State: lanes.Working, PastDeadline: true, WaitDeclared: true},
			want:  []lanes.Condition{lanes.Overdue, lanes.WaitingExternal},
		},
		{
			name:  "context crossed the warning",
			facts: lanes.Facts{State: lanes.Working, ContextTokens: 121, WarnTokens: 120},
			want:  []lanes.Condition{lanes.NearBudget},
		},
		{
			name:  "no warning set",
			facts: lanes.Facts{State: lanes.Working, ContextTokens: 121},
		},
		{
			name: "every flag the worker is told",
			facts: lanes.Facts{
				State: lanes.Idle, BriefUnconfirmed: true, PackOversized: true, HandoffDegraded: true,
				Degraded: true, UnpushedWork: true, Drifted: true,
			},
			want: []lanes.Condition{
				lanes.BriefUnconfirmed, lanes.PackOversized, lanes.HandoffDegraded,
				lanes.DegradedMode, lanes.UnpushedWork, lanes.Drifted,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.Conditions(tt.facts); !slices.Equal(got, tt.want) {
				t.Errorf("conditions %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLiveness(t *testing.T) {
	tests := []struct {
		name     string
		evidence lanes.Evidence
		want     lanes.Liveness
	}{
		{"nothing read", lanes.Evidence{}, lanes.LivenessUnknown},
		{"an incomplete read is not a negative answer", lanes.Evidence{Incomplete: true}, lanes.LivenessUnknown},
		{"a process that exists proves too little", lanes.Evidence{ProcessExists: true}, lanes.LivenessUnknown},
		{"a handshake that returned", lanes.Evidence{Answered: true}, lanes.LivenessAlive},
		{"dead needs a confirmed exit", lanes.Evidence{ExitConfirmed: true}, lanes.LivenessDead},
		{"a confirmed exit outranks an old answer", lanes.Evidence{Answered: true, ExitConfirmed: true}, lanes.LivenessDead},
		{"an incomplete read with no exit stays unknown", lanes.Evidence{Incomplete: true, ProcessExists: true}, lanes.LivenessUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.JudgeLiveness(tt.evidence); got != tt.want {
				t.Errorf("liveness %s", got)
			}
		})
	}
}

func TestModelReachability(t *testing.T) {
	const window = 100
	fresh := func(reach lanes.Reachability) lanes.ReachEvidence {
		return lanes.ReachEvidence{Reach: reach, Age: 10, Seen: true}
	}
	tests := []struct {
		name     string
		state    lanes.ActualState
		evidence lanes.ReachEvidence
		want     lanes.Reachability
	}{
		{"an idle session has no fresh evidence", lanes.Idle, fresh(lanes.Reachable), lanes.ReachUnknown},
		{"no evidence at all", lanes.Working, lanes.ReachEvidence{}, lanes.ReachUnknown},
		{"evidence past its window", lanes.Working, lanes.ReachEvidence{Reach: lanes.Reachable, Age: 200, Seen: true}, lanes.ReachUnknown},
		{"a turn in progress with fresh success", lanes.Working, fresh(lanes.Reachable), lanes.Reachable},
		{"a retry", lanes.Working, fresh(lanes.Impaired), lanes.Impaired},
		{"a sign-in failure", lanes.Working, fresh(lanes.Unreachable), lanes.Unreachable},
		{"a lane a sign-in failure parks still says why", lanes.Parking, fresh(lanes.Unreachable), lanes.Unreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.ReadReach(tt.state, tt.evidence, window); got != tt.want {
				t.Errorf("reach %s", got)
			}
		})
	}
}

func TestStopFailureCategories(t *testing.T) {
	tests := map[string]lanes.Reachability{
		"overloaded":            lanes.Impaired,
		"rate_limit":            lanes.Impaired,
		"authentication_failed": lanes.Unreachable,
		"something new":         lanes.Impaired,
	}
	for category, want := range tests {
		if got := lanes.ReachOfFailure(category); got != want {
			t.Errorf("%s: %s", category, got)
		}
	}
}

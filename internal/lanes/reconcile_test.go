package lanes_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

func running(id string) lanes.Record {
	return lanes.Record{ID: id, Generation: 1, State: lanes.StateRunning}
}

func handingOff(id string) lanes.Record {
	record := running(id)
	record.Handoff = &lanes.HandoffProgress{Reason: "r"}
	return record
}

func effectOf(kind lanes.EffectKind, state lanes.EffectState) lanes.Effect {
	effect := lanes.NewEffect(kind, "lane", 1, "", "")
	effect.State = state
	return effect
}

func TestPlan(t *testing.T) {
	alive := lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true}
	deadWithStream := lanes.Session{Found: true, Liveness: lanes.LivenessDead, Readable: true}
	deadSilent := lanes.Session{Found: true, Liveness: lanes.LivenessDead}
	unattached := lanes.Session{Found: true, Liveness: lanes.LivenessUnknown}
	sessionKey := effectOf(lanes.EffectSession, lanes.EffectUnknown).Key
	worktreeKey := effectOf(lanes.EffectWorktree, lanes.EffectUnknown).Key
	tests := []struct {
		name  string
		world lanes.World
		want  []lanes.Action
	}{
		{name: "nothing to do"},
		{
			name:  "desired state names a lane",
			world: lanes.World{Wanted: []string{"lane"}},
			want:  []lanes.Action{{Kind: lanes.ActionStart, Lane: "lane"}},
		},
		{
			name:  "a run owns the lane",
			world: lanes.World{Lanes: []lanes.Actual{{Record: running("lane"), Held: true}}},
		},
		{
			name:  "a handoff that lost its session finishes",
			world: lanes.World{Lanes: []lanes.Actual{{Record: handingOff("lane"), Session: deadSilent}}},
			want:  []lanes.Action{{Kind: lanes.ActionHandoff, Lane: "lane"}},
		},
		{
			name:  "a handoff that left no session to find finishes",
			world: lanes.World{Lanes: []lanes.Actual{{Record: handingOff("lane")}}},
			want:  []lanes.Action{{Kind: lanes.ActionHandoff, Lane: "lane"}},
		},
		{
			name:  "a handoff with a session to read adopts it",
			world: lanes.World{Lanes: []lanes.Actual{{Record: handingOff("lane"), Session: alive}}},
			want:  []lanes.Action{{Kind: lanes.ActionAdopt, Lane: "lane"}},
		},
		{
			name:  "a handoff waits out a read that did not finish",
			world: lanes.World{Lanes: []lanes.Actual{{Record: handingOff("lane"), Session: unattached}}},
		},
		{
			name: "a handoff whose generation began is over",
			world: lanes.World{Lanes: []lanes.Actual{{
				Record: lanes.Record{ID: "lane", Generation: 2, State: lanes.StateRunning, Handoff: &lanes.HandoffProgress{Begun: true}}, Session: deadSilent,
			}}},
			want: []lanes.Action{{Kind: lanes.ActionEnd, Lane: "lane"}},
		},
		{
			name:  "an ended lane stays ended",
			world: lanes.World{Lanes: []lanes.Actual{{Record: lanes.Record{ID: "lane", State: lanes.StateEnded}}}},
		},
		{
			name:  "an ended lane gives back what it held",
			world: lanes.World{Lanes: []lanes.Actual{{Record: lanes.Record{ID: "lane", State: lanes.StateEnded}}}, Allocations: []string{"lane"}},
			want:  []lanes.Action{{Kind: lanes.ActionRelease, Lane: "lane"}},
		},
		{
			name:  "a running lane keeps what it holds",
			world: lanes.World{Lanes: []lanes.Actual{{Record: running("lane"), Held: true}}, Allocations: []string{"lane"}},
		},
		{
			name:  "a lane with no record holds nothing",
			world: lanes.World{Allocations: []string{"lane"}},
			want:  []lanes.Action{{Kind: lanes.ActionRelease, Lane: "lane"}},
		},
		{
			name:  "a removal waits for the lane to end",
			world: lanes.World{Lanes: []lanes.Actual{{Record: running("lane"), Held: true}}, Archives: []string{"lane"}},
		},
		{
			name:  "an ended lane's worktree goes",
			world: lanes.World{Lanes: []lanes.Actual{{Record: lanes.Record{ID: "lane", State: lanes.StateEnded}}}, Archives: []string{"lane"}},
			want:  []lanes.Action{{Kind: lanes.ActionArchive, Lane: "lane"}},
		},
		{
			name:  "an archived lane is not removed twice",
			world: lanes.World{Lanes: []lanes.Actual{{Record: lanes.Record{ID: "lane", State: lanes.StateArchived}}}, Archives: []string{"lane"}},
		},
		{
			name:  "a session outlived the worker",
			world: lanes.World{Lanes: []lanes.Actual{{Record: running("lane"), Session: alive}}},
			want:  []lanes.Action{{Kind: lanes.ActionAdopt, Lane: "lane"}},
		},
		{
			name:  "a session ended while the worker was away",
			world: lanes.World{Lanes: []lanes.Actual{{Record: running("lane"), Session: deadWithStream}}},
			want:  []lanes.Action{{Kind: lanes.ActionAdopt, Lane: "lane"}},
		},
		{
			name:  "a dead session with nothing to read",
			world: lanes.World{Lanes: []lanes.Actual{{Record: running("lane"), Session: deadSilent}}},
			want:  []lanes.Action{{Kind: lanes.ActionEnd, Lane: "lane"}},
		},
		{
			name:  "unknown liveness starts nothing",
			world: lanes.World{Lanes: []lanes.Actual{{Record: running("lane"), Session: unattached}}},
		},
		{
			name: "a session that ran and left no trace",
			world: lanes.World{Lanes: []lanes.Actual{{
				Record: running("lane"), Effects: []lanes.Effect{effectOf(lanes.EffectSession, lanes.EffectCompleted)},
			}}},
			want: []lanes.Action{{Kind: lanes.ActionEnd, Lane: "lane"}},
		},
		{
			name:  "a crash before the session",
			world: lanes.World{Lanes: []lanes.Actual{{Record: running("lane")}}},
			want:  []lanes.Action{{Kind: lanes.ActionContinue, Lane: "lane"}},
		},
		{
			name: "an unknown spawn that happened",
			world: lanes.World{
				Lanes:   []lanes.Actual{{Record: running("lane"), Session: alive}},
				Unknown: []lanes.ObservedEffect{{Effect: effectOf(lanes.EffectSession, lanes.EffectUnknown), Finding: lanes.Found}},
			},
			want: []lanes.Action{{Kind: lanes.ActionConfirm, Lane: "lane", Effect: sessionKey}, {Kind: lanes.ActionAdopt, Lane: "lane"}},
		},
		{
			name: "an unknown spawn that did not happen",
			world: lanes.World{
				Lanes:   []lanes.Actual{{Record: running("lane")}},
				Unknown: []lanes.ObservedEffect{{Effect: effectOf(lanes.EffectSession, lanes.EffectUnknown), Finding: lanes.FoundNothing}},
			},
			want: []lanes.Action{{Kind: lanes.ActionRetry, Lane: "lane", Effect: sessionKey}, {Kind: lanes.ActionContinue, Lane: "lane"}},
		},
		{
			name: "an incomplete read of an unknown spawn retries nothing",
			world: lanes.World{
				Lanes:   []lanes.Actual{{Record: running("lane")}},
				Unknown: []lanes.ObservedEffect{{Effect: effectOf(lanes.EffectSession, lanes.EffectUnknown), Finding: lanes.FoundUnknown}},
			},
		},
		{
			name: "an input cannot be observed, so it waits for a person",
			world: lanes.World{
				Lanes:   []lanes.Actual{{Record: running("lane"), Held: true}},
				Unknown: []lanes.ObservedEffect{{Effect: effectOf(lanes.EffectInput, lanes.EffectUnknown), Finding: lanes.FoundNothing}},
			},
		},
		{
			name: "an unknown effect of an ended lane is closed",
			world: lanes.World{
				Lanes:   []lanes.Actual{{Record: lanes.Record{ID: "lane", State: lanes.StateEnded}}},
				Unknown: []lanes.ObservedEffect{{Effect: effectOf(lanes.EffectWorktree, lanes.EffectUnknown), Finding: lanes.FoundNothing}},
			},
			want: []lanes.Action{{Kind: lanes.ActionAbandon, Lane: "lane", Effect: worktreeKey}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.Plan(tt.world); !slices.Equal(got, tt.want) {
				t.Errorf("plan %v, want %v", got, tt.want)
			}
		})
	}
}

// TestOneStuckLaneStopsNoOther holds the design's rule that lanes reconcile alone.
func TestOneStuckLaneStopsNoOther(t *testing.T) {
	stuck := lanes.NewEffect(lanes.EffectSession, "stuck", 1, "", "")
	world := lanes.World{
		Lanes: []lanes.Actual{
			{Record: running("stuck")},
			{Record: running("next"), Session: lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true}},
		},
		Unknown: []lanes.ObservedEffect{{Effect: stuck, Finding: lanes.FoundUnknown}},
	}
	want := []lanes.Action{{Kind: lanes.ActionAdopt, Lane: "next"}}
	if got := lanes.Plan(world); !slices.Equal(got, want) {
		t.Errorf("plan %v", got)
	}
}

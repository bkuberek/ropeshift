package lanes_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/parking"
)

func parkingRecord(id string, progress lanes.ParkProgress) lanes.Record {
	record := running(id)
	record.Parking = &progress
	return record
}

func TestPlanFinishesParkingBeforeAnythingElse(t *testing.T) {
	progress := lanes.ParkProgress{Wake: "wake", Expr: `{"human_resume":true}`}
	spawnedSession := lanes.Effect{Kind: lanes.EffectSession, Generation: 1, State: lanes.EffectCompleted}
	tests := []struct {
		name    string
		session lanes.Session
		effects []lanes.Effect
		want    []lanes.Action
	}{
		{
			name:    "a session still there is adopted, and its run goes on parking",
			session: lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true},
			want:    []lanes.Action{{Kind: lanes.ActionAdopt, Lane: "lane"}},
		},
		{
			name: "a session gone leaves parking to finish with what the worktree holds",
			want: []lanes.Action{{Kind: lanes.ActionPark, Lane: "lane"}},
		},
		{
			name:    "a stored wake beats a session that ended, which would otherwise end the lane",
			effects: []lanes.Effect{spawnedSession},
			want:    []lanes.Action{{Kind: lanes.ActionPark, Lane: "lane"}},
		},
		{
			name:    "a session read as dead with nothing to read still parks",
			session: lanes.Session{Found: true, Liveness: lanes.LivenessDead},
			effects: []lanes.Effect{spawnedSession},
			want:    []lanes.Action{{Kind: lanes.ActionPark, Lane: "lane"}},
		},
		{
			name:    "a session nobody can read waits, because unknown stops nothing and commits nothing",
			session: lanes.Session{Found: true, Liveness: lanes.LivenessUnknown},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := lanes.World{Lanes: []lanes.Actual{{
				Record: parkingRecord("lane", progress), Session: tt.session, Effects: tt.effects,
			}}}
			if got := lanes.Plan(world); !slices.Equal(got, tt.want) {
				t.Errorf("plan %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanLeavesAParkedLaneAlone(t *testing.T) {
	record := running("lane")
	record.State = lanes.StateParked
	record.Parking = &lanes.ParkProgress{Wake: "wake"}
	world := lanes.World{Lanes: []lanes.Actual{{Record: record}}, Allocations: []string{"lane"}}
	want := []lanes.Action{{Kind: lanes.ActionRelease, Lane: "lane"}}
	if got := lanes.Plan(world); !slices.Equal(got, want) {
		t.Errorf("plan %v: a parked lane holds no process, and gives back what it held", got)
	}
}

func TestPlanEndsAParkRefusedByTheLedger(t *testing.T) {
	record := running("lane")
	record.State = lanes.StateParked
	record.Parking = &lanes.ParkProgress{Wake: "wake"}
	world := lanes.World{Lanes: []lanes.Actual{{Record: record, ParkRefused: true}}}
	want := []lanes.Action{{Kind: lanes.ActionEnd, Lane: "lane"}}
	if got := lanes.Plan(world); !slices.Equal(got, want) {
		t.Errorf("plan %v: a fenced commit leaves a lane nothing can wake", got)
	}
}

func TestAProgressIsOpenUntilItsNextGenerationBegins(t *testing.T) {
	var none *lanes.ParkProgress
	if none.Open() {
		t.Error("no parking reads open")
	}
	if !(&lanes.ParkProgress{Wake: "wake"}).Open() {
		t.Error("a stored wake reads closed")
	}
	if (&lanes.ParkProgress{Wake: "wake", Begun: true}).Open() {
		t.Error("a lane that resumed reads as parking")
	}
}

func TestAWakeIDIsTheSameWhoeverAsks(t *testing.T) {
	first, second := lanes.WakeID("lane", 3), lanes.WakeID("lane", 3)
	if first != second || first == lanes.WakeID("lane", 4) || first == lanes.WakeID("other", 3) {
		t.Errorf("wake IDs %s and %s", first, second)
	}
}

func TestParkingOnQuestionsTakesAPersonToo(t *testing.T) {
	expr, err := parking.Parse(lanes.QuestionWake([]string{"q1", "q2"}), "lane")
	if err != nil {
		t.Fatal(err)
	}
	both := parking.Facts{
		{Kind: parking.QuestionAnswered, Key: "q1"}: true, {Kind: parking.QuestionAnswered, Key: "q2"}: true,
	}
	if expr.Holds(parking.Facts{{Kind: parking.QuestionAnswered, Key: "q1"}: true}, 0) {
		t.Error("one answer of two woke the lane")
	}
	if !expr.Holds(both, 0) {
		t.Error("both answers did not wake the lane")
	}
	if !expr.Holds(parking.Facts{{Kind: parking.HumanResume, Key: "lane"}: true}, 0) {
		t.Error("a dismissed question left a lane nobody can wake")
	}
}

func TestAPersonWakesADrainedLane(t *testing.T) {
	expr, err := parking.Parse(lanes.HumanWake(), "lane")
	if err != nil {
		t.Fatal(err)
	}
	if !expr.Holds(parking.Facts{{Kind: parking.HumanResume, Key: "lane"}: true}, 0) {
		t.Error("the member's word did not hold")
	}
}

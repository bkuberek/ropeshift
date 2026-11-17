package worker

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// An end is forgotten only for the park of its own generation that the ledger answered and took. An end
// whose park is refused too, was never answered, is of another generation, or has no park stays refused.
//
// Each row differs from the first in the one thing its name gives, so a row fails when exactly that
// condition of mootEnds is dropped or turned around. A park sequence of zero is a lane that crashed between
// beginning to park and committing, and then ended with its lease gone: no commit was ever journaled.
func TestWhichRefusedEndsAParkMadeMoot(t *testing.T) {
	t.Parallel()
	end := func(seq, generation int64) ledger.UploadEvent {
		return ledger.UploadEvent{Seq: seq, Type: ledger.UploadLaneEnded, Lane: "lane", Generation: generation}
	}
	park := func(seq, generation int64) *lanes.ParkProgress {
		return &lanes.ParkProgress{Seq: seq, Generation: generation}
	}
	parked := func(seq, generation int64) ledger.UploadEvent {
		return ledger.UploadEvent{Seq: seq, Type: ledger.UploadLaneParked, Lane: "lane", Generation: generation}
	}
	tests := []struct {
		name    string
		refused []ledger.UploadEvent
		park    *lanes.ParkProgress
		through int64
		moot    bool
	}{
		{name: "the park the ledger took", refused: []ledger.UploadEvent{end(8, 2)}, park: park(6, 2), through: 9, moot: true},
		{name: "a park answered at the last answered write", refused: []ledger.UploadEvent{end(8, 2)}, park: park(6, 2), through: 6, moot: true},
		{name: "a park past the last answered write", refused: []ledger.UploadEvent{end(8, 2)}, park: park(6, 2), through: 5},
		{name: "a park after the end", refused: []ledger.UploadEvent{end(8, 2)}, park: park(10, 2), through: 12},
		{name: "the end as its own park", refused: []ledger.UploadEvent{end(8, 2)}, park: park(8, 2), through: 12},
		{name: "a park refused again", refused: []ledger.UploadEvent{parked(3, 2), end(4, 2)}, park: park(3, 2), through: 9},
		{name: "a park of another generation", refused: []ledger.UploadEvent{end(8, 3)}, park: park(6, 2), through: 9},
		{name: "a park that never committed", refused: []ledger.UploadEvent{end(8, 2)}, park: park(0, 2), through: 9},
		{name: "a lane that never parked", refused: []ledger.UploadEvent{end(8, 2)}, through: 9},
		{
			name:    "a refusal that is no end",
			refused: []ledger.UploadEvent{{Seq: 8, Type: ledger.UploadLaneStatus, Lane: "lane", Generation: 2}},
			park:    park(6, 2), through: 9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mootEnds(tt.refused, map[string]*lanes.ParkProgress{"lane": tt.park}, tt.through)
			if moot := len(got) == 1; moot != tt.moot || len(got) > 1 {
				t.Errorf("forgot %+v, want moot %v", got, tt.moot)
			}
		})
	}
}

// Only a resume that named no project goes again, and only while a lease still awaits the generation it
// began under the same claim on the same lane, for the same work order at the same fence. The writes of
// the lane under that lease follow it, and nothing of another lane or another claim does.
func TestWhatGoesAgainOfARefusedResume(t *testing.T) {
	t.Parallel()
	event := func(seq int64, eventType, lane, claim, project string, generation int64) ledger.UploadEvent {
		return ledger.UploadEvent{
			Seq: seq, Epoch: 1, Type: eventType, Lane: lane, ClaimID: claim, Project: project, Generation: generation, WorkOrder: "wo", Fence: 3,
		}
	}
	resumed := event(4, ledger.UploadLaneResumed, "lane-b", "claim-b", "", 2)
	refused := []ledger.UploadEvent{
		event(2, ledger.UploadLaneEnded, "lane-b", "claim-old", "pb", 1),
		resumed,
		event(5, ledger.UploadLaneStatus, "lane-a", "claim-a", "pa", 1),
		event(6, ledger.UploadGenerationBound, "lane-b", "claim-b", "pb", 2),
		event(7, ledger.UploadLaneEnded, "lane-b", "claim-b", "", 2),
	}
	awaiting := ledger.DesiredLease{
		Project: "pb", Lane: "lane-b", WorkOrder: "wo", ClaimID: "claim-b", Fence: 3, Resume: &ledger.DesiredResume{Generation: 1},
	}
	copied := func(e ledger.UploadEvent) ledger.UploadEvent {
		e.Epoch, e.Project = 9, "pb"
		return e
	}
	want := []ports.Reissue{{Lane: "lane-b", Project: "pb", Copies: []ledger.UploadEvent{copied(resumed), copied(refused[3]), copied(refused[4])}}}
	if got := reissues(refused, []ledger.DesiredLease{awaiting}, 9); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	named := resumed
	named.Project = "pb"
	running := awaiting
	running.Resume = nil
	later := awaiting
	later.Resume = &ledger.DesiredResume{Generation: 2}
	otherLane := awaiting
	otherLane.Lane = "lane-c"
	fenced := awaiting
	fenced.Fence = 4
	otherOrder := awaiting
	otherOrder.WorkOrder = "wo-2"
	tests := []struct {
		name    string
		refused []ledger.UploadEvent
		lease   ledger.DesiredLease
	}{
		{"a resume that named its project", []ledger.UploadEvent{named}, awaiting},
		{"a claim the ledger no longer awaits a resume under", []ledger.UploadEvent{resumed}, running},
		{"a lease that awaits another generation", []ledger.UploadEvent{resumed}, later},
		{"a lease of another lane", []ledger.UploadEvent{resumed}, otherLane},
		{"a lease at another fence", []ledger.UploadEvent{resumed}, fenced},
		{"a lease of another work order", []ledger.UploadEvent{resumed}, otherOrder},
		{"no lease", []ledger.UploadEvent{resumed}, ledger.DesiredLease{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reissues(tt.refused, []ledger.DesiredLease{tt.lease}, 9); len(got) != 0 {
				t.Errorf("sent again %+v", got)
			}
		})
	}
}

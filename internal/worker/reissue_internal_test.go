package worker

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Only a resume that named no project goes again, and only while a lease still awaits the generation it
// began under the same claim on the same lane. The writes of the lane under that claim follow it, and
// nothing of another lane or another claim does.
func TestWhatGoesAgainOfARefusedResume(t *testing.T) {
	t.Parallel()
	event := func(seq int64, eventType, lane, claim, project string, generation int64) ledger.UploadEvent {
		return ledger.UploadEvent{Seq: seq, Epoch: 1, Type: eventType, Lane: lane, ClaimID: claim, Project: project, Generation: generation}
	}
	resumed := event(4, ledger.UploadLaneResumed, "lane-b", "claim-b", "", 2)
	refused := []ledger.UploadEvent{
		event(2, ledger.UploadLaneEnded, "lane-b", "claim-old", "pb", 1),
		resumed,
		event(5, ledger.UploadLaneStatus, "lane-a", "claim-a", "pa", 1),
		event(6, ledger.UploadGenerationBound, "lane-b", "claim-b", "pb", 2),
		event(7, ledger.UploadLaneEnded, "lane-b", "claim-b", "", 2),
	}
	awaiting := ledger.DesiredLease{Project: "pb", Lane: "lane-b", ClaimID: "claim-b", Resume: &ledger.DesiredResume{Generation: 1}}
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
	tests := []struct {
		name    string
		refused []ledger.UploadEvent
		lease   ledger.DesiredLease
	}{
		{"a resume that named its project", []ledger.UploadEvent{named}, awaiting},
		{"a claim the ledger no longer awaits a resume under", []ledger.UploadEvent{resumed}, running},
		{"a lease that awaits another generation", []ledger.UploadEvent{resumed}, later},
		{"a lease of another lane", []ledger.UploadEvent{resumed}, otherLane},
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

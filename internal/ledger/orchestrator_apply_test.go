package ledger

import (
	"slices"
	"testing"
)

// A run that ended stays ended. A status of that run can reach the ledger after its end, from a worker that
// uploaded it late or twice, and it says nothing of a session that is over.
func TestAStatusAfterTheRunsEndRevivesNothing(t *testing.T) {
	t.Parallel()
	row := Orchestrator{ID: "orchestrator-w", Run: 2}
	row = row.Apply(OrchestratorReport{Lane: row.Lane(), Generation: 1, Actual: "working", Kind: UploadLaneStatus, At: 10})
	row = row.Apply(OrchestratorReport{Lane: row.Lane(), Generation: 1, Actual: StateEnded, Kind: UploadLaneEnded, At: 20})
	late := row.Apply(OrchestratorReport{Lane: row.Lane(), Generation: 1, Actual: "idle", Conditions: []string{"stalled"}, Kind: UploadLaneStatus, At: 30})
	if !late.Ended() || len(late.Conditions) != 0 {
		t.Errorf("a late status moved the ended row: %+v", late)
	}
	if again := late.Apply(OrchestratorReport{Lane: row.Lane(), Actual: StateEnded, Kind: UploadLaneEnded, At: 40}); !again.Ended() {
		t.Errorf("a repeat of the end moved the row: %+v", again)
	}
}

// The next run's row says nothing yet, and its own status applies, while a word about the run it replaced moves
// nothing.
func TestTheNextRunsStatusStillApplies(t *testing.T) {
	t.Parallel()
	ended := Orchestrator{ID: "orchestrator-w", Run: 1, Actual: StateEnded}
	next := Orchestrator{ID: ended.ID, Run: 2}
	if old := next.Apply(OrchestratorReport{Lane: ended.Lane(), Actual: "working", Kind: UploadLaneStatus}); old.Actual != "" {
		t.Errorf("a status of the replaced run moved the next: %+v", old)
	}
	moved := next.Apply(OrchestratorReport{Lane: next.Lane(), Actual: "working", Conditions: []string{"near_budget"}, Kind: UploadLaneStatus})
	if moved.Actual != "working" || !slices.Equal(moved.Conditions, []string{"near_budget"}) {
		t.Errorf("the next run's status did not apply: %+v", moved)
	}
}

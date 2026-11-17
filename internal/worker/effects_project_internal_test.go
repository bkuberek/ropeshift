package worker

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// An effect takes the project of its lane from the run that holds the lane when that run names one, and
// from the lane's record otherwise: a run whose lease names no project, as a lease an earlier build issued
// may not, says nothing about the lane's project, so the record that does is read.
func TestAnEffectTakesItsLanesProject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		run    *laneRun
		record string
		want   string
	}{
		{name: "a run that names the project", run: &laneRun{lease: ledger.DesiredLease{Project: "pa"}}, record: "pb", want: "pa"},
		{name: "a run that names no project", run: &laneRun{}, record: "pb", want: "pb"},
		{name: "no run", record: "pb", want: "pb"},
		{name: "a run that names no project and no record", run: &laneRun{}},
		{name: "no run and no record"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			journal, err := fake.NewJournal()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = journal.Close() })
			if err := journal.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			w := New(nil, journal, fake.NewClock(time.UnixMilli(1_000_000)), Config{})
			if tt.record != "" {
				record := lanes.Record{ID: "lane-b", Project: tt.record, WorkOrder: "wo", ClaimID: "claim", Fence: 1, Generation: 1, State: lanes.StateRunning}
				begun := ledger.UploadEvent{Type: ledger.UploadLaneStarted, WorkOrder: "wo", ClaimID: "claim", Fence: 1, Lane: "lane-b", Generation: 1}
				if _, err := journal.BeginLane(t.Context(), record, begun); err != nil {
					t.Fatal(err)
				}
			}
			if tt.run != nil {
				w.running["lane-b"] = tt.run
			}
			effect := w.inLanesProject(t.Context(), lanes.NewEffect(lanes.EffectInput, "lane-b", 1, "", "nudge"))
			if effect.Project != tt.want {
				t.Errorf("the effect names project %q, want %q", effect.Project, tt.want)
			}
		})
	}
}

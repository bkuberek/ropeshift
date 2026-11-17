package worker

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A resumed generation keeps the project of its lease on both paths, including on a worker that never ran
// the lane and so holds no record of it: the ledger routes the resume, and every later write the journal
// makes of the lane, by that project (ADR-0021).
func TestAResumedRecordNamesTheLeasesProject(t *testing.T) {
	t.Parallel()
	run := &laneRun{lease: ledger.DesiredLease{
		Project: "pb", Lane: "lane-b", WorkOrder: "wo", ClaimID: "claim", Fence: 3,
		Resume: &ledger.DesiredResume{Generation: 1},
	}}
	for _, path := range []lanes.ResumePath{lanes.ResumeNative, lanes.ResumeHandoff} {
		stored := run.resumeRecord(lanes.Record{}, 2, lanes.ResumeChoice{Path: path}, "session-1")
		if stored.Project != "pb" || stored.Generation != 2 || stored.ClaimID != "claim" {
			t.Errorf("%s: the resumed record %+v", path, stored)
		}
	}
}

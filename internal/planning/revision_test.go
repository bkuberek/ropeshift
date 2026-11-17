package planning_test

import (
	"encoding/json"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

func TestARevisionListsEachDecisionAndEachFinishedWorkOrderOnce(t *testing.T) {
	opened := planning.Revision{ID: "r1", PlanID: "factory", ApprovedVersion: 2, OpenedAt: 10}
	first := opened.Extend("d1", []string{"done"})
	second := first.Extend("d2", []string{"done", "load"})
	again := second.Extend("d2", []string{"done"})

	if !slices.Equal(second.DecisionIDs, []string{"d1", "d2"}) {
		t.Errorf("decisions %v, want d1 and d2 in the order they came", second.DecisionIDs)
	}
	want := []planning.RevisionWork{{WorkOrder: "done", Decisions: []string{"d1", "d2"}}, {WorkOrder: "load", Decisions: []string{"d2"}}}
	if !slices.EqualFunc(second.Finished, want, func(a, b planning.RevisionWork) bool {
		return a.WorkOrder == b.WorkOrder && slices.Equal(a.Decisions, b.Decisions)
	}) {
		t.Errorf("finished %+v, want %+v", second.Finished, want)
	}
	if len(again.DecisionIDs) != 2 || len(again.Finished[0].Decisions) != 2 {
		t.Errorf("listing a decision again changed the revision: %+v", again)
	}
	if len(opened.DecisionIDs) != 0 || len(first.DecisionIDs) != 1 || len(first.Finished) != 1 {
		t.Errorf("extending changed the revision it started from: %+v %+v", opened, first)
	}
}

func TestARevisionRecordsTheReworkOfOneWorkOrderAndLeavesOthers(t *testing.T) {
	revision := planning.Revision{ID: "r1", PlanID: "factory"}.Extend("d1", []string{"done", "load"})
	reworked := revision.Reworked("done", "redo")

	if reworked.Finished[0].Rework != "redo" || reworked.Finished[1].Rework != "" {
		t.Errorf("rework recorded as %+v", reworked.Finished)
	}
	if revision.Finished[0].Rework != "" {
		t.Errorf("recording rework changed the revision it started from")
	}
	if skipped := revision.Reworked("elsewhere", "redo"); slices.ContainsFunc(skipped.Finished, func(work planning.RevisionWork) bool { return work.Rework != "" }) {
		t.Errorf("rework of a work order the revision does not list was recorded: %+v", skipped.Finished)
	}
}

func TestARevisionIsOpenUntilItIsClosed(t *testing.T) {
	revision := planning.Revision{ID: "r1", PlanID: "factory", OpenedAt: 10}
	if !revision.Open() {
		t.Fatal("a new revision is closed")
	}
	closed := revision.Close("member:m1", 20, planning.RevisionApproved, "a-1")
	if closed.Open() || closed.Outcome != planning.RevisionApproved || closed.ClosedBy != "member:m1" || closed.Approval != "a-1" || closed.ClosedAt != 20 {
		t.Errorf("closed as %+v", closed)
	}
	if !revision.Open() {
		t.Error("closing changed the revision it started from")
	}
}

func TestARevisionEventHoldsTheWholeRevisionAboutItsPlan(t *testing.T) {
	revision := planning.Revision{ID: "r1", PlanID: "factory", ApprovedVersion: 1, OpenedAt: 10}.Extend("d1", []string{"done"})
	draft, err := planning.RevisionEvent(planning.EventRevisionOpened, revision, ledger.Actor{Kind: ledger.ActorMember, ID: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Type != planning.EventRevisionOpened || draft.Subject != (ledger.Subject{Kind: planning.SubjectPlan, ID: "factory"}) {
		t.Errorf("event %+v is not about the plan", draft)
	}
	var read planning.Revision
	if err := json.Unmarshal(draft.Data, &read); err != nil {
		t.Fatal(err)
	}
	if read.ID != revision.ID || !slices.Equal(read.DecisionIDs, revision.DecisionIDs) || len(read.Finished) != 1 || read.Finished[0].WorkOrder != "done" {
		t.Errorf("event holds %+v, want the revision %+v", read, revision)
	}
}

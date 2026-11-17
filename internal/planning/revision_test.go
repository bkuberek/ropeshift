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
	first := opened.Extend("d1", 10, []string{"done"})
	second := first.Extend("d2", 20, []string{"done", "load"})
	again := second.Extend("d2", 30, []string{"done"})

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
	revision := planning.Revision{ID: "r1", PlanID: "factory"}.Extend("d1", 10, []string{"done", "load"})
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

func TestARevisionNamesTheDecisionsListedAfterAMoment(t *testing.T) {
	revision := planning.Revision{ID: "r1", PlanID: "factory"}.Extend("d1", 10, nil).Extend("d2", 20, nil).Extend("d3", 30, nil)

	for _, test := range []struct {
		at   int64
		want []string
	}{{5, []string{"d1", "d2", "d3"}}, {10, []string{"d2", "d3"}}, {25, []string{"d3"}}, {30, nil}} {
		if got := revision.After(test.at); !slices.Equal(got, test.want) {
			t.Errorf("after %d the revision lists %v, want %v", test.at, got, test.want)
		}
	}
	if legacy := (planning.Revision{DecisionIDs: []string{"d1"}}); len(legacy.After(0)) != 0 {
		t.Errorf("a decision no time is known for was listed after the approval: %v", legacy.After(0))
	}
}

func TestARevisionRestrictedToSomeDecisionsKeepsTheirOrderAndTheWorkTheyReached(t *testing.T) {
	revision := planning.Revision{ID: "r1", PlanID: "factory"}.Extend("d1", 10, []string{"done"}).Extend("d2", 20, []string{"done", "load"}).Extend("d3", 30, nil)
	later := revision.Restrict([]string{"d3", "d2"})

	if !slices.Equal(later.DecisionIDs, []string{"d2", "d3"}) || later.ListedAt["d2"] != 20 || later.ListedAt["d3"] != 30 || len(later.ListedAt) != 2 {
		t.Errorf("decisions %v listed at %v, want d2 and d3 in the order the revision lists them, with their times", later.DecisionIDs, later.ListedAt)
	}
	want := []planning.RevisionWork{{WorkOrder: "done", Decisions: []string{"d2"}}, {WorkOrder: "load", Decisions: []string{"d2"}}}
	if !slices.EqualFunc(later.Finished, want, func(a, b planning.RevisionWork) bool {
		return a.WorkOrder == b.WorkOrder && slices.Equal(a.Decisions, b.Decisions)
	}) {
		t.Errorf("finished %+v, want %+v", later.Finished, want)
	}
	earlier := revision.Restrict([]string{"d1"})
	if len(earlier.Finished) != 1 || earlier.Finished[0].WorkOrder != "done" || len(revision.DecisionIDs) != 3 || len(revision.Finished) != 2 {
		t.Errorf("restricting changed the revision it started from or kept work no kept decision reached: %+v %+v", earlier, revision)
	}
	if none := revision.Restrict(nil); len(none.DecisionIDs) != 0 || len(none.Finished) != 0 {
		t.Errorf("restricted to nothing: %+v", none)
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
	revision := planning.Revision{ID: "r1", PlanID: "factory", ApprovedVersion: 1, OpenedAt: 10}.Extend("d1", 10, []string{"done"})
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

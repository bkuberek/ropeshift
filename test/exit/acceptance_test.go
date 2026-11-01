package exit

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/doubles"
)

// laneTick is the tick runLane asks for.
const laneTick = 20 * time.Millisecond

func (in *install) acceptance(id string) workorders.AcceptanceView {
	in.t.Helper()
	var view workorders.AcceptanceView
	if err := json.Unmarshal([]byte(in.must("work", "acceptance", id, "--json")), &view); err != nil {
		in.t.Fatal(err)
	}
	return view
}

func branchesOf(view workorders.AcceptanceView) []ledger.Artifact {
	var out []ledger.Artifact
	for _, artifact := range view.Artifacts {
		if artifact.Kind == ledger.ArtifactBranch {
			out = append(out, artifact)
		}
	}
	return out
}

// TestM3PlanToDone is the third milestone on the real binary: a contract file reaches `done`
// through review, a lane on fakeclaude, and a member's acceptance. The lane alone vouches for
// its check, so nothing moves until a member stands behind it, and the branch the worker read
// stays linked to the work order as the final artifact (D17).
func TestM3PlanToDone(t *testing.T) {
	in := newInstall(t)
	id, d := in.runLane("lane-checked")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	view := in.acceptance(id)
	if view.Assessment.Needs != workorders.NeedSecondPrincipal {
		t.Fatalf("the standing reads %+v", view.Assessment)
	}
	branches := branchesOf(view)
	if len(branches) != 1 || branches[0].URI != "lane/lane-1" || len(branches[0].Digest) != 40 {
		t.Fatalf("the final artifact: %+v", view.Artifacts)
	}
	if len(view.Checks) != 1 || view.Checks[0].Criterion != "c1" || view.Checks[0].Source != workorders.SourceLane || view.Checks[0].ArtifactID != branches[0].ID {
		t.Fatalf("the check on record: %+v", view.Checks)
	}
	// The acceptance tick runs every 20 ms here, and it must not accept on a lane's word.
	time.Sleep(10 * laneTick)
	if state := in.show(id).State; state != workorders.Acceptance {
		t.Fatalf("the work moved to %s with no second principal", state)
	}
	in.must("work", "accept", id)
	if state := in.show(id).State; state != workorders.Done {
		t.Fatalf("accepted work reads %s", state)
	}
	if after := in.acceptance(id); len(after.Artifacts) != len(view.Artifacts) || len(after.Checks) != 1 {
		t.Errorf("accepted work lost its evidence: %+v", after)
	}
	d.kill()
	events := in.events(t, id)
	last := events[len(events)-1]
	if last.Type != workorders.EventType(workorders.TriggerAcceptancePassed) || last.Actor.Kind != ledger.ActorMember {
		t.Errorf("the last event: %s from %+v", last.Type, last.Actor)
	}
	want := []string{
		workorders.EventType(workorders.TriggerLaneCompleted),
		workorders.EventType(workorders.TriggerAcceptancePassed),
	}
	if got := eventTypes(events); !slices.Equal(got[len(got)-len(want):], want) {
		t.Errorf("the walk ends %v", got)
	}
}

// TestReworkRunsAnotherAttempt sends finished work back: a second lane reports its own
// evidence, the first attempt's stops counting, and both stay on record.
func TestReworkRunsAnotherAttempt(t *testing.T) {
	in := newInstall(t)
	id, _ := in.runLane("lane-completed")
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", "lane-2"))
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	in.must("work", "rework", id, "--reason", "the budget does not hold")
	if state := in.show(id).State; state != workorders.Ready {
		t.Fatalf("reworked work reads %s", state)
	}
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c2")
	in.must("lane", "start", id, "--lane", "lane-2")
	in.waitFor("the second outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	view := in.acceptance(id)
	if view.Assessment.Needs != workorders.NeedMember {
		t.Errorf("the standing reads %+v", view.Assessment)
	}
	if branches := branchesOf(view); len(branches) != 2 || branches[1].URI != "lane/lane-2" {
		t.Errorf("the branches on record: %+v", branches)
	}
	in.must("work", "accept", id)
	if state := in.show(id).State; state != workorders.Done {
		t.Errorf("accepted work reads %s", state)
	}
}

// TestLaneCannotAcceptItsOwnWork holds FR-AUTH-1 at the outside of the binary: a lane's own
// credential, the one thing it holds, opens no acceptance. The use cases refuse the lane as an
// actor on every row of the evidence table, in contract check A1.
func TestLaneCannotAcceptItsOwnWork(t *testing.T) {
	in := newInstall(t)
	id, _ := in.runLane("lane-checked")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	token := doubles.SeenEnv(t, in.path, "ROPESHIFT_LANE_TOKEN")
	if token == "" {
		t.Fatal("the session saw no token")
	}
	lane := []string{authority.EnvMemberToken + "=" + token}
	for _, args := range [][]string{{"work", "accept", id}, {"work", "rework", id, "--reason", "my own work"}} {
		if code, _, _ := in.runWith(nil, "", lane, args...); code != cli.ExitForbidden {
			t.Errorf("%v with a lane's token: exit %d", args, code)
		}
	}
	if state := in.show(id).State; state != workorders.Acceptance {
		t.Errorf("the lane moved its own work to %s", state)
	}
}

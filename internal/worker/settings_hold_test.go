package worker_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// widened is a lane's worktree that added a PermissionRequest hook to the repository's settings.
func widened() *lanes.SettingsChange {
	return &lanes.SettingsChange{
		Files: []string{".claude/settings.json"}, Keys: []string{"hooks"},
		Summary: []string{".claude/settings.json hooks.PermissionRequest: added"}, Digest: "sha256:widened",
	}
}

// heldOnSettings starts a lane whose worktree widened its settings, and waits for it to park.
func heldOnSettings(t *testing.T) *laneRig {
	t.Helper()
	r := newLaneRig(t)
	r.driver.ChangeSettings(widened())
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	r.pass(t)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	return r
}

// settingsHeld reads the settings changes the ledger recorded the lane held on, oldest first.
func (r *laneRig) settingsHeld(t *testing.T) []lanes.SettingsChange {
	t.Helper()
	var out []lanes.SettingsChange
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: "lane-1"})
		for _, e := range events {
			if e.Type == ledger.EventLaneSettingsHeld {
				var change lanes.SettingsChange
				if err := json.Unmarshal(e.Data, &change); err != nil {
					return err
				}
				out = append(out, change)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// choose is a member's choice for the change the lane is held on.
func (r *laneRig) choose(t *testing.T, choice string) workorders.State {
	t.Helper()
	state, err := control.NewLaneSettings(r.store).Decide(t.Context(), scope, r.id, "lane-1",
		ledger.SettingsDecision{Digest: widened().Digest, Choice: choice}, member)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// A lane whose worktree changed its settings in a key that widens what it runs parks before its session starts, with
// no process and no model call, on a person's word, and the ledger names the change for the generation it held.
func TestALaneWhoseSettingsWidenWhatItRunsIsHeldForAMember(t *testing.T) {
	t.Parallel()
	r := heldOnSettings(t)
	if specs := r.driver.Specs(); len(specs) != 0 {
		t.Errorf("a held lane started %d sessions", len(specs))
	}
	held := r.settingsHeld(t)
	if len(held) != 1 || held[0].Generation != 1 || !slices.Equal(held[0].Keys, []string{"hooks"}) || held[0].Digest != widened().Digest {
		t.Errorf("the ledger holds %+v", held)
	}
	if wake := r.wakeOf(t, 1); !wake.Open() || wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v", wake)
	}
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		holds, err := control.SettingsHolds(tx)
		if err == nil && (len(holds) != 1 || holds[0].Lane != "lane-1" || holds[0].WorkOrder.ID != r.id) {
			t.Errorf("the holds read %+v", holds)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A lane whose settings match the repository's starts as any lane does.
func TestALaneWhoseSettingsMatchIsNotHeld(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	r.pass(t)
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	if held := r.settingsHeld(t); len(held) != 0 {
		t.Errorf("the ledger holds %+v", held)
	}
}

// Run with the repository's settings wakes the lane, and its next generation starts with the base commit's settings put
// back and is told so first, so it goes on with the project setting source it always has.
func TestRunningWithTheRepositorysSettingsRestoresThemAndSaysSo(t *testing.T) {
	t.Parallel()
	r := heldOnSettings(t)
	r.choose(t, lanes.SettingsBase)
	r.pass(t)
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	specs := r.driver.Specs()
	if len(specs) != 1 || !specs[0].SettingsAtBase || r.driver.Restored() != 1 {
		t.Fatalf("the lane started %d sessions, restoring %d", len(specs), r.driver.Restored())
	}
	if !strings.HasPrefix(specs[0].Brief, lanes.SettingsRestoredNote([]string{".claude/settings.json"})) {
		t.Errorf("the lane was not told its settings were restored:\n%s", specs[0].Brief)
	}
}

// Run with this lane's settings is the member's decision for that change: the lane's next generation runs with the
// settings it wrote, and nothing is restored.
func TestRunningWithTheLanesSettingsIsAMembersDecision(t *testing.T) {
	t.Parallel()
	r := heldOnSettings(t)
	r.choose(t, lanes.SettingsLane)
	r.pass(t)
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	specs := r.driver.Specs()
	if len(specs) != 1 || specs[0].SettingsAtBase || r.driver.Restored() != 0 {
		t.Fatalf("the lane started %d sessions, restoring %d", len(specs), r.driver.Restored())
	}
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: "lane-1"})
		decided := slices.ContainsFunc(events, func(e ledger.Event) bool {
			return e.Type == ledger.EventLaneSettingsDecided && e.Actor == member && strings.Contains(string(e.Data), `"choice":"lane"`)
		})
		if !decided {
			t.Error("the ledger holds no member's decision for the lane's settings")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Stop blocks the work with the reason settings_changed, ends the parked lane, and retires its wake, so nothing runs
// with the change.
func TestStoppingALaneHeldOnItsSettingsBlocksItsWork(t *testing.T) {
	t.Parallel()
	r := heldOnSettings(t)
	if state := r.choose(t, lanes.SettingsStop); state != workorders.Blocked {
		t.Fatalf("the work is %s", state)
	}
	if wake := r.wakeOf(t, 1); wake.Open() || wake.FiredBy != parking.RetiredSettingsStop {
		t.Errorf("wake %+v", wake)
	}
	r.pass(t)
	if specs := r.driver.Specs(); len(specs) != 0 {
		t.Errorf("a stopped lane started %d sessions", len(specs))
	}
}

// A choice is a member's alone, binds to the change the member read, and only a choice ends the hold: a delegate is
// refused, a stale digest is refused, and a plain resume is refused whoever asks.
func TestOnlyAMembersChoiceEndsASettingsHold(t *testing.T) {
	t.Parallel()
	r := heldOnSettings(t)
	settings := control.NewLaneSettings(r.store)
	delegate := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "worker-1"}
	if _, err := settings.Decide(t.Context(), scope, r.id, "lane-1", ledger.SettingsDecision{Digest: widened().Digest, Choice: lanes.SettingsLane}, delegate); !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("an orchestrator's choice: %v", err)
	}
	if _, err := settings.Decide(t.Context(), scope, r.id, "lane-1", ledger.SettingsDecision{Digest: "sha256:other", Choice: lanes.SettingsLane}, member); !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("a choice for another change: %v", err)
	}
	if _, err := control.NewWakes(r.store).ResumeLane(t.Context(), scope, "lane-1", member); !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("a plain resume: %v", err)
	}
	if row := r.row(); row.State != workorders.Parked {
		t.Errorf("the work is %s", row.State)
	}
}

// A member who runs the lane with the repository's settings, where restoring them still leaves the worktree differing from
// them, as a walk past its bound does, finds the start refused: the lane fails once, with one hold recorded and one
// restore tried, and its generations are the one that was held and the one that was refused, so no later pass spawns
// another or holds again.
func TestAStartRefusedAfterARestoreHoldsOnceAndSpawnsNoGenerationAgain(t *testing.T) {
	t.Parallel()
	r := heldOnSettings(t)
	r.driver.RefuseAfterRestore(errors.New("the repository's settings were restored and the lane is still held"))
	r.choose(t, lanes.SettingsBase)
	for range 4 {
		r.pass(t)
	}
	r.subject.Wait()
	if change := r.outcome(); change.Outcome != workorders.OutcomeFailed || change.Reason != workorders.ReasonSpawnFailed {
		t.Errorf("outcome %+v", change)
	}
	if held := r.settingsHeld(t); len(held) != 1 {
		t.Errorf("the ledger holds %d settings holds: %+v", len(held), held)
	}
	if specs, restored := r.driver.Specs(), r.driver.Restored(); len(specs) != 0 || restored != 1 {
		t.Errorf("the lane started %d sessions and tried %d restores, want none and one", len(specs), restored)
	}
	if generations := r.readGenerations(t); len(generations) != 2 || generations[0].EndReason != "parked" || generations[1].EndReason != "failed" {
		t.Errorf("the lane began %d generations, want the one that was held and the one that was refused: %+v", len(generations), generations)
	}
}

package worker_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A project's limit touches only its own project (FR-PROJ-1). Two projects of one installation each have a lane on one
// worker and the same daily limit. The first has spent its day, so its lane parks on the project's limit with no process
// and no model call, and the second, whose day has room, runs its lane to its end. What one project spent is in its own
// ledger and not the other's, so the second never read it.
func TestAProjectLimitParksItsOwnLaneAndLeavesAnotherProjectsRunning(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.profile.Put("/repository", handoffProfile)
	r.dispatch("pa", "lane-a")
	r.dispatch("pb", "lane-b")
	projectLimitIn(t, r.stores["pa"], "pa", dailyLimit, dailyLimit)
	projectLimitIn(t, r.stores["pb"], "pb", dailyLimit, 0)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(), completed(0.1)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row("pa"); row.State != workorders.Parked {
		t.Errorf("the project past its limit has its work order %s, want parked", row.State)
	}
	if row := r.row("pb"); row.State != workorders.Acceptance {
		t.Errorf("the project with room has its work order %s, want it done", row.State)
	}
	if specs := r.driver.Specs(); len(specs) != 1 || specs[0].Lane != "lane-b" {
		t.Errorf("sessions %+v, want the second project's lane alone", specs)
	}
	var wakes []string
	err := r.stores["pa"].Read(t.Context(), "pa", func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		open, err := tx.Wakes().Open()
		for _, wake := range open {
			if wake.Expr == lanes.ProjectLimitWake(ledger.WindowEnd(ledger.WindowDay, now)) {
				wakes = append(wakes, wake.ID)
			}
		}
		return err
	})
	if err != nil || len(wakes) != 1 {
		t.Errorf("the first project holds %d wakes on its limit, want its lane's: %v", len(wakes), err)
	}
	// A claim in the project past its limit is refused, and the other project grants its own.
	_, err = control.NewLeases(r.stores["pa"]).Claim(t.Context(), "pa", workorders.ClaimRequest{WorkOrder: "wo", ClaimID: "claim-again", Worker: r.subject.Auth().WorkerID, TTLMs: 60_000}, member)
	var limit *ledger.ProjectLimitError
	if !errors.As(err, &limit) || limit.Window != ledger.WindowDay {
		t.Errorf("a claim in a project past its limit was answered %v, want the day's limit", err)
	}
}

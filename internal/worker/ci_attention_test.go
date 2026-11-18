package worker_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ciKinds is what CI asks a person about the rig's pull request now.
func (f *forgeRig) ciKinds() []string {
	f.t.Helper()
	var kinds []string
	err := f.store.Read(f.t.Context(), scope, func(tx ports.ReadTx) error {
		items, err := control.CIItems(tx)
		for _, item := range items {
			kinds = append(kinds, item.Kind)
		}
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return kinds
}

// A run that is queued and never starts raises "No runner picked it up" at its limit, 30 minutes with no history, and
// never "CI has not finished", whatever the CI timeout. The worker reads it on its schedule, and starts no lane and no
// model call at any point of the wait.
func TestAQueuedForeverRunRaisesNoRunnerAndStartsNoLane(t *testing.T) {
	t.Parallel()
	f, _, _ := ciRig(t)
	number := f.forge.PullRequests()[0].Number
	f.forge.SetChecks(number, ledger.PullRequestCheck{Name: "CI", State: ledger.CheckPending, Status: ledger.CIRunQueued, Required: true})
	f.converge(2)
	sessions := len(f.driver.Specs())
	if kinds := f.ciKinds(); len(kinds) != 0 {
		t.Fatalf("a run just queued raises %v", kinds)
	}
	step := func(by time.Duration) {
		f.offset.Add(by.Milliseconds())
		f.advance(by)
	}
	step(25 * time.Minute)
	if kinds := f.ciKinds(); len(kinds) != 0 {
		t.Fatalf("a run queued for 25 minutes raises %v", kinds)
	}
	for _, wait := range []time.Duration{10 * time.Minute, time.Hour, 5 * time.Hour} {
		step(wait)
		if kinds := f.ciKinds(); len(kinds) != 1 || kinds[0] != ledger.CIItemQueueStalled {
			t.Fatalf("a run queued for %s further raises %v, want %s alone", wait, kinds, ledger.CIItemQueueStalled)
		}
	}
	if got := len(f.driver.Specs()); got != sessions {
		t.Errorf("a run no runner picked up started %d sessions", got-sessions)
	}
}

//go:build chaos

package chaos

import (
	"context"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
)

// These tests cover the steps a lease renewal names, and start no daemon and no child.

// renewDecided is the step a renewal hits when its guarded statement has run and it knows whether it won.
// The solo-sleep check releases the sweep at it, so it is hit while the renewal still holds the writer
// lock, whether the renewal won or was refused, and never by a renewal that ended before its statement.
const renewDecided = "decided"

// TestARenewalNamesTheStepWhereItsStatementDecided holds the steps a renewal hits: a renewal that won and one
// the guard refused both hit renewDecided before they end, in order, and one that ends after it began but
// before its statement ran never does.
func TestARenewalNamesTheStepWhereItsStatementDecided(t *testing.T) {
	for _, c := range []struct {
		name string
		// stale is whether the renewal names a fence the lease no longer holds.
		stale bool
		// abort is whether the context ends when the renewal begins.
		abort bool
		want  []string
	}{
		{name: "a renewal that won", want: []string{"begun", renewDecided, "before-commit", "committed"}},
		{name: "a renewal the guard refused", stale: true, want: []string{"begun", renewDecided}},
		{name: "a renewal that ends after it began", abort: true, want: []string{"begun"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rig := contract.NewRig(t, contract.SQLiteFile)
			store := rig.Store()
			id, err := rig.Ready(store)
			if err != nil {
				t.Fatal(err)
			}
			grant, err := rig.NewOps(store).Claim(rig.Ctx, rig.Scope,
				workorders.ClaimRequest{WorkOrder: id, ClaimID: rig.ID("claim"), Worker: "parent", TTLMs: longTTL}, rig.Actor)
			if err != nil {
				t.Fatal(err)
			}
			lease := grant.Lease
			if c.stale {
				lease.Fence++
			}
			ctx, cancel := context.WithCancel(rig.Ctx)
			defer cancel()
			var steps []string
			crashpoint.Arm(func(op, step string) {
				if op != sqlstore.OpRenew {
					return
				}
				steps = append(steps, step)
				if c.abort && step == "begun" {
					cancel()
				}
			})
			t.Cleanup(func() { crashpoint.Arm(func(string, string) {}) })
			err = store.Renew(ctx, rig.Scope, lease, longTTL)
			if (err != nil) != (c.stale || c.abort) {
				t.Errorf("the renewal returned %v", err)
			}
			if !slices.Equal(steps, c.want) {
				t.Errorf("the renewal hit %v, want %v", steps, c.want)
			}
		})
	}
}

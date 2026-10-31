//go:build chaos && postgres

package chaos

import (
	"context"
	"errors"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/pgtest"
)

func init() { afterTests = pgtest.StopShared }

// plainFenceRead compares the fence with a read inside the transaction, which is
// atomic only if something serialized the transaction first.
type plainFenceRead struct {
	contract.Ops
	store ports.Store
}

func (p plainFenceRead) Write(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, payload []byte, actor ledger.Actor) (ledger.Event, error) {
	var written ledger.Event
	err := p.store.Write(ctx, scope, "fenced_write", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(holder.WorkOrder)
		if err != nil {
			return err
		}
		if current.Fence != holder.Fence {
			return &workorders.FencedError{Current: current.Fence, State: current.State}
		}
		if stepper, ok := p.store.(contract.Stepper); ok {
			stepper.Step("fenced_write", contract.StepGuarded)
		}
		event, err := workorders.ChangeEvent(workorders.EventFencedWrite, holder.WorkOrder, workorders.Change{
			Guarded: true, Fence: holder.Fence, ClaimID: holder.ClaimID, Lane: holder.Lane,
			Generation: holder.Generation, Payload: payload,
		}, actor)
		if err != nil {
			return err
		}
		written, err = tx.Append(event)
		return err
	})
	return written, err
}

// TestRevisionLastFailsOnPostgres shows the two defects SQLite cannot show,
// because BEGIN IMMEDIATE already serializes it. The same checks hold in the
// contract suite with the revision first, so these failures come from the order.
func TestRevisionLastFailsOnPostgres(t *testing.T) {
	tests := []struct {
		name  string
		check string
		ops   func(ports.Store) contract.Ops
	}{
		{
			name:  "a write in flight, with a plain fence read",
			check: "L4",
			ops: func(s ports.Store) contract.Ops {
				return plainFenceRead{Ops: contract.RealOps(s), store: s}
			},
		},
		{name: "a work-in-progress limit under racing claims", check: "L15"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check, ok := findCheck(tt.check)
			if !ok {
				t.Fatalf("unknown check %q", tt.check)
			}
			sqlstore.Sabotaged(sqlstore.Sabotage{RevisionLast: true})
			defer sqlstore.Sabotaged(sqlstore.Sabotage{})
			rig := contract.NewRig(t, pgtest.Backend)
			if tt.ops != nil {
				rig.NewOps = tt.ops
			}
			err := check.Run(rig)
			var violation *contract.Violation
			switch {
			case errors.As(err, &violation):
				t.Logf("caught by %s: %s", tt.check, violation.Message)
			case err != nil:
				t.Errorf("a harness error is not a catch: %v", err)
			default:
				t.Errorf("NOT CAUGHT by %s", tt.check)
			}
		})
	}
}

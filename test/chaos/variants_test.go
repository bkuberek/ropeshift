//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// variant is one deliberately broken build of an invariant. Sabotage changes the
// adapter, and Ops replaces a use case. Neither exists in a normal build.
type variant struct {
	name string
	// sabotage breaks the adapter for this process.
	sabotage sqlstore.Sabotage
	// ops replaces the use cases.
	ops func(ports.Store) contract.Ops
	// check is a contract check ID, or "crash" for the crash case below.
	check string
	// crash is the case and step a crash variant runs.
	crash func() (crashCase, string)
}

// replace returns a Rewrite that swaps one piece of one statement. It panics when
// the text is gone, so a variant cannot quietly become a no-op.
func replace(statement, from, to string) func(name, text string) string {
	return func(name, text string) string {
		if name != statement {
			return text
		}
		if !strings.Contains(text, from) {
			panic(fmt.Sprintf("variant: %s no longer holds %q", statement, from))
		}
		return strings.Replace(text, from, to, 1)
	}
}

// splitCreate records a work order in one transaction and its event in another,
// which a crash between them exposes as a row with no history.
type splitCreate struct {
	contract.Ops
	store ports.Store
}

func (s splitCreate) Create(ctx context.Context, scope ledger.WorkspaceID, draft workorders.WorkOrder, actor ledger.Actor) (workorders.WorkOrder, error) {
	record, err := workorders.New(draft)
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	err = s.store.Write(ctx, scope, "work_order.create", func(tx ports.WriteTx) error {
		if _, err := tx.WorkOrders().Get(record.ID); err == nil {
			return nil
		}
		return tx.WorkOrders().Insert(record)
	})
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	crashpoint.Hit("work_order.create", "split")
	return record, s.store.Write(ctx, scope, "work_order.create", func(tx ports.WriteTx) error {
		event, err := workorders.CreatedEvent(record, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// allVariants lists every broken variant. The name is also how a child finds one.
func allVariants() []variant {
	return []variant{
		{
			name:     "revision taken last",
			sabotage: sqlstore.Sabotage{RevisionLast: true},
			check:    "S10",
		},
		{
			name: "a revision is skipped",
			sabotage: sqlstore.Sabotage{Rewrite: replace(sqlstore.StatementTakeRevision,
				"rev = rev + 1", "rev = rev + 2")},
			check: "S1",
		},
		{
			name: "the event list has no workspace scope",
			sabotage: sqlstore.Sabotage{Rewrite: replace("event.list",
				"workspace_id = :workspace_id AND rev > :after", "(:workspace_id = :workspace_id) AND rev > :after")},
			check: "S5",
		},
		{
			name: "a projection differs from its event",
			sabotage: sqlstore.Sabotage{Rewrite: replace("work_order.insert",
				":title, :repository_id", "'tampered', :repository_id")},
			check: "S8",
		},
		{
			name:     "a transaction with no event commits",
			sabotage: sqlstore.Sabotage{CommitEmpty: true},
			check:    "S4",
		},
		{
			name:     "a work order and its event in two transactions",
			sabotage: sqlstore.Sabotage{CommitEmpty: true},
			ops: func(s ports.Store) contract.Ops {
				return splitCreate{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) {
				c := createCase()
				return c, "split"
			},
		},
	}
}

func findVariant(name string) (variant, bool) {
	for _, v := range allVariants() {
		if v.name == name {
			return v, true
		}
	}
	return variant{}, false
}

// variantOps wraps a child's store for its variant.
func variantOps(name string, store ports.Store) (ports.Store, contract.Ops, error) {
	if name == "" {
		return store, contract.RealOps(store), nil
	}
	v, ok := findVariant(name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown variant %q", name)
	}
	sqlstore.Sabotaged(v.sabotage)
	if v.ops != nil {
		return store, v.ops(store), nil
	}
	return store, contract.RealOps(store), nil
}

func findCheck(id string) (contract.Check, bool) {
	for _, c := range contract.AllChecks() {
		if c.ID == id {
			return c, true
		}
	}
	return contract.Check{}, false
}

// runVariant returns the check's verdict on one variant.
func runVariant(t *testing.T, v variant) error {
	if v.check == "crash" {
		c, step := v.crash()
		return runCrashCase(newWorld(t, v.name), c, step)
	}
	check, ok := findCheck(v.check)
	if !ok {
		t.Fatalf("unknown check %q", v.check)
	}
	sqlstore.Sabotaged(v.sabotage)
	defer sqlstore.Sabotaged(sqlstore.Sabotage{})
	rig := contract.NewRig(t, contract.SQLiteFile)
	if v.ops != nil {
		rig.NewOps = v.ops
	}
	return check.Run(rig)
}

// TestBrokenVariants is the proof that the checks can fail. A variant counts as
// caught only through a typed violation, so an unrelated error cannot pass.
func TestBrokenVariants(t *testing.T) {
	caught := 0
	for _, v := range allVariants() {
		t.Run(v.name, func(t *testing.T) {
			err := runVariant(t, v)
			var violation *contract.Violation
			switch {
			case errors.As(err, &violation):
				t.Logf("caught by %s: %s", v.check, violation.Message)
				caught++
			case err != nil:
				t.Errorf("a harness error is not a catch: %v", err)
			default:
				t.Errorf("NOT CAUGHT: %s passed %s", v.name, v.check)
			}
		})
	}
	t.Logf("broken variants caught: %d of %d", caught, len(allVariants()))
}

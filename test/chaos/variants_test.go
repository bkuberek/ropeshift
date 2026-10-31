//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
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
	// wrap decorates every Store handle.
	wrap func(ports.Store) ports.Store
	// check is a contract check ID, "crash" for the crash case below, "random"
	// for a run of random kills, or "paused" for the stopped writer.
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

// chain applies several rewrites in order.
func chain(rewrites ...func(name, text string) string) func(name, text string) string {
	return func(name, text string) string {
		for _, rewrite := range rewrites {
			text = rewrite(name, text)
		}
		return text
	}
}

// noteEvent keeps a split transaction's revisions gapless, so that only a crash
// between the two halves can expose it.
func noteEvent(actor ledger.Actor) ledger.EventDraft {
	return ledger.EventDraft{Type: "note.added", Actor: actor, Subject: ledger.Subject{Kind: "note", ID: "split"}}
}

// splitCreate records a work order in one transaction and its event in another.
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
		if err := tx.WorkOrders().Insert(record); err != nil {
			return err
		}
		_, err := tx.Append(noteEvent(actor))
		return err
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

// splitClaim moves the fence in one transaction and records the grant in another.
type splitClaim struct {
	contract.Ops
	store ports.Store
}

func (s splitClaim) Claim(ctx context.Context, scope ledger.WorkspaceID, request workorders.ClaimRequest, actor ledger.Actor) (workorders.Grant, error) {
	var current workorders.WorkOrder
	err := s.store.Write(ctx, scope, "claim", func(tx ports.WriteTx) error {
		won, err := tx.Leases().Grant(request)
		if err != nil {
			return err
		}
		if !won {
			return workorders.ErrLost
		}
		if current, err = tx.WorkOrders().Get(request.WorkOrder); err != nil {
			return err
		}
		_, err = tx.Append(noteEvent(actor))
		return err
	})
	if err != nil {
		return workorders.Grant{}, err
	}
	crashpoint.Hit("claim", "split")
	grant := workorders.Grant{Lease: workorders.Lease{WorkOrder: request.WorkOrder, ClaimID: request.ClaimID, Fence: current.Fence}}
	err = s.store.Write(ctx, scope, "claim", func(tx ports.WriteTx) error {
		err := tx.Claims().Record(workorders.Claim{
			ID: request.ClaimID, WorkOrder: request.WorkOrder, Worker: request.Worker,
			Fence: current.Fence, GrantedAt: tx.Now(),
		})
		if err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(grantEventType(), request.WorkOrder, workorders.Change{
			Trigger: workorders.TriggerClaimGranted, Fence: current.Fence, ClaimID: request.ClaimID,
			Worker: request.Worker, LeaseUntil: current.LeaseUntil,
		}, actor)
		if err != nil {
			return err
		}
		appended, err := tx.Append(event)
		grant.Rev = appended.Rev
		return err
	})
	return grant, err
}

// outsideCheck compares the fence before its transaction, so a handover can pass
// between the check and the write.
type outsideCheck struct {
	contract.Ops
	store ports.Store
}

func (o outsideCheck) Write(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, payload []byte, actor ledger.Actor) (ledger.Event, error) {
	var current workorders.WorkOrder
	err := o.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		current, err = tx.WorkOrders().Get(holder.WorkOrder)
		return err
	})
	if err != nil {
		return ledger.Event{}, err
	}
	if current.Fence != holder.Fence {
		return ledger.Event{}, &workorders.FencedError{Current: current.Fence, State: current.State}
	}
	if stepper, ok := o.store.(contract.Stepper); ok {
		stepper.Step("fenced_write", contract.StepGuarded)
	}
	return appendWrite(ctx, o.store, scope, holder, payload, actor)
}

func appendWrite(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, holder workorders.Holder, payload []byte, actor ledger.Actor) (ledger.Event, error) {
	var written ledger.Event
	err := store.Write(ctx, scope, "fenced_write.unguarded", func(tx ports.WriteTx) error {
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

// renewOnly gives a returning worker only the renewal statement, which excludes
// `lapsed`.
type renewOnly struct {
	contract.Ops
}

func (r renewOnly) Return(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, ttlMs int64, _ ledger.Actor) error {
	return r.Renew(ctx, scope, lease, ttlMs)
}

var memoryFence atomic.Int64

// fenceInMemory takes the fence from a process counter, which a restart resets.
func fenceInMemory(name, text string) string {
	if name != "lease.grant" {
		return text
	}
	if !strings.Contains(text, "fence = fence + 1") {
		panic("variant: lease.grant no longer moves the fence")
	}
	return strings.Replace(text, "fence = fence + 1", "fence = "+strconv.FormatInt(memoryFence.Add(1), 10), 1)
}

func forgetFence(store ports.Store) ports.Store {
	memoryFence.Store(0)
	return store
}

const (
	stateGuard = "\n   AND state = 'ready'"
	fenceGuard = "\n   AND fence = :fence"
	leaseGuard = "\n   AND fence = :fence\n   AND claim_id = :claim_id"
)

// allVariants lists every broken variant. The name is also how a child finds one.
func allVariants() []variant {
	noStateGuard := sqlstore.Sabotage{Rewrite: replace("lease.grant", stateGuard, "")}
	noFencePredicate := sqlstore.Sabotage{Rewrite: replace("lease.guard", fenceGuard, "")}
	memoryFences := sqlstore.Sabotage{Rewrite: fenceInMemory}
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
			name: "a work order and its event in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitCreate{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return createCase(), "split" },
		},
		{name: "claim without the state guard", sabotage: noStateGuard, check: "L1"},
		{name: "claim without the state guard", sabotage: noStateGuard, check: "random"},
		{name: "fence kept in process memory", sabotage: memoryFences, wrap: forgetFence, check: "L2"},
		{name: "fence kept in process memory", sabotage: memoryFences, wrap: forgetFence, check: "random"},
		{name: "write without the fencing predicate", sabotage: noFencePredicate, check: "L3"},
		{name: "write without the fencing predicate", sabotage: noFencePredicate, check: "L5"},
		{name: "write without the fencing predicate", sabotage: noFencePredicate, check: "paused"},
		{
			name: "fence checked outside the transaction",
			ops: func(s ports.Store) contract.Ops {
				return outsideCheck{Ops: contract.RealOps(s), store: s}
			},
			check: "L4",
		},
		{
			name: "fence moves only on a grant",
			sabotage: sqlstore.Sabotage{Rewrite: chain(
				replace("lease.revoke", "\n       fence = fence + 1,", ""),
				replace("lease.release", "\n       fence = fence + 1,", ""),
			)},
			check: "L5",
		},
		{
			name:     "lapse transfers ownership",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.lapse", "SET state = 'lapsed'", "SET state = 'ready'")},
			check:    "L6",
		},
		{
			name:     "renewal without the fence predicate",
			sabotage: sqlstore.Sabotage{Rewrite: replace(sqlstore.OpRenew, leaseGuard, "")},
			check:    "L8",
		},
		{
			name:     "lapse trusts the sweep's earlier read",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.lapse", "\n   AND lease_until <= :now", "")},
			check:    "L9",
		},
		{name: "time read on the client", sabotage: sqlstore.Sabotage{ClientClock: true}, check: "L10"},
		{
			name:     "row count 0 means lost",
			sabotage: sqlstore.Sabotage{Rewrite: replace("claim.find", "AND id = :id", "AND id = :id AND 1 = 0")},
			check:    "L11",
		},
		{
			name: "only running can lapse",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.lapse",
				"state IN ('claimed', 'running')", "state IN ('running')")},
			check: "L12",
		},
		{
			name:  "only renewal exists for a return",
			ops:   func(s ports.Store) contract.Ops { return renewOnly{Ops: contract.RealOps(s)} },
			check: "L13",
		},
		{
			name: "return without a guard",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.return",
				"\n   AND fence = :fence\n   AND claim_id = :claim_id\n   AND state = 'lapsed'", "")},
			check: "L14",
		},
		{
			name: "claim skips eligibility",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.work_in_progress",
				"state IN ('claimed', 'running', 'lapsed')", "state IN ('nothing')")},
			check: "L15",
		},
		{
			name: "write without the generation check",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.generation",
				"SELECT current_generation FROM lanes", "SELECT 1 FROM lanes")},
			check: "L16",
		},
		{
			name: "claim split into two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitClaim{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return claimCase(), "split" },
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
	if v.wrap != nil {
		store = v.wrap(store)
	}
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
	switch v.check {
	case "crash":
		c, step := v.crash()
		return runCrashCase(newWorld(t, v.name), c, step)
	case "paused":
		return checkPausedWorker(newWorld(t, v.name))
	case "random":
		return runRandomVariant(t, v)
	}
	check, ok := findCheck(v.check)
	if !ok {
		t.Fatalf("unknown check %q", v.check)
	}
	sqlstore.Sabotaged(v.sabotage)
	defer sqlstore.Sabotaged(sqlstore.Sabotage{})
	rig := contract.NewRig(t, contract.SQLiteFile)
	rig.Wrap = v.wrap
	if v.ops != nil {
		rig.NewOps = v.ops
	}
	return check.Run(rig)
}

// runRandomVariant repeats a run up to a bound, because a defect that needs one
// interleaving is not found in every schedule.
func runRandomVariant(t *testing.T, v variant) error {
	var err error
	for attempt := range 4 {
		run := newRandomRun(newWorld(t, v.name), crashKills(40))
		run.seed += uint64(attempt)
		if err := run.seedWork(3); err != nil {
			return err
		}
		if err = run.execute(); err != nil {
			return err
		}
	}
	return err
}

// TestBrokenVariants is the proof that the checks can fail. A variant counts as
// caught only through a typed violation, so an unrelated error cannot pass.
func TestBrokenVariants(t *testing.T) {
	caught := 0
	for _, v := range allVariants() {
		t.Run(v.check+"-"+v.name, func(t *testing.T) {
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

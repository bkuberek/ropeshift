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

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/gitcli"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// variant is one deliberately broken build of an invariant. Sabotage changes the
// adapter, and Ops replaces a use case. Neither exists in a normal build.
type variant struct {
	name string
	// sabotage breaks the adapter for this process.
	sabotage sqlstore.Sabotage
	// gitSabotage breaks the git adapter for this process.
	gitSabotage gitcli.Sabotage
	// ops replaces the use cases.
	ops func(ports.Store) contract.Ops
	// wrap decorates every Store handle.
	wrap func(ports.Store) ports.Store
	// check is a contract check ID, "crash" for the crash case below, "random"
	// for a run of random kills, "paused" for the stopped writer, "holder" for a run
	// that stops a holder on purpose, or "shim" for a worker reading a shim.
	check string
	// crash is the case and step a crash variant runs.
	crash func() (crashCase, string)
	// pane names a broken driver for a pane run, and paneStep the step it crashes at.
	pane     string
	paneStep string
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

// splitIntake records a contract version in one transaction and its work orders in
// another, so a kill between them leaves a digest that says the file is done.
type splitIntake struct {
	contract.Ops
	store ports.Store
}

func (s splitIntake) Poll(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource) (control.IntakeReport, error) {
	batch, err := source.Poll(ctx, "")
	if err != nil {
		return control.IntakeReport{}, err
	}
	item := batch.Items[0]
	version := planning.Contract{
		ID: item.Key.NativeID, Version: 1, PlanID: "plan", Title: item.Contract.Title, Digest: item.Digest,
		Criteria: item.Contract.Criteria, Status: planning.StatusActive,
	}
	recorded := false
	err = s.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		if _, err := tx.Planning().Contract(version.ID, 1); err == nil {
			recorded = true
			return nil
		}
		if err := tx.Planning().InsertContract(version); err != nil {
			return err
		}
		event, err := planning.RecordEvent(planning.EventContractVersion, planning.SubjectContract, version.ID, version, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil || recorded {
		return control.IntakeReport{}, err
	}
	crashpoint.Hit("intake.item", "split")
	return control.IntakeReport{}, s.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		order, err := workorders.New(workorders.WorkOrder{ID: item.Contract.WorkOrders[0].ID, Kind: workorders.KindChange})
		if err != nil {
			return err
		}
		if err := tx.WorkOrders().Insert(order); err != nil {
			return err
		}
		event, err := workorders.CreatedEvent(order, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// splitRepin records a contract's new version in one transaction and pins its work in
// another, so a kill between them leaves a digest that says the file is done and ready work
// on the last version.
type splitRepin struct {
	contract.Ops
	store ports.Store
}

func (s splitRepin) Poll(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource) (control.IntakeReport, error) {
	batch, err := source.Poll(ctx, "")
	if err != nil {
		return control.IntakeReport{}, err
	}
	item := batch.Items[0]
	var order workorders.WorkOrder
	recorded := false
	err = s.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		link, err := tx.Planning().Link(planning.SubjectContract, item.Key)
		if err != nil {
			return err
		}
		latest, err := tx.Planning().LatestContract(link.RecordID)
		if err != nil {
			return err
		}
		if latest.Digest == item.Digest {
			recorded = true
			return nil
		}
		rows, err := tx.WorkOrders().ByContract(latest.ID)
		if err != nil {
			return err
		}
		order = rows[0]
		next := latest
		next.Version, next.Digest = latest.Version+1, item.Digest
		if err := tx.Planning().InsertContract(next); err != nil {
			return err
		}
		event, err := planning.RecordEvent(planning.EventContractVersion, planning.SubjectContract, next.ID, next, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil || recorded {
		return control.IntakeReport{}, err
	}
	crashpoint.Hit("intake.item", "split")
	return control.IntakeReport{}, s.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		if err := tx.WorkOrders().Pin(order.ID, order.ContractVersion+1); err != nil {
			return err
		}
		event, err := workorders.PinEvent(order.ID, order.ContractVersion+1, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		if _, err := tx.Append(event); err != nil {
			return err
		}
		return tx.WorkOrders().Reviewed(order.ID, "")
	})
}

// pinsStarted moves a pin the way a poll must not: under a lane that already started.
type pinsStarted struct {
	contract.Ops
	store ports.Store
}

func (p pinsStarted) Poll(ctx context.Context, scope ledger.WorkspaceID, source ports.WorkSource) (control.IntakeReport, error) {
	report, err := p.Ops.Poll(ctx, scope, source)
	if err != nil || report.Versions == 0 {
		return report, err
	}
	return report, p.store.Write(ctx, scope, "intake.item", func(tx ports.WriteTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != workorders.Running {
				continue
			}
			if err := tx.WorkOrders().Pin(row.ID, row.ContractVersion+1); err != nil {
				return err
			}
			event, err := workorders.PinEvent(row.ID, row.ContractVersion+1, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
			if err != nil {
				return err
			}
			if _, err := tx.Append(event); err != nil {
				return err
			}
		}
		return nil
	})
}

// outsideCheck compares the fence before its transaction, so a handover can pass
// between the check and the write.
type outsideCheck struct {
	contract.Ops
	store ports.Store
}

func (o outsideCheck) Write(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, _ string, payload []byte, actor ledger.Actor) (ledger.Event, error) {
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

	deadlineGuard = "\n   AND lease_until > :now"
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
		{name: "write without the fencing predicate", sabotage: noFencePredicate, check: "holder"},
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
			name:     "renewal revives an expired lease",
			sabotage: sqlstore.Sabotage{Rewrite: replace(sqlstore.OpRenew, deadlineGuard, "")},
			check:    "L24",
		},
		{name: "renewal revives an expired lease", check: "solo-sleep"},
		{
			name:     "a lane's end keeps the fence",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.end", "\n       fence = fence + 1,", "")},
			check:    "L26",
		},
		{
			name:     "a lane's end without the fence predicate",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.end", leaseGuard, "")},
			check:    "L27",
		},
		{
			name: "a lane's end without the generation check",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.generation",
				"SELECT current_generation FROM lanes", "SELECT 1 FROM lanes")},
			check: "L28",
		},
		{
			name:     "a lane's end keeps the claim",
			sabotage: sqlstore.Sabotage{Rewrite: replace("claim.end", "ended_at IS NULL", "1 = 0")},
			check:    "L25",
		},
		{
			name:     "a dispatch leaves no record on the claim",
			sabotage: sqlstore.Sabotage{Rewrite: replace("claim.dispatch", "ended_at IS NULL", "1 = 0")},
			check:    "L31",
		},
		{
			name:  splitLaneEnd,
			check: "crash",
			crash: func() (crashCase, string) { return workerLaneCase(), laneSplitStep },
		},
		{name: sabotageTrackedOnly, check: "crash", crash: func() (crashCase, string) { return stopCase("worker.lane", "allocated"), "allocated" }},
		{name: sabotageOwnIndex, check: "crash", crash: func() (crashCase, string) { return stopCase("worker.lane", "allocated"), "allocated" }},
		{name: sabotageNoGuard, check: "crash", crash: func() (crashCase, string) { return stopCase("worker.lane", "allocated"), "allocated" }},
		{name: sabotageFenced, check: "crash", crash: func() (crashCase, string) { return fencedCase("worker.lane", "allocated"), "allocated" }},
		{name: sabotageKeeps, check: "crash", crash: func() (crashCase, string) { return stopCase("worker.lane", "allocated"), "allocated" }},
		{name: "a removal that reads nothing first", check: "removal"},
		{name: "a guard's push that forces", check: "force", gitSabotage: gitcli.Sabotage{ForcePush: true}},
		{name: "a reader that skips what it missed", check: "shim"},
		{name: "a trust dialog confirmed where its focus stands", check: "pane", pane: claudecode.SabotageBlindConfirm, paneStep: "checked"},
		{name: "a brief sent again after a crash", check: "pane", pane: claudecode.SabotageRepeatBrief, paneStep: "briefed"},
		{name: "a crash charged again by the next worker", check: "restart"},
		{
			name:  retriesBlind,
			check: "crash",
			crash: func() (crashCase, string) { return effectCase("effect.session"), "performed" },
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
			name: "claim skips the repository cap",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.in_repository",
				"state IN ('claimed', 'running', 'lapsed')", "state IN ('nothing')")},
			check: "L32",
		},
		{
			name: "claim skips the worker cap",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.held_by_worker",
				"state IN ('claimed', 'running', 'lapsed')", "state IN ('nothing')")},
			check: "L32",
		},
		{
			name: "a lapsed lease leaves the worker's count",
			sabotage: sqlstore.Sabotage{Rewrite: chain(
				replace("lease.held_by_worker", "state IN ('claimed', 'running', 'lapsed')", "state IN ('claimed', 'running')"),
				replace("lease.work_in_progress", "state IN ('claimed', 'running', 'lapsed')", "state IN ('claimed', 'running')"),
			)},
			check: "L33",
		},
		{
			name: "write without the generation check",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.generation",
				"SELECT current_generation FROM lanes", "SELECT 1 FROM lanes")},
			check: "L16",
		},
		{
			name: "a contract version and its work orders in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitIntake{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return intakeCase("intake.item", withSteps()), "split" },
		},
		{
			name: "an uploaded event loses its identity",
			sabotage: sqlstore.Sabotage{Rewrite: replace("event.append",
				":rev, :id, :type", ":rev, :id || CAST(:rev AS TEXT), :type")},
			check: "plane",
			crash: func() (crashCase, string) {
				return uploadCase("worker.upload", []string{"answered"}), "answered"
			},
		},
		{
			name: "a lapsed lease cannot return",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.return",
				"state = 'lapsed'", "state = 'nothing'")},
			check: "sleep",
		},
		{
			name:        "a create keeps a dead registration",
			gitSabotage: gitcli.Sabotage{SkipPrune: true},
			check:       "worktree",
		},
		{
			name:     "a write retried after a lost answer is not found",
			sabotage: sqlstore.Sabotage{Rewrite: replace("event.find", "AND id = :id", "AND id = :id AND 1 = 0")},
			check:    "L23",
		},
		{
			name: "intake does not compare the digest",
			sabotage: sqlstore.Sabotage{Rewrite: replace("contract.latest",
				"plan_id, title, digest,", "plan_id, title, 'stale',")},
			check: "I4",
		},
		{
			name: "a new version leaves ready work where it was",
			sabotage: sqlstore.Sabotage{Rewrite: replace("work_order.by_contract",
				"AND contract_id = :contract_id", "AND contract_id = :contract_id AND 1 = 0")},
			check: "I16",
		},
		{
			name: "a review carries over to a new version",
			sabotage: sqlstore.Sabotage{Rewrite: replace("work_order.pin",
				", reviewed_by = NULL", "")},
			check: "I17",
		},
		{
			name: "a new version moves work that started",
			ops: func(s ports.Store) contract.Ops {
				return pinsStarted{Ops: contract.RealOps(s), store: s}
			},
			check: "I18",
		},
		{
			name: "a new version and its pin in two transactions",
			ops: func(s ports.Store) contract.Ops {
				return splitRepin{Ops: contract.RealOps(s), store: s}
			},
			check: "crash",
			crash: func() (crashCase, string) { return repinCase("split"), "split" },
		},
		{
			name: "a requirement read at its first version",
			sabotage: sqlstore.Sabotage{Rewrite: replace("requirement.latest",
				"ORDER BY version DESC", "ORDER BY version ASC")},
			check: "I21",
		},
		{
			name: "an unchanged requirement versions again",
			sabotage: sqlstore.Sabotage{Rewrite: replace("requirement.latest",
				"SELECT id, version, requirement_key, text, source, digest", "SELECT id, version, requirement_key, text, source, 'stale'")},
			check: "I20",
		},
		{
			name: "the definition of ready sees no dependency",
			sabotage: sqlstore.Sabotage{Rewrite: replace("dependency.of",
				"work_order_id = :work_order_id", "work_order_id = :work_order_id AND 1 = 0")},
			check: "I8",
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
	case "plane":
		sqlstore.Sabotaged(v.sabotage)
		defer sqlstore.Sabotaged(sqlstore.Sabotage{})
		c, step := v.crash()
		return runCrashCase(newWorld(t, ""), c, step)
	case "worktree":
		gitcli.Sabotaged(v.gitSabotage)
		defer gitcli.Sabotaged(gitcli.Sabotage{})
		return checkDeadRegistration(newWorld(t, ""))
	case "force":
		gitcli.Sabotaged(v.gitSabotage)
		defer gitcli.Sabotaged(gitcli.Sabotage{})
		return checkNoForce(newWorld(t, ""))
	case "removal":
		return checkRemovalReads(newWorld(t, sabotageBlindRemoval))
	case "sleep":
		sqlstore.Sabotaged(v.sabotage)
		defer sqlstore.Sabotaged(sqlstore.Sabotage{})
		return checkSleep(newWorld(t, ""), false)
	case "solo-sleep":
		return checkSoloSleep(newWorld(t, ""), sqlstore.EnvCut+"="+sqlstore.OpRenew+"|"+deadlineGuard)
	case "random":
		return runRandomVariant(t, v)
	case "shim":
		return checkShimGroundTruth(t)
	case "pane":
		return checkPaneVariant(t, v.pane, v.paneStep)
	case "restart":
		return runRestartCrash(t, "charged", worker.EnvSabotage+"="+worker.SabotageRecharge)
	case "holder":
		run := newRandomRun(newWorld(t, v.name), holderVariantKills)
		run.holders = 1
		if err := run.seedWork(3); err != nil {
			return err
		}
		return run.execute()
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

// holderVariantKills is small, because the stopped holder decides this variant.
const holderVariantKills = 4

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

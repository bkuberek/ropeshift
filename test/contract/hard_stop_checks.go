package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// HardStopChecks are what a member does at a work order's hard spend limit (ADR-0037): raise it, or resume the lane
// it parked. Each records the limit before and after, only a member may, a lane parked on `limit_raised` wakes once
// in the transaction that lifts the limit, and nothing lowers a limit a member raised.
var HardStopChecks = []Check{
	{"HS1", "a raise sets the hard limit above what the band derives, records the limit before and after, and only a member may raise", checkRaiseASpendLimit},
	{"HS2", "a lane parked on limit_raised wakes once when the limit rises above what was spent, at the commit when the raise came first", checkLimitRaisedWakesOnce},
	{"HS3", "a new version of the spend policy that lifts a limit wakes the lane it parked, and a revert wakes nothing twice", checkPolicyChangeWakesTheLane},
	{"HS4", "a resume grants one more generation's allowance, wakes the lane, and needs work a limit parked", checkResumeGrantsAGeneration},
}

// limitWake is what a lane parked at a hard limit waits on.
func limitWake(workOrder string) string {
	return fmt.Sprintf(`{"any_of":[{%q:%q},{%q:true}]}`, parking.LimitRaised, workOrder, parking.HumanResume)
}

// spendLimitChanges reads the raises and resumes recorded about a work order's spend, in order.
func (r *Rig) spendLimitChanges(store ports.Store, id string) ([]ledger.SpendLimitChange, error) {
	var out []ledger.SpendLimitChange
	err := store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectSpend, ID: id})
		for _, e := range events {
			if e.Type != ledger.EventSpendLimitRaised && e.Type != ledger.EventSpendResumed {
				continue
			}
			var change ledger.SpendLimitChange
			if err := json.Unmarshal(e.Data, &change); err != nil {
				return err
			}
			out = append(out, change)
		}
		return err
	})
	return out, err
}

func checkRaiseASpendLimit(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	if err := r.lanesWithUsage(s, id, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}}); err != nil {
		return err
	}
	before, err := r.spendOf(s, id)
	if err != nil || !near(before.HardLimit, 25) || !slices.Contains(before.Conditions, ledger.ConditionSpendLimit) {
		return Violatedf("a work order charged 30 under a size limit of 25 reads %+v: %v", before, err)
	}
	change, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ByPercent: 100}, theMember)
	if err != nil {
		return err
	}
	if change.Act != ledger.SpendActRaise || !near(change.BeforeUSD, 25) || !near(change.AfterUSD, 50) || !near(change.ChargedUSD, 30) || change.ByPercent != 100 {
		return Violatedf("raising a limit of 25 by 100 percent recorded %+v", change)
	}
	after, err := r.spendOf(s, id)
	if err != nil || !near(after.HardLimit, 50) || !near(after.RaisedTo, 50) || slices.Contains(after.Conditions, ledger.ConditionSpendLimit) {
		return Violatedf("after the raise the work order reads %+v: %v", after, err)
	}
	if changes, err := r.spendLimitChanges(s, id); err != nil || len(changes) != 1 || changes[0] != change {
		return Violatedf("the ledger holds %+v of the raise, and the call said %+v: %v", changes, change, err)
	}
	// A limit is raised to an amount that is higher than the one in force, and by a share, and never to or below it.
	for _, raise := range []control.SpendRaise{{WorkOrder: id, ToUSD: 50}, {WorkOrder: id, ToUSD: 10}, {WorkOrder: id}, {WorkOrder: id, ByPercent: 10, ToUSD: 90}, {WorkOrder: id, ByPercent: -5}} {
		if _, err := spend.Raise(r.Ctx, r.Scope, raise, theMember); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("the raise %+v was answered %v, want a refusal as invalid", raise, err)
		}
	}
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: "no-such-work-order", ToUSD: 90}, theMember); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a raise of a work order nobody made was answered %v", err)
	}
	// A second raise starts from the limit in force, and a change of policy never takes a raise back.
	policy, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", "10")
	if err != nil {
		return err
	}
	policy.Version = 2
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember); err != nil {
		return err
	}
	if lowered, err := r.spendOf(s, id); err != nil || !near(lowered.HardLimit, 50) {
		return Violatedf("a size limit lowered to 10 took back the raise: %+v: %v", lowered, err)
	}
	second, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ByPercent: 50}, theMember)
	if err != nil || !near(second.BeforeUSD, 50) || !near(second.AfterUSD, 75) {
		return Violatedf("raising a limit of 50 by half recorded %+v: %v", second, err)
	}
	// Only a member raises a limit: no orchestrator, lane, worker, or system actor, and no delegate with no card.
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}, {Kind: ledger.ActorLane, ID: "lane-1"}, {Kind: ledger.ActorWorker, ID: "worker-1"},
		{Kind: ledger.ActorSystem, ID: "tick"}, authority.DelegateActor("member-1", "dlg_1"),
	} {
		if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 200}, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s's raise was answered %v, want a refusal as forbidden", actor.Kind, err)
		}
	}
	if changes, err := r.spendLimitChanges(s, id); err != nil || len(changes) != 2 {
		return Violatedf("the ledger holds %d acts after two raises and refusals: %v", len(changes), err)
	}
	// A work order with no hard limit has none to raise.
	free, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", "0")
	if err != nil {
		return err
	}
	free.Version = 3
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: free.Body(), Policy: free}, theMember); err != nil {
		return err
	}
	unlimited, err := r.Ready(s)
	if err != nil {
		return err
	}
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: unlimited, ToUSD: 90}, theMember); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a work order with no hard limit was raised: %v", err)
	}
	return nil
}

// stopped parks a lane on a work order whose charged spend is past its limit, the way a worker does once a lane
// hands off at the hard limit, and returns the request before the parked commit.
func (r *Rig) stopped(store ports.Store, charged []ledger.GenerationUsage) (id string, request ParkRequest, err error) {
	if id, err = r.Ready(store); err != nil {
		return "", ParkRequest{}, err
	}
	if err = r.lanesWithUsage(store, id, charged); err != nil {
		return "", ParkRequest{}, err
	}
	request, err = r.parkingOn(store, id, limitWake(id))
	return id, request, err
}

func checkLimitRaisedWakesOnce(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	id, request, err := r.stopped(s, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}})
	if err != nil {
		return err
	}
	fired, err := r.NewOps(s).CommitParked(r.Ctx, r.Scope, request.Holder, PushedCommit(request.Wake), r.worker("a"))
	if err != nil || fired != "" {
		return Violatedf("a lane parked at its hard limit woke at its parked commit: %q, %v", fired, err)
	}
	if err := r.state(s, id, workorders.Parked); err != nil {
		return err
	}
	// A raise that leaves the limit at or below what was spent is a raise, and wakes nothing.
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 29}, theMember); err != nil {
		return err
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 0 {
		return Violatedf("a limit of 29 over spend of 30 woke the lane %d times: %v", n, err)
	}
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 40}, theMember); err != nil {
		return err
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 1 {
		return Violatedf("a limit of 40 over spend of 30 woke the lane %d times, want once: %v", n, err)
	}
	if err := r.state(s, id, workorders.Ready); err != nil {
		return err
	}
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 80}, theMember); err != nil {
		return err
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 1 {
		return Violatedf("a second raise woke the lane %d times in all, want once: %v", n, err)
	}
	// A raise that comes while the lane is still parking is evaluated at the parked commit, and not lost.
	earlyID, early, err := r.stopped(s, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}})
	if err != nil {
		return err
	}
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: earlyID, ToUSD: 40}, theMember); err != nil {
		return err
	}
	if n, err := r.firings(s, early.Wake); err != nil || n != 0 {
		return Violatedf("a raise woke a lane that had not parked yet %d times: %v", n, err)
	}
	fired, err = r.NewOps(s).CommitParked(r.Ctx, r.Scope, early.Holder, PushedCommit(early.Wake), r.worker("a"))
	if err != nil || fired != early.Wake {
		return Violatedf("a raise that came during parking was not read at the parked commit: fired %q, %v", fired, err)
	}
	if n, err := r.firings(s, early.Wake); err != nil || n != 1 {
		return Violatedf("the lane woke %d times, want once: %v", n, err)
	}
	return nil
}

func checkPolicyChangeWakesTheLane(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	id, request, err := r.stopped(s, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}})
	if err != nil {
		return err
	}
	if fired, err := r.NewOps(s).CommitParked(r.Ctx, r.Scope, request.Holder, PushedCommit(request.Wake), r.worker("a")); err != nil || fired != "" {
		return Violatedf("a lane parked at its hard limit woke at its parked commit: %q, %v", fired, err)
	}
	publish := func(version int64, size string) error {
		policy, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", size)
		if err != nil {
			return err
		}
		policy.Version = version
		_, err = spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember)
		return err
	}
	if err := publish(2, "28"); err != nil {
		return err
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 0 {
		return Violatedf("a size limit of 28 over spend of 30 woke the lane %d times: %v", n, err)
	}
	if err := publish(3, "40"); err != nil {
		return err
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 1 {
		return Violatedf("a size limit of 40 over spend of 30 woke the lane %d times, want once: %v", n, err)
	}
	if err := r.state(s, id, workorders.Ready); err != nil {
		return err
	}
	// The revert is the earlier values as a newer version, and the lane that woke is not woken again.
	if err := publish(4, "25"); err != nil {
		return err
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 1 {
		return Violatedf("a revert left the lane woken %d times, want once: %v", n, err)
	}
	return nil
}

func checkResumeGrantsAGeneration(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	running, err := r.Ready(s)
	if err != nil {
		return err
	}
	if _, err := spend.Resume(r.Ctx, r.Scope, running, theMember); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a work order no limit parked was resumed: %v", err)
	}
	// Two generations that spent 20 and 12 are charged 32 against a limit of 25, so one more of the mean, 16, is the
	// allowance, and the limit rises to 48.
	id, request, err := r.stopped(s, []ledger.GenerationUsage{{CostUSD: 20, KnownTurns: 2}, {CostUSD: 12, KnownTurns: 3}})
	if err != nil {
		return err
	}
	if _, err := spend.Resume(r.Ctx, r.Scope, id, theMember); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a work order whose lane had not parked was resumed: %v", err)
	}
	if fired, err := r.NewOps(s).CommitParked(r.Ctx, r.Scope, request.Holder, PushedCommit(request.Wake), r.worker("a")); err != nil || fired != "" {
		return Violatedf("a lane parked at its hard limit woke at its parked commit: %q, %v", fired, err)
	}
	if _, err := spend.Resume(r.Ctx, r.Scope, id, authority.DelegateActor("member-1", "dlg_1")); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate's resume was answered %v", err)
	}
	change, err := spend.Resume(r.Ctx, r.Scope, id, theMember)
	if err != nil {
		return err
	}
	if change.Act != ledger.SpendActResume || !near(change.BeforeUSD, 25) || !near(change.AfterUSD, 48) || !near(change.ChargedUSD, 32) {
		return Violatedf("a resume of work charged 32 over two generations recorded %+v", change)
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 1 {
		return Violatedf("a resume woke the lane %d times, want once: %v", n, err)
	}
	if err := r.state(s, id, workorders.Ready); err != nil {
		return err
	}
	if changes, err := r.spendLimitChanges(s, id); err != nil || len(changes) != 1 || changes[0] != change {
		return Violatedf("the ledger holds %+v of the resume: %v", changes, err)
	}
	return nil
}

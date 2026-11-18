package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	{"HS5", "a usage report that reaches the hard limit records one stop per limit, which marks the delivery record spend_limited", checkHardLimitIsRecorded},
	{"HS6", "a worker reads the allowance its lane's work order has, without the lane's own spend, and it follows a raise and a new policy at once", checkTheWorkersAllowance},
	{"HS7", "a generation's usage report never lowers what it spent, so a smaller figure cannot read as a raised limit", checkUsageNeverLowers},
	{"HS8", "the allowance holds what the lane's earlier generations were charged, apart from the one running, so a worker that never ran the lane still holds it to its limit", checkTheAllowanceHoldsTheLanesEarlierGenerations},
	{"HS9", "a lane parked at its hard limit is woken past it only by a member: a delegate's resume is refused, and a lane parked for anything else is still the delegate's to resume", checkOnlyAMemberResumesALaneAtItsLimit},
}

// spendEvents counts the events of a type about a work order's spend.
func (r *Rig) spendEvents(store ports.Store, id, eventType string) (int, error) {
	count := 0
	err := store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectSpend, ID: id})
		for _, e := range events {
			if e.Type == eventType {
				count++
			}
		}
		return err
	})
	return count, err
}

func checkHardLimitIsRecorded(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	report := func(cost float64) error {
		reply, err := s.usage(ledger.GenerationUsage{CostUSD: cost, KnownTurns: 4})
		if err == nil && len(reply.Rejected) != 0 {
			return Violatedf("a usage report was refused: %+v", reply.Rejected)
		}
		return err
	}
	if err := report(24); err != nil {
		return err
	}
	if n, err := r.spendEvents(s.store, id, ledger.EventSpendLimited); err != nil || n != 0 {
		return Violatedf("%d stops under the hard limit of 25: %v", n, err)
	}
	for _, cost := range []float64{25, 26, 27} {
		if err := report(cost); err != nil {
			return err
		}
	}
	if n, err := r.spendEvents(s.store, id, ledger.EventSpendLimited); err != nil || n != 1 {
		return Violatedf("%d stops at the hard limit across three reports, want one: %v", n, err)
	}
	// A raise moves the limit, and passing the new one is a new stop.
	if _, err := control.NewSpend(s.store).Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 40}, theMember); err != nil {
		return err
	}
	if err := report(30); err != nil {
		return err
	}
	if n, err := r.spendEvents(s.store, id, ledger.EventSpendLimited); err != nil || n != 1 {
		return Violatedf("%d stops after a raise to 40 and spend of 30, want the one before: %v", n, err)
	}
	if err := report(41); err != nil {
		return err
	}
	if n, err := r.spendEvents(s.store, id, ledger.EventSpendLimited); err != nil || n != 2 {
		return Violatedf("%d stops after spend passed the raised limit, want two: %v", n, err)
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	var stop ledger.Event
	for _, e := range events {
		if e.Type == ledger.EventSpendLimited {
			stop = e
		}
	}
	var notice struct {
		WorkOrder  string  `json:"work_order"`
		Limit      float64 `json:"limit"`
		ChargedUSD float64 `json:"charged_usd"`
	}
	if err := json.Unmarshal(stop.Data, &notice); err != nil || notice.WorkOrder != id || !near(notice.Limit, 40) || !near(notice.ChargedUSD, 41) {
		return Violatedf("the stop reads %s: %v", stop.Data, err)
	}
	return nil
}

func checkTheWorkersAllowance(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	id := s.grant.WorkOrder
	if err := r.lanesWithUsage(s.store, id, []ledger.GenerationUsage{{CostUSD: 7, KnownTurns: 2}}); err != nil {
		return err
	}
	if reply, err := s.usage(ledger.GenerationUsage{CostUSD: 5, KnownTurns: 1}); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
	}
	read := func() (*ledger.SpendAllowance, error) {
		facts, err := s.plane.LaneFacts(r.Ctx, ledger.LaneFactsRequest{
			Auth: s.auth, Epoch: s.epoch, WorkOrder: id, Fence: s.grant.Fence, Lane: s.lane,
		})
		return facts.Spend, err
	}
	allowance, err := read()
	if err != nil {
		return err
	}
	if allowance == nil || !near(allowance.HardUSD, 25) || !near(allowance.OthersUSD, 7) || !near(allowance.HeadroomShare, 0.1) {
		return Violatedf("the worker reads an allowance of %+v, want a limit of 25 with 7 spent by the other lanes and a headroom of 0.1", allowance)
	}
	spend := control.NewSpend(s.store)
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 60}, theMember); err != nil {
		return err
	}
	policy, err := authority.DefaultSpendPolicy().Set("budget_flag_headroom_share", "0.3")
	if err != nil {
		return err
	}
	policy.Version = 2
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember); err != nil {
		return err
	}
	if allowance, err = read(); err != nil || allowance == nil || !near(allowance.HardUSD, 60) || !near(allowance.HeadroomShare, 0.3) {
		return Violatedf("after a raise to 60 and a headroom of 0.3 the worker reads %+v: %v", allowance, err)
	}
	// Work the policy leaves with no hard limit has no allowance to hold a lane to.
	free, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", "0")
	if err != nil {
		return err
	}
	free.Version = 3
	if _, err := spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: free.Body(), Policy: free}, theMember); err != nil {
		return err
	}
	if allowance, err = read(); err != nil || allowance != nil {
		return Violatedf("work with no hard limit reads an allowance of %+v: %v", allowance, err)
	}
	return nil
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
	// A raise that would leave the limit at or below what was spent changes nothing a lane could use, so it is refused
	// with the figure it fell short of, and records nothing and wakes nothing.
	for _, to := range []float64{29, 30} {
		_, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: to}, theMember)
		if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "below charged spend") || !strings.Contains(err.Error(), "$30.00") {
			return Violatedf("a limit of %v over spend of 30 was answered %v, want a refusal that says it stays below charged spend", to, err)
		}
	}
	if changes, err := r.spendLimitChanges(s, id); err != nil || len(changes) != 0 {
		return Violatedf("a refused raise left %+v on record: %v", changes, err)
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 0 {
		return Violatedf("a refused raise woke the lane %d times: %v", n, err)
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
	// The member sees what "once" bought: the limit before and after, and the one generation's allowance between them and
	// where it came from, in the answer and in the event the ledger keeps.
	if !near(change.AllowanceUSD, 16) || change.AllowanceBasis != ledger.AllowanceMeanGeneration || !near(change.AfterUSD-change.ChargedUSD, change.AllowanceUSD) {
		return Violatedf("a resume of work charged 32 over two generations granted %+v, want an allowance of 16 by the mean of its generations", change)
	}
	if n, err := r.firings(s, request.Wake); err != nil || n != 1 {
		return Violatedf("a resume woke the lane %d times, want once: %v", n, err)
	}
	if err := r.state(s, id, workorders.Ready); err != nil {
		return err
	}
	changes, err := r.spendLimitChanges(s, id)
	if err != nil || len(changes) != 1 || changes[0] != change {
		return Violatedf("the ledger holds %+v of the resume: %v", changes, err)
	}
	if recorded := changes[0]; !near(recorded.BeforeUSD, 25) || !near(recorded.AfterUSD, 48) || !near(recorded.AllowanceUSD, 16) || recorded.AllowanceBasis != ledger.AllowanceMeanGeneration {
		return Violatedf("the spend.resumed event reads %+v, and a member reading it cannot see what the resume bought", recorded)
	}
	return nil
}

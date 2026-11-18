package contract

import (
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// checkMemberActsAtTheHardLimit holds a member's own retry, reset, and respawn to the same limit, and a member's grant of
// a request raised before the limit was reached (FR-SPEND-8): each is refused with the limit named and the way out, and
// changes nothing, and each runs once the limit is raised. A member's word is what raises a limit, so none of their other
// acts is a way round it.
func checkMemberActsAtTheHardLimit(r *Rig) error {
	spend := control.NewSpend(r.Store())
	pastTheLimit := ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3}
	refusal := func(act string, o *recoveryRig, err error) error {
		if err == nil || !strings.Contains(err.Error(), o.grant.WorkOrder) || !strings.Contains(err.Error(), "hard spend limit of $25.00") ||
			!strings.Contains(err.Error(), "$30.00 charged") || !strings.Contains(err.Error(), "raise it with ropeshift spend raise") {
			return Violatedf("a member's %s at the hard limit was answered %v, want a refusal that names the limit and says to raise it", act, err)
		}
		var limited *ledger.SpendLimitError
		if !errors.As(err, &limited) || limited.WorkOrder != o.grant.WorkOrder || limited.HardUSD != 25 {
			return Violatedf("a member's %s at the hard limit was refused with %T, which a caller cannot tell from any other refusal", act, err)
		}
		return nil
	}
	raise := func(o *recoveryRig) error {
		_, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: o.grant.WorkOrder, ToUSD: 100}, theMember)
		return err
	}

	retry, err := failedRecoveryRig(r, 3)
	if err != nil {
		return err
	}
	if err := r.lanesWithUsage(retry.store, retry.grant.WorkOrder, []ledger.GenerationUsage{pastTheLimit}); err != nil {
		return err
	}
	recoveries := control.NewRecoveries(retry.store)
	_, err = recoveries.Retry(r.Ctx, r.Scope, retry.grant.WorkOrder, "try once more", theMember)
	if err := refusal("work retry", retry, err); err != nil {
		return err
	}
	if order, err := retry.order(); err != nil || order.State != workorders.Failed {
		return Violatedf("a refused retry left the work %s: %v", order.State, err)
	}
	if err := raise(retry); err != nil {
		return err
	}
	if state, err := recoveries.Retry(r.Ctx, r.Scope, retry.grant.WorkOrder, "try once more", theMember); err != nil || state != workorders.Ready {
		return Violatedf("a retry once the limit was raised came to %s: %v", state, err)
	}

	reset, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := reset.laneStatus("tripped"); err != nil {
		return err
	}
	if reply, err := reset.usage(pastTheLimit); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
	}
	leases := control.NewLeases(reset.store)
	err = leases.ResetLane(r.Ctx, r.Scope, reset.grant.WorkOrder, theMember)
	if err := refusal("lane reset", reset, err); err != nil {
		return err
	}
	if n, err := reset.laneCount(resetsOf); err != nil || n != 0 {
		return Violatedf("a refused reset reset the breaker %d times: %v", n, err)
	}
	if err := raise(reset); err != nil {
		return err
	}
	if err := leases.ResetLane(r.Ctx, r.Scope, reset.grant.WorkOrder, theMember); err != nil {
		return Violatedf("a reset once the limit was raised was answered %v", err)
	}
	if n, err := reset.laneCount(resetsOf); err != nil || n != 1 {
		return Violatedf("a reset once the limit was raised reset the breaker %d times: %v", n, err)
	}

	respawn, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if reply, err := respawn.usage(pastTheLimit); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
	}
	leases = control.NewLeases(respawn.store)
	err = leases.RespawnLane(r.Ctx, r.Scope, respawn.grant.WorkOrder, theMember)
	if err := refusal("lane respawn", respawn, err); err != nil {
		return err
	}
	if n, err := respawn.laneCount(respawnsOf); err != nil || n != 0 {
		return Violatedf("a refused respawn asked the lane for %d generations: %v", n, err)
	}
	if err := raise(respawn); err != nil {
		return err
	}
	if err := leases.RespawnLane(r.Ctx, r.Scope, respawn.grant.WorkOrder, theMember); err != nil {
		return Violatedf("a respawn once the limit was raised was answered %v", err)
	}
	if n, err := respawn.laneCount(respawnsOf); err != nil || n != 1 {
		return Violatedf("a respawn once the limit was raised asked the lane for %d generations: %v", n, err)
	}

	// A request raised while the work was under its limit is the same act, and a member's grant of it is no way round once
	// a new version of the policy puts the work at its limit: a request is void when its work order moves, and a policy
	// moves no work order, so this is the road a grant can still take.
	sized := func(version int64, size string) error {
		policy, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", size)
		if err != nil {
			return err
		}
		policy.Version = version
		_, err = spend.PublishSpendPolicy(r.Ctx, r.Scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember)
		return err
	}
	if err := sized(2, "40"); err != nil {
		return err
	}
	granted, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := granted.laneStatus("working", "stalled"); err != nil {
		return err
	}
	other, err := newStatusRigOn(r, granted.store, r.Ready)
	if err != nil {
		return err
	}
	elsewhere, err := orchestrating(other)
	if err != nil {
		return err
	}
	elsewhere.call.WorkOrder = granted.grant.WorkOrder
	if reply, err := granted.usage(pastTheLimit); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
	}
	asked, err := elsewhere.act(ledger.VerbRestart, "it stalled")
	if err != nil || asked.Result != authority.RecoveryRefused || asked.Check != authority.CheckSameMachine || asked.Approval == "" {
		return Violatedf("a restart of another machine's lane under its limit of 40 came to %+v: %v", asked, err)
	}
	if err := sized(3, "25"); err != nil {
		return err
	}
	requests, err := recoveryRequests(granted.statusRig, granted.grant.WorkOrder)
	if err != nil || len(requests) != 1 {
		return Violatedf("requests %+v: %v", requests, err)
	}
	_, err = grant(granted.statusRig, requests[0])
	if err := refusal("grant of a restart", granted, err); err != nil {
		return err
	}
	if requests, err = recoveryRequests(granted.statusRig, granted.grant.WorkOrder); err != nil || len(requests) != 1 || !requests[0].Open() {
		return Violatedf("a refused grant left the request %+v: %v", requests, err)
	}
	if n, err := granted.laneCount(respawnsOf); err != nil || n != 0 {
		return Violatedf("a refused grant asked the lane for %d generations: %v", n, err)
	}
	if err := raise(granted); err != nil {
		return err
	}
	if decided, err := grant(granted.statusRig, requests[0]); err != nil || decided.State != authority.ApprovalApproved {
		return Violatedf("a grant once the limit was raised came to %+v: %v", decided, err)
	}
	if n, err := granted.laneCount(respawnsOf); err != nil || n != 1 {
		return Violatedf("a grant once the limit was raised asked the lane for %d generations: %v", n, err)
	}
	return nil
}

// checkOrchestratorStopAtTheHardLimit holds an orchestrator's stop of a lane, which respawns it, to the limit a member's
// respawn is held to (FR-SPEND-8): the stop of work at its hard limit is refused with the limit named, whoever asks, and the
// lane is asked for no generation. It runs once a member raises the limit.
func checkOrchestratorStopAtTheHardLimit(r *Rig) error {
	o, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	a := &autonomyRig{r: r, store: o.store, plane: o.plane, ops: control.NewAutonomy(o.store), auth: o.auth, epoch: o.epoch}
	if err := a.set(authority.LevelAttended); err != nil {
		return err
	}
	if reply, err := o.usage(ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3}); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
	}
	o.call.WorkOrder = o.grant.WorkOrder
	_, err = o.plane.Orchestrate(r.Ctx, ledger.VerbStop, o.call)
	var limited *ledger.SpendLimitError
	if !errors.As(err, &limited) || limited.WorkOrder != o.grant.WorkOrder || limited.HardUSD != 25 ||
		!strings.Contains(err.Error(), "raise it with ropeshift spend raise") {
		return Violatedf("an orchestrator's stop of work at its hard limit was answered %v, want a refusal that names the limit and says to raise it", err)
	}
	if n, err := o.laneCount(respawnsOf); err != nil || n != 0 {
		return Violatedf("a refused stop asked the lane for %d generations: %v", n, err)
	}
	if _, err := control.NewSpend(r.Store()).Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: o.grant.WorkOrder, ToUSD: 100}, theMember); err != nil {
		return err
	}
	if _, err := o.plane.Orchestrate(r.Ctx, ledger.VerbStop, o.call); err != nil {
		return Violatedf("an orchestrator's stop once the limit was raised was answered %v", err)
	}
	if n, err := o.laneCount(respawnsOf); err != nil || n != 1 {
		return Violatedf("a stop once the limit was raised asked the lane for %d generations: %v", n, err)
	}
	return nil
}

// checkRecoveryAtTheHardLimit holds the three recovery acts that start more spending to the limit that stopped it
// (FR-SPEND-8): work a hard limit stopped is refused its restart, its reset, and its retry, the refusal names the
// limit, no request is raised for a member to grant past it, and nothing is asked of the lane. A member raises the
// limit, and the act then runs.
func checkRecoveryAtTheHardLimit(r *Rig) error {
	spend := control.NewSpend(r.Store())
	pastTheLimit := ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3}
	check := func(o *recoveryRig, verb string, act authority.RecoveryAct) error {
		refused, err := o.act(verb, "it is stuck")
		if err != nil {
			return err
		}
		if refused.Result != authority.RecoveryRefused || refused.Check != authority.CheckSpendLimit || refused.Approval != "" ||
			!strings.Contains(refused.Limit, o.grant.WorkOrder) || !strings.Contains(refused.Limit, "hard spend limit of $25.00") {
			return Violatedf("%s at the hard limit came to %+v", act, refused)
		}
		requests, err := recoveryRequests(o.statusRig, o.grant.WorkOrder)
		if err != nil || len(requests) != 0 {
			return Violatedf("%s at the hard limit raised %d requests for a member: %v", act, len(requests), err)
		}
		records, err := recoveryRecords(o.statusRig)
		if err != nil {
			return err
		}
		records = slices.DeleteFunc(records, func(record authority.RecoveryRecord) bool { return record.WorkOrder != o.grant.WorkOrder })
		if len(records) != 1 || records[0].Result != authority.RecoveryRefused || records[0].Check != authority.CheckSpendLimit || records[0].Act != act {
			return Violatedf("%s at the hard limit left %+v on record", act, records)
		}
		if n, err := o.laneCount(respawnsOf); err != nil || n != 0 {
			return Violatedf("%s at the hard limit asked the lane for %d generations: %v", act, n, err)
		}
		if n, err := o.laneCount(windowResetsOf); err != nil || n != 0 {
			return Violatedf("%s at the hard limit reset the breaker %d times: %v", act, n, err)
		}
		if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: o.grant.WorkOrder, ToUSD: 100}, theMember); err != nil {
			return err
		}
		done, err := o.act(verb, "it is stuck")
		if err != nil {
			return err
		}
		if done.Result != authority.RecoveryDone {
			return Violatedf("%s after the limit was raised came to %+v", act, done)
		}
		return nil
	}

	restart, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := restart.laneStatus("working", "stalled"); err != nil {
		return err
	}
	if reply, err := restart.usage(pastTheLimit); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
	}
	if err := check(restart, ledger.VerbRestart, authority.RecoverRestart); err != nil {
		return err
	}

	reset, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := reset.laneStatus("tripped"); err != nil {
		return err
	}
	if reply, err := reset.usage(pastTheLimit); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v, %v", reply.Rejected, err)
	}
	if err := check(reset, ledger.VerbReset, authority.RecoverReset); err != nil {
		return err
	}

	retry, err := failedRecoveryRig(r, 3)
	if err != nil {
		return err
	}
	if err := r.lanesWithUsage(retry.store, retry.grant.WorkOrder, []ledger.GenerationUsage{pastTheLimit}); err != nil {
		return err
	}
	if err := check(retry, ledger.VerbRetry, authority.RecoverRetry); err != nil {
		return err
	}
	if order, err := retry.order(); err != nil || order.State != workorders.Ready {
		return Violatedf("a retry once the limit was raised left the work %s: %v", order.State, err)
	}

	// Work under its limit is untouched, and a replan is no spending, so it is never held to the limit.
	free, err := newRecoveryRig(r)
	if err != nil {
		return err
	}
	if err := free.laneStatus("working", "stalled"); err != nil {
		return err
	}
	if done, err := free.act(ledger.VerbRestart, "it stalled"); err != nil || done.Result != authority.RecoveryDone {
		return Violatedf("a restart under the limit came to %+v: %v", done, err)
	}
	return nil
}

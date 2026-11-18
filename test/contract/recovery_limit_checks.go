package contract

import (
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

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

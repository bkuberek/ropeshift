package control

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The operations of raising a limit.
const (
	opSpendRaise  = "spend.raise"
	opSpendResume = "spend.resume"
)

// maxSpendAmount is the largest limit a raise sets, which is the largest amount a spend policy key holds.
const maxSpendAmount = 1_000_000.0

// SpendRaise asks to raise a work order's hard limit, by a percentage of the limit in force or to an amount. It names
// one of the two (FR-SPEND-7).
type SpendRaise struct {
	WorkOrder string
	ByPercent float64
	ToUSD     float64
}

// Raise lifts a work order's hard limit, records the limit before and after, and wakes the lane the limit parked, in
// one transaction (FR-SPEND-8, FR-PARK-2). Only a member raises a limit: it is a person's decision to spend more than
// twice the expected cost, so no orchestrator, lane, worker, or tick holds the action, and a delegate's call reaches
// here only once a member confirms its card. A raise sets a limit higher than the one in force, and a limit the policy
// leaves unset has nothing to raise.
func (s *Spend) Raise(ctx context.Context, scope ledger.WorkspaceID, in SpendRaise, actor ledger.Actor) (ledger.SpendLimitChange, error) {
	if !authority.Allowed(actor, authority.ActionRaiseSpendLimit) {
		return ledger.SpendLimitChange{}, workorders.ErrForbidden
	}
	if in.WorkOrder == "" || in.ByPercent < 0 || in.ToUSD < 0 || (in.ByPercent > 0) == (in.ToUSD > 0) {
		return ledger.SpendLimitChange{}, fmt.Errorf("%w: a raise names a work order and either a percentage or an amount", workorders.ErrInvalid)
	}
	var change ledger.SpendLimitChange
	err := s.store.Write(ctx, scope, opSpendRaise, func(tx ports.WriteTx) error {
		spend, err := spendWritesOf(tx).spendOf(in.WorkOrder)
		if err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(in.WorkOrder)
		if err != nil {
			return err
		}
		if workorders.Terminal(order.State) {
			return fmt.Errorf("%w: work order %s is %s, so no limit stops it", workorders.ErrInvalid, in.WorkOrder, order.State)
		}
		if spend.HardLimit <= 0 {
			return fmt.Errorf("%w: work order %s has no hard limit to raise", workorders.ErrInvalid, in.WorkOrder)
		}
		after := in.ToUSD
		if in.ByPercent > 0 {
			after = spend.HardLimit * (1 + in.ByPercent/100)
		}
		if after <= spend.HardLimit || after > maxSpendAmount {
			return fmt.Errorf("%w: a hard limit of %s cannot be raised to %s, which must be higher and at most %s",
				workorders.ErrInvalid, usd(spend.HardLimit), usd(after), usd(maxSpendAmount))
		}
		change = ledger.SpendLimitChange{
			WorkOrder: in.WorkOrder, Act: ledger.SpendActRaise, BeforeUSD: spend.HardLimit, AfterUSD: after,
			ChargedUSD: spend.Charged.ChargedUSD, ByPercent: in.ByPercent, ToUSD: in.ToUSD, PolicyVersion: spend.PolicyVersion,
		}
		return recordLimitChange(tx, change, actor)
	})
	if err != nil {
		return ledger.SpendLimitChange{}, err
	}
	return change, nil
}

// Resume grants a work order one more generation's allowance, and so lets the lane its limit parked go on: it lifts
// the hard limit to what the work order is charged and one more generation of the mean it has spent a generation
// (FR-SPEND-7). It takes work a hard limit parked, records the limit before and after as a raise does, and is held to
// the same action.
func (s *Spend) Resume(ctx context.Context, scope ledger.WorkspaceID, workOrder string, actor ledger.Actor) (ledger.SpendLimitChange, error) {
	if !authority.Allowed(actor, authority.ActionRaiseSpendLimit) {
		return ledger.SpendLimitChange{}, workorders.ErrForbidden
	}
	var change ledger.SpendLimitChange
	err := s.store.Write(ctx, scope, opSpendResume, func(tx ports.WriteTx) error {
		reads := spendWritesOf(tx)
		spend, err := reads.spendOf(workOrder)
		if err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		if order.State != workorders.Parked || !slices.Contains(spend.Conditions, ledger.ConditionSpendLimit) {
			return fmt.Errorf("%w: work order %s is %s, and a resume takes work a hard spend limit parked", workorders.ErrInvalid, workOrder, order.State)
		}
		lanes, err := reads.usageOf(workOrder, "")
		if err != nil {
			return err
		}
		generations := 0
		for _, usage := range lanes {
			for _, g := range usage {
				if g != (ledger.GenerationUsage{}) {
					generations++
				}
			}
		}
		if generations == 0 {
			return fmt.Errorf("%w: work order %s has no generation to measure an allowance by", workorders.ErrInvalid, workOrder)
		}
		after := spend.Charged.ChargedUSD + spend.Charged.ChargedUSD/float64(generations)
		if after > maxSpendAmount {
			return fmt.Errorf("%w: a hard limit cannot be raised past %s", workorders.ErrInvalid, usd(maxSpendAmount))
		}
		change = ledger.SpendLimitChange{
			WorkOrder: workOrder, Act: ledger.SpendActResume, BeforeUSD: spend.HardLimit, AfterUSD: after,
			ChargedUSD: spend.Charged.ChargedUSD, PolicyVersion: spend.PolicyVersion,
		}
		return recordLimitChange(tx, change, actor)
	})
	if err != nil {
		return ledger.SpendLimitChange{}, err
	}
	return change, nil
}

// recordLimitChange keeps what a member did to a limit, and settles every lane parked on `limit_raised` in the same
// transaction, so a raise and the wake it fires are one commit and a lane that parked a moment before reads the raise
// at its parked commit (FR-PARK-2).
func recordLimitChange(tx ports.WriteTx, change ledger.SpendLimitChange, actor ledger.Actor) error {
	data, err := json.Marshal(change)
	if err != nil {
		return err
	}
	eventType := ledger.EventSpendLimitRaised
	if change.Act == ledger.SpendActResume {
		eventType = ledger.EventSpendResumed
	}
	if _, err := tx.Append(ledger.EventDraft{
		Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: ledger.SubjectSpend, ID: change.WorkOrder}, Data: data,
	}); err != nil {
		return err
	}
	_, err = fireWakes(tx, "limit:"+change.WorkOrder, actor, parking.LimitRaised)
	return err
}

// usd words an amount of dollars.
func usd(amount float64) string { return fmt.Sprintf("$%.2f", amount) }

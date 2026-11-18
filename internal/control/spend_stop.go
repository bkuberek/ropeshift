package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The operations that end a spend stop without lifting its limit.
const (
	opSpendBlock  = "spend.block"
	opSpendReplan = "spend.replan"
)

// SpendStop is a work order a hard spend limit parked, as Needs you shows it (ADR-0037, FR-SPEND-7): one for the work
// order however many lanes served it, with what it is charged against its limits, the band its limits came from, what
// Resume once would grant, and what each lane left. A read of it changes nothing.
type SpendStop struct {
	WorkOrder workorders.WorkOrder
	Spend     ledger.WorkOrderSpend
	// Band is the work order's cost band, nil when it has no estimate.
	Band *delivery.SpendBand
	// Resume is what Resume once grants, nil when nothing sizes one more generation and a member raises the limit instead.
	Resume *SpendResume
	// Lanes are the lanes that served the work order, oldest first.
	Lanes []SpendStopLane
}

// SpendResume is what a resume would do now: the one more generation's allowance, where the figure comes from, and the
// hard limit it sets.
type SpendResume struct {
	AllowanceUSD float64
	Basis        string
	LimitUSD     float64
}

// SpendStopLane is what one lane of a stopped work order left: its last digest and its last handoff. Generation is the
// last generation that left a digest.
type SpendStopLane struct {
	Lane       string
	Generation int64
	Digest     *lanes.Digest
	Handoff    *SpendHandoff
}

// SpendHandoff is a lane's last handoff: the narrative the agent wrote, or nothing when the handoff degraded to its
// deterministic part (FR-LANE-9).
type SpendHandoff struct {
	Generation int64
	Reason     string
	Degraded   bool
	Narrative  *lanes.Narrative
}

// stoppedBy says a work order is parked at its hard spend limit, which is what a stop is. A work order under its limit
// again, or parked for any other reason, is no stop.
func stoppedBy(order workorders.WorkOrder, spend ledger.WorkOrderSpend) bool {
	return order.State == workorders.Parked && slices.Contains(spend.Conditions, ledger.ConditionSpendLimit)
}

// SpendStops lists the work orders a hard spend limit parked, for the Needs you view.
func SpendStops(tx ports.ReadTx) ([]SpendStop, error) {
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, err
	}
	reads := spendReadsOf(tx)
	var stops []SpendStop
	for _, order := range orders {
		if order.State != workorders.Parked {
			continue
		}
		spend, err := reads.spendOf(order.ID)
		if err != nil {
			return nil, err
		}
		if !stoppedBy(order, spend) {
			continue
		}
		stop := SpendStop{WorkOrder: order, Spend: spend}
		if stop.Band, err = reads.bandOf(order.ID); err != nil {
			return nil, err
		}
		allowance, basis, err := reads.resumeAllowance(order, spend.Charged.ChargedUSD)
		switch {
		case err == nil:
			stop.Resume = &SpendResume{AllowanceUSD: allowance, Basis: basis, LimitUSD: spend.Charged.ChargedUSD + allowance}
		case !errors.Is(err, workorders.ErrInvalid):
			return nil, err
		}
		for _, row := range rows {
			if row.WorkOrder != order.ID {
				continue
			}
			left, err := laneLeft(tx.Lanes(), row)
			if err != nil {
				return nil, err
			}
			stop.Lanes = append(stop.Lanes, left)
		}
		stops = append(stops, stop)
	}
	return stops, nil
}

// laneLeft reads the last digest and the last handoff a lane's generations left, newest generation first. A generation
// that sent neither is passed, and so is a record this build cannot read, because a card shows what is there.
func laneLeft(reader ports.LaneReader, row ledger.LaneRow) (SpendStopLane, error) {
	out := SpendStopLane{Lane: row.ID}
	for generation := row.Generation; generation >= 1 && (out.Digest == nil || out.Handoff == nil); generation-- {
		if out.Digest == nil {
			digest, err := reader.Digest(ledger.DigestOwnerLane, row.ID, generation)
			switch {
			case err == nil:
				var body lanes.Digest
				if json.Unmarshal(digest.Body, &body) == nil {
					out.Digest, out.Generation = withCollections(&body), generation
				}
			case !errors.Is(err, ports.ErrNotFound):
				return SpendStopLane{}, err
			}
		}
		if out.Handoff == nil {
			record, err := reader.Handoff(row.ID, generation)
			switch {
			case err == nil:
				var body lanes.Handoff
				if json.Unmarshal(record.Body, &body) == nil {
					out.Handoff = &SpendHandoff{Generation: generation, Reason: body.Reason, Degraded: record.Degraded || body.Degraded(), Narrative: narrativeWithLists(body.Narrative)}
				}
			case !errors.Is(err, ports.ErrNotFound):
				return SpendStopLane{}, err
			}
		}
	}
	return out, nil
}

// narrativeWithLists is a narrative whose four lists are lists. An agent may leave a list out and still pass the handoff
// check, and a card reads each as a list, so a missing one is served empty and never null.
func narrativeWithLists(n *lanes.Narrative) *lanes.Narrative {
	if n == nil {
		return nil
	}
	out := *n
	for _, list := range []*[]string{&out.Done, &out.InProgress, &out.NextSteps, &out.Risks} {
		if *list == nil {
			*list = []string{}
		}
	}
	return &out
}

// withCollections is a digest whose tool calls and written files are a map and a list. A stored body may lack either,
// and a card reads them as such, so a missing one is served empty and never null.
func withCollections(d *lanes.Digest) *lanes.Digest {
	if d.ToolCalls == nil {
		d.ToolCalls = map[string]int64{}
	}
	if d.FilesWritten == nil {
		d.FilesWritten = []string{}
	}
	return d
}

// Block ends a spend stop by blocking its work order with the reason `spend_limit`: the parked lane ends and its wake
// is retired, so the work waits in the blocked list for a member to resolve or replan it, and nothing wakes it by
// raising a limit later (FR-SPEND-7). It is a member's act, held like a raise, and takes only work a hard limit parked.
func (s *Spend) Block(ctx context.Context, scope ledger.WorkspaceID, workOrder string, actor ledger.Actor) (workorders.State, error) {
	return s.endStop(ctx, scope, workOrder, workorders.TriggerSpendBlocked, opSpendBlock, actor)
}

// Replan ends a spend stop by sending its work order to planning, as a member's replan does for blocked work: the
// parked lane ends, its wake is retired, and the review of the contract the work was on is void, so the contract
// passes review again (FR-SPEND-7).
func (s *Spend) Replan(ctx context.Context, scope ledger.WorkspaceID, workOrder string, actor ledger.Actor) (workorders.State, error) {
	return s.endStop(ctx, scope, workOrder, workorders.TriggerSpendReplan, opSpendReplan, actor)
}

// endStop takes one of the edges out of a spend stop, in one transaction with the end of its lanes and its wakes, so a
// work order never reads as blocked with a lane that could wake it.
func (s *Spend) endStop(ctx context.Context, scope ledger.WorkspaceID, workOrder string, trigger workorders.Trigger, operation string, actor ledger.Actor) (workorders.State, error) {
	if !authority.Allowed(actor, authority.ActionRaiseSpendLimit) {
		return "", workorders.ErrForbidden
	}
	var state workorders.State
	err := s.store.Write(ctx, scope, operation, func(tx ports.WriteTx) error {
		order, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		spend, err := spendWritesOf(tx).spendOf(workOrder)
		if err != nil {
			return err
		}
		if !stoppedBy(order, spend) {
			return fmt.Errorf("%w: work order %s is %s, and a spend stop is work a hard spend limit parked", workorders.ErrInvalid, workOrder, order.State)
		}
		edge, err := workorders.Move(order, trigger)
		if err != nil {
			return err
		}
		won, err := tx.WorkOrders().Move(order.ID, edge.From, edge.To)
		if err != nil {
			return err
		}
		if !won {
			return workorders.ErrIllegalMove
		}
		if err := endStoppedLanes(tx, workOrder, actor); err != nil {
			return err
		}
		if err := retireWakes(tx, workOrder, parking.RetiredSpendStop); err != nil {
			return err
		}
		if trigger == workorders.TriggerSpendReplan {
			if err := tx.WorkOrders().Reviewed(workOrder, ""); err != nil {
				return err
			}
		}
		if err := voidRecoveries(tx, workOrder); err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventType(trigger), workOrder, workorders.Change{Trigger: trigger, Reason: workorders.ReasonSpendLimit}, actor)
		if err != nil {
			return err
		}
		if _, err = tx.Append(event); err != nil {
			return err
		}
		state = edge.To
		return nil
	})
	return state, err
}

// endStoppedLanes ends the lanes of a work order whose stop a member ended, which have no process and which no worker
// will report. A lane still parking has one, and the work order is not parked until its process is gone.
func endStoppedLanes(tx ports.WriteTx, workOrder string, actor ledger.Actor) error {
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.WorkOrder != workOrder || row.Status.ActualState != ledger.StateParked {
			continue
		}
		data, err := json.Marshal(workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonSpendLimit})
		if err != nil {
			return err
		}
		draft := ledger.EventDraft{Type: ledger.EventLaneEnded, Actor: actor, Data: data, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: row.ID}}
		if err := closeLane(tx, row, draft, workorders.OutcomeBlocked, workorders.ReasonSpendLimit); err != nil {
			return err
		}
		if err := rollMessages(tx, row.ID, row.Generation, false, actor); err != nil {
			return err
		}
	}
	return nil
}

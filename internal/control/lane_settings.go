package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// opLaneSettings is the write of a member's choice for a lane held on its own settings.
const opLaneSettings = "lane.settings_decide"

// LaneSettings decides how a lane runs whose worktree changed the project settings its agent CLI reads, in a key that
// runs code or widens access (ADR-0040). The lane parked before its session started, and only a member's choice moves
// it: an orchestrator, a delegate, a lane, and a worker hold none, because what a lane runs outside every rule is a
// security choice.
type LaneSettings struct {
	store ports.Store
}

// NewLaneSettings returns the use case over a store.
func NewLaneSettings(store ports.Store) *LaneSettings { return &LaneSettings{store: store} }

// SettingsHold is a lane held on its own settings, as Needs you shows it: the lane, its work order, and the change.
type SettingsHold struct {
	Lane      string
	WorkOrder workorders.WorkOrder
	Change    lanes.SettingsChange
	Since     int64
}

// Decide records a member's choice for the change a lane is held on, bound to the change they read by its digest, so a
// lane that changed its settings again since is held again. Running with the repository's settings or with the lane's
// own wakes the lane, whose next generation runs as chosen; stopping blocks the work with the reason
// `settings_changed`, ends the parked lane, and retires its wake, as a spend stop's block does. It returns the work
// order's state.
func (s *LaneSettings) Decide(ctx context.Context, scope ledger.WorkspaceID, workOrder, lane string, decision ledger.SettingsDecision, actor ledger.Actor) (workorders.State, error) {
	if !authority.Allowed(actor, authority.ActionDecideLaneSettings) {
		return "", workorders.ErrForbidden
	}
	if !slices.Contains(lanes.SettingsChoices, decision.Choice) {
		return "", fmt.Errorf("%w: the choice is one of %s", workorders.ErrInvalid, strings.Join(lanes.SettingsChoices, ", "))
	}
	var state workorders.State
	err := s.store.Write(ctx, scope, opLaneSettings, func(tx ports.WriteTx) error {
		hold, err := heldOn(tx, tx.Lanes(), tx.WorkOrders(), lane)
		if err != nil {
			return err
		}
		if hold == nil || hold.WorkOrder.ID != workOrder {
			return fmt.Errorf("%w: lane %s of work order %s is held on no settings change", workorders.ErrInvalid, lane, workOrder)
		}
		if hold.Change.Digest != decision.Digest {
			return fmt.Errorf("%w: lane %s is held on another settings change than the one named, so read it again", workorders.ErrInvalid, lane)
		}
		decision.Generation = hold.Change.Generation
		data, err := json.Marshal(decision)
		if err != nil {
			return err
		}
		if _, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventLaneSettingsDecided, Actor: actor, Data: data, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: lane},
		}); err != nil {
			return err
		}
		if decision.Choice == lanes.SettingsStop {
			state, err = stopOnSettings(tx, hold.WorkOrder, actor)
			return err
		}
		if _, err := signalIn(tx, parking.Signal{Kind: parking.HumanResume, Key: lane}, actor, noStamp); err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(workOrder)
		state = order.State
		return err
	})
	return state, err
}

// stopOnSettings blocks the work of a lane held on its own settings: the parked lane ends, its wake is retired, and the
// work waits in the blocked list for a member to resolve or replan it.
func stopOnSettings(tx ports.WriteTx, order workorders.WorkOrder, actor ledger.Actor) (workorders.State, error) {
	trigger := workorders.TriggerSettingsBlocked
	edge, err := workorders.Move(order, trigger)
	if err != nil {
		return "", err
	}
	won, err := tx.WorkOrders().Move(order.ID, edge.From, edge.To)
	if err != nil {
		return "", err
	}
	if !won {
		return "", workorders.ErrIllegalMove
	}
	if err := endStoppedLanes(tx, order, workorders.ReasonSettingsChanged, actor); err != nil {
		return "", err
	}
	if err := retireWakes(tx, order.ID, parking.RetiredSettingsStop); err != nil {
		return "", err
	}
	if err := voidRecoveries(tx, order.ID); err != nil {
		return "", err
	}
	event, err := workorders.ChangeEvent(workorders.EventType(trigger), order.ID, workorders.Change{Trigger: trigger, Reason: workorders.ReasonSettingsChanged}, actor)
	if err != nil {
		return "", err
	}
	if _, err := tx.Append(event); err != nil {
		return "", err
	}
	return edge.To, nil
}

// heldOn is the settings change a lane is held on now, or nil: its work order is parked, the newest change it was held
// on is of the generation it is at, and no member has chosen for that generation yet.
func heldOn(tx eventsAbout, rows ports.LaneReader, orders ports.WorkOrderReader, lane string) (*SettingsHold, error) {
	row, err := rows.Row(lane)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil || row.WorkOrder == "" {
		return nil, err
	}
	order, err := orders.Get(row.WorkOrder)
	if err != nil || order.State != workorders.Parked {
		return nil, err
	}
	events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: lane})
	if err != nil {
		return nil, err
	}
	var hold *SettingsHold
	for _, e := range events {
		switch e.Type {
		case ledger.EventLaneSettingsHeld:
			var change lanes.SettingsChange
			if err := json.Unmarshal(e.Data, &change); err != nil {
				return nil, err
			}
			hold = &SettingsHold{Lane: lane, WorkOrder: order, Change: change, Since: e.RecordedAt}
		case ledger.EventLaneSettingsDecided:
			var decision ledger.SettingsDecision
			if err := json.Unmarshal(e.Data, &decision); err != nil {
				return nil, err
			}
			if hold != nil && decision.Generation == hold.Change.Generation {
				hold = nil
			}
		}
	}
	if hold == nil || hold.Change.Generation != row.Generation {
		return nil, nil
	}
	return hold, nil
}

// SettingsHolds lists the lanes held on their own settings, for Needs you and the floor. A held lane is parked, so only
// a parked lane's events are read.
func SettingsHolds(tx ports.ReadTx) ([]SettingsHold, error) {
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, err
	}
	var out []SettingsHold
	for _, row := range rows {
		if row.Status.ActualState != ledger.StateParked {
			continue
		}
		hold, err := heldOn(tx, tx.Lanes(), tx.WorkOrders(), row.ID)
		if err != nil {
			return nil, err
		}
		if hold != nil {
			out = append(out, *hold)
		}
	}
	return out, nil
}

// settingsDecisions are the choices members made for a lane's settings changes, oldest first, which its worker reads
// as a generation starts.
func settingsDecisions(tx eventsAbout, lane string) ([]ledger.SettingsDecision, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: lane})
	if err != nil {
		return nil, err
	}
	var out []ledger.SettingsDecision
	for _, e := range events {
		if e.Type != ledger.EventLaneSettingsDecided {
			continue
		}
		var decision ledger.SettingsDecision
		if err := json.Unmarshal(e.Data, &decision); err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	return out, nil
}

// onlyAChoiceEndsASettingsHold refuses a plain resume of a lane held on its own settings, whoever asks: it would wake
// the lane only for its next generation to find the same change and park again, and the choice is a member's
// (ADR-0040).
func onlyAChoiceEndsASettingsHold(tx ports.WriteTx, lane string) error {
	hold, err := heldOn(tx, tx.Lanes(), tx.WorkOrders(), lane)
	if err != nil || hold == nil {
		return err
	}
	return fmt.Errorf("%w: lane %s waits on a member's choice of its own settings: run it with the repository's settings or its own, or stop it, with ropeshift lane settings",
		workorders.ErrForbidden, lane)
}

// reportSettingsHeld records on a lane the settings change one of its generations was held on, as bounded plain text:
// the files, keys, and summary are what a lane wrote, which every view of the lane shows (recordedSettings). The
// generation is the upload's, whatever the payload says.
func reportSettingsHeld(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var change lanes.SettingsChange
	if err := json.Unmarshal(e.Payload, &change); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if err := ranLane(tx, worker, e); err != nil {
		return err
	}
	change.Generation = e.Generation
	payload, err := json.Marshal(recordedSettings(change))
	if err != nil {
		return err
	}
	e.Payload = payload
	return appendLaneEvent(tx, worker, id, ledger.EventLaneSettingsHeld, e)
}

// recordedSettings is a held change as the ledger keeps it: each file, key, and line plain text on one line with the
// shapes of credentials taken out, each line held to lanes.MaxSettingsText bytes, and each list to its bound.
func recordedSettings(in lanes.SettingsChange) lanes.SettingsChange {
	out := lanes.SettingsChange{Generation: in.Generation, Digest: setupText(in.Digest)}
	for _, file := range in.Files[:min(len(in.Files), lanes.MaxSettingsLines)] {
		out.Files = append(out.Files, setupText(file))
	}
	for _, key := range in.Keys[:min(len(in.Keys), lanes.MaxSettingsLines)] {
		out.Keys = append(out.Keys, setupText(key))
	}
	for _, line := range in.Summary[:min(len(in.Summary), lanes.MaxSettingsLines)] {
		out.Summary = append(out.Summary, clipText(setupText(line), lanes.MaxSettingsText))
	}
	return out
}

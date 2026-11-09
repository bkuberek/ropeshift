package control

import (
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// insertExperience opens a record and records the event that holds it whole, so a replay rebuilds it
// (FR-EXP-1).
func insertExperience(tx ports.WriteTx, record measures.Experience, actor ledger.Actor) error {
	if err := tx.Measures().InsertExperience(record); err != nil {
		return err
	}
	event, err := questions.RecordEvent(questions.EventExperienceRecorded, record.SubjectKind, record.SubjectID, record, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// fillOutcome fills a record's outcome, which happens once.
func fillOutcome(tx ports.WriteTx, kind, subject string, outcome measures.Outcome, actor ledger.Actor) (bool, error) {
	filled, err := tx.Measures().FillOutcome(kind, subject, outcome, tx.Now())
	if err != nil || !filled {
		return false, err
	}
	event, err := questions.RecordEvent(questions.EventOutcomeFilled, kind, subject, outcome, actor)
	if err != nil {
		return false, err
	}
	_, err = tx.Append(event)
	return true, err
}

// laneSettings completes what a worker reported of a lane's settings with what only the ledger
// knows: the size of its work order (D27). An absent size records `unknown`.
func laneSettings(tx ports.WriteTx, workOrder string, reported *measures.Settings) (measures.Settings, string, error) {
	settings := measures.Settings{}
	if reported != nil {
		settings = *reported
	}
	settings.WorkOrderSize = measures.SizeUnknown
	if workOrder == "" {
		return settings, "", nil
	}
	order, err := tx.WorkOrders().Get(workOrder)
	if err != nil {
		return measures.Settings{}, "", err
	}
	settings.WorkOrderSize = measures.SizeOf(order.Size)
	return settings, string(order.Kind), nil
}

// lanePolicyVersion is the lane policy in force, or 0 while only the built-in one stands, which a
// lane's end must not publish as a side effect.
func lanePolicyVersion(tx ports.WriteTx) (int64, error) {
	version, err := tx.Policies().InForce(authority.PolicyLane)
	if errors.Is(err, ports.ErrNotFound) {
		return 0, nil
	}
	return version.Version, err
}

// openLaneExperience writes a lane's record the first time the ledger hears what it runs on: its
// first binding, or its end when it never bound. A record already open stays as it is.
func openLaneExperience(tx ports.WriteTx, lane, workOrder string, reported *measures.Settings, actor ledger.Actor) error {
	if _, err := tx.Measures().Experience(measures.SubjectLane, lane); err == nil || !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	settings, kind, err := laneSettings(tx, workOrder, reported)
	if err != nil {
		return err
	}
	version, err := lanePolicyVersion(tx)
	if err != nil {
		return err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return err
	}
	chosen := kind
	if chosen == "" {
		chosen = "orchestrate"
	}
	return insertExperience(tx, measures.Experience{
		ID: id, SubjectKind: measures.SubjectLane, SubjectID: lane, PolicyVersion: version, Settings: settings, Chosen: chosen,
	}, actor)
}

// closeLaneExperience fills a lane's outcome at its end, opening its record first when the lane
// never bound. A lane ends once, and a repeat of its end fills nothing.
func closeLaneExperience(tx ports.WriteTx, lane, workOrder string, result workorders.LaneResult, actor ledger.Actor) error {
	if err := openLaneExperience(tx, lane, workOrder, result.Settings, actor); err != nil {
		return err
	}
	outcome := measures.Outcome{
		Status: string(result.Outcome), Reason: result.Reason, CostUSD: result.CostUSD, CostLowerBound: result.UnknownCostTurns > 0,
	}
	_, err := fillOutcome(tx, measures.SubjectLane, lane, outcome, actor)
	return err
}

// deciderSettings are the settings of the session that made a decision: an orchestrator's current
// lane, or a lane's own. A person's decision ran on no session.
func deciderSettings(tx ports.WriteTx, actor ledger.Actor) (measures.Settings, error) {
	lane := ""
	switch authority.PartyOf(actor) {
	case authority.PartyLane:
		lane = actor.ID
	case authority.PartyOrchestrator:
		row, err := tx.Workers().Orchestrator(actor.ID)
		if errors.Is(err, ports.ErrNotFound) {
			return measures.Settings{}, nil
		}
		if err != nil {
			return measures.Settings{}, err
		}
		lane = row.Lane()
	default:
		return measures.Settings{}, nil
	}
	record, err := tx.Measures().Experience(measures.SubjectLane, lane)
	if errors.Is(err, ports.ErrNotFound) {
		return measures.Settings{}, nil
	}
	if err != nil {
		return measures.Settings{}, fmt.Errorf("the settings of %s: %w", lane, err)
	}
	return record.Settings, nil
}

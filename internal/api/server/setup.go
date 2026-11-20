package server

import (
	"encoding/json"
	"slices"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// laneSetup reads what a lane's latest session that reported it loaded at its start, and when the ledger recorded it.
// A lane whose sessions reported nothing has none.
func laneSetup(tx ports.ReadTx, lane string) (*lanes.Setup, int64, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: lane})
	if err != nil {
		return nil, 0, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != ledger.EventLaneSetup {
			continue
		}
		var setup lanes.Setup
		if err := json.Unmarshal(events[i].Data, &setup); err != nil {
			return nil, 0, err
		}
		return &setup, events[i].RecordedAt, nil
	}
	return nil, 0, nil
}

// running says a lane runs the generation a record names and that generation's session has not ended.
func running(lane ledger.LaneView, generation int64) bool {
	if lane.ActualState == ledger.StateEnded || lane.ActualState == ledger.StateParked || lane.Generation != generation {
		return false
	}
	at := slices.IndexFunc(lane.Generations, func(g ledger.Generation) bool { return g.Number == generation })
	return at >= 0 && lane.Generations[at].EndedAt == 0
}

// personalSetups finds the lanes whose running session loaded the member's own CLI setup, one item each. Ignoring it
// this time is leaving the lane running, so an item lasts as long as that session does and offers no act: the one way
// to stop a running lane is to cancel its work, which the card names and does not offer.
func personalSetups(tx ports.ReadTx, f floor, add func(string, int64, api.WorkNeed)) error {
	for i := range f.lanes {
		lane := &f.lanes[i]
		at := slices.IndexFunc(f.orders, func(row workorders.WorkOrder) bool { return row.ID == lane.WorkOrder })
		if at < 0 || workorders.Terminal(f.orders[at].State) {
			continue
		}
		setup, since, err := laneSetup(tx, lane.ID)
		if err != nil {
			return err
		}
		if setup == nil || len(setup.Personal) == 0 || !running(*lane, setup.Generation) {
			continue
		}
		choice, why := lanes.SetupAdvice(setup.Personal)
		add(lane.ID, since, api.WorkNeed{
			Reason: api.ReasonPersonalSetup, WorkOrder: &f.orders[at], Lane: lane,
			Detail:   "Your own " + lanes.PersonalWords(setup.Personal) + " loaded in lane " + lane.ID + ".",
			Personal: setup.Personal, Recommendation: choice, RecommendationRationale: why,
			Actions: []string{},
		})
	}
	return nil
}

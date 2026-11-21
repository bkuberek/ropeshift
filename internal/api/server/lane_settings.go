package server

import (
	"net/http"
	"slices"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// laneSettingsNeeds lists the lanes held on their own settings, as items of Needs you in the security category (ADR-0040):
// one for each lane, with the change it made and the three choices, the repository's settings first. It also says which
// work orders they are, so the plain items a parked work order would raise, a resume and a long park, are left to the
// hold that answers them.
func laneSettingsNeeds(tx ports.ReadTx) ([]api.NeedItem, map[string]bool, error) {
	holds, err := control.SettingsHolds(tx)
	if err != nil {
		return nil, nil, err
	}
	items := make([]api.NeedItem, 0, len(holds))
	held := make(map[string]bool, len(holds))
	for i := range holds {
		hold := &holds[i]
		held[hold.WorkOrder.ID] = true
		choice, why := lanes.SettingsAdvice()
		items = append(items, api.NeedItem{Kind: api.NeedLaneSettings, ID: hold.Lane, Since: hold.Since, LaneSettings: &api.LaneSettingsNeed{
			Lane: hold.Lane, WorkOrder: &hold.WorkOrder, Category: api.CategorySecurity, Change: hold.Change,
			Recommendation: choice, RecommendationRationale: why, Actions: slices.Clone(api.LaneSettingsActions),
		}})
	}
	return items, held, nil
}

// laneSettingsHold is the change of its own settings a lane is held on, or nil.
func laneSettingsHold(holds []control.SettingsHold, lane string) *lanes.SettingsChange {
	for i := range holds {
		if holds[i].Lane == lane {
			return &holds[i].Change
		}
	}
	return nil
}

// decideLaneSettings is a member's choice for a lane held on its own settings.
func (h *handler) decideLaneSettings(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.DecideLaneSettings
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	state, err := h.laneSettings.Decide(r.Context(), h.Scope, id, in.Lane, ledger.SettingsDecision{Digest: in.Digest, Choice: in.Choice}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.LaneSettingsDecided{WorkOrder: id, State: string(state), Choice: in.Choice})
}

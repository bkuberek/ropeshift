package server

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
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

// leakKey names one leaked piece on one machine and version, which is one item.
type leakKey struct {
	worker, version, kind, name string
}

func (k leakKey) id() string { return strings.Join([]string{k.worker, k.version, k.kind, k.name}, "/") }

// personalSetupNeeds finds what of the member's own CLI setup reached lanes despite the flags that keep it out: one item
// for each machine, CLI version, and piece, listing the lanes that are not ended whose latest session loaded it, unless
// a member accepted that piece for that machine and version (ADR-0040). A lane whose next session loads clean, or that
// ends, takes its part of an item with it.
func personalSetupNeeds(tx ports.ReadTx, f floor) ([]api.NeedItem, error) {
	titles := map[string]string{}
	for _, order := range f.orders {
		titles[order.ID] = order.Title
	}
	needs := map[leakKey]*api.PersonalSetupNeed{}
	since := map[leakKey]int64{}
	accepted := map[string][]ledger.SetupAcceptance{}
	for _, lane := range f.lanes {
		if lane.ActualState == ledger.StateEnded {
			continue
		}
		setup, at, err := laneSetup(tx, lane.ID)
		if err != nil {
			return nil, err
		}
		if setup == nil || len(setup.Personal) == 0 {
			continue
		}
		if _, read := accepted[lane.WorkerID]; !read {
			if accepted[lane.WorkerID], err = control.AcceptedSetups(tx, lane.WorkerID); err != nil {
				return nil, err
			}
		}
		for _, piece := range setup.Personal {
			if slices.Contains(accepted[lane.WorkerID], ledger.SetupAcceptance{Version: setup.Version, Kind: piece.Kind, Name: piece.Name}) {
				continue
			}
			key := leakKey{worker: lane.WorkerID, version: setup.Version, kind: piece.Kind, name: piece.Name}
			need := needs[key]
			if need == nil {
				choice, why := lanes.SetupAdvice(piece)
				need = &api.PersonalSetupNeed{
					Worker: lane.WorkerID, Version: setup.Version, Piece: piece,
					Recommendation: choice, RecommendationRationale: why, Actions: slices.Clone(api.PersonalSetupActions),
				}
				needs[key], since[key] = need, at
			}
			need.Lanes = append(need.Lanes, api.SetupLane{ID: lane.ID, WorkOrder: lane.WorkOrder, Title: titles[lane.WorkOrder]})
			since[key] = min(since[key], at)
		}
	}
	items := make([]api.NeedItem, 0, len(needs))
	for key, need := range needs {
		slices.SortFunc(need.Lanes, func(a, b api.SetupLane) int { return cmp.Compare(a.ID, b.ID) })
		items = append(items, api.NeedItem{Kind: api.NeedPersonalSetup, ID: key.id(), Since: since[key], PersonalSetup: need})
	}
	return items, nil
}

// personalSetupRoutes keep accepting a member's own setup behind a member's session, because what a lane loads is the
// member's choice.
func (h *handler) personalSetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/workers/{id}/personal-setup/acceptances", h.member(h.acceptPersonalSetup))
}

func (h *handler) acceptPersonalSetup(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.AcceptPersonalSetup
	if !decode(w, r, &in) {
		return
	}
	accepted := ledger.SetupAcceptance{Version: in.Version, Kind: in.Kind, Name: in.Name}
	if err := h.setups.Accept(r.Context(), h.Scope, r.PathValue("id"), accepted, actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

func (h *handler) planningRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/plans", h.listPlans)
	mux.HandleFunc("GET /v1/plans/{id}", h.showPlan)
	mux.HandleFunc("POST /v1/plans/{id}/reviews", h.member(h.reviewPlan))
	mux.HandleFunc("GET /v1/requirements", h.listRequirements)
	mux.HandleFunc("GET /v1/requirements/{key}", h.showRequirement)
	mux.HandleFunc("POST /v1/work-orders/{id}/send-back", h.member(h.sendBack))
	mux.HandleFunc("POST /v1/work-orders/{id}/resolve", h.member(h.unblock(workorders.TriggerResolved)))
	mux.HandleFunc("POST /v1/work-orders/{id}/replan", h.member(h.unblock(workorders.TriggerReplan)))
	mux.HandleFunc("GET /v1/documents", h.listDocuments)
	mux.HandleFunc("GET /v1/documents/read", h.readDocument)
	mux.HandleFunc("GET /v1/documents/versions", h.documentVersions)
}

func (h *handler) listPlans(w http.ResponseWriter, r *http.Request) {
	list := api.PlanList{Plans: []planning.Plan{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		plans, err := tx.Planning().Plans()
		if err == nil && plans != nil {
			list.Plans = plans
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

// reviewPlan starts a contract review. The client generates the review's ID, so a retry returns the
// first answer.
func (h *handler) reviewPlan(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ReviewPlan
	if !decode(w, r, &in) {
		return
	}
	if in.ID == "" {
		invalid(w, "a review needs an id")
		return
	}
	record, err := h.workOrders.ReviewPlan(r.Context(), h.Scope, in.ID, r.PathValue("id"), actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

func (h *handler) showPlan(w http.ResponseWriter, r *http.Request) {
	view := api.PlanView{Contracts: []planning.Contract{}, WorkOrders: []workorders.WorkOrder{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		plan, err := tx.Planning().Plan(r.PathValue("id"))
		if err != nil {
			return err
		}
		view.Plan = plan
		if plan.GoalID != "" {
			goal, err := tx.Planning().Goal(plan.GoalID)
			if err != nil {
				return err
			}
			view.Goal = &goal
		}
		return fillPlan(tx, &view)
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// fillPlan adds each contract's newest version and its work orders. Versions arrive in
// order, so the last of an ID is its newest.
func fillPlan(tx ports.ReadTx, view *api.PlanView) error {
	versions, err := tx.Planning().Contracts()
	if err != nil {
		return err
	}
	newest := map[string]int{}
	for _, contract := range versions {
		if contract.PlanID != view.Plan.ID {
			continue
		}
		if at, seen := newest[contract.ID]; seen {
			view.Contracts[at] = contract
			continue
		}
		newest[contract.ID] = len(view.Contracts)
		view.Contracts = append(view.Contracts, contract)
		orders, err := tx.WorkOrders().ByContract(contract.ID)
		if err != nil {
			return err
		}
		view.WorkOrders = append(view.WorkOrders, orders...)
	}
	return nil
}

func (h *handler) listRequirements(w http.ResponseWriter, r *http.Request) {
	list := api.RequirementList{Requirements: []planning.Requirement{}}
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		versions, err := tx.Planning().Requirements()
		if err != nil {
			return err
		}
		for _, requirement := range versions {
			if last := len(list.Requirements) - 1; last >= 0 && list.Requirements[last].Key == requirement.Key {
				list.Requirements[last] = requirement
				continue
			}
			list.Requirements = append(list.Requirements, requirement)
		}
		return nil
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, list)
}

func (h *handler) showRequirement(w http.ResponseWriter, r *http.Request) {
	var requirement planning.Requirement
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		var err error
		requirement, err = tx.Planning().Requirement(r.PathValue("key"))
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, requirement)
}

func (h *handler) sendBack(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.SendBack
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		invalid(w, "sending work back needs a reason")
		return
	}
	if err := h.reviews.SendBack(r.Context(), h.Scope, r.PathValue("id"), in.Reason, actor); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unblock takes one of the two edges out of `blocked`.
func (h *handler) unblock(trigger workorders.Trigger) func(http.ResponseWriter, *http.Request, ledger.Actor) {
	return func(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
		var in api.Unblock
		if !decode(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Reason) == "" {
			invalid(w, "taking work out of blocked needs a reason")
			return
		}
		if err := h.reviews.Unblock(r.Context(), h.Scope, r.PathValue("id"), trigger, in.Reason, actor); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func documentBody(d ports.KnowledgeDocument) api.Document {
	return api.Document{
		Adapter: d.Key.Adapter, Instance: d.Key.Instance, Path: d.Key.NativeID, Title: d.Title, Kind: d.Kind,
		Version: d.Version, Digest: d.Digest, Source: d.Source, Body: d.Body,
	}
}

func (h *handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	list := api.DocumentList{Documents: []api.Document{}}
	for _, source := range h.Knowledge {
		documents, err := source.List(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		for _, document := range documents {
			list.Documents = append(list.Documents, documentBody(document))
		}
	}
	reply(w, http.StatusOK, list)
}

// locate finds the source that holds a path. A named instance holds it or nothing does.
func (h *handler) locate(ctx context.Context, path, instance string) (ports.KnowledgeSource, ledger.ExternalKey, error) {
	for _, source := range h.Knowledge {
		description, err := source.Describe(ctx)
		if err != nil {
			return nil, ledger.ExternalKey{}, err
		}
		if instance != "" && description.Instance != instance {
			continue
		}
		key := ledger.ExternalKey{Adapter: description.Adapter, Instance: description.Instance, NativeID: path}
		_, err = source.Read(ctx, key, "")
		switch {
		case err == nil:
			return source, key, nil
		case !errors.Is(err, ports.ErrNotFound):
			return nil, ledger.ExternalKey{}, err
		}
	}
	return nil, ledger.ExternalKey{}, fmt.Errorf("document %s: %w", path, ports.ErrNotFound)
}

func (h *handler) readDocument(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	source, key, err := h.locate(r.Context(), query.Get("path"), query.Get("instance"))
	if err != nil {
		fail(w, err)
		return
	}
	document, err := source.Read(r.Context(), key, query.Get("version"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, documentBody(document))
}

func (h *handler) documentVersions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	source, key, err := h.locate(r.Context(), query.Get("path"), query.Get("instance"))
	if err != nil {
		fail(w, err)
		return
	}
	versions, err := source.Versions(r.Context(), key)
	if err != nil {
		fail(w, err)
		return
	}
	list := api.DocumentVersions{Versions: make([]api.DocumentVersion, 0, len(versions))}
	for version := range slices.Values(versions) {
		list.Versions = append(list.Versions, api.DocumentVersion{Version: version.Version, Digest: version.Digest})
	}
	reply(w, http.StatusOK, list)
}

package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
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
	mux.HandleFunc("POST /v1/work-orders/{id}/retry", h.member(h.retryWork))
	mux.HandleFunc("GET /v1/documents", h.listDocuments)
	mux.HandleFunc("GET /v1/documents/read", h.readDocument)
	mux.HandleFunc("GET /v1/documents/versions", h.documentVersions)
	mux.HandleFunc("GET /v1/documents/recorded", h.readRecorded)
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
	var wanted int64
	if raw := r.URL.Query().Get("version"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			invalid(w, "a version is a whole number from 1")
			return
		}
		wanted = parsed
	}
	view := api.PlanView{Contracts: []planning.Contract{}, WorkOrders: []workorders.WorkOrder{}, Versions: []api.PlanVersionLine{}}
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
		if err := fillPlan(tx, &view); err != nil {
			return err
		}
		if view.Versions, err = planVersions(tx, plan.ID); err != nil {
			return err
		}
		if wanted != 0 && !slices.ContainsFunc(view.Versions, func(line api.PlanVersionLine) bool { return line.Version == wanted }) {
			return fmt.Errorf("plan %s has no version %d: %w", plan.ID, wanted, ports.ErrNotFound)
		}
		view.Approval, err = planApprovalView(tx, plan.ID, wanted)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// betterApproval says whether a request stands in for another of the same plan: the one waiting, or else
// the one of the newer version, or else the one raised later.
func betterApproval(a, than authority.Approval) bool {
	switch {
	case a.Open() != than.Open():
		return a.Open()
	case a.PlanVersion != than.PlanVersion:
		return a.PlanVersion > than.PlanVersion
	}
	return a.RaisedAt > than.RaisedAt
}

// planApprovalView reads the request to approve a plan: the one waiting, or else the newest, or, for a
// version asked for by number, that version's, with the version it is about. A plan nobody has asked about
// has none.
func planApprovalView(tx ports.ReadTx, plan string, wanted int64) (*api.PlanApprovalView, error) {
	rows, err := tx.Approvals().All()
	if err != nil {
		return nil, err
	}
	var chosen *authority.Approval
	for i := range rows {
		if rows[i].Kind != authority.KindPlan || rows[i].Plan != plan || (wanted != 0 && rows[i].PlanVersion != wanted) {
			continue
		}
		if chosen == nil || betterApproval(rows[i], *chosen) {
			chosen = &rows[i]
		}
	}
	if chosen == nil {
		return nil, nil
	}
	view := &api.PlanApprovalView{Approval: *chosen, Detail: api.PlanDetailOf(*chosen)}
	version, err := tx.Planning().PlanVersion(plan, chosen.PlanVersion)
	if err != nil {
		return nil, err
	}
	view.Records, view.Drafts = version.Records, version.Decisions
	return view, nil
}

// planVersions lists the versions a plan has, newest first: those the ledger holds and those a request
// names, each with the request that stands for it.
func planVersions(tx ports.ReadTx, plan string) ([]api.PlanVersionLine, error) {
	held, err := tx.Planning().PlanVersions()
	if err != nil {
		return nil, err
	}
	lines := map[int64]*api.PlanVersionLine{}
	for _, version := range held {
		if version.PlanID == plan {
			lines[version.Version] = &api.PlanVersionLine{Version: version.Version, Digest: version.Digest}
		}
	}
	rows, err := tx.Approvals().All()
	if err != nil {
		return nil, err
	}
	standing := map[int64]authority.Approval{}
	for _, row := range rows {
		if row.Kind != authority.KindPlan || row.Plan != plan {
			continue
		}
		if was, seen := standing[row.PlanVersion]; !seen || betterApproval(row, was) {
			standing[row.PlanVersion] = row
		}
	}
	for version, row := range standing {
		line := lines[version]
		if line == nil {
			line = &api.PlanVersionLine{Version: version}
			lines[version] = line
		}
		line.Approval, line.State = row.ID, row.State
	}
	out := make([]api.PlanVersionLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, *line)
	}
	slices.SortFunc(out, func(a, b api.PlanVersionLine) int { return cmp.Compare(b.Version, a.Version) })
	return out, nil
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

// retryWork is a member's retry of failed work, with why.
func (h *handler) retryWork(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.Retry
	if !decode(w, r, &in) {
		return
	}
	state, err := h.recoveries.Retry(r.Context(), h.Scope, r.PathValue("id"), in.Reason, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.Retried{WorkOrder: r.PathValue("id"), State: state})
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

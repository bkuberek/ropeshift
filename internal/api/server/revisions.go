package server

import (
	"errors"
	"net/http"
	"slices"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

func (h *handler) revisionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/plans/{id}/revision", h.showRevision)
	mux.HandleFunc("POST /v1/plans/{id}/revision/confirm", h.member(h.confirmRevision))
	mux.HandleFunc("POST /v1/plans/{id}/revision/sessions", h.member(h.reviseSession))
	mux.HandleFunc("POST /v1/work-orders/{id}/decision-rework", h.member(h.decisionRework))
}

// showRevision reads the open revision of a plan, or a view with none for a plan that has none open.
func (h *handler) showRevision(w http.ResponseWriter, r *http.Request) {
	var view api.PlanRevisionView
	err := h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		plan, err := tx.Planning().Plan(r.PathValue("id"))
		if err != nil {
			return err
		}
		view, err = openRevisionView(tx, plan)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, view)
}

// confirmRevision records that the approved version of a plan stands with the decisions made since.
func (h *handler) confirmRevision(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	closed, err := h.planRevisions.ConfirmRevision(r.Context(), h.Scope, r.PathValue("id"), actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, closed)
}

// reviseSession records the planning work order of a session that revises a plan. The client generates its ID, so a
// retry returns what the first attempt wrote.
func (h *handler) reviseSession(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ReviseSession
	if !decode(w, r, &in) {
		return
	}
	order, err := h.planRevisions.ReviseSession(r.Context(), h.Scope, r.PathValue("id"), control.ReviseSession{ID: in.ID, Repository: in.Repository}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, order)
}

// decisionRework makes the rework of one finished work order for decisions its plan's revision lists.
func (h *handler) decisionRework(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.DecisionRework
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	rework, err := h.questions.DecisionRework(r.Context(), h.Scope, id, in.Decisions, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.DecisionReworked{WorkOrder: id, Rework: rework})
}

// openRevisionView reads a plan's open revision as a member reads it, or a view with none.
func openRevisionView(tx ports.ReadTx, plan planning.Plan) (api.PlanRevisionView, error) {
	revision, err := tx.Planning().OpenRevision(plan.ID)
	if errors.Is(err, ports.ErrNotFound) {
		return api.PlanRevisionView{Plan: plan.ID, Title: plan.Title, Decisions: []api.RevisionDecision{}, Finished: []api.RevisionFinishedWork{}, Waiting: []api.RevisionWaitingWork{}}, nil
	}
	if err != nil {
		return api.PlanRevisionView{}, err
	}
	return revisionView(tx, plan, revision)
}

// revisionView reads a revision with what its decisions say, the finished work they reached, and the work that
// waits for it. A closed revision waits for nothing.
func revisionView(tx ports.ReadTx, plan planning.Plan, revision planning.Revision) (api.PlanRevisionView, error) {
	view := api.PlanRevisionView{
		Plan: plan.ID, Title: plan.Title, Revision: &revision,
		Decisions: []api.RevisionDecision{}, Finished: []api.RevisionFinishedWork{}, Waiting: []api.RevisionWaitingWork{},
	}
	for _, id := range revision.DecisionIDs {
		decision, err := tx.Questions().Decision(id)
		if err != nil {
			return api.PlanRevisionView{}, err
		}
		reversibility := questions.Reversible
		if decision.Reversibility == questions.OneWay {
			reversibility = questions.OneWay
		}
		view.Decisions = append(view.Decisions, api.RevisionDecision{
			ID: decision.ID, Statement: decision.Statement, Scope: decision.Scope, Reversibility: reversibility,
			Status: decision.Status, DecidedBy: decision.DecidedBy,
		})
	}
	for _, work := range revision.Finished {
		order, err := tx.WorkOrders().Get(work.WorkOrder)
		if err != nil {
			return api.PlanRevisionView{}, err
		}
		view.Finished = append(view.Finished, api.RevisionFinishedWork{
			WorkOrder: order.ID, Title: order.Title, State: order.State, Decisions: slices.Clone(work.Decisions), Rework: work.Rework,
		})
	}
	if !revision.Open() {
		return view, nil
	}
	contracts, err := tx.Planning().Contracts()
	if err != nil {
		return api.PlanRevisionView{}, err
	}
	seen := map[string]bool{}
	for _, contract := range contracts {
		if contract.PlanID != plan.ID || seen[contract.ID] {
			continue
		}
		seen[contract.ID] = true
		orders, err := tx.WorkOrders().ByContract(contract.ID)
		if err != nil {
			return api.PlanRevisionView{}, err
		}
		for _, order := range orders {
			if order.State == workorders.ContractReview && (order.Kind == workorders.KindChange || order.Kind == workorders.KindRework) {
				view.Waiting = append(view.Waiting, api.RevisionWaitingWork{WorkOrder: order.ID, Title: order.Title})
			}
		}
	}
	return view, nil
}

// revisionOfApproval is the revision a plan approval answers: the plan's open revision while the request waits, and
// the one the approval closed once it is approved. It is nil when none applies.
func revisionOfApproval(tx ports.ReadTx, plan planning.Plan, approval api.PlanApprovalView) (*api.PlanRevisionView, error) {
	var revision planning.Revision
	if approval.Approval.Open() {
		open, err := tx.Planning().OpenRevision(plan.ID)
		if errors.Is(err, ports.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		revision = open
	} else {
		all, err := tx.Planning().Revisions()
		if err != nil {
			return nil, err
		}
		at := slices.IndexFunc(all, func(row planning.Revision) bool { return row.PlanID == plan.ID && row.Approval == approval.Approval.ID })
		if at < 0 {
			return nil, nil
		}
		revision = all[at]
	}
	view, err := revisionView(tx, plan, revision)
	return &view, err
}

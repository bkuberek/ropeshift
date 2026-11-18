package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// spendRoutes serve what a member reads of spend (ADR-0037): each key of the spend policies in force with its version,
// and the charged spend of work orders against their limits. Changing a key is a policy publication, which the policy
// route runs, so every rule that holds a policy holds a spend setting.
func (h *handler) spendRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/spend", h.readSpend)
	mux.HandleFunc("GET /v1/work-orders/{id}/spend", h.readWorkOrderSpend)
	mux.HandleFunc("POST /v1/work-orders/{id}/spend/raise", h.member(h.raiseSpend))
	mux.HandleFunc("POST /v1/work-orders/{id}/spend/resume", h.member(h.resumeSpend))
}

// raiseSpend lifts a work order's hard limit, by a percentage or to an amount, and answers the limit before and after
// (FR-SPEND-7, FR-SPEND-8). It is a member's act: the use case holds the action, so a call that gets past the class gate
// with a role that lacks it is still refused.
func (h *handler) raiseSpend(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RaiseSpend
	if !decode(w, r, &in) {
		return
	}
	change, err := h.spend.Raise(r.Context(), h.Scope, control.SpendRaise{WorkOrder: r.PathValue("id"), ByPercent: in.ByPercent, ToUSD: in.ToUSD}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, change)
}

// resumeSpend grants a work order one more generation's allowance, which wakes the lane its hard limit parked.
func (h *handler) resumeSpend(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	change, err := h.spend.Resume(r.Context(), h.Scope, r.PathValue("id"), actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, change)
}

// spendRaiseChanges is what a card for a delegate's raise shows a member: the hard limit in force and the one the raise
// asks for.
func spendRaiseChanges(h *handler, r *http.Request, body []byte) (any, error) {
	var in api.RaiseSpend
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	spend, err := h.spend.WorkOrderSpend(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	to := control.SpendRaise{ByPercent: in.ByPercent, ToUSD: in.ToUSD}.LimitFrom(spend.HardLimit)
	return []authority.FieldChange{{Field: "hard_limit_usd", From: spend.HardLimit, To: to}}, nil
}

// spendResumeChanges is the same for a resume, which sets the limit to what the work order is charged and one more
// generation's mean.
func spendResumeChanges(h *handler, r *http.Request, _ []byte) (any, error) {
	spend, err := h.spend.WorkOrderSpend(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	return []authority.FieldChange{{Field: "hard_limit_usd", From: spend.HardLimit, To: "what it is charged and one more generation"}}, nil
}

func spendKeys[P interface{ Text(string) string }](policy P, keys []string) []api.SpendKey {
	out := make([]api.SpendKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, api.SpendKey{Key: key, Value: policy.Text(key)})
	}
	return out
}

func policyView(version authority.PolicyVersion, keys []api.SpendKey) api.SpendPolicyView {
	return api.SpendPolicyView{Version: version.Version, AuthoredBy: version.AuthoredBy, CreatedAt: version.CreatedAt, Keys: keys}
}

func (h *handler) readSpend(w http.ResponseWriter, r *http.Request) {
	state, err := h.spend.State(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	out := api.SpendView{
		Project:      policyView(state.ProjectVersion, spendKeys(state.Project, authority.SpendPolicyKeys)),
		Installation: policyView(state.InstallationVersion, spendKeys(state.Installation, authority.InstallationSpendPolicyKeys)),
		WorkOrders:   state.WorkOrders,
	}
	if out.WorkOrders == nil {
		out.WorkOrders = []ledger.WorkOrderSpend{}
	}
	reply(w, http.StatusOK, out)
}

func (h *handler) readWorkOrderSpend(w http.ResponseWriter, r *http.Request) {
	spend, err := h.spend.WorkOrderSpend(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, spend)
}

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// spendRoutes serve what a member reads of spend (ADR-0037): each key of the spend policies in force with its version,
// and the charged spend of work orders against their limits. Changing a key is a policy publication, which the policy
// route runs, so every rule that holds a policy holds a spend setting.
func (h *handler) spendRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/spend", h.readSpend)
	mux.HandleFunc("GET /v1/work-orders/{id}/spend", h.readWorkOrderSpend)
	mux.HandleFunc("POST /v1/work-orders/{id}/spend/raise", h.member(h.raiseSpend))
	mux.HandleFunc("POST /v1/work-orders/{id}/spend/resume", h.member(h.resumeSpend))
	mux.HandleFunc("POST /v1/spend/project/raise", h.member(h.raiseProjectSpend))
	mux.HandleFunc("POST /v1/work-orders/{id}/spend/block", h.member(h.blockSpend))
	mux.HandleFunc("POST /v1/work-orders/{id}/spend/replan", h.member(h.replanSpend))
}

// raiseProjectSpend lifts the project's daily or weekly limit for the window it is made in, by a percentage or to an
// amount, and answers the limit before and after (FR-SPEND-6, FR-SPEND-8). It is a member's act, held by the use case.
func (h *handler) raiseProjectSpend(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.RaiseProjectSpend
	if !decode(w, r, &in) {
		return
	}
	change, err := h.spend.RaiseProject(r.Context(), h.Scope,
		control.ProjectRaise{Window: in.Window, ByPercent: in.ByPercent, ToUSD: in.ToUSD, WindowStart: in.WindowStart}, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, change)
}

// spendRaiseProjectAsAnAmount names a delegate's raise by a percentage as the amount it comes to now, for the reason a work
// order's raise does: a card is confirmed later, and a percentage read then would be of whatever limit is in force by then.
// It names the window's start too, for the same reason: a card confirmed in a later day or week must not lift that one with
// an amount a member read in this one, and the raise refuses a window that is over. The start is the server's to name: a
// delegate's own is overwritten, because a card bound to a window the delegate chose would show a member a window the call
// was not made in.
func spendRaiseProjectAsAnAmount(h *handler, r *http.Request, body []byte) ([]byte, error) {
	var in api.RaiseProjectSpend
	if err := json.Unmarshal(body, &in); err != nil || !ledger.ValidWindow(in.Window) {
		return body, nil
	}
	spend, err := h.spend.ProjectSpend(r.Context(), h.Scope)
	if err != nil {
		return nil, err
	}
	window := spend.Day
	if in.Window == ledger.WindowWeek {
		window = spend.Week
	}
	in.WindowStart = window.StartsAt
	if in.ByPercent > 0 && in.ToUSD == 0 && window.HardLimit > 0 {
		in.ToUSD, in.ByPercent = math.Round(control.SpendRaise{ByPercent: in.ByPercent}.LimitFrom(window.HardLimit)*100)/100, 0
	}
	return json.Marshal(in)
}

// spendRaiseProjectChanges is what a card for a delegate's raise of a project's limit shows a member: the limit in force, the
// one the raise asks for, and the window it lifts, such as "day starting 2026-11-18 00:00 UTC".
func spendRaiseProjectChanges(h *handler, r *http.Request, body []byte) (any, error) {
	var in api.RaiseProjectSpend
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("%w: %v", authority.ErrInvalid, err)
	}
	if !ledger.ValidWindow(in.Window) {
		return nil, fmt.Errorf("%w: a raise names the day or the week", authority.ErrInvalid)
	}
	spend, err := h.spend.ProjectSpend(r.Context(), h.Scope)
	if err != nil {
		return nil, err
	}
	field, window := "daily_limit_usd", spend.Day
	if in.Window == ledger.WindowWeek {
		field, window = "weekly_limit_usd", spend.Week
	}
	start := in.WindowStart
	if start == 0 {
		start = window.StartsAt
	}
	return []any{
		authority.FieldChange{Field: field, From: window.HardLimit, To: control.SpendRaise{ByPercent: in.ByPercent, ToUSD: in.ToUSD}.LimitFrom(window.HardLimit)},
		authority.ArgumentChange{Field: "window", To: in.Window + " starting " + time.UnixMilli(start).UTC().Format("2006-01-02 15:04 MST")},
	}, nil
}

// blockSpend blocks the work order a hard limit parked, with the reason `spend_limit` (FR-SPEND-7). It is a member's
// act, held by the use case as a raise is.
func (h *handler) blockSpend(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	h.endSpendStop(w, r, actor, h.spend.Block)
}

// replanSpend sends the work order a hard limit parked to replanning.
func (h *handler) replanSpend(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	h.endSpendStop(w, r, actor, h.spend.Replan)
}

func (h *handler) endSpendStop(w http.ResponseWriter, r *http.Request, actor ledger.Actor,
	end func(context.Context, ledger.WorkspaceID, string, ledger.Actor) (workorders.State, error)) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	state, err := end(r.Context(), h.Scope, id, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.SpendStopEnded{WorkOrder: id, State: string(state), Reason: workorders.ReasonSpendLimit})
}

// spendBlockChanges is what a card for a delegate's block shows a member: the work order leaves `parked` for `blocked`.
func spendBlockChanges(_ *handler, _ *http.Request, _ []byte) (any, error) {
	return []authority.FieldChange{{Field: "state", From: workorders.Parked, To: workorders.Blocked}}, nil
}

// spendReplanChanges is the same for a delegate's replan, which sends the work order to `planning`.
func spendReplanChanges(_ *handler, _ *http.Request, _ []byte) (any, error) {
	return []authority.FieldChange{{Field: "state", From: workorders.Parked, To: workorders.Planning}}, nil
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

// spendRaiseAsAnAmount names a delegate's raise by a percentage as the amount it comes to now. A card is confirmed
// later, and a percentage read then would be of whatever limit is in force by then, so a member who confirms a card that
// shows $37.50 could lift the limit to something else. The card binds to, shows, and is replayed with the amount, which
// is cut to the cent so the figure a member reads is the one that is asked for.
func spendRaiseAsAnAmount(h *handler, r *http.Request, body []byte) ([]byte, error) {
	var in api.RaiseSpend
	if err := json.Unmarshal(body, &in); err != nil || in.ByPercent <= 0 || in.ToUSD != 0 {
		return body, nil
	}
	spend, err := h.spend.WorkOrderSpend(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if spend.HardLimit <= 0 {
		return body, nil
	}
	to := math.Round(control.SpendRaise{ByPercent: in.ByPercent}.LimitFrom(spend.HardLimit)*100) / 100
	return json.Marshal(api.RaiseSpend{ToUSD: to})
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
		ProjectSpend: state.ProjectSpend,
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

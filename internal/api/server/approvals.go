package server

import (
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func (h *handler) approvalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/approvals", h.listApprovals)
	mux.HandleFunc("GET /v1/approvals/{id}", h.showApproval)
	mux.HandleFunc("POST /v1/approvals/{id}/decide", h.member(h.decideApproval))
}

func (h *handler) listApprovals(w http.ResponseWriter, r *http.Request) {
	rows, err := h.approvals.List(r.Context(), h.Scope, authority.ApprovalState(r.URL.Query().Get("state")))
	if err != nil {
		fail(w, err)
		return
	}
	if rows == nil {
		rows = []authority.Approval{}
	}
	reply(w, http.StatusOK, api.ApprovalList{Approvals: rows})
}

func (h *handler) showApproval(w http.ResponseWriter, r *http.Request) {
	row, err := h.approvals.Get(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	detail := api.ApprovalDetail{Approval: row}
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		var err error
		detail.Conversation, err = cardConversation(tx.Members(), tx.Conversations(), row)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, detail)
}

// decideApproval is a member's word. The session this route demands is the one thing a lane cannot
// present, and a decision that names a digest other than the request's is refused.
func (h *handler) decideApproval(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.DecideApproval
	if !decode(w, r, &in) {
		return
	}
	if in.Digest == "" {
		invalid(w, "a decision names the digest it saw")
		return
	}
	channel, ok := placed(w, r, in.Place)
	if !ok {
		return
	}
	decision := authority.Decision{ID: r.PathValue("id"), Digest: in.Digest, Allow: in.Allow, Reason: in.Reason}
	if row, err := h.approvals.Get(r.Context(), h.Scope, decision.ID); err == nil && row.Kind == authority.KindConfirm {
		h.confirmCard(w, r.WithContext(withChannel(r.Context(), channel)), decision, actor)
		return
	}
	record, err := h.approvals.DecideFrom(r.Context(), h.Scope, decision, actor, channel)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, record)
}

// workerUseApproval is what a lane's hook polls, through its worker.
func (h *handler) workerUseApproval(w http.ResponseWriter, r *http.Request) {
	var in authority.ApprovalUse
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	answer, err := h.plane.UseApproval(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, answer)
}

// workerDecideApproval is how an orchestrator's decision reaches the ledger: through its worker,
// which proves itself, and which the control plane holds to the policy in force.
func (h *handler) workerDecideApproval(w http.ResponseWriter, r *http.Request) {
	var in authority.WorkerDecision
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	record, err := h.plane.DecideApproval(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, record)
}

// placed reads where a member decided, and the channel the record keeps: the conversation for a word given
// inline in one on the web listener, and otherwise the web listener or the command line.
func placed(w http.ResponseWriter, r *http.Request, place string) (string, bool) {
	channel := channelOf(r.Context())
	switch place {
	case "", api.PlaceNeedsYou:
	case api.PlaceConversation:
		if channel == authority.ChannelWeb {
			channel = authority.ChannelConversation
		}
	default:
		invalid(w, "place is %s or %s, and not %q", api.PlaceNeedsYou, api.PlaceConversation, place)
		return "", false
	}
	return channel, true
}

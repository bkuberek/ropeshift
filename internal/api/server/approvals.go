package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

func (h *handler) approvalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/approvals", h.listApprovals)
	mux.HandleFunc("GET /v1/approvals/{id}", h.showApproval)
	mux.HandleFunc("POST /v1/approvals/{id}/decide", h.member(h.decideApproval))
	mux.HandleFunc("POST /v1/approvals/{id}/repositories/{name}/retry", h.member(h.retryRepository))
	mux.HandleFunc("POST /v1/approvals/{id}/repositories/{name}/skip", h.member(h.skipRepository))
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

// showApproval answers one request. A plan approval carries what its approval registered, or, while it waits and
// its version's manifest lists repositories, the preview the plan's route reads, so the first version a planning
// branch asks about, which has no plan route until a member approves it, previews as well.
func (h *handler) showApproval(w http.ResponseWriter, r *http.Request) {
	row, err := h.approvals.Get(r.Context(), h.Scope, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	detail := api.ApprovalDetail{Approval: row}
	var listed []planning.ManifestRepository
	err = h.Store.Read(r.Context(), h.Scope, func(tx ports.ReadTx) error {
		var err error
		if detail.Conversation, err = cardConversation(tx.Members(), tx.Conversations(), row); err != nil || row.Kind != authority.KindPlan {
			return err
		}
		if detail.Registration, err = registrationView(tx, row.ID); err != nil || !row.Open() {
			return err
		}
		listed, err = control.VersionManifest(tx, row)
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	if len(listed) != 0 {
		detail.Registration = h.previewRegistration(r, row)
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
	// A member who closes the page or interrupts the command does not stop a creation, which the approval started.
	reply(w, http.StatusOK, h.registerApproved(context.WithoutCancel(r.Context()), record, actor))
}

// registerApproved registers, as the member who approved a plan version, the repositories its manifest adds,
// from the clones the worker this daemon runs keeps beside the planning repository's clone (ADR-0022).
// A plan approval is a member's act, and registration is one too, so it runs here and in no tick. The
// approval stands whatever registration comes to, so a failure is told beside it.
func (h *handler) registerApproved(ctx context.Context, record authority.Approval, actor ledger.Actor) api.ApprovalDecided {
	decided := api.ApprovalDecided{Approval: record}
	if record.Kind != authority.KindPlan || record.State != authority.ApprovalApproved || h.Checkouts == nil {
		return decided
	}
	added, err := h.repositories.RegisterApproved(ctx, h.Scope, record.ID, h.Checkouts, h.Creator, h.projectName(ctx), false, actor)
	if err != nil {
		decided.RepositoriesProblem = "registering the repositories the manifest adds failed: " + err.Error()
	}
	if len(added) != 0 {
		decided.Repositories = addedViews(added)
	}
	return decided
}

// retryRepository runs the creation of one repository an approved plan version names again, as a member's word. A member who
// closes the page or interrupts the command does not stop it.
func (h *handler) retryRepository(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if h.Checkouts == nil {
		fail(w, fmt.Errorf("%w: this daemon runs no worker, so it creates and registers nothing", workorders.ErrIllegalMove))
		return
	}
	ctx := context.WithoutCancel(r.Context())
	id := r.PathValue("id")
	added, err := h.repositories.RetryCreation(ctx, h.Scope, id, r.PathValue("name"), h.Checkouts, h.Creator, h.projectName(ctx), actor)
	h.answerRepository(w, ctx, id, added, err)
}

// skipRepository records that a member chose to leave one repository an approved plan version names uncreated, with why.
func (h *handler) skipRepository(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.SkipRepository
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	added, err := h.repositories.SkipCreation(r.Context(), h.Scope, id, r.PathValue("name"), in.Reason, actor)
	h.answerRepository(w, r.Context(), id, added, err)
}

// answerRepository answers a retry or a skip with the approval and what its registration holds now. A refusal is the
// answer itself, and an error a run met after it began is told beside the registration, as a decision tells it.
func (h *handler) answerRepository(w http.ResponseWriter, ctx context.Context, id string, added []control.Added, err error) {
	refused := errors.Is(err, workorders.ErrIllegalMove) || errors.Is(err, workorders.ErrForbidden) || errors.Is(err, workorders.ErrInvalid) || errors.Is(err, ports.ErrNotFound)
	if err != nil && (refused || added == nil) {
		fail(w, err)
		return
	}
	record, getErr := h.approvals.Get(ctx, h.Scope, id)
	if getErr != nil {
		fail(w, getErr)
		return
	}
	decided := api.ApprovalDecided{Approval: record, Repositories: addedViews(added)}
	if err != nil {
		decided.RepositoriesProblem = "creating the repository failed: " + err.Error()
	}
	reply(w, http.StatusOK, decided)
}

// projectName is the name of the project a call is for, as its path names it, or else as the installation's registry
// names the project this handler serves.
func (h *handler) projectName(ctx context.Context) string {
	if project, ok := projectOf(ctx); ok {
		return project.Name
	}
	projects, err := h.Projects.List(ctx)
	if err != nil {
		return ""
	}
	for _, project := range projects {
		if project.ID == h.Scope {
			return project.Name
		}
	}
	return ""
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

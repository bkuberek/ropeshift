package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// workerRoutes are the installation's routes for workers, and the plane protocol a worker speaks with its
// credential.
func (h *handler) workerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/workers/tokens", h.member(h.createJoinToken))
	mux.HandleFunc("POST /v1/workers/register", h.register)
	mux.HandleFunc("GET /v1/workers", h.listWorkers)
	mux.HandleFunc("POST /v1/workers/{id}/sessions", h.openSession)
	mux.HandleFunc("POST /v1/workers/{id}/heartbeat", h.heartbeat)
	mux.HandleFunc("GET /v1/workers/{id}/desired", h.desired)
	mux.HandleFunc("GET /v1/workers/{id}/watch", h.watch)
	mux.HandleFunc("POST /v1/workers/{id}/events", h.events)
	mux.HandleFunc("POST /v1/workers/{id}/claims", h.workerClaim)
	mux.HandleFunc("POST /v1/workers/{id}/ask", h.workerAsk)
	mux.HandleFunc("POST /v1/workers/{id}/lane-facts", h.workerLaneFacts)
	mux.HandleFunc("POST /v1/workers/{id}/lane-context", h.workerLaneContext)
	mux.HandleFunc("POST /v1/workers/{id}/rulings", h.workerRule)
	mux.HandleFunc("POST /v1/workers/{id}/approvals/use", h.workerUseApproval)
	mux.HandleFunc("POST /v1/workers/{id}/approvals/decide", h.workerDecideApproval)
	mux.HandleFunc("POST /v1/workers/{id}/assistant-credentials", h.workerAssistantCredential)
	mux.HandleFunc("POST /v1/workers/{id}/assistant-entries", h.workerAssistantEntry)
}

// workerAssistantCredential makes the credential of one generation of an assistant's run the worker holds.
// The token crosses here once, to the worker that puts it in the session's environment.
func (h *handler) workerAssistantCredential(w http.ResponseWriter, r *http.Request) {
	var in ledger.AssistantCredentialRequest
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	grant, err := h.plane.AssistantCredential(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, grant)
}

// workerAssistantEntry records a reply an assistant's run the worker holds wrote.
func (h *handler) workerAssistantEntry(w http.ResponseWriter, r *http.Request) {
	var in ledger.AssistantEntryRequest
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	if err := h.plane.AssistantEntry(r.Context(), in); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// laneRoutes are a project's routes for its lanes and its fleet.
func (h *handler) laneRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/lanes", h.listLanes)
	mux.HandleFunc("GET /v1/lanes/{id}", h.showLane)
	mux.HandleFunc("POST /v1/work-orders/{id}/lane-resume", h.member(h.resumeLane))
	mux.HandleFunc("POST /v1/work-orders/{id}/lane-reset", h.member(h.resetLane))
	mux.HandleFunc("POST /v1/work-orders/{id}/lane-respawn", h.member(h.respawnLane))
	mux.HandleFunc("POST /v1/work-orders/{id}/lane-archive", h.member(h.archiveLane))
	mux.HandleFunc("GET /v1/fleet", h.showFleet)
	mux.HandleFunc("PUT /v1/fleet/mode", h.member(h.setFleetMode))
	mux.HandleFunc("PUT /v1/fleet/caps", h.member(h.setFleetCaps))
}

func bearer(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token
}

// authOf reads who a call says it is. The use case decides whether to believe it.
func authOf(r *http.Request) ledger.WorkerAuth {
	return ledger.WorkerAuth{WorkerID: r.PathValue("id"), Credential: bearer(r)}
}

func (h *handler) createJoinToken(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	token, err := h.plane.CreateJoinToken(r.Context(), actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, api.JoinTokenResponse{Token: token})
}

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	var in ledger.Registration
	if !decode(w, r, &in) {
		return
	}
	if in.WorkerID == "" {
		invalid(w, "a registration needs a worker_id")
		return
	}
	credential, err := h.plane.Register(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, credential)
}

// listLanes and showLane are reads. A lane's state derives from what happened, so the API
// has no route that writes one (FR-LANE-5).
func (h *handler) listLanes(w http.ResponseWriter, r *http.Request) {
	lanes, err := h.workers.Lanes(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if lanes == nil {
		lanes = []ledger.LaneView{}
	}
	reply(w, http.StatusOK, api.LaneList{Lanes: lanes})
}

func (h *handler) showLane(w http.ResponseWriter, r *http.Request) {
	lane, err := h.workers.Lane(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, lane)
}

func (h *handler) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := h.plane.List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if workers == nil {
		workers = []ledger.Worker{}
	}
	var now int64
	err = h.Installation.Read(r.Context(), func(tx ports.SharedReadTx) error {
		now, err = tx.Now()
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.WorkerList{Workers: workers, AsOf: now})
}

func (h *handler) openSession(w http.ResponseWriter, r *http.Request) {
	var in ledger.SessionRequest
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	session, err := h.plane.OpenSession(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, session)
}

func (h *handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	var in ledger.Heartbeat
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	if in.Nonce == "" {
		invalid(w, "a heartbeat needs a nonce")
		return
	}
	answer, err := h.plane.Heartbeat(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, answer)
}

func (h *handler) desired(w http.ResponseWriter, r *http.Request) {
	since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	if err != nil {
		invalid(w, "since must be a revision")
		return
	}
	revisions, err := api.ParseRevisions(r.URL.Query()["revision"])
	if err != nil {
		invalid(w, "%v", err)
		return
	}
	state, err := h.plane.Desired(r.Context(), ledger.DesiredRequest{Auth: authOf(r), Since: since, Revisions: revisions})
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, state)
}

// watch streams hints only. If the stream breaks, the next heartbeat still carries
// the current revision, so a lost hint costs latency.
func (h *handler) watch(w http.ResponseWriter, r *http.Request) {
	after, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if err != nil {
		after = 0
	}
	hints, err := h.plane.Watch(r.Context(), ledger.WatchRequest{Auth: authOf(r), After: after})
	if err != nil {
		fail(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		refuse(w, http.StatusInternalServerError, api.ErrorDetail{Code: api.CodeUnavailable, Message: "streaming is not available"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	for rev := range hints {
		if _, err := fmt.Fprintf(w, "id: %d\nevent: revision\ndata: {\"rev\":%d}\n\n", rev, rev); err != nil {
			return
		}
		flusher.Flush()
	}
}

func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	var in ledger.UploadBatch
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	answer, err := h.plane.Upload(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, answer)
}

// archiveLane asks the worker that ran a lane to take its worktree away. It goes through
// the work order, as a lane's start does.
func (h *handler) workerAsk(w http.ResponseWriter, r *http.Request) {
	var in questions.LaneAsk
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	result, err := h.plane.Ask(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

// workerLaneFacts is what a worker reads for a handoff, about a lane it holds.
func (h *handler) workerLaneFacts(w http.ResponseWriter, r *http.Request) {
	var in ledger.LaneFactsRequest
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	result, err := h.plane.LaneFacts(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

// workerLaneContext is what a lane reads past its pack, through the worker that holds it.
func (h *handler) workerLaneContext(w http.ResponseWriter, r *http.Request) {
	var in ledger.LaneContextRequest
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	result, err := h.plane.LaneContext(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, result)
}

// workerRule is how an orchestrator's rung 3 or 4 reaches the ledger: through its worker,
// which proves itself, and which the control plane holds to the policy in force.
func (h *handler) workerRule(w http.ResponseWriter, r *http.Request) {
	var in questions.WorkerRuling
	if !decode(w, r, &in) {
		return
	}
	in.Auth = authOf(r)
	record, err := h.plane.Rule(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusCreated, record)
}

func (h *handler) archiveLane(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.ArchiveLane
	if !decode(w, r, &in) {
		return
	}
	if in.LaneID == "" {
		invalid(w, "an archive needs a lane_id")
		return
	}
	if err := h.leases.ArchiveLane(r.Context(), h.Scope, r.PathValue("id"), in.LaneID, in.Force, actor); err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusAccepted, in)
}

func (h *handler) answerFleet(w http.ResponseWriter, r *http.Request) {
	workspace, err := h.fleet.Read(r.Context(), h.Scope)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.Fleet{Mode: workspace.FleetMode, Caps: workspace.Caps()})
}

func (h *handler) showFleet(w http.ResponseWriter, r *http.Request) { h.answerFleet(w, r) }

func (h *handler) setFleetMode(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.SetFleetMode
	if !decode(w, r, &in) {
		return
	}
	if err := h.fleet.SetMode(r.Context(), h.Scope, in.Mode, actor); err != nil {
		fail(w, err)
		return
	}
	h.answerFleet(w, r)
}

func (h *handler) setFleetCaps(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in ledger.Caps
	if !decode(w, r, &in) {
		return
	}
	if err := h.fleet.SetCaps(r.Context(), h.Scope, in, actor); err != nil {
		fail(w, err)
		return
	}
	h.answerFleet(w, r)
}

// resumeLane is a member's word to a work order's parked lane, which a `human_resume` term waits
// on.
func (h *handler) resumeLane(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	fired, err := h.wakes.ResumeWork(r.Context(), h.Scope, r.PathValue("id"), actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.LaneResumed{WorkOrder: r.PathValue("id"), Fired: fired})
}

// respawnLane asks for the next generation of a work order's running lane.
func (h *handler) respawnLane(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.leases.RespawnLane(r.Context(), h.Scope, r.PathValue("id"), actor); err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.LaneReset{WorkOrder: r.PathValue("id")})
}

// resetLane lets a member start the lane a work order's breaker stopped.
func (h *handler) resetLane(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	if err := h.leases.ResetLane(r.Context(), h.Scope, r.PathValue("id"), actor); err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.LaneReset{WorkOrder: r.PathValue("id")})
}

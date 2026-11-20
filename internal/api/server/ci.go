package server

import (
	"net/http"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func (h *handler) ciRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/work-orders/{id}/ci-wait", h.member(h.ciWait))
	mux.HandleFunc("POST /v1/work-orders/{id}/ci-rerun", h.member(h.ciRerun))
}

// ciWait is a member's Wait longer on a head that has not finished.
func (h *handler) ciWait(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	var in api.CIWait
	if !decode(w, r, &in) {
		return
	}
	var by time.Duration
	if in.By != "" {
		var err error
		if by, err = time.ParseDuration(in.By); err != nil || by <= 0 {
			invalid(w, "%q is not a time to wait, such as \"45m\"", in.By)
			return
		}
	}
	id := r.PathValue("id")
	watch, err := h.ci.Wait(r.Context(), h.Scope, id, by, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.CIWaited{WorkOrder: id, Watch: watch})
}

// ciRerun is a member's rerun of the checks of a head that has not finished or ended in an infrastructure failure.
func (h *handler) ciRerun(w http.ResponseWriter, r *http.Request, actor ledger.Actor) {
	id := r.PathValue("id")
	waiting, err := h.ci.Rerun(r.Context(), h.Scope, id, actor)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, http.StatusOK, api.CIRerun{WorkOrder: id, Reruns: waiting.Reruns})
}

// ciActions says what a member can do about an item of CI. Wait longer extends a timeout and a rerun runs checks
// again, so each is offered for what has them: a stalled queue needs a runner, and a finding the classification made
// of a base or of a check that waits for a person is not mended by running it again. A rerun is of a head, so a pull
// request with no head commit on record is offered none.
func ciActions(item control.CIItem) []string {
	var actions []string
	rerun := ledger.CommitHash(item.PullRequest.HeadCommit)
	switch item.Kind {
	case ledger.CIItemNotFinished:
		actions = append(actions, api.ActCIWait)
		if rerun {
			actions = append(actions, api.ActCIRerun)
		}
	case ledger.CIItemInfrastructure:
		if rerun {
			actions = append(actions, api.ActCIRerun)
		}
	}
	if ciRunURL(item) != "" {
		actions = append(actions, api.ActCIOpen)
	}
	return actions
}

// ciRunURL is the page of the run on the forge, when the forge gave an address a link can hold.
func ciRunURL(item control.CIItem) string {
	if item.Finding != nil {
		return item.Finding.Link()
	}
	return item.Watch.RunURL
}

// ciNeeds lists what CI asks of a person, as items of Needs you.
func ciNeeds(tx ports.ReadTx) ([]api.NeedItem, error) {
	items, err := control.CIItems(tx)
	if err != nil {
		return nil, err
	}
	out := make([]api.NeedItem, 0, len(items))
	for _, item := range items {
		// The finding's address is the forge's word, so what leaves here is a link or nothing, as the run's address is.
		var finding *ledger.CIFinding
		if item.Finding != nil {
			safe := *item.Finding
			safe.URL = safe.Link()
			finding = &safe
		}
		need := &api.CINeed{
			Kind: item.Kind, WorkOrder: item.WorkOrder, Number: item.PullRequest.Number, PullRequest: item.PullRequest.URL,
			Watch: item.Watch, Finding: finding, RunURL: ciRunURL(item), Actions: ciActions(item),
		}
		out = append(out, api.NeedItem{Kind: api.NeedCI, ID: item.WorkOrder.ID + ":" + item.Kind, Since: item.Since, CI: need})
	}
	return out, nil
}

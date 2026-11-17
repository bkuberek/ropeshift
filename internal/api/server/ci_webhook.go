package server

import (
	"errors"
	"io"
	"mime"
	"net/http"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
)

// ciWebhookBodyMax bounds a delivery. The events this route takes are a few kilobytes, and GitHub's own cap is far
// above this, so a body past it is no delivery of one of them.
const ciWebhookBodyMax = 1 << 20

// githubWebhook takes a GitHub webhook delivery of a CI event: a check_suite, a check_run, or a workflow_run. It is
// the one route that takes no session and no credential, and its signature, made with the key of the repository it
// names, is its authority. The request is let through the limit on the address it came from first, and every
// refusal counts against that address as a wrong passphrase does, so an address that floods the route is refused
// early. The signature's form is checked before the body is read, and the body, whose size is bounded, is read and
// then verified by the use case before it trusts a word of it. Every delivery it cannot verify is answered the same,
// and counted. A delivery that holds is a hint, which carries no state: the read decides what changed (FR-SPEND-20).
// It is reachable only where a person exposes a listener to GitHub, and polling works alone where none is.
func (h *handler) githubWebhook(w http.ResponseWriter, r *http.Request) {
	attempt, ok := beginAttempt(h.Attempts, w, r)
	if !ok {
		return
	}
	// A request that ends without a verdict counts for nothing; every refusal below is a failure.
	defer attempt.Release()
	signature := r.Header.Get("X-Hub-Signature-256")
	switch {
	case signature == "":
		h.refuseDelivery(w, attempt, control.RefusedMissingSignature)
		return
	case !control.WellFormedSignature(signature):
		h.refuseDelivery(w, attempt, control.RefusedBadSignature)
		return
	}
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		h.ciWebhooks.Count(control.RefusedContentType)
		attempt.Fail()
		refuse(w, http.StatusUnsupportedMediaType, api.ErrorDetail{Code: api.CodeInvalid, Message: "a webhook delivery is application/json"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, ciWebhookBodyMax))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		h.ciWebhooks.Count(control.RefusedOversized)
		attempt.Fail()
		refuse(w, http.StatusRequestEntityTooLarge, api.ErrorDetail{Code: api.CodeInvalid, Message: "a webhook delivery is under a megabyte"})
		return
	case err != nil:
		attempt.Fail()
		invalid(w, "the delivery's body could not be read")
		return
	}
	outcome, err := h.ciWebhooks.Receive(r.Context(), control.CIDelivery{
		Event: r.Header.Get("X-GitHub-Event"), ID: r.Header.Get("X-GitHub-Delivery"), Signature: signature,
		EnterpriseHost: r.Header.Get("X-GitHub-Enterprise-Host"), Body: body,
	})
	var refused *control.CIRefusal
	switch {
	case errors.As(err, &refused) && refused.Malformed:
		attempt.Fail()
		invalid(w, "a delivery names its event and its ID")
	case errors.As(err, &refused):
		attempt.Fail()
		refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeBadSignature, Message: "this delivery's signature is not accepted"})
	case err != nil:
		fail(w, err)
	case outcome == control.CIAccepted:
		reply(w, http.StatusAccepted, map[string]string{"status": string(outcome)})
	default:
		reply(w, http.StatusOK, map[string]string{"status": string(outcome)})
	}
}

// refuseDelivery counts a delivery refused before its body was read, counts it against its address, and answers it as
// every refusal is answered.
func (h *handler) refuseDelivery(w http.ResponseWriter, attempt *Attempt, reason string) {
	h.ciWebhooks.Count(reason)
	attempt.Fail()
	refuse(w, http.StatusForbidden, api.ErrorDetail{Code: api.CodeBadSignature, Message: "this delivery's signature is not accepted"})
}

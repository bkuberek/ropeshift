package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// recovering is a world with the three things a member recovers: failed work, a claim to give back, and a
// lane that ended with work no remote holds.
func recovering(t *testing.T) (*webRig, *apitest.World, workorders.Grant) {
	t.Helper()
	d := apitest.Start(t)
	r := webOn(t, d)
	w := apitest.NewWorld(t, d)
	w.Running("wo-failed", "Try the flaky call").End(workorders.OutcomeFailed, workorders.ReasonCrashed)
	lane := w.Running("wo-ended", "Write the guide")
	lane.End(workorders.OutcomeCompleted, "")
	lane.Upload(ledger.UploadLaneGuarded, ledger.LaneGuard{UnpushedWork: true})
	w.Ready("wo-claimed", "Wait for a lane")
	grant, err := d.Client.Claim(t.Context(), "wo-claimed", api.ClaimRequest{ClaimID: "claim-held", WorkerID: apitest.WorldWorker})
	if err != nil {
		t.Fatal(err)
	}
	return r, w, workorders.Grant{WorkOrder: "wo-claimed", ClaimID: grant.ClaimID, Fence: grant.Fence}
}

// FR-UI-7: the browser retries failed work, gives a claim back, and removes the worktree of a lane that
// ended, through the same routes the command line calls. Each goes through the session and the token the
// listener holds every change to, and a delegate gains nothing, because the listener answers it with a refusal
// before any route runs.
func TestAMemberRecoversWorkThroughTheFrontDoor(t *testing.T) {
	t.Parallel()
	r, _, grant := recovering(t)
	cookies, token := r.signedIn(t)
	headers := map[string]string{api.HeaderCSRF: token, "Origin": "http://" + r.web.Host}
	send := func(path string, body any) (*http.Response, []byte) {
		return r.send(t, call{method: http.MethodPost, path: apitest.Route(path), body: body, cookies: cookies, headers: headers})
	}
	state := func(id string) workorders.State {
		order, err := r.d.Client.WorkOrder(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return order.State
	}

	response, raw := send("/v1/work-orders/wo-failed/retry", api.Retry{Reason: "The network came back."})
	wantStatus(t, response, raw, http.StatusOK, "")
	if got := state("wo-failed"); got != workorders.Ready {
		t.Errorf("a retry left the work %s", got)
	}

	response, raw = send("/v1/work-orders/wo-claimed/claims/"+grant.ClaimID+"/release", api.ReleaseRequest{Fence: grant.Fence})
	wantStatus(t, response, raw, http.StatusNoContent, "")
	if got := state("wo-claimed"); got != workorders.Ready {
		t.Errorf("a release left the work %s", got)
	}

	// The plain archive is refused with the reason, which the page shows first, and a forced one goes through.
	response, raw = send("/v1/work-orders/wo-ended/lane-archive", api.ArchiveLane{LaneID: "lane-wo-ended"})
	wantStatus(t, response, raw, http.StatusConflict, api.CodeUnpushedWork)
	response, raw = send("/v1/work-orders/wo-ended/lane-archive", api.ArchiveLane{LaneID: "lane-wo-ended", Force: true})
	wantStatus(t, response, raw, http.StatusAccepted, "")
}

// ATTACK: a page that holds no token, or another page's, cannot recover work, and a request that names a
// delegate is refused whatever route it asks for.
func TestRecoveringWorkTakesTheTokenAndNoDelegate(t *testing.T) {
	t.Parallel()
	r, _, grant := recovering(t)
	cookies, token := r.signedIn(t)
	paths := map[string]any{
		"/v1/work-orders/wo-failed/retry":                                 api.Retry{Reason: "again"},
		"/v1/work-orders/wo-claimed/claims/" + grant.ClaimID + "/release": api.ReleaseRequest{Fence: grant.Fence},
		"/v1/work-orders/wo-ended/lane-archive":                           api.ArchiveLane{LaneID: "lane-wo-ended", Force: true},
	}
	for path, body := range paths {
		t.Run(path, func(t *testing.T) {
			response, raw := r.send(t, call{method: http.MethodPost, path: apitest.Route(path), body: body, cookies: cookies, headers: map[string]string{"Origin": "http://" + r.web.Host}})
			wantStatus(t, response, raw, http.StatusForbidden, api.CodeCSRF)
			response, raw = r.send(t, call{method: http.MethodPost, path: apitest.Route(path), body: body, cookies: cookies, headers: map[string]string{
				api.HeaderCSRF: token, "Origin": "http://" + r.web.Host, api.HeaderDelegate: "delegate-1",
			}})
			wantStatus(t, response, raw, http.StatusForbidden, api.CodeForbidden)
		})
	}
	for _, id := range []string{"wo-failed", "wo-claimed", "wo-ended"} {
		order, err := r.d.Client.WorkOrder(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if id == "wo-failed" && order.State != workorders.Failed || id == "wo-claimed" && order.State != workorders.Claimed {
			t.Errorf("%s moved to %s", id, order.State)
		}
	}
}

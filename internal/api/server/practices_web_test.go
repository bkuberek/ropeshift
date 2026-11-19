package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/apitest"
)

// webPractices serves a project whose repository holds an open finding, through the web listener.
func webPractices(t *testing.T) (*webRig, string) {
	t.Helper()
	d := practicesDaemon(t, map[string][]string{"plan": {"repo-a"}}, "plan")
	connectWorker(t, d, "plan", ledger.ProtocolVersion)
	d.HoldClone(t, "repo-a", "widgets")
	finding := practices.FindingID(practices.Codeowners)
	keepResult(t, d, "request-1", practices.Result{
		Repository: "repo-a", Commit: "1111111111111111111111111111111111111111", BaseBranch: "main", Worker: "plan",
		CheckedAt: time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC), TableVersion: practices.TableVersion, RulesVersion: ledger.CredentialRulesVersion,
		Practices: []practices.PracticeStatus{{ID: practices.Codeowners, Status: practices.StatusFinding}},
		Findings:  []practices.Finding{{ID: finding, Practice: practices.Codeowners, Severity: practices.Low, Detail: "no CODEOWNERS file"}},
	})
	return webOn(t, d), finding
}

// A member in the browser reads the practices, asks for a check, acknowledges a finding with the reason, and withdraws
// the acknowledgement, each change through the web listener with its session and its token.
func TestAMemberActsOnPracticesThroughTheWebListener(t *testing.T) {
	t.Parallel()
	r, finding := webPractices(t)
	cookies, token := r.signedIn(t)
	headers := map[string]string{api.HeaderCSRF: token, "Origin": "http://" + r.web.Host}

	response, raw := r.send(t, call{path: apitest.Route(api.PracticesPath), cookies: cookies})
	wantStatus(t, response, raw, http.StatusOK, "")
	var list api.PracticesList
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if len(list.Repositories) != 1 || list.Repositories[0].Result == nil || len(list.Repositories[0].Result.Findings) != 1 {
		t.Fatalf("the read held %s", raw)
	}

	response, raw = r.send(t, call{method: http.MethodPost, path: apitest.Route(api.PracticesChecksPath), body: api.PracticesCheck{Repositories: []string{}}, cookies: cookies, headers: headers})
	wantStatus(t, response, raw, http.StatusAccepted, "")
	var checked api.PracticesChecked
	if err := json.Unmarshal(raw, &checked); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if len(checked.Checks) != 1 || checked.Checks[0].Request == nil {
		t.Fatalf("the check was answered with %s", raw)
	}
	response, raw = r.send(t, call{path: apitest.Route(api.PracticesChecksPath + "/" + checked.Checks[0].Request.ID), cookies: cookies})
	wantStatus(t, response, raw, http.StatusOK, "")

	acknowledge := call{method: http.MethodPost, path: apitest.Route(api.PracticesAcknowledgementsPath), body: api.PracticesAcknowledge{Repository: "widgets", Finding: finding, Reason: "the owners are the whole team"}, cookies: cookies}
	acknowledge.headers = headers
	response, raw = r.send(t, acknowledge)
	wantStatus(t, response, raw, http.StatusCreated, "")
	var kept api.PracticesAcknowledgement
	if err := json.Unmarshal(raw, &kept); err != nil || kept.Finding != finding || !strings.Contains(kept.Reason, "whole team") {
		t.Fatalf("the acknowledgement was %s (%v)", raw, err)
	}

	withdraw := call{method: http.MethodPost, path: apitest.Route(api.PracticesWithdrawPath), body: api.PracticesWithdraw{Repository: "widgets", Finding: finding}, cookies: cookies, headers: headers}
	response, raw = r.send(t, withdraw)
	wantStatus(t, response, raw, http.StatusOK, "")
}

// ATTACK: a request from another page that carries the member's cookies and no token the page can read must not
// acknowledge a finding, withdraw an acknowledgement, or ask for a check.
func TestAForgedPracticesChangeIsRefused(t *testing.T) {
	t.Parallel()
	r, finding := webPractices(t)
	cookies, token := r.signedIn(t)
	changes := map[string]call{
		"acknowledge": {method: http.MethodPost, path: apitest.Route(api.PracticesAcknowledgementsPath), body: api.PracticesAcknowledge{Repository: "widgets", Finding: finding, Reason: "forged"}},
		"withdraw":    {method: http.MethodPost, path: apitest.Route(api.PracticesWithdrawPath), body: api.PracticesWithdraw{Repository: "widgets", Finding: finding}},
		"check":       {method: http.MethodPost, path: apitest.Route(api.PracticesChecksPath), body: api.PracticesCheck{Repositories: []string{}}},
	}
	forged := map[string]map[string]string{
		"no token":    {"Origin": "http://" + r.web.Host},
		"wrong token": {api.HeaderCSRF: strings.Repeat("0", len(token)), "Origin": "http://" + r.web.Host},
	}
	for name, change := range changes {
		for how, headers := range forged {
			t.Run(name+", "+how, func(t *testing.T) {
				change.cookies, change.headers = cookies, headers
				response, raw := r.send(t, change)
				wantStatus(t, response, raw, http.StatusForbidden, api.CodeCSRF)
			})
		}
	}
	response, raw := r.send(t, call{path: apitest.Route(api.PracticesPath), cookies: cookies})
	wantStatus(t, response, raw, http.StatusOK, "")
	var list api.PracticesList
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if len(list.Repositories[0].Acknowledgement) != 0 || list.Repositories[0].Request != nil {
		t.Fatalf("a forged request changed the practices: %s", raw)
	}
}

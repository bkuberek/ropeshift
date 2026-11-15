package server_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// The questions, decisions, and categories a member records from the browser are the API's own routes,
// behind the web listener's session and token (FR-UI-7). These hold what the web UI relies on: a member's
// session does each, the listener's checks stand in front of each, and a delegate gains nothing, because
// the class table and the decision rules are the handlers' and the listener adds a door, not a rule.

// browser is a member signed in to the web listener, who sends what a page of it would.
type browser struct {
	r       *webRig
	cookies []*http.Cookie
	headers map[string]string
}

func signedInBrowser(t *testing.T) browser {
	t.Helper()
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	return browser{r: r, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token, "Origin": "http://" + r.web.Host}}
}

// send makes one request of the project's routes and says what came back.
func (b browser) send(t *testing.T, method, path string, body any, status int) []byte {
	t.Helper()
	response, raw := b.r.send(t, call{method: method, path: apitest.Route(path), body: body, cookies: b.cookies, headers: b.headers})
	if response.StatusCode != status {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, response.StatusCode, status, raw)
	}
	return raw
}

// wantCode reads the code of a refusal.
func wantCode(t *testing.T, raw []byte, code string) {
	t.Helper()
	var body api.ErrorBody
	if err := json.Unmarshal(raw, &body); err != nil || body.Error.Code != code {
		t.Fatalf("refusal %s (%v), want code %q", raw, err, code)
	}
}

func TestAMemberAddsACategoryFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	b.send(t, http.MethodPost, "/v1/categories", api.AddCategory{Name: "billing"}, http.StatusNoContent)
	// A repeat is done, as it is from the command line.
	b.send(t, http.MethodPost, "/v1/categories", api.AddCategory{Name: "billing"}, http.StatusNoContent)
	names, err := b.r.d.Client.Categories(t.Context())
	if err != nil || !slices.Contains(names, "billing") {
		t.Fatalf("the categories read %v: %v", names, err)
	}
	// The ledger refuses a name that is not lowercase words, and the page shows its words.
	raw := b.send(t, http.MethodPost, "/v1/categories", api.AddCategory{Name: "Billing Team"}, http.StatusBadRequest)
	wantCode(t, raw, api.CodeInvalid)
}

func TestAMemberRecordsAndDismissesAQuestionFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	b.send(t, http.MethodPost, "/v1/categories", api.AddCategory{Name: "billing"}, http.StatusNoContent)
	raw := b.send(t, http.MethodPost, "/v1/questions", api.RecordQuestion{
		ID: "q-web", Text: "Which currency do invoices use?", Categories: []string{"billing", questions.CategoryProduct}, SubjectKind: "plan", SubjectID: "plan-1",
	}, http.StatusCreated)
	var recorded questions.Question
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Status != questions.StatusOpen || recorded.AskedBy != memberOf(t, b.r.d) || recorded.SubjectID != "plan-1" {
		t.Fatalf("recorded %+v", recorded)
	}
	// A repeat under one ID is the record the first made, so a page may send it again after a lost reply.
	b.send(t, http.MethodPost, "/v1/questions", api.RecordQuestion{ID: "q-web", Text: "Which currency do invoices use?"}, http.StatusCreated)
	b.send(t, http.MethodPost, "/v1/questions/q-web/dismiss", struct{}{}, http.StatusNoContent)
	read, err := b.r.d.Client.Question(t.Context(), "q-web")
	if err != nil || read.Status != questions.StatusDismissed {
		t.Fatalf("the question reads %+v: %v", read, err)
	}
	// A question that is closed is not dismissed again, and the page shows why.
	wantCode(t, b.send(t, http.MethodPost, "/v1/questions/q-web/dismiss", struct{}{}, http.StatusBadRequest), api.CodeInvalid)
}

func TestTheLedgerRefusesAQuestionTheBrowserSendsMalformed(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	for name, request := range map[string]api.RecordQuestion{
		"no text":                     {ID: "q-1"},
		"an ID with space":            {ID: "q 1", Text: "Which?"},
		"a category it does not know": {ID: "q-3", Text: "Which?", Categories: []string{"no-such-category"}},
		"a subject of no kind":        {ID: "q-4", Text: "Which?", SubjectKind: "teleport", SubjectID: "x"},
		"a subject with a bad ID":     {ID: "q-5", Text: "Which?", SubjectKind: "plan", SubjectID: "has space"},
	} {
		t.Run(name, func(t *testing.T) {
			wantCode(t, b.send(t, http.MethodPost, "/v1/questions", request, http.StatusBadRequest), api.CodeInvalid)
		})
	}
	rows, err := b.r.d.Client.Questions(t.Context(), api.RecordFilter{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("a refused question left %+v: %v", rows, err)
	}
}

// ATTACK: a delegate's credential on the web listener. A delegate uses the local socket, where its call is
// a card or is refused by its class, so the listener refuses it before any route reads it, and a route that
// the web UI gains gives it nothing.
func TestADelegateGainsNothingFromTheBrowserRoutesOfRecords(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	created := aDelegate(t, b.r.d.Client.Client, apitest.ProjectName)
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/v1/categories"},
		{http.MethodPost, "/v1/questions"},
		{http.MethodPost, "/v1/questions/x/dismiss"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			headers := map[string]string{api.HeaderDelegate: created.Token, api.HeaderCSRF: b.headers[api.HeaderCSRF]}
			response, raw := b.r.send(t, call{method: route.method, path: apitest.Route(route.path), body: struct{}{}, cookies: b.cookies, headers: headers})
			wantStatus(t, response, raw, http.StatusForbidden, api.CodeForbidden)
		})
	}
	names, err := b.r.d.Client.Categories(t.Context())
	if err != nil || slices.Contains(names, "billing") {
		t.Fatalf("the categories read %v: %v", names, err)
	}
}

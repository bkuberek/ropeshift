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

func TestAMemberRecordsDecisionsFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	member := memberOf(t, b.r.d)
	read := func(id string) questions.Decision {
		t.Helper()
		row, err := b.r.d.Client.Decision(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return row.Decision
	}
	b.r.d.HoldRepository(t, "billing")
	raw := b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{
		ID: "d-web-1", Status: questions.DecisionDecided, Statement: "Charge in euros.", Rationale: "Every customer is in the union.", Scope: "repo:billing",
		Reversibility: questions.OneWay, Categories: []string{questions.CategoryProduct}, Citations: []string{"requirement:FR-BILL-1"},
	}, http.StatusCreated)
	var recorded questions.Decision
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	if got := read("d-web-1"); got.Status != questions.DecisionDecided || got.DecidedBy != member || got.Reversibility != questions.OneWay || recorded.ID != "d-web-1" {
		t.Fatalf("the decision reads %+v", got)
	}
	// An assumption is a decision of the other status, and a member's own word runs past the policy a lane is held to.
	b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{ID: "d-web-2", Status: questions.DecisionAssumed, Statement: "Prices are in euros.", Scope: "repo:billing", Reversibility: questions.Reversible}, http.StatusCreated)
	if got := read("d-web-2"); got.Status != questions.DecisionAssumed || !slices.Contains(got.Overlaps, "d-web-1") {
		t.Fatalf("the assumption reads %+v, and it should name the decision it overlaps", got)
	}
	// A repeat under one ID is the record the first made, so a page may send it again after a lost reply.
	b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{ID: "d-web-2", Status: questions.DecisionAssumed, Statement: "Prices are in euros.", Scope: "repo:billing", Reversibility: questions.Reversible}, http.StatusCreated)
	// A decision replaces one in force with no rework, and one that ended cannot be replaced again.
	b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{ID: "d-web-3", Status: questions.DecisionDecided, Statement: "Prices are in dollars.", Supersedes: "d-web-2", Reversibility: questions.Reversible}, http.StatusCreated)
	if got := read("d-web-2"); got.Status != questions.DecisionReplaced {
		t.Fatalf("the replaced assumption reads %+v", got)
	}
	wantCode(t, b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{ID: "d-web-4", Status: questions.DecisionDecided, Statement: "Again.", Supersedes: "d-web-2"}, http.StatusConflict), api.CodeDecisionEnded)
	// What the ledger would not take for its form is refused in its words.
	for name, request := range map[string]api.RecordDecision{
		"no statement":               {ID: "d-web-5", Status: questions.DecisionDecided},
		"a status it never opens":    {ID: "d-web-6", Status: questions.DecisionOverturned, Statement: "x"},
		"a reversibility it lacks":   {ID: "d-web-7", Status: questions.DecisionDecided, Statement: "x", Reversibility: "sometimes"},
		"an unknown category":        {ID: "d-web-8", Status: questions.DecisionDecided, Statement: "x", Categories: []string{"no-such-category"}},
		"a bare ID as scope":         {ID: "d-web-9", Status: questions.DecisionDecided, Statement: "x", Scope: "billing"},
		"a scope of no kind":         {ID: "d-web-10", Status: questions.DecisionDecided, Statement: "x", Scope: "team:billing"},
		"a scope with a path":        {ID: "d-web-11", Status: questions.DecisionDecided, Statement: "x", Scope: "repo:billing/eu"},
		"a scope that names nothing": {ID: "d-web-12", Status: questions.DecisionDecided, Statement: "x", Scope: "wo:ghost"},
	} {
		t.Run(name, func(t *testing.T) {
			wantCode(t, b.send(t, http.MethodPost, "/v1/decisions", request, http.StatusBadRequest), api.CodeInvalid)
		})
	}
}

func TestAMemberOverturnsADecisionFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{ID: "d-web-1", Status: questions.DecisionAssumed, Statement: "Prices are in euros.", Reversibility: questions.Reversible}, http.StatusCreated)
	body := api.OverturnDecision{ID: "d-web-2", Statement: "Prices are in dollars.", Rationale: "Most customers pay in dollars.", Citations: []string{"docs/billing.md"}}
	raw := b.send(t, http.MethodPost, "/v1/decisions/d-web-1/overturn", body, http.StatusCreated)
	var overturned questions.Overturned
	if err := json.Unmarshal(raw, &overturned); err != nil {
		t.Fatal(err)
	}
	if overturned.Old.ID != "d-web-1" || overturned.Old.Status != questions.DecisionOverturned || overturned.New.ID != "d-web-2" || overturned.New.Supersedes != "d-web-1" || overturned.New.DecidedBy != memberOf(t, b.r.d) {
		t.Fatalf("overturned %+v", overturned)
	}
	// A repeat under one ID is what the first made, so a page may send it again after a lost reply.
	b.send(t, http.MethodPost, "/v1/decisions/d-web-1/overturn", body, http.StatusCreated)
	// A decision that was overturned is not overturned again.
	wantCode(t, b.send(t, http.MethodPost, "/v1/decisions/d-web-1/overturn", api.OverturnDecision{ID: "d-web-3", Statement: "Again."}, http.StatusConflict), api.CodeDecisionEnded)
	wantCode(t, b.send(t, http.MethodPost, "/v1/decisions/d-web-2/overturn", api.OverturnDecision{ID: "d-web-4"}, http.StatusBadRequest), api.CodeInvalid)
	wantCode(t, b.send(t, http.MethodPost, "/v1/decisions/no-such-decision/overturn", api.OverturnDecision{ID: "d-web-5", Statement: "x"}, http.StatusNotFound), api.CodeNotFound)
}

func TestAMemberSearchesForARecordToCiteFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{ID: "d-web-1", Status: questions.DecisionDecided, Statement: "The retry budget is three calls."}, http.StatusCreated)
	raw := b.send(t, http.MethodGet, "/v1/candidates?text=What+is+the+retry+budget%3F&kinds=decision,requirement,contract,question&limit=8", nil, http.StatusOK)
	var found api.CandidateList
	if err := json.Unmarshal(raw, &found); err != nil {
		t.Fatal(err)
	}
	if len(found.Candidates) != 1 || found.Candidates[0].RecordID != "d-web-1" || found.Candidates[0].Kind != questions.KindDecision {
		t.Fatalf("candidates %+v", found.Candidates)
	}
	// A search names the kinds it reads, as it does on the socket.
	wantCode(t, b.send(t, http.MethodGet, "/v1/candidates?text=retry", nil, http.StatusBadRequest), api.CodeInvalid)
}

// ATTACK: an ID plan approval derives, which anyone who reads a plan can work out. Only plan approval names a
// decision under that prefix, so a decision recorded from the browser is refused it, as it is on the socket,
// and no decision of that ID exists afterwards.
func TestAPlanApprovalsDecisionIDIsRefusedFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	for _, id := range []string{"pd-", "pd-0123456789abcdef0123456789", "pd-x"} {
		raw := b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{ID: id, Status: questions.DecisionDecided, Statement: "Take the name."}, http.StatusBadRequest)
		wantCode(t, raw, api.CodeInvalid)
		_, err := b.r.d.Client.Decision(t.Context(), id)
		refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	}
	// A decision that takes the place of another, by an overturn, is named by its ID as well, and the decision it
	// would have ended stands.
	b.send(t, http.MethodPost, "/v1/decisions", api.RecordDecision{ID: "d-web-1", Status: questions.DecisionDecided, Statement: "Prices are in euros."}, http.StatusCreated)
	raw := b.send(t, http.MethodPost, "/v1/decisions/d-web-1/overturn", api.OverturnDecision{ID: "pd-0123456789abcdef", Statement: "Take the name."}, http.StatusBadRequest)
	wantCode(t, raw, api.CodeInvalid)
	if got, err := b.r.d.Client.Decision(t.Context(), "d-web-1"); err != nil || got.Status != questions.DecisionDecided {
		t.Fatalf("the refused overturn left %+v: %v", got, err)
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
		{http.MethodPost, "/v1/decisions"},
		{http.MethodPost, "/v1/decisions/x/overturn"},
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

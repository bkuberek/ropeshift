package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// The network listener is a group server's one TCP listener (ADR-0031). It serves the web UI, the API,
// and the worker plane, and no route of it trusts the transport: a request proves itself with a worker's
// credential, a join token, a member's session as a bearer token, or the web UI's session cookie with its
// CSRF token. These tests hold the list of what is open, and walk every route there is with no credential.

// openRoutes is every route the network listener serves with no credential of any kind. A route added to
// this list is a route anyone who reaches the listener can call, so it changes here, in a review that reads it.
var openRoutes = []string{
	"GET /",
	"HEAD /",
	"POST /v1/sessions",
}

func networkOn(t *testing.T) *webRig {
	t.Helper()
	d := apitest.Start(t)
	return &webRig{d: d, web: d.ServeNetwork(t, builtAssets, 20*time.Millisecond, nil)}
}

func TestTheNetworkListenerOpensOnlyWhatTheTestNames(t *testing.T) {
	t.Parallel()
	var listed []string
	for _, route := range server.OpenRoutes() {
		listed = append(listed, route.Method+" "+route.Pattern)
		if route.Why == "" {
			t.Errorf("%s %s says nothing of why it is open", route.Method, route.Pattern)
		}
	}
	if !slices.Equal(listed, openRoutes) {
		t.Errorf("the listener opens %v, and the test names %v", listed, openRoutes)
	}
}

// substitute fills a pattern's wildcards with a value that names nothing.
var wildcard = regexp.MustCompile(`\{[^}]*\}`)

func pathOf(pattern string) string {
	return wildcard.ReplaceAllStringFunc(pattern, func(name string) string {
		if name == "{project}" {
			return "default"
		}
		return "nothing"
	})
}

// walked is every route the walk calls: each row of the web listener's closed list, each of the plane's,
// and the routes of the API no list names, which a member's session reaches and nothing else may.
func walked() []api.WebRoute {
	var routes []api.WebRoute
	routes = append(routes, api.WebRoutes()...)
	for _, route := range server.PlaneRoutes() {
		routes = append(routes, api.WebRoute{Method: route.Method, Pattern: route.Pattern})
	}
	for _, other := range []string{
		"GET /v1/passphrase", "PUT /v1/passphrase", "GET /v1/sessions", "POST /v1/sessions/end", "POST /v1/sessions/end-all",
		"GET /v1/health", "GET /v1/members", "GET /v1/workers", "POST /v1/workers/tokens", "GET /v1/lanes", "GET /v1/lanes/nothing",
		"GET /v1/fleet", "PUT /v1/fleet/mode", "GET /v1/approvals", "POST /v1/work-orders/nothing/claims", "POST /v1/work-orders/nothing/writes",
		"GET /v1/sources", "GET /v1/delegates", "POST /v1/delegates", "GET /v1/operations", "GET /v1/audit", "GET /v1/backup",
		"POST /v1/backup", "GET /v1/projects/default/sources", "POST /v1/projects/default/work-orders/nothing/writes",
		"POST /v1/projects/default/work-orders/nothing/claims", "GET /v1/projects/default/lanes", "GET /v1/projects/default/fleet",
		"DELETE /v1/workers/nothing", "PATCH /v1/projects", "GET /v1", "GET /v1/", "GET /v1/nothing",
		"POST /v1/nothing", "GET /v1/projects", "GET /v1/projects/", "GET /v1/projects/nothing/nothing", "GET /v1//workers",
		"GET /v1/workers/nothing/../tokens", "GET /v1/%2e%2e/%2e%2e/v1/health", "OPTIONS /v1/health", "TRACE /v1/health", "HEAD /v1/health",
		"GET /v1/health?x=1", "GET /v1/projects/default/events", "GET /v1/projects/default/lanes/nothing/pane",
	} {
		method, path, _ := strings.Cut(other, " ")
		routes = append(routes, api.WebRoute{Method: method, Pattern: path})
	}
	return routes
}

// With no credential every route of the listener is refused, except the ones it opens: the walk is the
// table of the listener's routes, and no route of it trusts the transport.
func TestEveryRouteOfTheNetworkListenerRefusesARequestWithNoCredential(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	opens := map[string]bool{"POST /v1/sessions": true}
	count := 0
	for _, route := range walked() {
		path := pathOf(route.Pattern)
		key := route.Method + " " + route.Pattern
		t.Run(key, func(t *testing.T) {
			response, raw := r.send(t, call{method: route.Method, path: path})
			count++
			if opens[key] {
				if response.StatusCode == http.StatusUnauthorized {
					t.Errorf("the open route %s refused: %s", key, raw)
				}
				return
			}
			if route.Method == http.MethodHead || route.Method == http.MethodOptions || route.Method == "TRACE" {
				if response.StatusCode != http.StatusUnauthorized {
					t.Errorf("%s answered %d with no credential", key, response.StatusCode)
				}
				return
			}
			var body api.ErrorBody
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("%s answered %d with %q, which is no refusal of the API", key, response.StatusCode, raw)
			}
			if response.StatusCode != http.StatusUnauthorized || !slices.Contains([]string{api.CodeSessionRequired, api.CodeBadCredential, api.CodeBadJoinToken}, body.Error.Code) {
				t.Errorf("%s answered %d %s with no credential", key, response.StatusCode, body.Error.Code)
			}
		})
	}
	if count < 120 {
		t.Errorf("the walk called %d routes, so it reads too little of the table", count)
	}
}

// A path that is not the API's takes no credential, because it is the application's own file, and only a
// file: nothing else of the listener answers outside /v1.
func TestOnlyTheApplicationsFilesAreOpenOutsideTheAPI(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	for _, path := range []string{"/", "/lanes/some-lane", "/assets/app-abc123.js", "/assets/app-abc123.css", "/missing.css"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			response, raw := r.send(t, call{method: method, path: path})
			if response.StatusCode == http.StatusUnauthorized || response.StatusCode >= http.StatusInternalServerError {
				t.Errorf("%s %s answered %d: %s", method, path, response.StatusCode, raw)
			}
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		response, raw := r.send(t, call{method: method, path: "/", body: struct{}{}})
		wantStatus(t, response, raw, http.StatusMethodNotAllowed, "")
	}
}

// Only a request addressed to one of the listener's own names is answered, as the web listener holds.
func TestTheNetworkListenerAnswersOnlyItsOwnNames(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	response, raw := r.send(t, call{path: "/", headers: map[string]string{"Host": "attacker.example"}})
	wantStatus(t, response, raw, http.StatusForbidden, api.CodeForeignHost)
}

// A member's session reaches the API as a bearer token, and a wrong one reaches nothing.
func TestAMembersSessionReachesTheAPIAsABearerToken(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	response, raw := r.send(t, call{path: "/v1/health", headers: map[string]string{api.HeaderSession: r.d.Session}})
	wantStatus(t, response, raw, http.StatusOK, "")
	response, raw = r.send(t, call{path: "/v1/members", headers: map[string]string{api.HeaderSession: r.d.Session}})
	wantStatus(t, response, raw, http.StatusOK, "")
	for _, wrong := range []string{"nothing", "a-session-that-was-never-made", strings.Repeat("x", 4096)} {
		for _, route := range walked() {
			response, raw := r.send(t, call{method: route.Method, path: pathOf(route.Pattern), headers: map[string]string{api.HeaderSession: wrong}})
			if route.Pattern == api.SignInPath && route.Method == http.MethodPost {
				continue
			}
			if response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden {
				t.Fatalf("%s %s with a session that is none answered %d: %s", route.Method, route.Pattern, response.StatusCode, raw)
			}
		}
	}
}

// A session that ended opens nothing, whichever way it reached the listener.
func TestASessionThatEndedOpensNothingOverTheNetwork(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	session, err := r.d.Client.Client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	response, raw := r.send(t, call{path: "/v1/health", headers: map[string]string{api.HeaderSession: session.Token}})
	wantStatus(t, response, raw, http.StatusOK, "")
	ending := r.d.Client.Client.WithCredential("")
	ending.UseSession(session.Token)
	if err := ending.SignOut(t.Context()); err != nil {
		t.Fatal(err)
	}
	response, raw = r.send(t, call{path: "/v1/health", headers: map[string]string{api.HeaderSession: session.Token}})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
}

// The web UI signs in through the open route, and from then on its cookie and its CSRF token open the
// routes of its closed list and nothing else.
func TestTheWebSessionCookieOpensOnlyTheWebUIsRoutes(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	cookies, token := r.signedIn(t)
	response, raw := r.send(t, call{path: api.ProjectsPath, cookies: cookies})
	wantStatus(t, response, raw, http.StatusOK, "")
	// A change with no CSRF token is the cross-site request the cookie alone would let through.
	response, raw = r.send(t, call{method: http.MethodPost, path: api.SignOutPath, body: struct{}{}, cookies: cookies})
	wantStatus(t, response, raw, http.StatusForbidden, api.CodeCSRF)
	// A route the web UI does not serve stays closed to its cookie.
	for _, path := range []string{api.PassphrasePath, "/v1/workers/nothing/heartbeat", "/v1/work-orders/nothing/claims"} {
		response, raw = r.send(t, call{method: http.MethodPost, path: path, body: struct{}{}, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}})
		if response.StatusCode < 400 {
			t.Errorf("%s answered %d to the web UI's cookie: %s", path, response.StatusCode, raw)
		}
	}
	response, raw = r.send(t, call{path: "/v1/workers/nothing/watch", cookies: cookies})
	if response.StatusCode < 400 {
		t.Errorf("a worker route answered %d to the web UI's cookie: %s", response.StatusCode, raw)
	}
}

// A request carries one credential. Two of them leave no way to say which the caller means, and the
// listener never picks the more generous one.
func TestARequestCarriesOneCredential(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	cookies, _ := r.signedIn(t)
	pairs := []map[string]string{
		{api.HeaderSession: r.d.Session, "Authorization": "Bearer a-credential"},
	}
	for _, headers := range pairs {
		response, raw := r.send(t, call{path: "/v1/health", headers: headers})
		wantStatus(t, response, raw, http.StatusBadRequest, api.CodeInvalid)
	}
	response, raw := r.send(t, call{path: "/v1/health", cookies: cookies, headers: map[string]string{api.HeaderSession: r.d.Session}})
	wantStatus(t, response, raw, http.StatusBadRequest, api.CodeInvalid)
	response, raw = r.send(t, call{path: "/v1/workers/nothing/desired?since=0", cookies: cookies, headers: map[string]string{"Authorization": "Bearer a-credential"}})
	wantStatus(t, response, raw, http.StatusBadRequest, api.CodeInvalid)
}

// A delegate acts on the socket of the machine it was made on, and never over a network.
func TestADelegateNeverReachesTheNetworkListener(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	for _, headers := range []map[string]string{
		{api.HeaderDelegate: "a-delegate-credential"},
		{api.HeaderDelegate: "a-delegate-credential", api.HeaderSession: r.d.Session},
	} {
		response, raw := r.send(t, call{path: "/v1/health", headers: headers})
		wantStatus(t, response, raw, http.StatusForbidden, api.CodeForbidden)
	}
}

// registered is a worker that joined through the listener, with the credential it holds.
func (r *webRig) registered(t *testing.T, id string) string {
	t.Helper()
	plane := control.NewWorkers(r.d.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})
	token, err := plane.CreateJoinToken(t.Context(), ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	response, raw := r.send(t, call{method: http.MethodPost, path: "/v1/workers/register", body: ledger.Registration{JoinToken: token, WorkerID: id, Name: id}})
	wantStatus(t, response, raw, http.StatusOK, "")
	var credential ledger.WorkerCredential
	if err := json.Unmarshal(raw, &credential); err != nil || credential.Credential == "" {
		t.Fatalf("%v: %s", err, raw)
	}
	return credential.Credential
}

// A worker joins with a join token and then holds a credential. Its credential opens the plane's routes for
// it and no route that is a member's, whether the credential is its own or any other string.
func TestAWorkersCredentialOpensOnlyThePlane(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	credential := r.registered(t, "worker-a")
	planeRoutes := map[string]bool{}
	for _, route := range server.PlaneRoutes() {
		planeRoutes[route.Method+" "+route.Pattern] = true
	}
	for _, bearer := range []string{credential, "not-a-credential"} {
		for _, route := range walked() {
			key := route.Method + " " + route.Pattern
			if key == "POST /v1/workers/register" {
				continue
			}
			response, raw := r.send(t, call{method: route.Method, path: pathOf(route.Pattern), headers: map[string]string{"Authorization": "Bearer " + bearer}})
			if planeRoutes[key] {
				// The listener hands it to the use case, which answers for a credential that is not the worker's.
				if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound && !strings.Contains(string(raw), "not_found") {
					t.Errorf("%s answered %d to a worker credential: %s", key, response.StatusCode, raw)
				}
				continue
			}
			if route.Method == http.MethodHead || route.Method == http.MethodOptions || route.Method == "TRACE" {
				if response.StatusCode < 400 {
					t.Errorf("%s answered %d to a worker credential", key, response.StatusCode)
				}
				continue
			}
			wantStatus(t, response, raw, http.StatusForbidden, api.CodeForbidden)
		}
	}
}

// The worker's own credential carries a heartbeat through the listener, and another worker's carries none of
// this worker's.
func TestAWorkersCredentialCarriesItsPlaneCalls(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	credential := r.registered(t, "worker-b")
	response, raw := r.send(t, call{
		method: http.MethodPost, path: "/v1/workers/worker-b/sessions", body: ledger.SessionRequest{Protocol: ledger.ProtocolVersion},
		headers: map[string]string{"Authorization": "Bearer " + credential},
	})
	wantStatus(t, response, raw, http.StatusOK, "")
	response, raw = r.send(t, call{
		method: http.MethodPost, path: "/v1/workers/worker-b/sessions", body: ledger.SessionRequest{Protocol: ledger.ProtocolVersion},
		headers: map[string]string{"Authorization": "Bearer someone-elses"},
	})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadCredential)
}

// Registration is the one route a worker calls before it holds a credential, so its credential is the join
// token in the request, and a request with none, or with another's, goes no further.
func TestRegistrationNeedsAJoinToken(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	for _, body := range []any{
		struct{}{},
		ledger.Registration{WorkerID: "worker-c", Name: "worker-c"},
		ledger.Registration{JoinToken: "not-a-join-token", WorkerID: "worker-c", Name: "worker-c"},
	} {
		response, raw := r.send(t, call{method: http.MethodPost, path: "/v1/workers/register", body: body})
		wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadJoinToken)
	}
	response, raw := r.send(t, call{method: http.MethodPost, path: "/v1/workers/register", body: ledger.Registration{JoinToken: "x", WorkerID: "worker-c"}, headers: map[string]string{api.HeaderSession: r.d.Session}})
	wantStatus(t, response, raw, http.StatusForbidden, api.CodeForbidden)
	// A body past the limit is refused without being read to its end.
	huge := strings.Repeat("x", 1<<20)
	response, raw = r.send(t, call{method: http.MethodPost, path: "/v1/workers/register", body: map[string]string{"join_token": huge}})
	if response.StatusCode < 400 || response.StatusCode >= 500 {
		t.Errorf("an oversized registration answered %d: %.80s", response.StatusCode, raw)
	}
	// A join token is used once: the same one registers the same worker again and no other.
	plane := control.NewWorkers(r.d.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})
	token, err := plane.CreateJoinToken(t.Context(), ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	response, raw = r.send(t, call{method: http.MethodPost, path: "/v1/workers/register", body: ledger.Registration{JoinToken: token, WorkerID: "worker-d", Name: "d"}})
	wantStatus(t, response, raw, http.StatusOK, "")
	response, raw = r.send(t, call{method: http.MethodPost, path: "/v1/workers/register", body: ledger.Registration{JoinToken: token, WorkerID: "worker-e", Name: "e"}})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadJoinToken)
}

// A stream a bearer token opened ends with its session, as the web UI's does, so a session that ended
// leaves nothing listening.
func TestAStreamOpenedWithABearerTokenEndsWithItsSession(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	session, err := r.d.Client.Client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), streamWait)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.web.URL(apitest.Route(api.EventsPath)+"?after=0"), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(api.HeaderSession, session.Token)
	response, err := r.web.HTTP.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d, type %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_, _ = io.Copy(io.Discard, response.Body)
	}()
	ending := r.d.Client.Client.WithCredential("")
	ending.UseSession(session.Token)
	if err := ending.SignOut(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("the stream outlived its session")
	}
}

// The listener's refusals carry no cache and no credential, and say nothing of what the caller sent.
func TestARefusalOnTheNetworkListenerIsNotCached(t *testing.T) {
	t.Parallel()
	r := networkOn(t)
	response, _ := r.send(t, call{path: "/v1/members"})
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control is %q", got)
	}
	response, raw := r.send(t, call{path: "/v1/members", headers: map[string]string{api.HeaderSession: "a-secret-session"}})
	if strings.Contains(string(raw), "a-secret-session") || strings.Contains(response.Header.Get("Set-Cookie"), "a-secret-session") {
		t.Errorf("a refusal repeats the session: %s", raw)
	}
}

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/web"
)

// builtAssets stands for a web build.
var builtAssets = fstest.MapFS{
	"index.html":            {Data: []byte("<!doctype html><title>ropeshift</title><div id=root></div>")},
	"placeholder.html":      {Data: []byte("<!doctype html><title>not built</title>")},
	"assets/app-abc123.js":  {Data: []byte("console.log('app')")},
	"assets/app-abc123.css": {Data: []byte("body{}")},
}

type webRig struct {
	d   *apitest.Daemon
	web *apitest.Web
}

func webOn(t *testing.T, d *apitest.Daemon) *webRig {
	t.Helper()
	return &webRig{d: d, web: d.ServeWeb(t, builtAssets, 20*time.Millisecond)}
}

// call is one request, as a browser would make it unless a test says otherwise.
type call struct {
	method  string
	path    string
	body    any
	cookies []*http.Cookie
	headers map[string]string
}

func (r *webRig) send(t *testing.T, c call) (*http.Response, []byte) {
	t.Helper()
	if c.method == "" {
		c.method = http.MethodGet
	}
	var body io.Reader
	if c.body != nil {
		raw, err := json.Marshal(c.body)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(t.Context(), c.method, r.web.URL(c.path), body)
	if err != nil {
		t.Fatal(err)
	}
	if c.body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	for name, value := range c.headers {
		if name == "Host" {
			request.Host = value
			continue
		}
		request.Header.Set(name, value)
	}
	response, err := r.web.HTTP.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, raw
}

// signedIn signs in as a browser does, and returns its cookies and the token it would read from one.
func (r *webRig) signedIn(t *testing.T) ([]*http.Cookie, string) {
	t.Helper()
	response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("sign-in: %d %s", response.StatusCode, raw)
	}
	cookies := response.Cookies()
	for _, cookie := range cookies {
		if cookie.Name == api.CookieCSRF {
			return cookies, cookie.Value
		}
	}
	t.Fatalf("no CSRF cookie among %v", cookies)
	return nil, ""
}

func wantStatus(t *testing.T, response *http.Response, raw []byte, status int, code string) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status %d, want %d: %s", response.StatusCode, status, raw)
	}
	if code == "" {
		return
	}
	var body api.ErrorBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if body.Error.Code != code {
		t.Fatalf("code %q, want %q", body.Error.Code, code)
	}
}

// aChange is a request that would change the ledger, and what it would have changed.
func (r *webRig) aChange(t *testing.T) (call, func() bool) {
	t.Helper()
	w := apitest.NewWorld(t, r.d)
	w.Running("wo-effect", "Add the retry budget").Effect("lane-wo-effect/1/input/nudge", "input", 1)
	var view api.NeedsYou
	if err := r.d.Client.ReadView(t.Context(), api.ViewNeedsYou, "", &view); err != nil {
		t.Fatal(err)
	}
	id := view.Items[0].ID
	changed := func() bool {
		effect, err := r.d.Client.Effect(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return !effect.Waiting()
	}
	return call{method: http.MethodPost, path: "/v1/effects/" + url.PathEscape(id) + "/decide", body: api.DecideEffect{Outcome: ledger.EffectHappened}}, changed
}

func TestAChangeThroughTheFrontDoorWorks(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	change, changed := r.aChange(t)
	change.cookies, change.headers = cookies, map[string]string{api.HeaderCSRF: token, "Origin": "http://" + r.web.Host}
	response, raw := r.send(t, change)
	wantStatus(t, response, raw, http.StatusOK, "")
	if !changed() {
		t.Fatal("the decision was not kept")
	}
}

// ATTACK: DNS rebinding. A page on a hostile name resolves that name to this machine, so a browser
// sends its requests here, and only the Host header shows where it thinks it is.
func TestARebindingRequestIsRefused(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	for _, host := range []string{
		"attacker.example", "attacker.example:" + strconv.Itoa(r.web.Port), "127.0.0.1", "127.0.0.1:1", "localhost:1", "127.0.0.1.attacker.example:" + strconv.Itoa(r.web.Port), "",
	} {
		for _, path := range []string{"/", "/assets/app-abc123.js", api.ViewPath(api.ViewNeedsYou), api.SignInPath} {
			t.Run(host+path, func(t *testing.T) {
				request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, r.web.URL(path), nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Host = host
				for _, cookie := range cookies {
					request.AddCookie(cookie)
				}
				request.Header.Set(api.HeaderCSRF, token)
				if host == "" {
					t.Skip("a client always names a host, and Go's fills in the address")
				}
				response, err := r.web.HTTP.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				raw, _ := io.ReadAll(response.Body)
				wantStatus(t, response, raw, http.StatusForbidden, api.CodeForeignHost)
			})
		}
	}
}

func TestEveryLoopbackNameOfThisPortIsAdmitted(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	port := strconv.Itoa(r.web.Port)
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port} {
		response, raw := r.send(t, call{path: "/", headers: map[string]string{"Host": host}})
		wantStatus(t, response, raw, http.StatusOK, "")
	}
}

// ATTACK: an unauthenticated change. Reaching this port proves nothing: any local process can.
func TestAChangeWithNoSessionIsRefused(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	change, changed := r.aChange(t)
	response, raw := r.send(t, change)
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
	if changed() {
		t.Fatal("an unauthenticated request changed the ledger")
	}
}

// Every changing route the listener serves needs a session, by name, so a new route cannot slip past.
func TestEveryChangingWebRouteNeedsASession(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	for _, route := range api.WebRoutes() {
		if route.Method == http.MethodGet || route.Pattern == api.SignInPath {
			continue
		}
		t.Run(route.Pattern, func(t *testing.T) {
			response, raw := r.send(t, call{method: route.Method, path: strings.ReplaceAll(route.Pattern, "{id}", "x"), body: struct{}{}})
			wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
		})
	}
}

// This listener reaches further than the socket, so its reads need a session too.
func TestEveryReadNeedsASession(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	for _, route := range api.WebRoutes() {
		if route.Method != http.MethodGet {
			continue
		}
		t.Run(route.Pattern, func(t *testing.T) {
			response, raw := r.send(t, call{path: route.Pattern})
			wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
		})
	}
}

// ATTACK: cross-site request forgery. A browser attaches a same-site cookie to a request a page on
// this machine makes, so the cookie proves nothing and the token a page cannot read does.
func TestAChangeWithNoCSRFTokenIsRefused(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	change, changed := r.aChange(t)
	change.cookies = cookies
	for name, headers := range map[string]map[string]string{
		"no token":    {"Origin": "http://" + r.web.Host},
		"wrong token": {api.HeaderCSRF: strings.Repeat("0", len(token)), "Origin": "http://" + r.web.Host},
		"empty token": {api.HeaderCSRF: "", "Origin": "http://" + r.web.Host},
	} {
		t.Run(name, func(t *testing.T) {
			change.headers = headers
			response, raw := r.send(t, change)
			wantStatus(t, response, raw, http.StatusForbidden, api.CodeCSRF)
			if changed() {
				t.Fatal("a forged request changed the ledger")
			}
		})
	}
}

// ATTACK: a request from another origin, with every credential a browser could attach. Another port on
// this machine is another origin and the same site, which SameSite does not stop.
func TestARequestFromAnotherOriginIsRefused(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	change, changed := r.aChange(t)
	change.cookies = cookies
	for _, origin := range []string{"http://attacker.example", "http://127.0.0.1:1", "http://localhost:1", "https://" + r.web.Host, "null"} {
		t.Run(origin, func(t *testing.T) {
			change.headers = map[string]string{api.HeaderCSRF: token, "Origin": origin}
			response, raw := r.send(t, change)
			wantStatus(t, response, raw, http.StatusForbidden, api.CodeForeignOrigin)
			if changed() {
				t.Fatal("a foreign origin changed the ledger")
			}
			read, raw := r.send(t, call{path: api.ViewPath(api.ViewNeedsYou), cookies: cookies, headers: map[string]string{"Origin": origin}})
			wantStatus(t, read, raw, http.StatusForbidden, api.CodeForeignOrigin)
		})
	}
}

func TestAFetchABrowserCallsCrossSiteIsRefused(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	change, changed := r.aChange(t)
	change.cookies, change.headers = cookies, map[string]string{api.HeaderCSRF: token, "Sec-Fetch-Site": "cross-site"}
	response, raw := r.send(t, change)
	wantStatus(t, response, raw, http.StatusForbidden, api.CodeForeignOrigin)
	if changed() {
		t.Fatal("a cross-site fetch changed the ledger")
	}
}

// ATTACK: a forged session. The token proves nothing the ledger does not hold, whatever matches it.
func TestAForgedSessionIsRefused(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	_, token := r.signedIn(t)
	change, changed := r.aChange(t)
	forged := []*http.Cookie{{Name: api.CookieSession, Value: strings.Repeat("a", 64)}, {Name: api.CookieCSRF, Value: token}}
	for name, attempt := range map[string]call{
		"a guessed token": {cookies: forged, headers: map[string]string{api.HeaderCSRF: token}},
		"its own csrf":    {cookies: []*http.Cookie{{Name: api.CookieSession, Value: "forged"}, {Name: api.CookieCSRF, Value: "forged"}}, headers: map[string]string{api.HeaderCSRF: "forged"}},
	} {
		t.Run(name, func(t *testing.T) {
			attempt.method, attempt.path, attempt.body = change.method, change.path, change.body
			response, raw := r.send(t, attempt)
			wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
			if changed() {
				t.Fatal("a forged session changed the ledger")
			}
		})
	}
}

// A token in a header is how a CLI proves itself on the socket, and this listener takes only the
// cookie, so a page gains nothing by naming one.
func TestASessionInAHeaderOpensNothingHere(t *testing.T) {
	d := apitest.Start(t)
	r := webOn(t, d)
	change, changed := r.aChange(t)
	change.headers = map[string]string{api.HeaderSession: d.Session}
	response, raw := r.send(t, change)
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
	if changed() {
		t.Fatal("a session in a header changed the ledger")
	}
}

func TestAWorkersRoutesStayOnTheSocket(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	for _, attempt := range []call{
		{method: http.MethodPost, path: "/v1/workers/register", body: ledger.Registration{WorkerID: "w"}},
		{method: http.MethodPost, path: "/v1/workers/w/heartbeat", body: ledger.Heartbeat{}},
		{method: http.MethodPost, path: "/v1/workers/w/events", body: ledger.UploadBatch{}},
		{path: "/v1/workers/w/desired?since=0"},
		{path: "/v1/workers/w/watch"},
		{method: http.MethodPut, path: "/v1/passphrase", body: api.SetPassphrase{Passphrase: "a new passphrase"}},
		{method: http.MethodPost, path: "/v1/workers/tokens", body: struct{}{}},
		{method: http.MethodPost, path: "/v1/policies", body: struct{}{}},
	} {
		t.Run(attempt.method+attempt.path, func(t *testing.T) {
			attempt.cookies, attempt.headers = cookies, map[string]string{api.HeaderCSRF: token}
			response, raw := r.send(t, attempt)
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status %d: %s", response.StatusCode, raw)
			}
		})
	}
}

func TestSignInHidesTheTokenFromAPage(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}})
	wantStatus(t, response, raw, http.StatusCreated, "")
	var session, csrf *http.Cookie
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case api.CookieSession:
			session = cookie
		case api.CookieCSRF:
			csrf = cookie
		}
	}
	if session == nil || csrf == nil {
		t.Fatalf("cookies %v", response.Cookies())
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteStrictMode || session.Path != "/" {
		t.Errorf("session cookie %+v", session)
	}
	if session.MaxAge != int(authority.LoginTTLMs/1000) || !session.Expires.IsZero() {
		t.Errorf("a cookie's life counts from now, on no clock but the browser's: %+v", session)
	}
	if csrf.HttpOnly || csrf.SameSite != http.SameSiteStrictMode {
		t.Errorf("a page must read this one: %+v", csrf)
	}
	if strings.Contains(string(raw), session.Value) {
		t.Error("the body hands a page the session")
	}
	if csrf.Value == session.Value {
		t.Error("a page can read the session")
	}
}

func TestAWrongPassphraseSetsNoCookie(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: "a guess at it"}})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	if len(response.Cookies()) != 0 {
		t.Errorf("cookies %v", response.Cookies())
	}
}

// ATTACK: a sign-in from a page. A form can post across origins with no preflight, and the listener
// reads only JSON, which a form cannot send.
func TestAFormCannotSignIn(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, r.web.URL(api.SignInPath), strings.NewReader(`{"passphrase":"`+apitest.Passphrase+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "text/plain")
	response, err := r.web.HTTP.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	wantStatus(t, response, raw, http.StatusUnsupportedMediaType, api.CodeMediaType)
}

func TestASignInFromAnotherOriginIsRefused(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	response, raw := r.send(t, call{
		method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase},
		headers: map[string]string{"Origin": "http://attacker.example"},
	})
	wantStatus(t, response, raw, http.StatusForbidden, api.CodeForeignOrigin)
}

func TestSigningOutEndsTheSession(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	read := call{path: api.ViewPath(api.ViewNeedsYou), cookies: cookies}
	response, raw := r.send(t, read)
	wantStatus(t, response, raw, http.StatusOK, "")
	response, raw = r.send(t, call{method: http.MethodPost, path: api.SignOutPath, body: struct{}{}, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}})
	wantStatus(t, response, raw, http.StatusNoContent, "")
	for _, cookie := range response.Cookies() {
		if cookie.MaxAge >= 0 {
			t.Errorf("a cookie stays: %+v", cookie)
		}
	}
	if len(response.Cookies()) != 2 {
		t.Errorf("cookies %v", response.Cookies())
	}
	response, raw = r.send(t, read)
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
}

func TestAPageReadsAViewAsUnrenderedJSON(t *testing.T) {
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	r := webOn(t, d)
	cookies, _ := r.signedIn(t)
	w := apitest.NewWorld(t, d)
	apitest.Populate(w, clock)
	response, raw := r.send(t, call{path: api.ViewPath(api.ViewNeedsYou), cookies: cookies})
	wantStatus(t, response, raw, http.StatusOK, "")
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("type %q", got)
	}
	if bytes.Contains(raw, []byte("<img")) {
		t.Errorf("a browser sniffing this would find markup: %s", raw)
	}
	var view api.NeedsYou
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range view.Items {
		if item.Batch == nil {
			continue
		}
		found = found || slices.ContainsFunc(item.Batch.Items, func(q questions.Item) bool { return strings.Contains(q.Text, apitest.WorldHostileBit) })
	}
	if !found {
		t.Error("the question's text did not survive as data")
	}
}

func TestEveryResponseStatesItsLimits(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, _ := r.signedIn(t)
	for _, path := range []string{"/", "/assets/app-abc123.js", api.ViewPath(api.ViewNeedsYou)} {
		t.Run(path, func(t *testing.T) {
			response, _ := r.send(t, call{path: path, cookies: cookies})
			policy := response.Header.Get("Content-Security-Policy")
			for _, want := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'"} {
				if !strings.Contains(policy, want) {
					t.Errorf("policy %q lacks %q", policy, want)
				}
			}
			if strings.Contains(policy, "unsafe") {
				t.Errorf("policy %q", policy)
			}
			for name, want := range map[string]string{
				"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
			} {
				if got := response.Header.Get(name); got != want {
					t.Errorf("%s: %q", name, got)
				}
			}
			if response.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Error("this listener answers no other origin")
			}
		})
	}
}

func TestAViewIsNeverCached(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, _ := r.signedIn(t)
	response, _ := r.send(t, call{path: api.ViewPath(api.ViewNeedsYou), cookies: cookies})
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache %q", got)
	}
}

func TestAssetsAreServed(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cases := []struct {
		path, want, mediaType, cache string
	}{
		{"/", "<!doctype html><title>ropeshift", "text/html", "no-cache"},
		{"/index.html", "<!doctype html><title>ropeshift", "text/html", "no-cache"},
		{"/needs-you", "<!doctype html><title>ropeshift", "text/html", "no-cache"},
		{"/assets/app-abc123.js", "console.log", "text/javascript", "public, max-age=31536000, immutable"},
		{"/assets/app-abc123.css", "body{}", "text/css", "public, max-age=31536000, immutable"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			response, raw := r.send(t, call{path: tc.path})
			wantStatus(t, response, raw, http.StatusOK, "")
			if !strings.HasPrefix(string(raw), tc.want) {
				t.Errorf("body %q", raw)
			}
			if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.mediaType) {
				t.Errorf("type %q", got)
			}
			if got := response.Header.Get("Cache-Control"); got != tc.cache {
				t.Errorf("cache %q", got)
			}
		})
	}
}

// The tree holds the placeholder or a build, and either way what is embedded serves, with every file
// a built page names.
func TestTheEmbeddedAssetsAreServed(t *testing.T) {
	assets, err := web.Assets()
	if err != nil {
		t.Fatal(err)
	}
	d := apitest.Start(t)
	r := &webRig{d: d, web: d.ServeWeb(t, assets, time.Second)}
	response, raw := r.send(t, call{path: "/"})
	wantStatus(t, response, raw, http.StatusOK, "")
	if !bytes.Contains(raw, []byte("<title>ropeshift</title>")) {
		t.Fatalf("page %s", raw)
	}
	for _, match := range regexp.MustCompile(`(?:src|href)="(/[^"]+)"`).FindAllSubmatch(raw, -1) {
		named := string(match[1])
		asset, body := r.send(t, call{path: named})
		wantStatus(t, asset, body, http.StatusOK, "")
		if len(body) == 0 {
			t.Errorf("%s is empty", named)
		}
	}
}

func TestAnUnbuiltTreeServesItsPlaceholder(t *testing.T) {
	d := apitest.Start(t)
	web := d.ServeWeb(t, fstest.MapFS{"placeholder.html": {Data: []byte("not built")}}, time.Second)
	r := &webRig{d: d, web: web}
	response, raw := r.send(t, call{path: "/"})
	wantStatus(t, response, raw, http.StatusOK, "")
	if string(raw) != "not built" {
		t.Errorf("body %q", raw)
	}
}

func TestAPathLeavesNoAssetTree(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	for _, path := range []string{"/assets/missing.js", "/assets/", "/assets", "/missing.css", "/%2e%2e/%2e%2e/go.mod"} {
		t.Run(path, func(t *testing.T) {
			response, raw := r.send(t, call{path: path})
			if response.StatusCode == http.StatusOK && !strings.HasPrefix(string(raw), "<!doctype html><title>ropeshift") {
				t.Errorf("status %d: %q", response.StatusCode, raw)
			}
		})
	}
}

func TestAnAssetTakesNoOtherMethod(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	response, raw := r.send(t, call{method: http.MethodPost, path: "/", body: struct{}{}})
	wantStatus(t, response, raw, http.StatusMethodNotAllowed, "")
}

// An open stream outlives the request that checked it, so a session that ends must end the stream.
func TestAStreamEndsWithItsSession(t *testing.T) {
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	ctx, cancel := context.WithTimeout(t.Context(), streamWait)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.web.URL(api.EventsPath+"?after=0"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
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
	signOut, raw := r.send(t, call{method: http.MethodPost, path: api.SignOutPath, body: struct{}{}, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}})
	wantStatus(t, signOut, raw, http.StatusNoContent, "")
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("the stream outlived its session")
	}
}

// TestTheTCPListenersBindLoopbackOnly holds both TCP listeners the design names, the web UI's and the
// OTLP receiver's, to loopback addresses.
func TestTheTCPListenersBindLoopbackOnly(t *testing.T) {
	for name, listen := range map[string]func(string) (net.Listener, error){"web": server.ListenWeb, "telemetry": server.ListenTelemetry} {
		for _, address := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
			t.Run(name+"/"+address, func(t *testing.T) {
				listener, err := listen(address)
				if err != nil {
					if strings.Contains(err.Error(), "cannot assign") {
						t.Skip("this machine has no such address")
					}
					t.Fatal(err)
				}
				defer listener.Close()
				if ip := listener.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
					t.Errorf("bound %s", ip)
				}
			})
		}
		for _, address := range []string{"0.0.0.0:0", "[::]:0", ":0", "192.168.1.2:0", "10.0.0.1:0", "attacker.example:80", "127.0.0.1.attacker.example:80", "no port"} {
			t.Run(name+"/"+address, func(t *testing.T) {
				listener, err := listen(address)
				if err == nil {
					listener.Close()
					t.Fatalf("bound %s", address)
				}
			})
		}
	}
}

func documentsOn(t *testing.T) *webRig {
	t.Helper()
	knowledge := fake.NewKnowledgeSource()
	knowledge.Write("design.md", "# One\n")
	knowledge.Write("design.md", "# Two\n")
	return webOn(t, apitest.StartWith(t, apitest.Options{Knowledge: []ports.KnowledgeSource{knowledge}}))
}

// A list holds no bodies, so a page that cannot open one document shows only titles.
func TestADocumentOpensThroughTheWebListener(t *testing.T) {
	r := documentsOn(t)
	cookies, _ := r.signedIn(t)
	response, raw := r.send(t, call{path: "/v1/documents/read?path=design.md", cookies: cookies})
	wantStatus(t, response, raw, http.StatusOK, "")
	var document api.Document
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.Body != "# Two\n" {
		t.Errorf("document %+v", document)
	}
	response, raw = r.send(t, call{path: "/v1/documents/versions?path=design.md", cookies: cookies})
	wantStatus(t, response, raw, http.StatusOK, "")
	var versions api.DocumentVersions
	if err := json.Unmarshal(raw, &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions.Versions) != 2 {
		t.Errorf("versions %+v", versions)
	}
	response, raw = r.send(t, call{path: "/v1/documents/read?path=design.md&version=" + versions.Versions[1].Version, cookies: cookies})
	wantStatus(t, response, raw, http.StatusOK, "")
	if !bytes.Contains(raw, []byte(`# One`)) {
		t.Errorf("the earlier version reads %s", raw)
	}
}

// ATTACK: a document's text comes from a repository, so a session is what stands between it and any
// page on this machine.
func TestADocumentStaysBehindASession(t *testing.T) {
	r := documentsOn(t)
	for _, path := range []string{"/v1/documents/read?path=design.md", "/v1/documents/versions?path=design.md"} {
		response, raw := r.send(t, call{path: path})
		wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
		if bytes.Contains(raw, []byte("# Two")) {
			t.Fatalf("%s gave up the document with no session: %s", path, raw)
		}
	}
}

// ATTACK: a path in a link a stranger sends reads only what the source lists.
func TestADocumentPathNamesOnlyWhatTheSourceLists(t *testing.T) {
	r := documentsOn(t)
	cookies, _ := r.signedIn(t)
	for _, path := range []string{"../../etc/passwd", "/etc/passwd", "design.md/../../secret.md", "", "design.md\x00.txt"} {
		response, raw := r.send(t, call{path: "/v1/documents/read?path=" + url.QueryEscape(path), cookies: cookies})
		wantStatus(t, response, raw, http.StatusNotFound, api.CodeNotFound)
	}
}

// The closed list is the whole of what the listener serves, so a route it names and the API lacks is
// a typo that would read as a refusal.
func TestEveryWebRouteExistsInTheAPI(t *testing.T) {
	d := apitest.Start(t)
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	ids := regexp.MustCompile(`\{id\}`)
	for _, route := range api.WebRoutes() {
		if route.Pattern == api.SignOutPath || route.Pattern == api.EventsPath {
			continue
		}
		t.Run(fmt.Sprintf("%s %s", route.Method, route.Pattern), func(t *testing.T) {
			attempt := call{method: route.Method, path: ids.ReplaceAllString(route.Pattern, "no-such-record"), cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}}
			if route.Method != http.MethodGet {
				attempt.body = struct{}{}
			}
			response, raw := r.send(t, attempt)
			if response.StatusCode == http.StatusNotFound && !bytes.Contains(raw, []byte(api.CodeNotFound)) {
				t.Errorf("no such route: %s", raw)
			}
		})
	}
}

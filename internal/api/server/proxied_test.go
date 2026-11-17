package server_test

import (
	"net/http"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/test/apitest"
)

// A server behind a reverse proxy that terminates TLS (ADR-0031, mode c) serves the web UI at the origin its
// members' browsers use, which is public_url, reads where a request came from out of the proxy's headers, and
// takes those headers from the proxies it lists and from no other peer. These tests serve the network listener on
// loopback, with the test client as the proxy: it is a listed peer when 127.0.0.1 is listed, and a stranger when
// only 10.0.0.0/8 is.

const (
	publicHost   = "ropeshift.example"
	publicOrigin = "https://" + publicHost
)

func proxiesOf(t *testing.T, entries ...string) *server.Proxies {
	t.Helper()
	proxies, err := server.ParseProxies(entries)
	if err != nil {
		t.Fatal(err)
	}
	return proxies
}

// behindAProxy is a network listener that lists proxies, serves the web UI at publicURL, and refuses a client
// address after three wrong guesses.
func behindAProxy(t *testing.T, publicURL string, proxies ...string) *webRig {
	t.Helper()
	d := apitest.StartWith(t, apitest.Options{Attempts: server.NewAttempts(server.AttemptLimits{MaxFailures: 3, Lockout: 15 * time.Minute})})
	list := proxiesOf(t, proxies...)
	return &webRig{d: d, web: d.ServeNetworkWith(t, apitest.NetworkOptions{
		Assets: builtAssets, Recheck: 20 * time.Millisecond, Proxies: list, PublicURL: publicURL, Client: list.Client,
	})}
}

func (r *webRig) at(host string, headers map[string]string) map[string]string {
	merged := map[string]string{"Host": host}
	for name, value := range headers {
		merged[name] = value
	}
	return merged
}

// The web UI answers to the host of public_url, in any case, and to loopback names, and to no other host. The Host
// header is the client's to choose, so this decides which pages are served and is no access control.
func TestTheWebUIAnswersToThePublicHostAndLoopbackAndNoOther(t *testing.T) {
	t.Parallel()
	r := behindAProxy(t, publicOrigin, "127.0.0.1/32")
	for host, want := range map[string]int{
		publicHost: http.StatusOK, "Ropeshift.Example": http.StatusOK, "ropeshift.example:443": http.StatusForbidden, "other.example": http.StatusForbidden,
		"ropeshift.example.evil.example": http.StatusForbidden, "evil.example": http.StatusForbidden, "ropeshift.example:8443": http.StatusForbidden,
		"localhost:" + portOf(r): http.StatusOK, "127.0.0.1:" + portOf(r): http.StatusOK,
	} {
		response, raw := r.send(t, call{path: "/", headers: map[string]string{"Host": host}})
		if response.StatusCode != want {
			t.Errorf("Host %q answered %d, and wants %d: %s", host, response.StatusCode, want, raw)
		}
		if want == http.StatusForbidden {
			wantStatus(t, response, raw, http.StatusForbidden, api.CodeForeignHost)
		}
	}
	// A public URL on another port is the host a browser sends with that port.
	r = behindAProxy(t, "https://ropeshift.example:8443", "127.0.0.1/32")
	if response, raw := r.send(t, call{path: "/", headers: map[string]string{"Host": "ropeshift.example:8443"}}); response.StatusCode != http.StatusOK {
		t.Errorf("the public host with its port answered %d: %s", response.StatusCode, raw)
	}
	if response, _ := r.send(t, call{path: "/", headers: map[string]string{"Host": publicHost}}); response.StatusCode != http.StatusForbidden {
		t.Errorf("the public host without its port answered %d", response.StatusCode)
	}
	// With no public URL the web UI answers to loopback only, as it did.
	r = behindAProxy(t, "", "127.0.0.1/32")
	if response, _ := r.send(t, call{path: "/", headers: map[string]string{"Host": publicHost}}); response.StatusCode != http.StatusForbidden {
		t.Errorf("a server with no public URL answered to %s with %d", publicHost, response.StatusCode)
	}
}

// A page served under a host is the page of that host's origin alone: a request that names another origin, another
// scheme, or another port is refused, which is the check a CSRF token is the second line of.
func TestTheOriginOfARequestIsTheOriginOfItsHost(t *testing.T) {
	t.Parallel()
	r := behindAProxy(t, publicOrigin, "127.0.0.1/32")
	loopback := "127.0.0.1:" + portOf(r)
	for _, test := range []struct {
		host, origin string
		want         int
		why          string
	}{
		{publicHost, publicOrigin, http.StatusCreated, "the public origin"},
		{publicHost, "", http.StatusCreated, "no origin"},
		{publicHost, "https://evil.example", http.StatusForbidden, "another origin"},
		{publicHost, "http://" + publicHost, http.StatusForbidden, "the public host over http"},
		{publicHost, publicOrigin + ":8443", http.StatusForbidden, "the public host on another port"},
		{publicHost, "http://" + loopback, http.StatusForbidden, "a loopback origin on the public host"},
		{publicHost, "null", http.StatusForbidden, "an opaque origin"},
		{loopback, "http://" + loopback, http.StatusCreated, "the loopback origin on loopback"},
		{loopback, publicOrigin, http.StatusForbidden, "the public origin on loopback"},
		{loopback, "https://" + loopback, http.StatusForbidden, "a loopback origin over https"},
	} {
		response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: r.at(test.host, map[string]string{"Origin": test.origin})})
		if response.StatusCode != test.want {
			t.Errorf("%s: Host %q and Origin %q answered %d: %s", test.why, test.host, test.origin, response.StatusCode, raw)
		}
		if test.want == http.StatusForbidden {
			wantStatus(t, response, raw, http.StatusForbidden, api.CodeForeignOrigin)
		}
	}
}

// A session cookie is Secure when the page was served under an https public URL, which is the proxy's own
// address, and not when it was served on loopback, where a browser would not send a Secure cookie back over http.
func TestCookiesAreSecureUnderAnHTTPSPublicURL(t *testing.T) {
	t.Parallel()
	r := behindAProxy(t, publicOrigin, "127.0.0.1/32")
	secure := func(cookies []*http.Cookie) (count int, all bool) {
		all = len(cookies) > 0
		for _, cookie := range cookies {
			if cookie.Secure {
				count++
			} else {
				all = false
			}
		}
		return count, all
	}
	response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: r.at(publicHost, map[string]string{"Origin": publicOrigin})})
	wantStatus(t, response, raw, http.StatusCreated, "")
	if count, all := secure(response.Cookies()); !all || count != 2 {
		t.Errorf("the cookies of a sign-in at the public host are %+v", response.Cookies())
	}
	cookies := response.Cookies()
	token := csrfOf(sessionOf(cookies))
	response, raw = r.send(t, call{method: http.MethodPost, path: api.SignOutPath, body: struct{}{}, cookies: cookies, headers: r.at(publicHost, map[string]string{api.HeaderCSRF: token, "Origin": publicOrigin})})
	wantStatus(t, response, raw, http.StatusNoContent, "")
	if count, all := secure(response.Cookies()); !all || count != 2 {
		t.Errorf("the cookies that end a session at the public host are %+v", response.Cookies())
	}
	// The change of the passphrase sets them again under a new token.
	response, raw = r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: r.at(publicHost, nil)})
	wantStatus(t, response, raw, http.StatusCreated, "")
	cookies = response.Cookies()
	response, raw = r.send(t, call{method: http.MethodPut, path: api.PassphrasePath, body: api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: newPassphrase}, cookies: cookies,
		headers: r.at(publicHost, map[string]string{api.HeaderCSRF: csrfOf(sessionOf(cookies)), "Origin": publicOrigin})})
	wantStatus(t, response, raw, http.StatusOK, "")
	if count, all := secure(response.Cookies()); !all || count != 2 {
		t.Errorf("the cookies of a change of the passphrase at the public host are %+v", response.Cookies())
	}

	// On loopback the cookies are not Secure, so the person at the server's own machine can sign in over http.
	response, raw = r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: newPassphrase}})
	wantStatus(t, response, raw, http.StatusCreated, "")
	if count, _ := secure(response.Cookies()); count != 0 || len(response.Cookies()) != 2 {
		t.Errorf("the cookies of a sign-in on loopback are %+v", response.Cookies())
	}
	// A public URL that is http is no promise of TLS, and marks nothing Secure.
	plain := behindAProxy(t, "http://"+publicHost, "127.0.0.1/32")
	response, raw = plain.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: plain.at(publicHost, nil)})
	wantStatus(t, response, raw, http.StatusCreated, "")
	if count, _ := secure(response.Cookies()); count != 0 {
		t.Errorf("an http public URL marked %d cookies Secure", count)
	}
}

// A listed proxy that says its own client came over plain http has put a passphrase on the wire in the clear, and the
// server answers none of what that request carries. A peer that is not listed says nothing, so its header is no
// reason to refuse.
//
// X-Forwarded-Proto is not required: a request from a listed peer that carries none is served, as the last loop below
// holds. A server that required it would refuse every client that shares a listed proxy's address, which is what a
// worker or an operator on the server's own machine is once 127.0.0.1 is listed for a proxy there. What the header
// guards is a proxy that also accepts plain http, and one set up as the guide says sends the scheme it was reached
// by, which the server refuses when it is http. A proxy that sends no header at all is one the server could refuse
// only by refusing those clients with it.
func TestARequestThatReachedItsProxyOverPlainHTTPIsRefused(t *testing.T) {
	t.Parallel()
	listed := behindAProxy(t, publicOrigin, "127.0.0.1/32")
	for _, path := range []string{"/", api.SignInPath, "/v1/health", "/v1/workers/register"} {
		method := http.MethodGet
		if path == api.SignInPath || path == "/v1/workers/register" {
			method = http.MethodPost
		}
		response, raw := listed.send(t, call{method: method, path: path, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: listed.at(publicHost, map[string]string{"X-Forwarded-Proto": "http"})})
		wantStatus(t, response, raw, http.StatusForbidden, api.CodeInsecureTransport)
	}
	if response, raw := listed.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: listed.at(publicHost, map[string]string{"X-Forwarded-Proto": "https, http"})}); response.StatusCode != http.StatusForbidden {
		t.Errorf("a request the nearest proxy said was http answered %d: %s", response.StatusCode, raw)
	}
	for _, proto := range []string{"https", "http, https", "", "gopher"} {
		headers := map[string]string{}
		if proto != "" {
			headers["X-Forwarded-Proto"] = proto
		}
		response, raw := listed.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: listed.at(publicHost, headers)})
		wantStatus(t, response, raw, http.StatusCreated, "")
	}
	// The test client is not a listed proxy here, so what it says of its protocol is ignored.
	stranger := behindAProxy(t, publicOrigin, "10.0.0.0/8")
	response, raw := stranger.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: stranger.at(publicHost, map[string]string{"X-Forwarded-Proto": "http"})})
	wantStatus(t, response, raw, http.StatusCreated, "")
}

// The limit on wrong guesses counts by the client a listed proxy names, so one client's guesses are refused and
// another's, through the same proxy, are not. A peer that is not listed is counted as itself, whatever it writes.
func TestTheLimitOnGuessesCountsEachClientBehindTheProxy(t *testing.T) {
	t.Parallel()
	r := behindAProxy(t, publicOrigin, "127.0.0.1/32")
	guess := func(client, passphrase string, hop ...string) (*http.Response, []byte) {
		forwarded := client
		for _, other := range hop {
			forwarded += ", " + other
		}
		return r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: passphrase}, headers: r.at(publicHost, map[string]string{"X-Forwarded-For": forwarded, "X-Forwarded-Proto": "https"})})
	}
	for range 3 {
		response, raw := guess("203.0.113.7", wrongPassphrase)
		wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	}
	response, raw := guess("203.0.113.7", apitest.Passphrase)
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	// A client that writes another address in front of its own is still the one the proxy appended.
	response, raw = r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: r.at(publicHost, map[string]string{"X-Forwarded-For": "198.51.100.1, 203.0.113.7"})})
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	// Another client through the same proxy signs in.
	response, raw = guess("203.0.113.8", apitest.Passphrase)
	wantStatus(t, response, raw, http.StatusCreated, "")
	// And so does a client whose hop is listed twice over.
	response, raw = guess("203.0.113.9", apitest.Passphrase, "127.0.0.1")
	wantStatus(t, response, raw, http.StatusCreated, "")

	// A peer that is not listed is one client, whatever its headers say.
	stranger := behindAProxy(t, publicOrigin, "10.0.0.0/8")
	for i, client := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
		response, raw := stranger.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: wrongPassphrase}, headers: stranger.at(publicHost, map[string]string{"X-Forwarded-For": client})})
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d answered %d: %s", i+1, response.StatusCode, raw)
		}
	}
	response, raw = stranger.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: stranger.at(publicHost, map[string]string{"X-Forwarded-For": "203.0.113.99"})})
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
}

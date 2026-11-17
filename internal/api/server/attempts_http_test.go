package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// Every guess a client makes at a passphrase or a join token on the network listener is counted against the
// address its connection came from. The listener is reachable from any machine, and its Host header is the
// client's to choose, so what protects it is the credential every route asks for and this limit on guessing one.

const wrongPassphrase = "a passphrase nobody chose"

// byHeader tells the clients of a test apart by a header, because every request of a test comes from loopback.
func byHeader(r *http.Request) netip.Addr {
	if value := r.Header.Get("X-Test-Client"); value != "" {
		return netip.MustParseAddr(value)
	}
	return server.PeerAddress(r)
}

// limitedNetwork is a network listener that refuses a client address after three wrong guesses in 15 minutes.
func limitedNetwork(t *testing.T, client func(*http.Request) netip.Addr) *webRig {
	t.Helper()
	d := apitest.StartWith(t, apitest.Options{Attempts: server.NewAttempts(server.AttemptLimits{MaxFailures: 3, Lockout: 15 * time.Minute})})
	return &webRig{d: d, web: d.ServeNetworkWith(t, apitest.NetworkOptions{Assets: builtAssets, Recheck: 20 * time.Millisecond, Client: client})}
}

func as(client string) map[string]string {
	if client == "" {
		return nil
	}
	return map[string]string{"X-Test-Client": client}
}

func (r *webRig) signInAs(t *testing.T, client, passphrase string) (*http.Response, []byte) {
	t.Helper()
	return r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: passphrase}, headers: as(client)})
}

func (r *webRig) registerAs(t *testing.T, client, token, id string) (*http.Response, []byte) {
	t.Helper()
	return r.send(t, call{method: http.MethodPost, path: "/v1/workers/register", body: ledger.Registration{JoinToken: token, WorkerID: id, Name: id}, headers: as(client)})
}

func (r *webRig) joinToken(t *testing.T) string {
	t.Helper()
	plane := control.NewWorkers(r.d.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})
	token, err := plane.CreateJoinToken(t.Context(), ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// After three wrong passphrases from one address, that address is refused with 429 and a Retry-After, whatever it
// sends next, the right passphrase included, and another address is unaffected.
func TestSignInIsRefusedAfterThreeWrongPassphrasesFromOneAddress(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, byHeader)
	for i := range 3 {
		response, raw := r.signInAs(t, "203.0.113.7", wrongPassphrase)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d answered %d: %s", i+1, response.StatusCode, raw)
		}
	}
	response, raw := r.signInAs(t, "203.0.113.7", wrongPassphrase)
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	if got := response.Header.Get("Retry-After"); got != "900" {
		t.Errorf("Retry-After is %q, want the 900 seconds of the lockout", got)
	}
	response, raw = r.signInAs(t, "203.0.113.7", apitest.Passphrase)
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	if len(response.Cookies()) != 0 {
		t.Error("a refused sign-in set a cookie")
	}
	// Another address signs in, and guesses on its own count.
	response, raw = r.signInAs(t, "203.0.113.8", wrongPassphrase)
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	response, raw = r.signInAs(t, "203.0.113.9", apitest.Passphrase)
	wantStatus(t, response, raw, http.StatusCreated, "")
}

// The address is the one the connection came from. A client that sets any header it likes, the Host of a loopback
// name the web UI answers to included, is one address, and a header that names another address changes nothing.
func TestTheAddressIsThePeerWhateverHeadersAClientSends(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, nil)
	for i, headers := range []map[string]string{
		nil,
		{"X-Forwarded-For": "198.51.100.1"},
		{"X-Forwarded-For": "198.51.100.2, 198.51.100.3", "Forwarded": "for=198.51.100.4", "X-Real-IP": "198.51.100.5"},
	} {
		response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: wrongPassphrase}, headers: headers})
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d answered %d: %s", i+1, response.StatusCode, raw)
		}
	}
	response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: map[string]string{"X-Forwarded-For": "198.51.100.99", "Host": "localhost:" + portOf(r)}})
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
}

func portOf(r *webRig) string { return strconv.Itoa(r.web.Port) }

// credentialOf reads the credential a registration answered with.
func credentialOf(t *testing.T, raw []byte) string {
	t.Helper()
	var credential ledger.WorkerCredential
	if err := json.Unmarshal(raw, &credential); err != nil || credential.Credential == "" {
		t.Fatalf("%v: %s", err, raw)
	}
	return credential.Credential
}

// A wrong join token is a guess, counted with the wrong passphrases of the same address, and a registration from
// an address that is refused is refused too. A join that worked clears nothing.
func TestAWrongJoinTokenCountsWithTheWrongPassphrasesOfItsAddress(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, byHeader)
	client := "203.0.113.7"
	response, raw := r.signInAs(t, client, wrongPassphrase)
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	response, raw = r.registerAs(t, client, "no-such-token.and-no-secret", "worker-a")
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadJoinToken)
	// A join that works is no right passphrase, and leaves what the address has failed at as it was.
	response, raw = r.registerAs(t, client, r.joinToken(t), "worker-b")
	wantStatus(t, response, raw, http.StatusOK, "")
	response, raw = r.registerAs(t, client, "another-wrong.token", "worker-c")
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadJoinToken)

	response, raw = r.registerAs(t, client, r.joinToken(t), "worker-d")
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	response, raw = r.signInAs(t, client, apitest.Passphrase)
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	// Another address registers.
	response, raw = r.registerAs(t, "203.0.113.8", r.joinToken(t), "worker-e")
	wantStatus(t, response, raw, http.StatusOK, "")
}

// A worker that holds its credential is not a guess at one, so a refused address still reaches the plane with it.
func TestAWorkerWithItsCredentialIsNotHeldByTheLimitOfItsAddress(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, byHeader)
	client := "203.0.113.7"
	credential := ""
	{
		response, raw := r.registerAs(t, client, r.joinToken(t), "worker-a")
		wantStatus(t, response, raw, http.StatusOK, "")
		credential = credentialOf(t, raw)
	}
	for range 3 {
		r.signInAs(t, client, wrongPassphrase)
	}
	response, raw := r.signInAs(t, client, apitest.Passphrase)
	wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	headers := as(client)
	headers["Authorization"] = "Bearer " + credential
	response, raw = r.send(t, call{method: http.MethodPost, path: "/v1/workers/worker-a/sessions", body: ledger.SessionRequest{Protocol: ledger.ProtocolVersion}, headers: headers})
	wantStatus(t, response, raw, http.StatusOK, "")
}

// A right passphrase clears the count of its address, so failures before it are forgotten.
func TestARightPassphraseClearsTheCountOfItsAddress(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, byHeader)
	client := "203.0.113.7"
	for range 2 {
		response, raw := r.signInAs(t, client, wrongPassphrase)
		wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	}
	response, raw := r.signInAs(t, client, apitest.Passphrase)
	wantStatus(t, response, raw, http.StatusCreated, "")
	for range 2 {
		response, raw := r.signInAs(t, client, wrongPassphrase)
		wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	}
}

// A session as a bearer token reaches the API's own sign-in, and the web UI's change of the passphrase asks for the
// current one: each is a guess at the passphrase, and counts like the sign-in page's.
func TestEveryRouteThatChecksThePassphraseCountsItsWrongGuesses(t *testing.T) {
	t.Parallel()
	t.Run("the API's sign-in, with a session as a bearer token", func(t *testing.T) {
		r := limitedNetwork(t, byHeader)
		headers := as("203.0.113.7")
		headers[api.HeaderSession] = r.d.Session
		for range 3 {
			response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: wrongPassphrase}, headers: headers})
			wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
		}
		response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: headers})
		wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	})
	t.Run("a change of the passphrase on the web UI", func(t *testing.T) {
		r := limitedNetwork(t, byHeader)
		response, raw := r.signInAs(t, "203.0.113.7", apitest.Passphrase)
		wantStatus(t, response, raw, http.StatusCreated, "")
		cookies := response.Cookies()
		token := csrfOf(sessionOf(cookies))
		for range 3 {
			response, raw := r.send(t, call{method: http.MethodPut, path: api.PassphrasePath, cookies: cookies, body: api.ChangePassphrase{Current: wrongPassphrase, Passphrase: newPassphrase}, headers: map[string]string{api.HeaderCSRF: token, "X-Test-Client": "203.0.113.7"}})
			wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
		}
		response, raw = r.send(t, call{method: http.MethodPut, path: api.PassphrasePath, cookies: cookies, body: api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: newPassphrase}, headers: map[string]string{api.HeaderCSRF: token, "X-Test-Client": "203.0.113.7"}})
		wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
		if r.signsIn(t, newPassphrase) {
			t.Error("the passphrase was changed by a request that was refused")
		}
	})
}

// Guesses sent at once are let through no more than the limit allows, so a client cannot beat the count by being
// quick: of forty at once, exactly three are answered as wrong guesses and the rest are refused.
func TestGuessesSentAtOnceAreLetThroughNoMoreThanTheLimitAllows(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, byHeader)
	var mu sync.Mutex
	statuses := map[int]int{}
	var wait sync.WaitGroup
	for range 40 {
		wait.Go(func() {
			response, _ := r.signInAs(t, "203.0.113.7", wrongPassphrase)
			mu.Lock()
			statuses[response.StatusCode]++
			mu.Unlock()
		})
	}
	wait.Wait()
	if statuses[http.StatusUnauthorized] != 3 || statuses[http.StatusTooManyRequests] != 37 {
		t.Errorf("of forty guesses at once: %v", statuses)
	}
}

// checking is an identity provider that counts the proofs it is asked to check, which is what a hash costs.
type checking struct {
	ports.IdentityProvider
	checks atomic.Int64
}

func (c *checking) Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof) (string, error) {
	c.checks.Add(1)
	return c.IdentityProvider.Authenticate(ctx, scope, proof)
}

func (c *checking) Change(ctx context.Context, scope ledger.WorkspaceID, current, next ports.Proof, actor ledger.Actor, alongside func(tx ports.SharedTx, member string) error) error {
	c.checks.Add(1)
	return c.IdentityProvider.Change(ctx, scope, current, next, actor, alongside)
}

// A refused address costs the server no hash: its guesses are turned away before anything checks a passphrase,
// the right one with the rest, and the checks the server runs are the ones the limit let through.
func TestARefusedAddressRunsNoHash(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Attempts: server.NewAttempts(server.AttemptLimits{MaxFailures: 3, Lockout: 15 * time.Minute})})
	identity := &checking{IdentityProvider: d.Config.Identity}
	d.Config.Identity = identity
	r := &webRig{d: d, web: d.ServeNetworkWith(t, apitest.NetworkOptions{Assets: builtAssets, Recheck: 20 * time.Millisecond, Client: byHeader})}
	for range 3 {
		response, raw := r.signInAs(t, "203.0.113.7", wrongPassphrase)
		wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	}
	if got := identity.checks.Load(); got != 3 {
		t.Fatalf("three guesses ran %d checks", got)
	}
	for _, passphrase := range []string{wrongPassphrase, apitest.Passphrase, "", wrongPassphrase} {
		response, raw := r.signInAs(t, "203.0.113.7", passphrase)
		wantStatus(t, response, raw, http.StatusTooManyRequests, api.CodeTooManyAttempts)
	}
	signed, raw := r.signInAs(t, "203.0.113.8", apitest.Passphrase)
	wantStatus(t, signed, raw, http.StatusCreated, "")
	if got := identity.checks.Load(); got != 4 {
		t.Errorf("the refused address ran checks: %d in all, want 4", got)
	}
}

// abandoning is an identity provider whose client goes away the moment its check is done, which is when a client
// that means to guess without being counted goes.
type abandoning struct {
	ports.IdentityProvider
	cancel context.CancelFunc
}

func (a abandoning) Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof) (string, error) {
	member, err := a.IdentityProvider.Authenticate(ctx, scope, proof)
	a.cancel()
	return member, err
}

// A guess whose client leaves after the check still counts. A client that cancels each request the moment the
// server has judged it must not get more guesses than one that waits.
func TestAGuessWhoseClientLeavesAfterTheCheckStillCounts(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Attempts: server.NewAttempts(server.AttemptLimits{MaxFailures: 3, Lockout: 15 * time.Minute})})
	identity := d.Config.Identity
	send := func(passphrase string) int {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		config := d.Config
		config.Identity = abandoning{identity, cancel}
		handler := server.NewNetwork(config, server.WebOptions{Port: 7400, Assets: builtAssets})
		body, err := json.Marshal(api.SignIn{Passphrase: passphrase})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:7400"+api.SignInPath, bytes.NewReader(body))
		request.RemoteAddr = "203.0.113.7:4000"
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	var let int
	for range 20 {
		if send(wrongPassphrase) != http.StatusTooManyRequests {
			let++
		}
	}
	if let != 3 {
		t.Errorf("%d of 20 guesses whose client left were let through a limit of 3", let)
	}
	if got := send(apitest.Passphrase); got != http.StatusTooManyRequests {
		t.Errorf("the right passphrase from that address answered %d", got)
	}
}

// The control socket's callers prove themselves by the file permissions of the socket, and no limit applies to
// them: a person who mistypes at the terminal is not locked out, and does not lock out the address of anyone else.
func TestTheControlSocketIsNeverLimitedAndNeverCounts(t *testing.T) {
	t.Parallel()
	r := limitedNetwork(t, byHeader)
	for range 20 {
		if _, err := r.d.Client.SignIn(t.Context(), api.SignIn{Passphrase: wrongPassphrase}); err == nil {
			t.Fatal("a wrong passphrase signed in on the socket")
		} else if refused, ok := err.(*api.Error); !ok || refused.Detail.Code != api.CodeBadPassphrase {
			t.Fatalf("the socket answered %v, which is no wrong passphrase", err)
		}
	}
	if _, err := r.d.Client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase}); err != nil {
		t.Errorf("the right passphrase is refused on the socket after wrong ones: %v", err)
	}
	response, raw := r.signInAs(t, "203.0.113.7", apitest.Passphrase)
	wantStatus(t, response, raw, http.StatusCreated, "")
}

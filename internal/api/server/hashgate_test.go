package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// held waits until a gate holds n places, which is how a test knows a goroutine is in the line.
func held(t *testing.T, gate *server.HashGate, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); gate.Held() != n; {
		if time.Now().After(deadline) {
			t.Fatalf("the gate holds %d places, and the test waited for %d", gate.Held(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// A check runs alone, those behind it wait their turn in a line of a size the gate names, and a check that finds
// the line full is refused at once, so a crowd of guessers cannot hold the gate for as long as they like.
func TestAHashGateRunsOneCheckAtATimeAndRefusesWhenItsLineIsFull(t *testing.T) {
	t.Parallel()
	gate := server.NewHashGate(2)
	first, err := gate.Enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan func())
	go func() {
		leave, err := gate.Enter(t.Context())
		if err != nil {
			t.Error(err)
			close(entered)
			return
		}
		entered <- leave
	}()
	held(t, gate, 2)
	select {
	case <-entered:
		t.Fatal("a second check ran beside the first")
	default:
	}
	if _, err := gate.Enter(t.Context()); !errors.Is(err, server.ErrHashBusy) {
		t.Fatalf("a third check in a gate of two answers %v", err)
	}
	first()
	second := <-entered
	if gate.Held() != 1 {
		t.Errorf("the gate holds %d places with one check running", gate.Held())
	}
	// The place the first one freed is offered to a third, who waits behind the second.
	thirdIn := make(chan func(), 1)
	go func() {
		leave, err := gate.Enter(t.Context())
		if err != nil {
			t.Error(err)
			close(thirdIn)
			return
		}
		thirdIn <- leave
	}()
	held(t, gate, 2)
	second()
	third := <-thirdIn
	third()
	if gate.Held() != 0 {
		t.Errorf("the gate holds %d places once every check left", gate.Held())
	}
	first()
	second()
	if gate.Held() != 0 {
		t.Errorf("a check that left twice freed %d places too many", -gate.Held())
	}
}

// A check that gives up while it waits frees its place, so a client that goes away is no weight on the line.
func TestACheckThatGivesUpWhileItWaitsFreesItsPlace(t *testing.T) {
	t.Parallel()
	gate := server.NewHashGate(2)
	first, err := gate.Enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := gate.Enter(ctx)
		done <- err
	}()
	held(t, gate, 2)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("a check that gave up answers %v", err)
	}
	held(t, gate, 1)
	var none *server.HashGate
	leave, err := none.Enter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	leave()
	if server.NewHashGate(0).Held() != 0 {
		t.Error("a gate of no depth holds a place")
	}
	if leave, err := server.NewHashGate(0).Enter(t.Context()); err != nil {
		t.Errorf("a gate with no depth named is refused: %v", err)
	} else {
		leave()
	}
}

// holding is an identity provider whose check does not end until the test says, so a test can fill the gate.
type holding struct {
	ports.IdentityProvider
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *holding) Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof) (string, error) {
	h.once.Do(func() { close(h.started) })
	select {
	case <-h.release:
	case <-ctx.Done():
	}
	return h.IdentityProvider.Authenticate(ctx, scope, proof)
}

// One address holds two places of the line at most, so a few addresses cannot fill it and leave a member the
// immediate 503: of twelve sign-ins at once from three addresses, six are in the line, two for each address, and
// six are refused at once with 429. A member at another address still finds a place, and every sign-in that was in
// the line finishes when it is let to.
func TestOneAddressCannotHoldMoreThanTwoPlacesOfTheLine(t *testing.T) {
	t.Parallel()
	gate := server.NewHashGate(16)
	attempts := server.NewAttempts(server.AttemptLimits{MaxFailures: 10, Lockout: 15 * time.Minute})
	d := apitest.StartWith(t, apitest.Options{Attempts: attempts, Hashes: gate})
	identity := &holding{IdentityProvider: d.Config.Identity, started: make(chan struct{}), release: make(chan struct{})}
	d.Config.Identity = identity
	r := &webRig{d: d, web: d.ServeNetworkWith(t, apitest.NetworkOptions{Assets: builtAssets, Recheck: 20 * time.Millisecond, Client: byHeader})}

	type result struct {
		status int
		code   string
		retry  string
	}
	results := make(chan result, 32)
	signIn := func(client string) {
		response, raw := r.signInAs(t, client, apitest.Passphrase)
		var body api.ErrorBody
		_ = json.Unmarshal(raw, &body)
		results <- result{response.StatusCode, body.Error.Code, response.Header.Get("Retry-After")}
	}
	for _, client := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
		for range 4 {
			go signIn(client)
		}
	}
	for range 6 {
		select {
		case got := <-results:
			if got.status != http.StatusTooManyRequests || got.code != api.CodeTooManyAttempts || got.retry != "1" {
				t.Fatalf("a sign-in beyond two of its address answered %d %s with Retry-After %q", got.status, got.code, got.retry)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("six of twelve sign-ins were not refused at once: the line holds %d places", gate.Held())
		}
	}
	held(t, gate, 6)
	go signIn("203.0.113.4")
	held(t, gate, 7)
	close(identity.release)
	for range 7 {
		select {
		case got := <-results:
			if got.status != http.StatusCreated {
				t.Errorf("a sign-in that was in the line answered %d", got.status)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the sign-ins in the line never finished")
		}
	}
	if gate.Held() != 0 || attempts.Tracked() != 0 {
		t.Errorf("the line holds %d places and the limit holds %d addresses after every sign-in finished", gate.Held(), attempts.Tracked())
	}
}

// With the gate full, a sign-in is refused with 503 and a Retry-After, whoever sends it and by whichever route, and
// is counted against nobody. The checks that were in the gate finish when they are let to.
func TestASignInThatFindsTheGateFullIsRefusedWith503AndCountedAgainstNoOne(t *testing.T) {
	t.Parallel()
	attempts := server.NewAttempts(server.AttemptLimits{MaxFailures: 3, Lockout: 15 * time.Minute})
	d := apitest.StartWith(t, apitest.Options{Attempts: attempts, Hashes: server.NewHashGate(2)})
	identity := &holding{IdentityProvider: d.Config.Identity, started: make(chan struct{}), release: make(chan struct{})}
	d.Config.Identity = identity
	r := &webRig{d: d, web: d.ServeNetworkWith(t, apitest.NetworkOptions{Assets: builtAssets, Recheck: 20 * time.Millisecond, Client: byHeader})}

	type result struct {
		status int
		retry  string
	}
	results := make(chan result, 8)
	signIn := func(client string) {
		response, _ := r.signInAs(t, client, apitest.Passphrase)
		results <- result{response.StatusCode, response.Header.Get("Retry-After")}
	}
	for _, client := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4"} {
		go signIn(client)
	}
	<-identity.started
	var refused int
	for refused < 2 {
		select {
		case got := <-results:
			if got.status != http.StatusServiceUnavailable || got.retry != "1" {
				t.Fatalf("a sign-in that found the gate full answered %d with Retry-After %q", got.status, got.retry)
			}
			refused++
		case <-time.After(30 * time.Second):
			t.Fatal("two sign-ins were not refused while the gate held two checks")
		}
	}
	// The API's own sign-in, with a session as a bearer token, waits at the same gate.
	headers := as("203.0.113.5")
	headers[api.HeaderSession] = r.d.Session
	response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: apitest.Passphrase}, headers: headers})
	wantStatus(t, response, raw, http.StatusServiceUnavailable, api.CodeUnavailable)
	if response.Header.Get("Retry-After") != "1" {
		t.Errorf("the API's sign-in was told to wait %q", response.Header.Get("Retry-After"))
	}
	close(identity.release)
	for range 2 {
		select {
		case got := <-results:
			if got.status != http.StatusCreated {
				t.Errorf("a check that was in the gate answered %d", got.status)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the checks in the gate never finished")
		}
	}
	if got := attempts.Tracked(); got != 0 {
		t.Errorf("the limit holds %d addresses after sign-ins that were refused, and sign-ins that worked", got)
	}
}

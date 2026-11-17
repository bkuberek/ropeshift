package server_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

const newPassphrase = "a passphrase for the next season"

// change is the request a Settings form sends: the current passphrase proves who is asking, and the new one
// replaces it.
func (r *webRig) change(t *testing.T, cookies []*http.Cookie, token string, body any) (*http.Response, []byte) {
	t.Helper()
	return r.send(t, call{method: http.MethodPut, path: api.PassphrasePath, body: body, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}})
}

func sessionOf(cookies []*http.Cookie) string {
	for _, cookie := range cookies {
		if cookie.Name == api.CookieSession {
			return cookie.Value
		}
	}
	return ""
}

func digestOf(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// csrfOf is the token this listener holds a session to, which a page reads from its cookie.
func csrfOf(session string) string {
	mac := hmac.New(sha256.New, []byte(session))
	mac.Write([]byte("ropeshift web csrf"))
	return hex.EncodeToString(mac.Sum(nil))
}

// liveSessions lists the digests of the sessions the ledger holds.
func liveSessions(t *testing.T, d *apitest.Daemon) []string {
	t.Helper()
	var digests []string
	err := d.Store.Installation(apitest.Scope).Read(t.Context(), func(tx ports.SharedReadTx) error {
		sessions, err := tx.Members().Sessions()
		for _, session := range sessions {
			digests = append(digests, session.Digest)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return digests
}

// signsIn says whether a passphrase opens a session on the web listener.
func (r *webRig) signsIn(t *testing.T, passphrase string) bool {
	t.Helper()
	response, _ := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: passphrase}})
	return response.StatusCode == http.StatusCreated
}

func installationEventTypes(t *testing.T, d *apitest.Daemon) (types []string, holding func(string) bool) {
	t.Helper()
	var events []ledger.Event
	err := d.Store.Installation(apitest.Scope).Read(t.Context(), func(tx ports.SharedReadTx) error {
		var err error
		events, err = tx.Events(0, 10_000)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types, func(secret string) bool {
		for _, event := range events {
			if strings.Contains(string(event.Data), secret) {
				return true
			}
		}
		return false
	}
}

// A member changes the passphrase from a web session: the new one signs in, the old one no longer does, every
// other session of the member ends, and the session the change came from stays.
func TestAMemberChangesThePassphraseFromTheWeb(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	otherTab, _ := r.signedIn(t)
	// d.Session is a third session of the same member, as the terminal's would be.
	if got := len(liveSessions(t, d)); got != 3 {
		t.Fatalf("%d sessions before the change, want 3", got)
	}
	response, raw := r.change(t, cookies, token, api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: newPassphrase})
	wantStatus(t, response, raw, http.StatusOK, "")
	var changed api.PassphraseChanged
	if err := json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	if changed.SessionsEnded != 2 {
		t.Errorf("the change says it ended %d sessions, want 2", changed.SessionsEnded)
	}
	if strings.Contains(string(raw), newPassphrase) || strings.Contains(string(raw), apitest.Passphrase) {
		t.Errorf("the answer holds a passphrase: %s", raw)
	}
	// The session the change came from stays, on a new token: a copy of the cookie it had opens nothing, and a copy of
	// the token a page read from it opens no change.
	rotated := response.Cookies()
	fresh := sessionOf(rotated)
	if fresh == "" || fresh == sessionOf(cookies) {
		t.Fatalf("the change set no new session cookie: %v", rotated)
	}
	var session, page *http.Cookie
	for _, cookie := range rotated {
		switch cookie.Name {
		case api.CookieSession:
			session = cookie
		case api.CookieCSRF:
			page = cookie
		}
	}
	if session == nil || page == nil {
		t.Fatalf("cookies %v", rotated)
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteStrictMode || session.Path != "/" || page.HttpOnly || page.Value != csrfOf(fresh) {
		t.Errorf("the new cookies are not the sign-in's: %+v and %+v", session, page)
	}
	if session.MaxAge <= 0 || session.MaxAge > int(authority.LoginTTLMs/1000) || !session.Expires.IsZero() {
		t.Errorf("the new session's life counts from now, within the old one's: %+v", session)
	}
	if left := liveSessions(t, d); len(left) != 1 || left[0] != digestOf(fresh) {
		t.Errorf("the sessions left are %v, want the new one alone", left)
	}
	read := call{path: apitest.Route(api.ViewPath(api.ViewNeedsYou))}
	read.cookies = cookies
	response, raw = r.send(t, read)
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
	read.cookies = rotated
	response, raw = r.send(t, read)
	wantStatus(t, response, raw, http.StatusOK, "")
	read.cookies = otherTab
	response, raw = r.send(t, read)
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
	// The old token, with the new session, is not the token of this page, and the new one is.
	response, raw = r.change(t, rotated, token, api.ChangePassphrase{Current: newPassphrase, Passphrase: "yet another passphrase"})
	wantStatus(t, response, raw, http.StatusForbidden, api.CodeCSRF)
	if r.signsIn(t, apitest.Passphrase) {
		t.Error("the old passphrase still signs in")
	}
	if !r.signsIn(t, newPassphrase) {
		t.Error("the new passphrase does not sign in")
	}
	// The harness's provider keeps its passphrase in memory, so the proof's own event is the contract suite's to hold.
	types, holding := installationEventTypes(t, d)
	if !slices.Contains(types, control.EventSessionsEnded) {
		t.Errorf("the installation's log has no %s: %v", control.EventSessionsEnded, types)
	}
	if holding(apitest.Passphrase) || holding(newPassphrase) {
		t.Error("an event holds a passphrase")
	}
	response, raw = r.change(t, rotated, page.Value, api.ChangePassphrase{Current: newPassphrase, Passphrase: "yet another passphrase"})
	wantStatus(t, response, raw, http.StatusOK, "")
}

// A change ends sessions and no delegate credential, as the documents say, and the answer counts the credentials
// that still act, so the page can offer the sign-out that ends them.
func TestAChangeCountsTheDelegateCredentialsThatSurviveIt(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	r := webOn(t, d)
	created, err := d.Client.CreateDelegate(t.Context(), api.CreateDelegate{Project: apitest.ProjectName, Label: "ci", ExpiresInMs: 24 * 60 * 60 * 1000})
	if err != nil {
		t.Fatal(err)
	}
	cookies, token := r.signedIn(t)
	response, raw := r.change(t, cookies, token, api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: newPassphrase})
	wantStatus(t, response, raw, http.StatusOK, "")
	var changed api.PassphraseChanged
	if err := json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	if changed.Delegates != 1 {
		t.Errorf("the change counts %d credentials that act, want 1: %s", changed.Delegates, raw)
	}
	// The harness's own session ended with the change, so the credentials are read through the new one.
	response, raw = r.send(t, call{path: api.DelegatesPath, cookies: response.Cookies()})
	wantStatus(t, response, raw, http.StatusOK, "")
	var listed api.DelegateList
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Delegates) == 0 {
		t.Fatalf("the member's credentials: %s", raw)
	}
	for _, credential := range listed.Delegates {
		if credential.ID == created.ID && credential.State != "active" {
			t.Errorf("the change ended the credential: %s", credential.State)
		}
	}
}

// A change that lost to another one, though its current passphrase was right, is told so with a status of its own,
// and leaves no refused sign-in on record.
type lostRace struct{ ports.IdentityProvider }

func (lostRace) Change(context.Context, ledger.WorkspaceID, ports.Proof, ports.Proof, ledger.Actor, func(ports.SharedTx, string) error) error {
	return authority.ErrPassphraseChanged
}

func TestAChangeThatLostARaceGetsItsOwnAnswerAndNoRefusalOnRecord(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	d.Config.Identity = lostRace{IdentityProvider: d.Config.Identity}
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	before, _ := installationEventTypes(t, d)
	response, raw := r.change(t, cookies, token, api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: newPassphrase})
	wantStatus(t, response, raw, http.StatusConflict, api.CodePassphraseChanged)
	if !strings.Contains(string(raw), "sign in again") {
		t.Errorf("the answer does not say what to do: %s", raw)
	}
	after, _ := installationEventTypes(t, d)
	if len(after) != len(before) {
		t.Errorf("a lost race left events: %v", after[len(before):])
	}
}

// A passphrase has an upper bound as well as a lower. The web says so once the current passphrase has proved the
// caller, and the socket's first set holds to it too.
func TestAPassphraseOverTheUpperBoundIsRefused(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	tooLong := strings.Repeat("a", authority.MaxPassphrase+1)
	response, raw := r.change(t, cookies, token, api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: tooLong})
	wantStatus(t, response, raw, http.StatusBadRequest, api.CodeInvalid)
	if !strings.Contains(string(raw), "1024") || strings.Contains(string(raw), tooLong) {
		t.Errorf("the refusal does not name the bound, or repeats the passphrase: %.200s", raw)
	}
	response, raw = r.change(t, cookies, token, api.ChangePassphrase{Current: "a guess at the passphrase", Passphrase: tooLong})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	response, raw = r.change(t, cookies, token, api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: strings.Repeat("b", authority.MaxPassphrase)})
	wantStatus(t, response, raw, http.StatusOK, "")

	first := apitest.StartWith(t, apitest.Options{NoMember: true})
	_, err := first.Client.SetPassphrase(t.Context(), api.SetPassphrase{Passphrase: tooLong})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if state, err := first.Client.Passphrase(t.Context()); err != nil || state.Set {
		t.Fatalf("a refused first set set one: %+v, %v", state, err)
	}
	if _, err := first.Client.SetPassphrase(t.Context(), api.SetPassphrase{Passphrase: strings.Repeat("b", authority.MaxPassphrase)}); err != nil {
		t.Fatalf("the longest first set: %v", err)
	}
}

// ATTACK: a change with one of the three proofs missing. The session says who is asking, the CSRF token says
// the request came from this listener's own page, and the current passphrase says the person at the page
// knows it. Each refusal changes nothing, and ends no session.
func TestAPassphraseChangeNeedsTheSessionTheTokenAndTheCurrentPassphrase(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	body := api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: newPassphrase}
	refusals := []struct {
		name    string
		attempt call
		status  int
		code    string
	}{
		{"no session", call{method: http.MethodPut, path: api.PassphrasePath, body: body, headers: map[string]string{api.HeaderCSRF: token}}, http.StatusUnauthorized, api.CodeSessionRequired},
		{"a session in a header", call{method: http.MethodPut, path: api.PassphrasePath, body: body, headers: map[string]string{api.HeaderCSRF: token, api.HeaderSession: d.Session}}, http.StatusUnauthorized, api.CodeSessionRequired},
		{"a forged session", call{method: http.MethodPut, path: api.PassphrasePath, body: body, cookies: []*http.Cookie{{Name: api.CookieSession, Value: "forged"}}, headers: map[string]string{api.HeaderCSRF: csrfOf("forged")}}, http.StatusUnauthorized, api.CodeSessionRequired},
		{"no token", call{method: http.MethodPut, path: api.PassphrasePath, body: body, cookies: cookies}, http.StatusForbidden, api.CodeCSRF},
		{"another session's token", call{method: http.MethodPut, path: api.PassphrasePath, body: body, cookies: cookies, headers: map[string]string{api.HeaderCSRF: csrfOf("another")}}, http.StatusForbidden, api.CodeCSRF},
		{"another origin", call{method: http.MethodPut, path: api.PassphrasePath, body: body, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token, "Origin": "http://attacker.example"}}, http.StatusForbidden, api.CodeForeignOrigin},
		{"a form", call{method: http.MethodPut, path: api.PassphrasePath, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token, "Content-Type": "text/plain"}}, http.StatusUnsupportedMediaType, api.CodeMediaType},
		{"no current passphrase", call{method: http.MethodPut, path: api.PassphrasePath, body: api.ChangePassphrase{Passphrase: newPassphrase}, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}}, http.StatusUnauthorized, api.CodeBadPassphrase},
		{"a wrong current passphrase", call{method: http.MethodPut, path: api.PassphrasePath, body: api.ChangePassphrase{Current: "a guess at the passphrase", Passphrase: newPassphrase}, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}}, http.StatusUnauthorized, api.CodeBadPassphrase},
	}
	for _, refusal := range refusals {
		t.Run(refusal.name, func(t *testing.T) {
			response, raw := r.send(t, refusal.attempt)
			wantStatus(t, response, raw, refusal.status, refusal.code)
			if strings.Contains(string(raw), newPassphrase) {
				t.Errorf("the refusal repeats the new passphrase: %s", raw)
			}
		})
	}
	if !r.signsIn(t, apitest.Passphrase) || r.signsIn(t, newPassphrase) {
		t.Error("a refused change moved the passphrase")
	}
	if len(liveSessions(t, d)) < 3 {
		t.Error("a refused change ended a session")
	}
}

// A missing current passphrase and a wrong one are refused with the same answer, byte for byte, so the answer
// says nothing of which the caller sent.
func TestAMissingAndAWrongCurrentPassphraseGetTheSameAnswer(t *testing.T) {
	t.Parallel()
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	var answers []string
	for _, current := range []string{"", "a guess at the passphrase", apitest.Passphrase + " "} {
		response, raw := r.change(t, cookies, token, api.ChangePassphrase{Current: current, Passphrase: newPassphrase})
		wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
		answers = append(answers, string(raw))
	}
	if answers[0] != answers[1] || answers[1] != answers[2] {
		t.Errorf("the answers differ: %q", answers)
	}
	// The same refusal as a sign-in with a wrong passphrase gives.
	response, raw := r.send(t, call{method: http.MethodPost, path: api.SignInPath, body: api.SignIn{Passphrase: "a guess at the passphrase"}})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	if string(raw) != answers[0] {
		t.Errorf("a change and a sign-in refuse differently: %q and %q", answers[0], raw)
	}
}

// A guess at the current passphrase is a guess at the passphrase, so it is on record as a refused sign-in is,
// naming no part of the guess, and the new passphrase is never recorded either.
func TestAWrongCurrentPassphraseLeavesARefusalOnRecord(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	const guess = "a guess at the current passphrase"
	before, _ := installationEventTypes(t, d)
	response, raw := r.change(t, cookies, token, api.ChangePassphrase{Current: guess, Passphrase: newPassphrase})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	after, holding := installationEventTypes(t, d)
	if len(after) != len(before)+1 || after[len(after)-1] != "member.sign_in_refused" {
		t.Errorf("the log went from %v to %v, want one refusal", before, after)
	}
	if holding(guess) || holding(newPassphrase) {
		t.Error("an event holds a passphrase")
	}
}

// A new passphrase below the minimum is refused at the field, in the API's words, once the current passphrase
// has proved the caller. A caller who has not proved it learns nothing of the minimum.
func TestAShortNewPassphraseIsRefusedOnlyAfterTheCurrentOneProves(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	response, raw := r.change(t, cookies, token, api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: "short"})
	wantStatus(t, response, raw, http.StatusBadRequest, api.CodeInvalid)
	if !strings.Contains(string(raw), "8 bytes") {
		t.Errorf("the refusal does not say the minimum: %s", raw)
	}
	response, raw = r.change(t, cookies, token, api.ChangePassphrase{Current: "a guess at the passphrase", Passphrase: "short"})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeBadPassphrase)
	if !r.signsIn(t, apitest.Passphrase) {
		t.Error("a refused change moved the passphrase")
	}
	if len(liveSessions(t, d)) < 2 {
		t.Error("a refused change ended a session")
	}
}

// The first passphrase is set on a terminal, under init, so the web listener refuses a change while none is
// set, even for a session a restored database left behind.
func TestThePassphraseChangeIsRefusedWhileNoneIsSet(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{NoMember: true})
	r := webOn(t, d)
	const orphan = "a session that outlived the member"
	err := d.Store.Installation(apitest.Scope).Write(t.Context(), "test.session", func(tx ports.SharedTx) error {
		if err := tx.Members().InsertSession(authority.Session{Digest: digestOf(orphan), Member: "member-1", CreatedAt: tx.Now(), ExpiresAt: tx.Now() + 60_000}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.session", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: "member", ID: "member-1"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies := []*http.Cookie{{Name: api.CookieSession, Value: orphan}}
	response, raw := r.change(t, cookies, csrfOf(orphan), api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: newPassphrase})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeNotEnrolled)
	state, err := d.Client.Passphrase(t.Context())
	if err != nil || state.Set {
		t.Fatalf("a refused change set a passphrase: %+v, %v", state, err)
	}
	// No session at all is the same refusal as on any route, and sets nothing either.
	response, raw = r.send(t, call{method: http.MethodPut, path: api.PassphrasePath, body: api.ChangePassphrase{Passphrase: newPassphrase}})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
	if state, err := d.Client.Passphrase(t.Context()); err != nil || state.Set {
		t.Fatalf("a request with no session set a passphrase: %+v, %v", state, err)
	}
}

// The listener serves a change and nothing else at this address: whether a passphrase is set is for the socket,
// and a page that has a session knows one is.
func TestTheWebListenerServesOnlyTheChangeAtThePassphraseAddress(t *testing.T) {
	t.Parallel()
	r := webOn(t, apitest.Start(t))
	cookies, token := r.signedIn(t)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPatch} {
		response, raw := r.send(t, call{method: method, path: api.PassphrasePath, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token}})
		if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusNoContent {
			t.Errorf("%s answered %d: %s", method, response.StatusCode, raw)
		}
	}
}

// The socket's route keeps its own form: a session proves who is asking there, and a terminal asks for the
// passphrase to open one. It takes no current passphrase, so a body that names one is refused whole.
func TestTheSocketRouteTakesNoCurrentPassphrase(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	client := api.NewUnixClient(d.Socket)
	defer client.Close()
	client.UseSession(d.Session)
	err := client.Do(t.Context(), http.MethodPut, api.PassphrasePath, api.ChangePassphrase{Current: apitest.Passphrase, Passphrase: newPassphrase}, nil)
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if _, err := client.SignIn(t.Context(), api.SignIn{Passphrase: apitest.Passphrase}); err != nil {
		t.Errorf("a refused request moved the passphrase: %v", err)
	}
}

// hashing counts how many passphrase checks run at once.
type hashing struct {
	ports.IdentityProvider
	running, most atomic.Int64
}

func (h *hashing) enter() func() {
	now := h.running.Add(1)
	for {
		seen := h.most.Load()
		if now <= seen || h.most.CompareAndSwap(seen, now) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	return func() { h.running.Add(-1) }
}

func (h *hashing) Authenticate(ctx context.Context, scope ledger.WorkspaceID, proof ports.Proof) (string, error) {
	defer h.enter()()
	return h.IdentityProvider.Authenticate(ctx, scope, proof)
}

func (h *hashing) Change(ctx context.Context, scope ledger.WorkspaceID, current, next ports.Proof, actor ledger.Actor, alongside func(ports.SharedTx, string) error) error {
	defer h.enter()()
	return h.IdentityProvider.Change(ctx, scope, current, next, actor, alongside)
}

// ATTACK: a flood of guesses at the passphrase through the change form. The listener checks one passphrase at a
// time, whether it comes with a sign-in or a change, so a flood costs its sender the wait and gets no more
// guesses in than one sign-in at a time would (FR-AUTH-5).
func TestChangesAndSignInsShareTheOneAttemptAtATime(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	counted := &hashing{IdentityProvider: d.Config.Identity}
	d.Config.Identity = counted
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	counted.most.Store(0)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				response, _ := r.change(t, cookies, token, api.ChangePassphrase{Current: "a guess at the passphrase", Passphrase: newPassphrase})
				if response.StatusCode != http.StatusUnauthorized {
					t.Errorf("change %d: status %d", i, response.StatusCode)
				}
				return
			}
			if r.signsIn(t, "a guess at the passphrase") {
				t.Errorf("sign-in %d opened a session on a guess", i)
			}
		}()
	}
	wg.Wait()
	if most := counted.most.Load(); most != 1 {
		t.Errorf("%d passphrase checks ran at once, want one", most)
	}
}

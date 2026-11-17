package server_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// The webhook route of CI events (FR-SPEND-20) is the one route of the network listener that takes no credential, so
// its signature is the whole of its authority: these tests hold it to refuse what it cannot verify, to repeat
// nothing, to read a body only up to a bound, and to leave a worker's heartbeat the hints of what it took.

const (
	hookSecret = "the signing key of acme widgets"
	hookEnv    = "ROPESHIFT_HOOK_WIDGETS"
	hookHead   = "0123456789abcdef0123456789abcdef01234567"
)

type hookRig struct {
	*webRig
	credential string
	epoch      int64
	lock       sync.Mutex
	warned     []string
}

func (h *hookRig) warn(line string) {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.warned = append(h.warned, line)
}

func (h *hookRig) warnings() string {
	h.lock.Lock()
	defer h.lock.Unlock()
	return strings.Join(h.warned, "\n")
}

// hookOn serves a network listener over a repository bound to acme/widgets whose webhook secret is in the
// environment variable its record names, and a worker joined to the same listener, whose heartbeat carries the hints.
func hookOn(t *testing.T) *hookRig {
	t.Helper()
	return hookWith(t, func(name string) (string, bool) { return hookSecret, name == hookEnv })
}

// hookWith is hookOn over a server whose environment is the one given.
func hookWith(t *testing.T, env func(string) (string, bool)) *hookRig {
	t.Helper()
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	binding := "github.com/acme/widgets"
	if _, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{ForgeBinding: &binding, CI: map[string]string{"ci_webhook_secret_env": hookEnv}}); err != nil {
		t.Fatal(err)
	}
	h := &hookRig{}
	d.Config.Env = env
	d.Config.Warn = h.warn
	h.webRig = &webRig{d: d, web: d.ServeNetwork(t, builtAssets, 20*time.Millisecond, nil)}
	h.credential = h.registered(t, "hook-worker")
	response, raw := h.send(t, call{
		method: http.MethodPost, path: "/v1/workers/hook-worker/sessions", body: ledger.SessionRequest{Protocol: ledger.ProtocolVersion},
		headers: map[string]string{"Authorization": "Bearer " + h.credential},
	})
	wantStatus(t, response, raw, http.StatusOK, "")
	var session ledger.Session
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	h.epoch = session.Epoch
	return h
}

// beat is the worker's heartbeat with a mark, and what the control plane answered.
func (h *hookRig) beat(t *testing.T, through int64) ledger.HeartbeatReply {
	t.Helper()
	response, raw := h.send(t, call{
		method: http.MethodPost, path: "/v1/workers/hook-worker/heartbeat",
		body:    ledger.Heartbeat{Epoch: h.epoch, Protocol: ledger.ProtocolVersion, Nonce: "n", HintsThrough: through},
		headers: map[string]string{"Authorization": "Bearer " + h.credential},
	})
	wantStatus(t, response, raw, http.StatusOK, "")
	var reply ledger.HeartbeatReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func signed(key string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func suiteBody(repository, head string) []byte {
	return fmt.Appendf(nil, `{"action":"completed","check_suite":{"head_sha":%q,"pull_requests":[{"number":3}]},"repository":{"full_name":%q}}`, head, repository)
}

// deliver posts a body the way GitHub does, with the headers it sends.
func (h *hookRig) deliver(t *testing.T, body []byte, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.web.URL(api.GitHubWebhookPath), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := h.web.HTTP.Do(request)
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

func good(id string, body []byte) map[string]string {
	return map[string]string{"X-GitHub-Event": "check_suite", "X-GitHub-Delivery": id, "X-Hub-Signature-256": signed(hookSecret, body)}
}

func TestASignedDeliveryIsAHintAWorkersHeartbeatCarries(t *testing.T) {
	t.Parallel()
	h := hookOn(t)
	if reply := h.beat(t, 0); len(reply.Hints) != 0 {
		t.Fatalf("a heartbeat before any delivery carries %+v", reply.Hints)
	}
	body := suiteBody("acme/widgets", hookHead)
	response, raw := h.deliver(t, body, good("d-1", body))
	wantStatus(t, response, raw, http.StatusAccepted, "")
	reply := h.beat(t, 0)
	if len(reply.Hints) != 1 || reply.Hints[0].Binding != "github.com/acme/widgets" || reply.Hints[0].Head != hookHead || reply.HintsSeq != reply.Hints[0].Seq {
		t.Fatalf("the heartbeat after a delivery carries %+v up to %d", reply.Hints, reply.HintsSeq)
	}
	if again := h.beat(t, reply.HintsSeq); len(again.Hints) != 0 {
		t.Errorf("a heartbeat with the mark carries %+v", again.Hints)
	}
}

// GitHub's ping of a new webhook is answered, and a name in another case is the same repository.
func TestAPingIsAnsweredAndARepositoryIsNamedWithoutRegardToCase(t *testing.T) {
	t.Parallel()
	h := hookOn(t)
	ping := []byte(`{"zen":"Keep it logically awesome.","repository":{"full_name":"Acme/Widgets"}}`)
	headers := good("d-ping", ping)
	headers["X-GitHub-Event"] = "ping"
	response, raw := h.deliver(t, ping, headers)
	wantStatus(t, response, raw, http.StatusOK, "")
	body := suiteBody("ACME/WIDGETS", hookHead)
	response, raw = h.deliver(t, body, good("d-case", body))
	wantStatus(t, response, raw, http.StatusAccepted, "")
	if reply := h.beat(t, 0); len(reply.Hints) != 1 {
		t.Errorf("the delivery for ACME/WIDGETS made %+v", reply.Hints)
	}
}

// The route is a security boundary: every delivery it cannot verify is refused the same way, makes no hint, and is
// counted, and none of what an operator is told holds the secret.
func TestAnUnverifiedDeliveryIsRefusedAlikeAndCounted(t *testing.T) {
	t.Parallel()
	h := hookOn(t)
	body := suiteBody("acme/widgets", hookHead)
	tests := map[string]struct {
		body    []byte
		headers map[string]string
		reason  string
	}{
		"no signature":       {body, map[string]string{"X-GitHub-Event": "check_suite", "X-GitHub-Delivery": "d-1"}, "missing_signature"},
		"bad signature":      {body, map[string]string{"X-GitHub-Event": "check_suite", "X-GitHub-Delivery": "d-2", "X-Hub-Signature-256": signed("a guess", body)}, "bad_signature"},
		"unknown repository": {suiteBody("acme/unknown", hookHead), nil, "unknown_repository"},
		"no repository":      {[]byte(`{"action":"completed"}`), nil, "unknown_repository"},
		"not JSON":           {[]byte("nope"), nil, "unknown_repository"},
	}
	var answers []string
	for name, tt := range tests {
		headers := tt.headers
		if headers == nil {
			headers = good("d-"+name, tt.body)
		}
		response, raw := h.deliver(t, tt.body, headers)
		wantStatus(t, response, raw, http.StatusForbidden, api.CodeBadSignature)
		answers = append(answers, string(raw))
	}
	for _, answer := range answers[1:] {
		if answer != answers[0] {
			t.Errorf("refusals differ, so a caller learns which kind it met: %q and %q", answers[0], answer)
		}
	}
	if reply := h.beat(t, 0); len(reply.Hints) != 0 {
		t.Errorf("a refused delivery made hints: %+v", reply.Hints)
	}
	warned := h.warnings()
	for _, reason := range []string{"missing_signature", "bad_signature", "unknown_repository"} {
		if !strings.Contains(warned, reason) {
			t.Errorf("an operator heard nothing of a %s refusal: %q", reason, warned)
		}
	}
	if strings.Contains(warned, hookSecret) || strings.Contains(warned, hookEnv) || strings.Contains(warned, "acme/unknown") {
		t.Errorf("a warning names a secret, a variable, or a repository a caller wrote: %q", warned)
	}
}

// A repository whose record names no variable, or a variable the server does not have, takes no delivery.
func TestARepositoryWithNoSecretTakesNoDelivery(t *testing.T) {
	t.Parallel()
	h := hookWith(t, func(string) (string, bool) { return "", false })
	body := suiteBody("acme/widgets", hookHead)
	response, raw := h.deliver(t, body, good("d-1", body))
	wantStatus(t, response, raw, http.StatusForbidden, api.CodeBadSignature)
	if reply := h.beat(t, 0); len(reply.Hints) != 0 {
		t.Errorf("a delivery for a repository with no secret made hints: %+v", reply.Hints)
	}
}

// A delivery ID is dropped the second time, and the answer says so without failing, so GitHub does not retry it.
func TestAReplayedDeliveryIsDropped(t *testing.T) {
	t.Parallel()
	h := hookOn(t)
	body := suiteBody("acme/widgets", hookHead)
	for i, want := range []int{http.StatusAccepted, http.StatusOK, http.StatusOK} {
		response, raw := h.deliver(t, body, good("d-same", body))
		wantStatus(t, response, raw, want, "")
		if reply := h.beat(t, 0); len(reply.Hints) != 1 {
			t.Fatalf("after delivery %d the heartbeat carries %d hints, want 1", i+1, len(reply.Hints))
		}
	}
}

// A body past the bound is refused unread, and counted, whatever it is signed with.
func TestAnOversizedBodyIsRefused(t *testing.T) {
	t.Parallel()
	h := hookOn(t)
	body := bytes.Repeat([]byte("x"), 1<<20+1)
	response, raw := h.deliver(t, body, good("d-big", body))
	wantStatus(t, response, raw, http.StatusRequestEntityTooLarge, api.CodeInvalid)
	if !strings.Contains(h.warnings(), "oversized") {
		t.Errorf("an operator heard %q of an oversized body", h.warnings())
	}
	// The bound is a megabyte: a body just under it is read.
	under := suiteBody("acme/widgets", hookHead)
	padded := append(append(append([]byte{}, under[:len(under)-1]...), bytes.Repeat([]byte(" "), 1<<20-len(under))...), '}')
	response, raw = h.deliver(t, padded, good("d-under", padded))
	wantStatus(t, response, raw, http.StatusAccepted, "")
}

// A body that is not JSON is refused by its type, before it is read.
func TestADeliveryThatIsNotJSONIsRefused(t *testing.T) {
	t.Parallel()
	h := hookOn(t)
	body := []byte("payload=%7B%7D")
	headers := good("d-form", body)
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	response, raw := h.deliver(t, body, headers)
	wantStatus(t, response, raw, http.StatusUnsupportedMediaType, api.CodeInvalid)
}

// A signed delivery that names no delivery ID or no event is malformed, which its signature lets the caller be told.
func TestAMalformedSignedDeliveryIsToldSo(t *testing.T) {
	t.Parallel()
	h := hookOn(t)
	body := suiteBody("acme/widgets", hookHead)
	for name, headers := range map[string]map[string]string{
		"no delivery ID": {"X-GitHub-Event": "check_suite", "X-Hub-Signature-256": signed(hookSecret, body)},
		"no event":       {"X-GitHub-Delivery": "d-1", "X-Hub-Signature-256": signed(hookSecret, body)},
	} {
		response, raw := h.deliver(t, body, headers)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s answered %d: %s", name, response.StatusCode, raw)
		}
	}
}

// Only a POST reaches it.
func TestOnlyAPostReachesTheWebhookRoute(t *testing.T) {
	t.Parallel()
	h := hookOn(t)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		response, _ := h.send(t, call{method: method, path: api.GitHubWebhookPath})
		if response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusOK {
			t.Errorf("%s answered %d", method, response.StatusCode)
		}
	}
}

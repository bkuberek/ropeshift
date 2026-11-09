package contract

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/notify"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func TestFakeNotifierContract(t *testing.T) {
	NotifierContract(t, func(*testing.T) NotifierRig {
		channel := fake.NewNotifier("fake")
		return NotifierRig{Notifier: channel, Heard: channel.Notices, Refuse: func() { channel.Fail(errors.New("no")) }}
	})
}

func TestWebNotifierContract(t *testing.T) {
	NotifierContract(t, func(*testing.T) NotifierRig {
		channel := notify.Web{}
		var heard []ledger.Notice
		return NotifierRig{Notifier: tapped{channel, &heard}, Heard: func() []ledger.Notice { return heard }}
	})
}

// tapped records what a channel took, for one with no far end: a page reads the ledger's stream.
type tapped struct {
	ports.Notifier
	heard *[]ledger.Notice
}

func (t tapped) Notify(ctx context.Context, notice ledger.Notice) error {
	if err := t.Notifier.Notify(ctx, notice); err != nil {
		return err
	}
	*t.heard = append(*t.heard, notice)
	return nil
}

const hookPath = "/hooks/ropeshift"

// hookServer is the far end of a webhook, on loopback, as a test's own.
type hookServer struct {
	mu     sync.Mutex
	heard  []ledger.Notice
	bodies [][]byte
	heads  []http.Header
	status int
}

func (h *hookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.status != 0 {
		w.WriteHeader(h.status)
		return
	}
	var notice ledger.Notice
	if err := json.Unmarshal(body, &notice); err == nil {
		h.heard = append(h.heard, notice)
	}
	h.bodies, h.heads = append(h.bodies, body), append(h.heads, r.Header.Clone())
	w.WriteHeader(http.StatusNoContent)
}

func (h *hookServer) notices() []ledger.Notice {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.heard)
}

const hookKey = "a-signing-key"

func hookRig(t *testing.T) (*hookServer, *notify.Webhook, string) {
	t.Helper()
	far := &hookServer{}
	server := httptest.NewServer(far)
	t.Cleanup(server.Close)
	url := server.URL + hookPath
	return far, notify.NewWebhook(notify.WebhookConfig{URL: url, Key: hookKey}), url
}

func TestWebhookNotifierContract(t *testing.T) {
	NotifierContract(t, func(t *testing.T) NotifierRig {
		far, channel, url := hookRig(t)
		return NotifierRig{
			Notifier: channel, Heard: far.notices, Secrets: []string{url, hookKey, strings.TrimPrefix(url, "http://")},
			Refuse: func() {
				far.mu.Lock()
				defer far.mu.Unlock()
				far.status = http.StatusForbidden
			},
		}
	})
}

// The key proves a delivery and never crosses the wire, so a log on either end holds nothing to steal.
func TestAWebhookSignsAndCarriesNoSecret(t *testing.T) {
	far, channel, _ := hookRig(t)
	notice := NotifierNotice()
	notice.Attempt = 2
	if err := channel.Notify(t.Context(), notice); err != nil {
		t.Fatal(err)
	}
	head, body := far.heads[0], far.bodies[0]
	mac := hmac.New(sha256.New, []byte(hookKey))
	mac.Write(body)
	if got, want := head.Get(notify.HeaderSignature), "sha256="+hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Errorf("signature %q, want %q", got, want)
	}
	if head.Get(notify.HeaderDelivery) != notice.Delivery {
		t.Errorf("delivery %q", head.Get(notify.HeaderDelivery))
	}
	if strings.Contains(string(body), hookKey) || head.Get("Authorization") != "" {
		t.Errorf("the key crossed the wire: %s, %v", body, head)
	}
}

func TestAWebhookFollowsNoRedirect(t *testing.T) {
	landed := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { landed = true }))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	channel := notify.NewWebhook(notify.WebhookConfig{URL: server.URL + hookPath})
	if err := channel.Notify(t.Context(), NotifierNotice()); err == nil {
		t.Error("a redirect read as a delivery")
	}
	if landed {
		t.Error("a notice followed a redirect somewhere the workspace never named")
	}
}

// The address has to stay out of what a failure says, which is the log and the ledger.
func TestAWebhookThatCannotBeReachedKeepsItsAddress(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL + hookPath
	server.Close()
	err := notify.NewWebhook(notify.WebhookConfig{URL: url}).Notify(t.Context(), NotifierNotice())
	if err == nil {
		t.Fatal("no error")
	}
	for _, leaked := range []string{url, strings.TrimPrefix(server.URL, "http://"), "127.0.0.1"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("%q holds %q", err, leaked)
		}
	}
}

func TestDesktopNotifierContract(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			NotifierContract(t, func(*testing.T) NotifierRig {
				runner, heard, refuse := desktopRunner()
				channel := notify.NewDesktop(notify.DesktopConfig{GOOS: goos, Processes: runner, LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil }})
				return NotifierRig{Notifier: channel, Heard: heard, Refuse: refuse}
			})
		})
	}
}

// desktopRunner stands for the two programs, and reads a notice back out of the words each is given.
func desktopRunner() (*fake.ProcessRunner, func() []ledger.Notice, func()) {
	var mu sync.Mutex
	var heard []ledger.Notice
	code := 0
	runner := fake.NewProcessRunner()
	show := func(spec ports.ProcessSpec, _ <-chan struct{}) int {
		mu.Lock()
		defer mu.Unlock()
		if code == 0 {
			heard = append(heard, ledger.Notice{Text: spec.Args[len(spec.Args)-1]})
		}
		return code
	}
	runner.Install("/usr/bin/osascript", show)
	runner.Install("/usr/bin/notify-send", show)
	return runner, func() []ledger.Notice {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(heard)
		}, func() {
			mu.Lock()
			defer mu.Unlock()
			code = 1
		}
}

func TestADesktopWithNoProgramSaysSo(t *testing.T) {
	channel := notify.NewDesktop(notify.DesktopConfig{GOOS: "linux", Processes: fake.NewProcessRunner(), LookPath: func(string) (string, error) { return "", errors.New("not found") }})
	if err := channel.Notify(t.Context(), NotifierNotice()); err == nil {
		t.Error("a machine with no program read as told")
	}
}

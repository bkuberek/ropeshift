package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
)

// downRepos is a registry of repositories whose stores cannot be read.
type downRepos struct{}

func (downRepos) CIRepository(context.Context, string, string, string) ([]control.CIRegistration, error) {
	return nil, errors.New("the stores are down")
}

// A delivery the server could not look up is no free request: it makes the server read its stores, so it counts
// against the address like every refusal, and an address that keeps sending them is limited.
func TestAWebhookDeliveryTheServerCannotLookUpCountsAgainstTheAddress(t *testing.T) {
	t.Parallel()
	limits := NewAttempts(AttemptLimits{MaxFailures: 3, Lockout: 15 * time.Minute, InFlight: WebhookInFlight})
	h := &handler{
		Config:     Config{WebhookAttempts: limits},
		ciWebhooks: control.NewCIWebhooks(downRepos{}, control.NewCIHints(time.Now), nil, time.Now, nil),
	}
	client := netip.MustParseAddr("203.0.113.9")
	post := func() int {
		request := httptest.NewRequestWithContext(withClient(t.Context(), client), http.MethodPost, "/v1/webhooks/github",
			strings.NewReader(`{"repository":{"full_name":"acme/widgets"}}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-GitHub-Event", "check_suite")
		request.Header.Set("X-GitHub-Delivery", "d-1")
		request.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64))
		recorder := httptest.NewRecorder()
		h.githubWebhook(recorder, request)
		return recorder.Code
	}
	for i := range 3 {
		if code := post(); code != http.StatusServiceUnavailable {
			t.Fatalf("delivery %d that could not be looked up was answered %d, want 503", i+1, code)
		}
	}
	if code := post(); code != http.StatusTooManyRequests {
		t.Errorf("the delivery after three that could not be looked up was answered %d, want 429", code)
	}
}

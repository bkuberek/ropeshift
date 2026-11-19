package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// An error body larger than the client reads is cut, and the client reports the status it saw and not the part of the
// body it kept.
func TestTheClientBoundsTheErrorBodyItDecodes(t *testing.T) {
	t.Parallel()
	const oversized = 17 << 20
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"`))
		chunk := []byte(strings.Repeat("a", 64<<10))
		for written := 0; written < oversized; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}}`))
	}))
	t.Cleanup(server.Close)
	client, err := api.NewTCPClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	_, err = client.Health(context.Background())
	var refused *api.Error
	if !errors.As(err, &refused) {
		t.Fatalf("the error reads %v", err)
	}
	if refused.Status != http.StatusInternalServerError || refused.Detail.Code != api.CodeUnavailable || len(refused.Detail.Message) > 1<<10 {
		t.Errorf("the error reads status %d, code %q, a message of %d bytes", refused.Status, refused.Detail.Code, len(refused.Detail.Message))
	}
}

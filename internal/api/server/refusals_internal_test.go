package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ports"
)

// A read the control plane cannot make on its own machine reaches the member as a typed refusal that names
// the read, and never as a generic outage a client would retry.
func TestAReadTheServerCannotMakeIsRefusedWithItsOwnCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		code string
	}{
		{"a repository read", fmt.Errorf("%w: reading documents", ports.ErrRepositoryReadsUnavailable), api.CodeRepositoryReadsUnavailable},
		{"a pane", fmt.Errorf("%w: a lane's pane", ports.ErrPaneRelayUnavailable), api.CodePaneRelayUnavailable},
		{"a repository read, wrapped twice", fmt.Errorf("start: %w", fmt.Errorf("%w: a lane's base", ports.ErrRepositoryReadsUnavailable)), api.CodeRepositoryReadsUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			fail(recorder, tt.err)
			if recorder.Code != http.StatusNotImplemented {
				t.Errorf("status %d, want %d", recorder.Code, http.StatusNotImplemented)
			}
			var body api.ErrorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tt.code {
				t.Errorf("code %q, want %q", body.Error.Code, tt.code)
			}
			if !strings.Contains(body.Error.Message, tt.code) {
				t.Errorf("the message %q does not name %s", body.Error.Message, tt.code)
			}
		})
	}
}

// The two codes are the reasons the design names, spelled as it spells them.
func TestTheServersRefusalCodesAreTheDesignsReasons(t *testing.T) {
	t.Parallel()
	if api.CodeRepositoryReadsUnavailable != "repository_reads_unavailable" || api.CodePaneRelayUnavailable != "pane_relay_unavailable" {
		t.Errorf("the codes are %q and %q", api.CodeRepositoryReadsUnavailable, api.CodePaneRelayUnavailable)
	}
}

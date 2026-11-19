package cli_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
)

// refusingSocket serves every request with an error whose message is message, and returns the socket's path.
func refusingSocket(t *testing.T, message string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeUnavailable, Message: message}})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Close() })
	return socket
}

// An error the daemon answers with is printed with no escape sequence and no hidden character in its message,
// whether the person reads text or JSON.
func TestAnAPIErrorIsPrintedWithoutEscapesOrHiddenCharacters(t *testing.T) {
	message := "refused \x1b[31mred\x1b[0m \u202ehidden\u200f \x07done"
	for _, flags := range [][]string{nil, {"--json"}} {
		socket := refusingSocket(t, message)
		var stdout, stderr bytes.Buffer
		args := append([]string{"work", "show", "wo", "--socket", socket}, flags...)
		cli.Run(args, &stdout, &stderr, cli.Options{})
		for _, bad := range []string{"\x1b", "\u202e", "\u200f", "\x07", `\u001b`, `\u0007`} {
			if strings.Contains(stderr.String(), bad) {
				t.Errorf("flags %v: stderr %q holds %q", flags, stderr.String(), bad)
			}
		}
		if !strings.Contains(stderr.String(), "refused red hidden done") {
			t.Errorf("flags %v: stderr %q lost the message's words", flags, stderr.String())
		}
	}
}

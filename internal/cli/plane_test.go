package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// request is one call a command made to the plane.
type request struct {
	Method string
	Path   string
	Body   string
}

// plane is a control plane that answers from a script, for a command whose steps matter more than
// the ledger behind them. A route nobody scripted answers 404. It holds one project, and a script names
// that project's routes as they read under /v1.
type plane struct {
	t      *testing.T
	Socket string
	mux    *http.ServeMux

	mu    sync.Mutex
	calls []request
}

func newPlane(t *testing.T) *plane {
	t.Helper()
	p := &plane{t: t, Socket: filepath.Join(apitest.ShortDir(t), "plane.sock"), mux: http.NewServeMux()}
	listener, err := net.Listen("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(p.serve)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return p
}

func (p *plane) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == api.ProjectsPath {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ProjectList{Projects: []api.Project{{Name: apitest.ProjectName, State: ledger.ProjectActive, Repositories: []string{}}}})
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, apitest.Route("/v1")); ok {
		r.URL.Path, r.URL.RawPath = "/v1"+rest, ""
	}
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.calls = append(p.calls, request{Method: r.Method, Path: r.URL.Path, Body: string(body)})
	p.mu.Unlock()
	r.Body = io.NopCloser(bytes.NewReader(body))
	p.mux.ServeHTTP(w, r)
}

// on scripts one route. The handler returns the document to answer with, or an error to refuse with.
func (p *plane) on(pattern string, answer func(r *http.Request, body []byte) (any, *api.Error)) {
	p.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		out, refused := answer(r, body)
		w.Header().Set("Content-Type", "application/json")
		if refused != nil {
			w.WriteHeader(refused.Status)
			_ = json.NewEncoder(w).Encode(api.ErrorBody{Error: refused.Detail})
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}

// reads lists the paths of every call made, in order, as "METHOD /path".
func (p *plane) reads() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, call := range p.calls {
		out = append(out, call.Method+" "+call.Path)
	}
	return out
}

// bodies lists the bodies of every call to a path, in order.
func (p *plane) bodies(method, path string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, call := range p.calls {
		if call.Method == method && call.Path == path {
			out = append(out, call.Body)
		}
	}
	return out
}

func notFound(what string) *api.Error {
	return &api.Error{Status: http.StatusNotFound, Detail: api.ErrorDetail{Code: api.CodeNotFound, Message: what + " does not exist"}}
}

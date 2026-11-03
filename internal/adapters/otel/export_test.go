package otel_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/otel"
)

// collector is a stand-in OTLP endpoint on loopback that keeps every body it is sent.
type collector struct {
	mu      sync.Mutex
	bodies  map[string][]string
	headers http.Header
	status  atomic.Int32
	server  *httptest.Server
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{bodies: map[string][]string{}}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies[r.URL.Path] = append(c.bodies[r.URL.Path], string(raw))
		c.headers = r.Header.Clone()
		c.mu.Unlock()
		if status := c.status.Load(); status != 0 {
			c.status.Store(0)
			w.WriteHeader(int(status))
			return
		}
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *collector) received(path string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.bodies[path]...)
}

func env(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// exportThrough runs a receiver whose requests go to an exporter, and returns once the exporter has
// sent everything.
func exportThrough(t *testing.T, config otel.ExportConfig, path, body string) {
	t.Helper()
	exporter := otel.NewExporter(config)
	ctx, cancel := context.WithCancel(t.Context())
	go exporter.Run(ctx)
	server, _ := receiver(t, exporter)
	if response := send(t, server.URL, path, body, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
	cancel()
	exporter.Wait()
}

// TestAStopDrainsWithinOneTimeout queues requests for a backend that never answers and stops the
// exporter: the drain gives up after one timeout in all, rather than one per request or none at all.
func TestAStopDrainsWithinOneTimeout(t *testing.T) {
	held := make(chan struct{})
	var heard atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		heard.Add(1)
		select {
		case <-held:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(held) })
	const timeout = 300 * time.Millisecond
	exporter := otel.NewExporter(otel.ExportConfig{Endpoints: map[otel.Signal]string{otel.Logs: server.URL + "/v1/logs"}, Timeout: timeout, Backoff: time.Millisecond})
	request, err := otel.Decode(otel.Logs, []byte(logsBody))
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		exporter.Forward(request)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	exporter.Run(ctx)
	if took := time.Since(started); took > 3*timeout {
		t.Errorf("a stop took %v against a backend that does not answer, with a timeout of %v", took, timeout)
	}
	if heard.Load() == 0 {
		t.Error("the drain sent nothing of what was queued")
	}
	if sent, _ := exporter.Counts(); sent != 0 {
		t.Errorf("%d requests counted as sent to a backend that never answered", sent)
	}
}

func TestExportDropsAccountAttributesByDefault(t *testing.T) {
	c := newCollector(t)
	config, ok, err := otel.ConfigFromEnv(env(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": c.server.URL}))
	if err != nil || !ok {
		t.Fatalf("config %v %v", ok, err)
	}
	exportThrough(t, config, "/v1/logs", logsBody)
	bodies := c.received("/v1/logs")
	if len(bodies) != 1 {
		t.Fatalf("%d bodies", len(bodies))
	}
	for _, key := range []string{"user.email", "user.account_uuid", "user.id", "organization.id", "someone@example.com"} {
		if strings.Contains(bodies[0], key) {
			t.Errorf("the export carries %s: %s", key, bodies[0])
		}
	}
	for _, key := range []string{"ropeshift.lane.id", "lane-1", "cache_read_tokens", "claude_code.api_request", "1793500000000000000"} {
		if !strings.Contains(bodies[0], key) {
			t.Errorf("the export lost %s: %s", key, bodies[0])
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &decoded); err != nil {
		t.Errorf("the export is not JSON: %v", err)
	}
	if got := c.headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("content type %q", got)
	}
}

func TestAProfileCanKeepAccountAttributesOnExport(t *testing.T) {
	c := newCollector(t)
	config, _, err := otel.ConfigFromEnv(env(map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": c.server.URL + "/custom/logs"}))
	if err != nil {
		t.Fatal(err)
	}
	config.KeepAccount = true
	exportThrough(t, config, "/v1/logs", logsBody)
	bodies := c.received("/custom/logs")
	if len(bodies) != 1 || !strings.Contains(bodies[0], "someone@example.com") || !strings.Contains(bodies[0], "organization.id") {
		t.Errorf("bodies %v", bodies)
	}
}

func TestExportConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		ok        bool
		refused   bool
		endpoints map[otel.Signal]string
		header    string
	}{
		{name: "nothing configured", env: map[string]string{}},
		{name: "headers alone send nothing", env: map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "a=b"}},
		{
			name: "a base endpoint", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "https://otel.example:4318/", "OTEL_EXPORTER_OTLP_HEADERS": "authorization=Bearer%20secret"},
			ok: true, header: "Bearer secret",
			endpoints: map[otel.Signal]string{otel.Traces: "https://otel.example:4318/v1/traces", otel.Metrics: "https://otel.example:4318/v1/metrics", otel.Logs: "https://otel.example:4318/v1/logs"},
		},
		{
			name: "a signal turned off", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "OTEL_LOGS_EXPORTER": "none"},
			ok: true, endpoints: map[otel.Signal]string{otel.Traces: "http://127.0.0.1:4318/v1/traces", otel.Metrics: "http://127.0.0.1:4318/v1/metrics"},
		},
		{name: "protobuf", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"}, refused: true},
		{name: "grpc for one signal", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "grpc"}, refused: true},
		{name: "json said out loud", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json"}, ok: true,
			endpoints: map[otel.Signal]string{otel.Traces: "http://127.0.0.1:4318/v1/traces", otel.Metrics: "http://127.0.0.1:4318/v1/metrics", otel.Logs: "http://127.0.0.1:4318/v1/logs"}},
		{name: "not a URL", env: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "otel.example:4318"}, refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, ok, err := otel.ConfigFromEnv(env(tt.env))
			if refused := errors.Is(err, otel.ErrProtocol) || err != nil; refused != tt.refused {
				t.Fatalf("error %v", err)
			}
			if ok != tt.ok {
				t.Fatalf("ok %v", ok)
			}
			for signal, want := range tt.endpoints {
				if config.Endpoints[signal] != want {
					t.Errorf("%s: %q", signal, config.Endpoints[signal])
				}
			}
			if len(config.Endpoints) != len(tt.endpoints) {
				t.Errorf("endpoints %v", config.Endpoints)
			}
			if tt.header != "" && config.Headers[otel.Logs]["authorization"] != tt.header {
				t.Errorf("headers %v", config.Headers)
			}
		})
	}
}

func TestNoEndpointNoExporter(t *testing.T) {
	if exporter := otel.NewExporter(otel.ExportConfig{}); exporter != nil {
		t.Fatal("an exporter with nowhere to send")
	}
	var exporter *otel.Exporter
	exporter.Forward(otel.Request{Signal: otel.Logs})
	exporter.Run(t.Context())
	exporter.Wait()
}

func TestExportRetriesWhatOTLPSaysToRetry(t *testing.T) {
	c := newCollector(t)
	c.status.Store(http.StatusServiceUnavailable)
	config, _, _ := otel.ConfigFromEnv(env(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": c.server.URL}))
	config.Backoff = time.Millisecond
	exporter := otel.NewExporter(config)
	ctx, cancel := context.WithCancel(t.Context())
	defer func() {
		cancel()
		exporter.Wait()
	}()
	go exporter.Run(ctx)
	server, _ := receiver(t, exporter)
	if response := send(t, server.URL, "/v1/metrics", metricsBody, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(c.received("/v1/metrics")) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if sent, _ := exporter.Counts(); len(c.received("/v1/metrics")) != 2 {
		t.Errorf("%d attempts and %d sent, want a refusal and a retry", len(c.received("/v1/metrics")), sent)
	}
}

// TestExportDialsOnlyItsEndpoint counts the connections the exporter opens through its one dial.
func TestExportDialsOnlyItsEndpoint(t *testing.T) {
	c := newCollector(t)
	var dials atomic.Int32
	var dialed sync.Map
	config, _, _ := otel.ConfigFromEnv(env(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": c.server.URL}))
	config.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		dialed.Store(address, true)
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	exportThrough(t, config, "/v1/traces", tracesBody)
	if dials.Load() == 0 {
		t.Fatal("the export opened no connection through its dial")
	}
	dialed.Range(func(address, _ any) bool {
		if !strings.HasSuffix(c.server.URL, address.(string)) {
			t.Errorf("dialed %s", address)
		}
		return true
	})
}

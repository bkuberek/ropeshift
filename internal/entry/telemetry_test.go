package entry

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/otel"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// A lane's request log, with the account attributes Claude Code's records carry. Every value is
// invented.
const laneLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"ropeshift.lane.id","value":{"stringValue":"lane-1"}},
  {"key":"user.email","value":{"stringValue":"someone@example.com"}},
  {"key":"organization.id","value":{"stringValue":"org-1"}}]},
  "scopeLogs":[{"logRecords":[{"timeUnixNano":"1793500000000000000","body":{"stringValue":"claude_code.api_request"},
  "attributes":[{"key":"input_tokens","value":{"intValue":"5"}}]}]}]}]}`

type telemetryRig struct {
	t      *testing.T
	paths  api.Paths
	dials  atomic.Int32
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newTelemetryRig(t *testing.T) *telemetryRig {
	t.Helper()
	dir := apitest.ShortDir(t)
	paths := api.Paths{StateDir: filepath.Join(dir, "state"), RunDir: filepath.Join(dir, "run")}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &telemetryRig{t: t, paths: paths}
}

// start runs the daemon's telemetry with this environment, and every connection the exporter opens
// goes through a dial that counts it, and fails the test when allowed is false.
func (r *telemetryRig) start(env map[string]string, allowed bool) *soloTelemetry {
	r.t.Helper()
	deps := telemetryDeps{
		Lookup: func(name string) (string, bool) {
			value, ok := env[name]
			return value, ok
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			r.dials.Add(1)
			if !allowed {
				r.t.Errorf("a connection opened to %s with nothing configured", address)
				return nil, io.ErrClosedPipe
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
		Now: time.Now,
	}
	telemetry, err := startTelemetry(r.t.Context(), r.paths, "127.0.0.1:0", deps, &r.stdout, &r.stderr)
	if err != nil {
		r.t.Fatal(err)
	}
	return telemetry
}

// sendAsALane posts what a lane's agent would, with the header its environment names.
func sendAsALane(t *testing.T, telemetry *soloTelemetry) {
	t.Helper()
	name, token, _ := strings.Cut(telemetry.lane.Header, "=")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, telemetry.lane.Endpoint+"/v1/logs", strings.NewReader(laneLogs))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(name, token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func emitOwn(telemetry *soloTelemetry) {
	telemetry.emitter.Span(ports.Span{Name: ledger.SpanLane, TraceID: ledger.WorkOrderTrace("wo"), SpanID: ledger.LaneSpan("lane-1"), Start: 1, End: 2})
	telemetry.emitter.Measure(ports.Measurement{Name: ledger.MetricCost, Kind: ports.MetricCounter, Unit: "USD", Value: 0.1, At: 2})
}

// TestNoConfigurationSendsNothingOffTheMachine holds NFR-PRIV-1 for telemetry: a lane's telemetry and
// ropeshift's own reach the local store, and with no exporter configured no connection opens. The
// dial fails the test the moment one does, and a rule test keeps any other code from dialing. The
// second half proves the dial sees a connection when one is configured.
func TestNoConfigurationSendsNothingOffTheMachine(t *testing.T) {
	r := newTelemetryRig(t)
	telemetry := r.start(map[string]string{"HOME": "/home/someone"}, false)
	if telemetry.lane.Endpoint == "" || !strings.HasPrefix(telemetry.lane.Endpoint, "http://127.0.0.1:") {
		t.Fatalf("lane telemetry %+v", telemetry.lane)
	}
	sendAsALane(t, telemetry)
	emitOwn(telemetry)
	if err := telemetry.emitter.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, err := telemetry.store.Records(t.Context(), ports.TelemetryQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	telemetry.close()
	if len(records) != 3 {
		t.Errorf("%d records in the local store", len(records))
	}
	if got := r.dials.Load(); got != 0 {
		t.Fatalf("%d connections opened with nothing configured", got)
	}
	if strings.Contains(r.stdout.String(), "export") {
		t.Errorf("the daemon says it exports: %s", r.stdout.String())
	}

	var mu sync.Mutex
	var bodies []string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		mu.Lock()
		bodies = append(bodies, req.URL.Path+" "+string(raw))
		mu.Unlock()
		_, _ = io.WriteString(w, "{}")
	}))
	defer collector.Close()
	configured := newTelemetryRig(t)
	telemetry = configured.start(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL}, true)
	sendAsALane(t, telemetry)
	emitOwn(telemetry)
	if err := telemetry.emitter.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	telemetry.close()
	if configured.dials.Load() == 0 {
		t.Fatal("a configured exporter opened no connection, so the watch above proves nothing")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("the collector heard %d requests: %v", len(bodies), bodies)
	}
	for _, body := range bodies {
		if strings.Contains(body, "someone@example.com") || strings.Contains(body, "organization.id") {
			t.Errorf("an export carries an account attribute: %s", body)
		}
	}
	if !strings.Contains(configured.stdout.String(), "telemetry export") {
		t.Errorf("the daemon does not say where it exports: %s", configured.stdout.String())
	}
}

// TestAStopExportsWhatItLastFlushed emits ropeshift's own telemetry and closes at once: the emitter's
// last flush still reaches a configured exporter, because the exporter drains after it.
func TestAStopExportsWhatItLastFlushed(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
		mu.Unlock()
		_, _ = io.WriteString(w, "{}")
	}))
	defer collector.Close()
	r := newTelemetryRig(t)
	telemetry := r.start(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL}, true)
	emitOwn(telemetry)
	telemetry.close()
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(paths, "/v1/traces") || !slices.Contains(paths, "/v1/metrics") {
		t.Errorf("the collector heard %v of what the last flush wrote", paths)
	}
}

func TestAnExporterThisBuildCannotHonorStopsTheDaemon(t *testing.T) {
	r := newTelemetryRig(t)
	deps := telemetryDeps{
		Lookup: func(name string) (string, bool) {
			value, ok := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4317", "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}[name]
			return value, ok
		},
		Now: time.Now,
	}
	if _, err := startTelemetry(t.Context(), r.paths, "127.0.0.1:0", deps, &r.stdout, &r.stderr); err == nil || !strings.Contains(err.Error(), otel.ProtocolJSON) {
		t.Errorf("error %v", err)
	}
}

func TestTheReceiverTokenSurvivesARestart(t *testing.T) {
	r := newTelemetryRig(t)
	first := r.start(nil, false)
	header := first.lane.Header
	first.close()
	second := r.start(nil, false)
	defer second.close()
	if second.lane.Header != header || !strings.HasPrefix(header, otel.HeaderToken+"=") {
		t.Errorf("headers %q and %q", header, second.lane.Header)
	}
	info, err := os.Stat(filepath.Join(r.paths.StateDir, telemetryTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the token file is %v", info.Mode().Perm())
	}
}

func TestTheReceiverCanBeTurnedOff(t *testing.T) {
	r := newTelemetryRig(t)
	telemetry, err := startTelemetry(t.Context(), r.paths, api.WebOff, telemetryDeps{Lookup: func(string) (string, bool) { return "", false }, Now: time.Now}, &r.stdout, &r.stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer telemetry.close()
	if telemetry.lane.Endpoint != "" || telemetry.receiver != nil {
		t.Errorf("a receiver turned off still serves: %+v", telemetry.lane)
	}
}

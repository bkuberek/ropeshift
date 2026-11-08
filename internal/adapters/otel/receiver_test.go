package otel_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/otel"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/ports"
)

const token = "0123456789abcdef"

// The shape of what a Claude Code lane sends, with the tags its environment set and the account
// attributes its log records carry. Every value is invented.
const (
	resource = `{"attributes":[
  {"key":"service.name","value":{"stringValue":"claude-code"}},
  {"key":"ropeshift.lane.id","value":{"stringValue":"lane-1"}},
  {"key":"ropeshift.work_order.id","value":{"stringValue":"wo-1"}},
  {"key":"ropeshift.generation","value":{"stringValue":"2"}},
  {"key":"user.email","value":{"stringValue":"someone@example.com"}},
  {"key":"user.account_uuid","value":{"stringValue":"00000000-0000-0000-0000-000000000001"}},
  {"key":"organization.id","value":{"stringValue":"00000000-0000-0000-0000-000000000002"}}]}`
	logsBody = `{"resourceLogs":[{"resource":` + resource + `,"scopeLogs":[{"scope":{"name":"com.anthropic.claude_code.events"},
  "logRecords":[{"timeUnixNano":"1793500000000000000","body":{"stringValue":"claude_code.api_request"},
  "attributes":[{"key":"event.name","value":{"stringValue":"api_request"}},{"key":"user.id","value":{"stringValue":"anon"}},
  {"key":"input_tokens","value":{"intValue":"12"}},{"key":"cache_read_tokens","value":{"intValue":3000}},
  {"key":"cache_creation_tokens","value":{"stringValue":"500"}},{"key":"output_tokens","value":{"intValue":"40"}},
  {"key":"cost_usd","value":{"doubleValue":0.01}}]}]}]}]}`
	metricsBody = `{"resourceMetrics":[{"resource":` + resource + `,"scopeMetrics":[{"scope":{"name":"com.anthropic.claude_code"},
  "metrics":[{"name":"claude_code.cost.usage","unit":"USD","sum":{"aggregationTemporality":1,"isMonotonic":true,
  "dataPoints":[{"timeUnixNano":"1793500001000000000","asDouble":0.01,"attributes":[{"key":"model","value":{"stringValue":"m"}}]}]}},
  {"name":"claude_code.token.usage","unit":"tokens","sum":{"aggregationTemporality":1,"isMonotonic":true,
  "dataPoints":[{"timeUnixNano":"1793500001000000000","asInt":"40","attributes":[{"key":"type","value":{"stringValue":"output"}}]}]}}]}]}]}`
	tracesBody = `{"resourceSpans":[{"resource":` + resource + `,"scopeSpans":[{"scope":{"name":"x"},"spans":[
  {"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","name":"claude_code.interaction","kind":1,
  "startTimeUnixNano":"1793500000000000000","endTimeUnixNano":"1793500002000000000","attributes":[]}]}]}]}`
)

type forwarded struct {
	mu       sync.Mutex
	requests []otel.Request
}

func (f *forwarded) Forward(request otel.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
}

func memoryStore(t *testing.T) *sqlite.TelemetryStore {
	t.Helper()
	store, err := sqlite.OpenMemoryTelemetry()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store
}

func receiver(t *testing.T, forward otel.Forwarder) (*httptest.Server, *sqlite.TelemetryStore) {
	t.Helper()
	store := memoryStore(t)
	server := httptest.NewServer(otel.NewReceiver(otel.ReceiverConfig{
		Store: store, Forward: forward, Token: token, Now: func() time.Time { return time.UnixMilli(1793500005000) },
	}))
	t.Cleanup(server.Close)
	return server, store
}

// send is a synthetic OTLP client: one POST, as an exporter writes it.
func send(t *testing.T, url, path, body string, change func(*http.Request)) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(otel.HeaderToken, token)
	if change != nil {
		change(request)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestTheReceiverStoresWhatALaneSends(t *testing.T) {
	forward := &forwarded{}
	server, store := receiver(t, forward)
	for path, body := range map[string]string{"/v1/logs": logsBody, "/v1/metrics": metricsBody, "/v1/traces": tracesBody} {
		if response := send(t, server.URL, path, body, nil); response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, response.StatusCode)
		}
	}
	records, err := store.Records(t.Context(), ports.TelemetryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("%d records: %+v", len(records), records)
	}
	byName := map[string]ports.TelemetryRecord{}
	for _, record := range records {
		byName[record.Name] = record
		if record.Lane != "lane-1" || record.WorkOrder != "wo-1" || record.Generation != 2 || record.ReceivedAt != 1793500005000 {
			t.Errorf("record %s is not tagged: %+v", record.Name, record)
		}
	}
	request := byName["claude_code.api_request"]
	if request.Signal != ports.SignalLog || request.Value == nil || *request.Value != 3512 || request.ObservedAt != 1793500000000 {
		t.Errorf("api request %+v", request)
	}
	if request.Attributes["user.email"] != "someone@example.com" || request.Attributes["user.id"] != "anon" {
		t.Errorf("the local store lost the account attributes: %v", request.Attributes)
	}
	if tokens := byName["claude_code.token.usage"]; tokens.Value == nil || *tokens.Value != 40 || tokens.Detail["temporality"] != "delta" {
		t.Errorf("tokens %+v", tokens)
	}
	if span := byName["claude_code.interaction"]; span.Signal != ports.SignalSpan || span.Detail["duration_ms"] != float64(2000) {
		t.Errorf("span %+v", span)
	}
	lanes, err := store.Lanes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(lanes) != 1 || lanes[0].ContextTokens != 3512 || lanes[0].Tokens["output"] != 40 || lanes[0].CostUSD != 0.01 {
		t.Errorf("lanes %+v", lanes)
	}
	if len(forward.requests) != 3 {
		t.Errorf("%d requests forwarded", len(forward.requests))
	}
}

func TestTheReceiverTakesGzip(t *testing.T) {
	server, store := receiver(t, nil)
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(logsBody))
	_ = writer.Close()
	response := send(t, server.URL, "/v1/logs", compressed.String(), func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
	if records, _ := store.Records(t.Context(), ports.TelemetryQuery{}); len(records) != 1 {
		t.Errorf("%d records", len(records))
	}
}

func TestTheReceiverRefusesWhatIsNotALanesTelemetry(t *testing.T) {
	server, store := receiver(t, nil)
	tests := []struct {
		name   string
		path   string
		change func(*http.Request)
		status int
	}{
		{"no token", "/v1/logs", func(r *http.Request) { r.Header.Del(otel.HeaderToken) }, http.StatusUnauthorized},
		{"a wrong token", "/v1/logs", func(r *http.Request) { r.Header.Set(otel.HeaderToken, "guess") }, http.StatusUnauthorized},
		{"protobuf", "/v1/logs", func(r *http.Request) { r.Header.Set("Content-Type", "application/x-protobuf") }, http.StatusUnsupportedMediaType},
		{"a page in a browser", "/v1/logs", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, http.StatusForbidden},
		{"a rebound host", "/v1/logs", func(r *http.Request) { r.Host = "evil.example:7318" }, http.StatusForbidden},
		{"an unknown path", "/v1/profiles", nil, http.StatusNotFound},
		{"a read", "/v1/logs", func(r *http.Request) { r.Method = http.MethodGet }, http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if response := send(t, server.URL, tt.path, logsBody, tt.change); response.StatusCode != tt.status {
				t.Errorf("status %d, want %d", response.StatusCode, tt.status)
			}
		})
	}
	if response := send(t, server.URL, "/v1/logs", "{not json", nil); response.StatusCode != http.StatusBadRequest {
		t.Errorf("bad JSON: status %d", response.StatusCode)
	}
	if response := send(t, server.URL, "/v1/logs", metricsBody, nil); response.StatusCode != http.StatusBadRequest {
		t.Errorf("metrics on the logs path: status %d", response.StatusCode)
	}
	if records, _ := store.Records(t.Context(), ports.TelemetryQuery{}); len(records) != 0 {
		t.Errorf("a refused request left %d records", len(records))
	}
}

// failingStore is a store that cannot write.
type failingStore struct{}

func (failingStore) Append(context.Context, int64, []ports.TelemetryRecord) error {
	return context.DeadlineExceeded
}

func TestAStoreThatCannotWriteAsksTheSenderToRetry(t *testing.T) {
	server := httptest.NewServer(otel.NewReceiver(otel.ReceiverConfig{Store: failingStore{}, Token: token}))
	t.Cleanup(server.Close)
	if response := send(t, server.URL, "/v1/logs", logsBody, nil); response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status %d", response.StatusCode)
	}
}

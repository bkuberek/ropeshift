package otel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Dial opens one connection. Every connection the exporter opens goes through it, and a test that
// counts its calls can say that none opened.
type Dial func(ctx context.Context, network, address string) (net.Conn, error)

// The exporter's bounds. A full queue drops the newest request, because telemetry is lossy and the
// worker's work must never wait on a backend.
const (
	exportQueue    = 256
	exportTimeout  = 10 * time.Second
	exportAttempts = 3
	reportEvery    = 30 * time.Second
)

// ProtocolJSON is the one protocol the exporter speaks in phase 1 (D3).
const ProtocolJSON = "http/json"

// ExportConfig is where telemetry goes. An empty Endpoints sends nothing.
type ExportConfig struct {
	// Endpoints is each signal's full URL.
	Endpoints map[Signal]string
	// Headers is each signal's headers.
	Headers map[Signal]map[string]string
	// KeepAccount keeps account identifiers on export, which a profile must ask for (D3).
	KeepAccount bool
	Dial        Dial
	Report      func(error)
	// Backoff is the wait before a retry. Zero takes one second.
	Backoff time.Duration
	// Timeout bounds one request, and the whole drain when the exporter stops. Zero takes ten seconds.
	Timeout time.Duration
}

// ErrProtocol refuses an exporter configured for a protocol it does not speak.
var ErrProtocol = errors.New("ropeshift exports OTLP over HTTP with JSON")

// ConfigFromEnv reads the standard `OTEL_EXPORTER_OTLP_*` settings, with per-signal overrides. A
// base endpoint gets each signal's path appended, and a per-signal endpoint is used as it is. With no
// endpoint at all it reports false, and nothing is ever sent. A signal whose `OTEL_*_EXPORTER` is
// `none` is not sent.
func ConfigFromEnv(lookup func(string) (string, bool)) (ExportConfig, bool, error) {
	get := func(name string) string {
		value, _ := lookup(name)
		return strings.TrimSpace(value)
	}
	config := ExportConfig{Endpoints: map[Signal]string{}, Headers: map[Signal]map[string]string{}}
	base := get("OTEL_EXPORTER_OTLP_ENDPOINT")
	shared, err := parseHeaders(get("OTEL_EXPORTER_OTLP_HEADERS"))
	if err != nil {
		return ExportConfig{}, false, err
	}
	for _, signal := range Signals() {
		upper := strings.ToUpper(string(signal))
		if get("OTEL_"+upper+"_EXPORTER") == "none" {
			continue
		}
		endpoint := get("OTEL_EXPORTER_OTLP_" + upper + "_ENDPOINT")
		if endpoint == "" && base != "" {
			endpoint = strings.TrimSuffix(base, "/") + "/v1/" + string(signal)
		}
		if endpoint == "" {
			continue
		}
		protocol := get("OTEL_EXPORTER_OTLP_" + upper + "_PROTOCOL")
		if protocol == "" {
			protocol = get("OTEL_EXPORTER_OTLP_PROTOCOL")
		}
		if protocol != "" && protocol != ProtocolJSON {
			return ExportConfig{}, false, fmt.Errorf("%w, and %s is set to %q: set it to %s or leave it unset", ErrProtocol, "the protocol for "+string(signal), protocol, ProtocolJSON)
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return ExportConfig{}, false, fmt.Errorf("the %s endpoint is not an http or https URL", signal)
		}
		own, err := parseHeaders(get("OTEL_EXPORTER_OTLP_" + upper + "_HEADERS"))
		if err != nil {
			return ExportConfig{}, false, err
		}
		headers := map[string]string{}
		for name, value := range shared {
			headers[name] = value
		}
		for name, value := range own {
			headers[name] = value
		}
		config.Endpoints[signal], config.Headers[signal] = endpoint, headers
	}
	return config, len(config.Endpoints) > 0, nil
}

// parseHeaders reads `key=value` pairs separated by commas, with each part percent-decoded, as the
// OpenTelemetry environment specification writes them.
func parseHeaders(text string) (map[string]string, error) {
	out := map[string]string{}
	if text == "" {
		return out, nil
	}
	for pair := range strings.SplitSeq(text, ",") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, errors.New("an OTLP header has no value")
		}
		name, err := url.QueryUnescape(strings.TrimSpace(key))
		if err != nil {
			return nil, err
		}
		decoded, err := url.PathUnescape(strings.TrimSpace(value))
		if err != nil {
			return nil, err
		}
		out[name] = decoded
	}
	return out, nil
}

// Hosts names where a configuration sends, for the line a daemon prints at start.
func (c ExportConfig) Hosts() []string {
	var out []string
	for _, signal := range Signals() {
		if endpoint, ok := c.Endpoints[signal]; ok {
			if parsed, err := url.Parse(endpoint); err == nil {
				out = append(out, string(signal)+" to "+parsed.Host)
			}
		}
	}
	return out
}

// Exporter forwards requests to the configured endpoints, one at a time, in the background (FR-OBS-4).
// Account identifiers are dropped first unless the configuration keeps them.
type Exporter struct {
	config ExportConfig
	client *http.Client
	queue  chan Request
	done   chan struct{}

	mu       sync.Mutex
	dropped  int64
	sent     int64
	reported time.Time
}

// NewExporter starts an exporter. It returns nil for a configuration with no endpoint, and a nil
// exporter forwards nothing.
func NewExporter(config ExportConfig) *Exporter {
	if len(config.Endpoints) == 0 {
		return nil
	}
	if config.Timeout <= 0 {
		config.Timeout = exportTimeout
	}
	dial := config.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: config.Timeout}).DialContext
	}
	if config.Report == nil {
		config.Report = func(error) {}
	}
	if config.Backoff == 0 {
		config.Backoff = time.Second
	}
	e := &Exporter{
		config: config,
		client: &http.Client{
			Timeout:       config.Timeout,
			Transport:     &http.Transport{DialContext: dial, ResponseHeaderTimeout: config.Timeout, Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		queue: make(chan Request, exportQueue),
		done:  make(chan struct{}),
	}
	return e
}

// Forward queues one request. It never blocks: a full queue drops the request and counts it.
func (e *Exporter) Forward(request Request) {
	if e == nil {
		return
	}
	if _, ok := e.config.Endpoints[request.Signal]; !ok {
		return
	}
	select {
	case e.queue <- request:
	default:
		e.mu.Lock()
		e.dropped++
		e.mu.Unlock()
	}
}

// Run sends what is queued until the context ends, then sends what is left within one timeout. A
// request already on its way finishes under the client's own timeout, and is not retried once the
// context has ended.
func (e *Exporter) Run(ctx context.Context) {
	if e == nil {
		return
	}
	defer close(e.done)
	for {
		// A stop comes before the queue, because a select picks among ready cases at random, and each
		// request sent outside the drain could hold a stop one more timeout.
		if ctx.Err() != nil {
			e.drain()
			return
		}
		select {
		case request := <-e.queue:
			e.send(ctx, context.WithoutCancel(ctx), request)
		case <-ctx.Done():
			e.drain()
			return
		}
	}
}

// drain sends what is queued within one timeout in all, so a backend that does not answer holds a stop
// that long and no longer, and what is left is dropped.
func (e *Exporter) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), e.config.Timeout)
	defer cancel()
	for ctx.Err() == nil {
		select {
		case request := <-e.queue:
			e.send(ctx, ctx, request)
		default:
			return
		}
	}
}

// Wait returns once Run has stopped.
func (e *Exporter) Wait() {
	if e != nil {
		<-e.done
	}
}

// Counts says how many requests went out and how many the queue dropped.
func (e *Exporter) Counts() (sent, dropped int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sent, e.dropped
}

// send posts one request under postCtx, and retries while ctx lasts. A request on its way while Run is
// told to stop finishes under the client's timeout, and a request of the drain under the drain's.
func (e *Exporter) send(ctx, postCtx context.Context, request Request) {
	if !e.config.KeepAccount {
		request = request.WithoutAccount()
	}
	body, err := request.Encode()
	if err != nil {
		e.fail(err)
		return
	}
	for attempt := 1; ; attempt++ {
		retry, err := e.post(postCtx, request.Signal, body)
		if err == nil {
			e.mu.Lock()
			e.sent++
			e.mu.Unlock()
			return
		}
		if !retry || attempt == exportAttempts || ctx.Err() != nil {
			e.fail(err)
			return
		}
		select {
		case <-ctx.Done():
			e.fail(err)
			return
		case <-time.After(e.config.Backoff * time.Duration(attempt)):
		}
	}
}

// post sends one request and says whether a failure may be retried: OTLP retries 429, 502, 503, and
// 504, and a connection that failed, and nothing else.
func (e *Exporter) post(ctx context.Context, signal Signal, body []byte) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.config.Endpoints[signal], bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range e.config.Headers[signal] {
		request.Header.Set(name, value)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return true, fmt.Errorf("telemetry export of %s: the endpoint did not answer", signal)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return false, nil
	case response.StatusCode == http.StatusTooManyRequests, response.StatusCode == http.StatusBadGateway,
		response.StatusCode == http.StatusServiceUnavailable, response.StatusCode == http.StatusGatewayTimeout:
		return true, fmt.Errorf("telemetry export of %s: status %d", signal, response.StatusCode)
	}
	return false, fmt.Errorf("telemetry export of %s: status %d", signal, response.StatusCode)
}

// fail reports at most once per interval, because a backend that is down fails every request.
func (e *Exporter) fail(err error) {
	e.mu.Lock()
	quiet := time.Since(e.reported) < reportEvery
	if !quiet {
		e.reported = time.Now()
	}
	e.mu.Unlock()
	if !quiet {
		e.config.Report(err)
	}
}

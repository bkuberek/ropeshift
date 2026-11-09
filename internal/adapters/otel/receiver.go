package otel

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// HeaderToken is the header a lane's agent sends, set through `OTEL_EXPORTER_OTLP_HEADERS`. The
// receiver listens on loopback, where any local process could reach it, and the token is what keeps
// its data to the lanes this worker started.
const HeaderToken = "x-ropeshift-telemetry"

// Body bounds. A request is a batch of one export interval, and a gzip body expands only so far.
const (
	maxRequestBytes = 8 << 20
	maxDecodedBytes = 32 << 20
)

// Store is where the receiver keeps what it takes.
type Store interface {
	Append(ctx context.Context, receivedAt int64, records []ports.TelemetryRecord) error
}

// Forwarder sends a request on. A nil Forwarder sends nothing, which is the default.
type Forwarder interface {
	Forward(request Request)
}

// Receiver is the worker's embedded OTLP/HTTP receiver (FR-OBS-3). It takes JSON on the three
// standard paths, keeps every record in the local store, and hands the request to the exporter when
// one is configured. It answers a request it cannot take with the status OTLP names, so a sender
// neither retries what can never succeed nor drops what could.
type Receiver struct {
	store   Store
	forward Forwarder
	token   string
	now     func() time.Time
	report  func(error)
}

// ReceiverConfig is what a receiver runs on.
type ReceiverConfig struct {
	Store   Store
	Forward Forwarder
	// Token is what HeaderToken must carry. Empty refuses every request, because a receiver that
	// anything local could write to would let a stranger's numbers into `top`.
	Token  string
	Now    func() time.Time
	Report func(error)
}

// NewReceiver returns a receiver.
func NewReceiver(config ReceiverConfig) *Receiver {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Report == nil {
		config.Report = func(error) {}
	}
	return &Receiver{store: config.Store, forward: config.Forward, token: config.Token, now: config.Now, report: config.Report}
}

func (r *Receiver) signalOf(path string) (Signal, bool) {
	for _, signal := range Signals() {
		if path == "/v1/"+string(signal) {
			return signal, true
		}
	}
	return "", false
}

// ServeHTTP takes one export request.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	signal, ok := r.signalOf(req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !loopbackHost(req.Host) || req.Header.Get("Origin") != "" {
		http.Error(w, "this receiver takes telemetry from this machine's lanes", http.StatusForbidden)
		return
	}
	if r.token == "" || subtle.ConstantTimeCompare([]byte(req.Header.Get(HeaderToken)), []byte(r.token)) != 1 {
		http.Error(w, "no lane token", http.StatusUnauthorized)
		return
	}
	if media, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || media != "application/json" {
		http.Error(w, "this receiver takes OTLP over HTTP with JSON", http.StatusUnsupportedMediaType)
		return
	}
	raw, err := readBody(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request, err := Decode(signal, raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := r.store.Append(req.Context(), r.now().UnixMilli(), Flatten(request)); err != nil {
		r.report(fmt.Errorf("telemetry store: %w", err))
		http.Error(w, "the store cannot take this now", http.StatusServiceUnavailable)
		return
	}
	if r.forward != nil {
		r.forward.Forward(request)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, "{}")
}

func readBody(req *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(req.Body, maxRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxRequestBytes {
		return nil, errors.New("the request is over the receiver's limit")
	}
	switch strings.ToLower(req.Header.Get("Content-Encoding")) {
	case "", "identity":
		return raw, nil
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		decoded, err := io.ReadAll(io.LimitReader(reader, maxDecodedBytes+1))
		if err != nil {
			return nil, err
		}
		if len(decoded) > maxDecodedBytes {
			return nil, errors.New("the request expands past the receiver's limit")
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("content encoding %q", req.Header.Get("Content-Encoding"))
}

// loopbackHost refuses a request that names another host, which is what a rebinding page sends.
func loopbackHost(host string) bool {
	name, _, err := net.SplitHostPort(host)
	if err != nil {
		name = host
	}
	if name == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(name, "[]"))
	return ip != nil && ip.IsLoopback()
}

package fake

import (
	"context"
	"slices"
	"sync"

	"ropeshift.dev/ropeshift/internal/ports"
)

// Telemetry keeps what it is given, in order, and answers queries from it.
type Telemetry struct {
	mu           sync.Mutex
	spans        []ports.Span
	measurements []ports.Measurement
	records      []ports.TelemetryRecord
	lanes        []ports.LaneTelemetry
}

// NewTelemetry returns an empty sink.
func NewTelemetry() *Telemetry { return &Telemetry{} }

// Span keeps one span.
func (t *Telemetry) Span(span ports.Span) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.spans = append(t.spans, span)
}

// Measure keeps one measurement.
func (t *Telemetry) Measure(measurement ports.Measurement) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.measurements = append(t.measurements, measurement)
}

// Spans lists the spans it was given, optionally those of one name.
func (t *Telemetry) Spans(name string) []ports.Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []ports.Span
	for _, span := range t.spans {
		if name == "" || span.Name == name {
			out = append(out, span)
		}
	}
	return out
}

// Measurements lists the measurements it was given, optionally those of one metric.
func (t *Telemetry) Measurements(name string) []ports.Measurement {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []ports.Measurement
	for _, m := range t.measurements {
		if name == "" || m.Name == name {
			out = append(out, m)
		}
	}
	return out
}

// Hold sets what the reader returns.
func (t *Telemetry) Hold(records []ports.TelemetryRecord, lanes []ports.LaneTelemetry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.records, t.lanes = slices.Clone(records), slices.Clone(lanes)
}

// Records filters what it holds the way the store does, newest first.
func (t *Telemetry) Records(ctx context.Context, query ports.TelemetryQuery) ([]ports.TelemetryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []ports.TelemetryRecord
	for _, record := range slices.Backward(t.records) {
		if (query.Signal == "" || record.Signal == query.Signal) && (query.Name == "" || record.Name == query.Name) &&
			(query.Lane == "" || record.Lane == query.Lane) && record.ObservedAt >= query.Since {
			out = append(out, record)
		}
		if query.Limit > 0 && len(out) == query.Limit {
			break
		}
	}
	return out, nil
}

// Lanes returns what it holds.
func (t *Telemetry) Lanes(ctx context.Context) ([]ports.LaneTelemetry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.lanes), nil
}

var (
	_ ports.Telemetry       = (*Telemetry)(nil)
	_ ports.TelemetryReader = (*Telemetry)(nil)
)

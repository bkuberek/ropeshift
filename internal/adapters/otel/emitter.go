package otel

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/ports"
)

// The emitter's bounds. What waits past the cap is dropped and counted, because a stalled store must
// not grow the worker's memory.
const (
	emitCap   = 8192
	scopeName = "ropeshift"
)

// Histogram bounds by unit. One observation is one data point, so each carries one count.
var (
	secondsBounds = []float64{1, 5, 15, 60, 300, 900, 3600, 14400, 86400}
	millisBounds  = []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 5000}
)

// Emitter is ropeshift's own telemetry: it keeps spans and measurements in memory and, on each flush,
// writes them to the local store as one request per signal and forwards those requests to the
// exporter. A crash loses at most one interval, which telemetry may; the ledger loses nothing.
type Emitter struct {
	resource []ports.Attribute
	store    Store
	forward  Forwarder
	now      func() time.Time
	report   func(error)

	mu           sync.Mutex
	spans        []ports.Span
	measurements []ports.Measurement
	dropped      int64
}

// EmitterConfig is what an emitter runs on.
type EmitterConfig struct {
	// Resource names this process: its service and its worker.
	Resource []ports.Attribute
	Store    Store
	Forward  Forwarder
	Now      func() time.Time
	Report   func(error)
}

// NewEmitter returns an emitter.
func NewEmitter(config EmitterConfig) *Emitter {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Report == nil {
		config.Report = func(error) {}
	}
	return &Emitter{resource: config.Resource, store: config.Store, forward: config.Forward, now: config.Now, report: config.Report}
}

// Span keeps one span until the next flush.
func (e *Emitter) Span(span ports.Span) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.spans)+len(e.measurements) >= emitCap {
		e.dropped++
		return
	}
	e.spans = append(e.spans, span)
}

// Measure keeps one measurement until the next flush.
func (e *Emitter) Measure(measurement ports.Measurement) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.spans)+len(e.measurements) >= emitCap {
		e.dropped++
		return
	}
	e.measurements = append(e.measurements, measurement)
}

// Dropped counts what the cap turned away.
func (e *Emitter) Dropped() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dropped
}

// Flush writes what waits.
func (e *Emitter) Flush(ctx context.Context) error {
	e.mu.Lock()
	spans, measurements := e.spans, e.measurements
	e.spans, e.measurements = nil, nil
	e.mu.Unlock()
	var requests []Request
	if len(spans) > 0 {
		requests = append(requests, e.traces(spans))
	}
	if len(measurements) > 0 {
		requests = append(requests, e.metrics(measurements))
	}
	for _, request := range requests {
		if e.store != nil {
			if err := e.store.Append(ctx, e.now().UnixMilli(), Flatten(request)); err != nil {
				return err
			}
		}
		if e.forward != nil {
			e.forward.Forward(request)
		}
	}
	return nil
}

// Run flushes on each interval until the context ends, and once more after.
func (e *Emitter) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := e.Flush(context.WithoutCancel(ctx)); err != nil {
				e.report(err)
			}
			return
		case <-ticker.C:
			if err := e.Flush(ctx); err != nil {
				e.report(err)
			}
		}
	}
}

func (e *Emitter) envelope(signal Signal, items []any) Request {
	return Request{Signal: signal, Body: map[string]any{
		signal.resourceKey(): []any{map[string]any{
			"resource": map[string]any{"attributes": encodeAttributes(e.resource)},
			signal.scopeKey(): []any{map[string]any{
				"scope":          map[string]any{"name": scopeName},
				signal.itemKey(): items,
			}},
		}},
	}}
}

// spanKinds are OTLP's numbers for each kind.
var spanKinds = map[ports.SpanKind]int64{ports.SpanInternal: 1, ports.SpanClient: 3, ports.SpanProducer: 4, ports.SpanConsumer: 5}

func (e *Emitter) traces(spans []ports.Span) Request {
	items := make([]any, 0, len(spans))
	for _, span := range spans {
		kind := spanKinds[span.Kind]
		if kind == 0 {
			kind = 1
		}
		status := map[string]any{"code": int64(1)}
		if span.Failed {
			status["code"] = int64(2)
		}
		item := map[string]any{
			"traceId": span.TraceID, "spanId": span.SpanID, "name": span.Name, "kind": kind,
			"startTimeUnixNano": unixNano(span.Start), "endTimeUnixNano": unixNano(max(span.End, span.Start)),
			"attributes": encodeAttributes(span.Attributes), "status": status,
		}
		if span.ParentID != "" {
			item["parentSpanId"] = span.ParentID
		}
		if len(span.Links) > 0 {
			links := make([]any, 0, len(span.Links))
			for _, link := range span.Links {
				links = append(links, map[string]any{"traceId": link.TraceID, "spanId": link.SpanID})
			}
			item["links"] = links
		}
		items = append(items, item)
	}
	return e.envelope(Traces, items)
}

// metrics writes each measurement as its own data point, under one metric per name. A counter's
// point is a delta, so a backend sums them, and a gauge's is a reading.
func (e *Emitter) metrics(measurements []ports.Measurement) Request {
	type key struct {
		name string
		kind ports.MetricKind
		unit string
	}
	var order []key
	points := map[key][]any{}
	for _, m := range measurements {
		k := key{m.Name, m.Kind, m.Unit}
		if _, seen := points[k]; !seen {
			order = append(order, k)
		}
		points[k] = append(points[k], point(m))
	}
	items := make([]any, 0, len(order))
	for _, k := range order {
		metric := map[string]any{"name": k.name, "unit": k.unit}
		switch k.kind {
		case ports.MetricGauge:
			metric["gauge"] = map[string]any{"dataPoints": points[k]}
		case ports.MetricHistogram:
			metric["histogram"] = map[string]any{"dataPoints": points[k], "aggregationTemporality": int64(1)}
		default:
			metric["sum"] = map[string]any{"dataPoints": points[k], "aggregationTemporality": int64(1), "isMonotonic": k.kind == ports.MetricCounter}
		}
		items = append(items, metric)
	}
	return e.envelope(Metrics, items)
}

func point(m ports.Measurement) map[string]any {
	out := map[string]any{"timeUnixNano": unixNano(m.At), "startTimeUnixNano": unixNano(m.At), "attributes": encodeAttributes(m.Attributes)}
	if m.Kind != ports.MetricHistogram {
		out["asDouble"] = m.Value
		return out
	}
	bounds := secondsBounds
	if m.Unit == "ms" {
		bounds = millisBounds
	}
	counts := make([]any, len(bounds)+1)
	for i := range counts {
		counts[i] = "0"
	}
	bucket, _ := slices.BinarySearch(bounds, m.Value)
	counts[bucket] = "1"
	explicit := make([]any, len(bounds))
	for i, bound := range bounds {
		explicit[i] = bound
	}
	out["count"], out["sum"], out["bucketCounts"], out["explicitBounds"] = "1", m.Value, counts, explicit
	return out
}

func unixNano(ms int64) string { return strconv.FormatInt(ms*int64(time.Millisecond), 10) }

// encodeAttributes writes OTLP's key-value list. An int64 travels as a string, as the JSON mapping
// writes every 64-bit integer.
func encodeAttributes(attributes []ports.Attribute) []any {
	out := make([]any, 0, len(attributes))
	for _, attribute := range attributes {
		var value map[string]any
		switch v := attribute.Value.(type) {
		case string:
			value = map[string]any{"stringValue": v}
		case int:
			value = map[string]any{"intValue": strconv.Itoa(v)}
		case int64:
			value = map[string]any{"intValue": strconv.FormatInt(v, 10)}
		case float64:
			value = map[string]any{"doubleValue": v}
		case bool:
			value = map[string]any{"boolValue": v}
		default:
			continue
		}
		out = append(out, map[string]any{"key": attribute.Key, "value": value})
	}
	return out
}

var _ ports.Telemetry = (*Emitter)(nil)

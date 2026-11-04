// Package otel is ropeshift's OpenTelemetry adapter (ADR-0004): the worker's embedded OTLP receiver,
// the exporter that forwards to an endpoint the workspace configured, and the emitter of ropeshift's
// own spans and metrics. It speaks OTLP over HTTP with JSON, which is what the driver tells every
// phase 1 sender to use (D3), so it needs no protobuf or gRPC code in a binary whose hook start is
// measured (NFR-PERF-2).
package otel

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// Signal is one of OTLP's three signals, as its URL path names it.
type Signal string

// The signals.
const (
	Traces  Signal = "traces"
	Metrics Signal = "metrics"
	Logs    Signal = "logs"
)

// Signals lists them in a fixed order.
func Signals() []Signal { return []Signal{Traces, Metrics, Logs} }

// resourceKey is the top-level field of each signal's request.
func (s Signal) resourceKey() string {
	switch s {
	case Traces:
		return "resourceSpans"
	case Metrics:
		return "resourceMetrics"
	}
	return "resourceLogs"
}

func (s Signal) scopeKey() string {
	switch s {
	case Traces:
		return "scopeSpans"
	case Metrics:
		return "scopeMetrics"
	}
	return "scopeLogs"
}

func (s Signal) itemKey() string {
	switch s {
	case Traces:
		return "spans"
	case Metrics:
		return "metrics"
	}
	return "logRecords"
}

// Request is one export request, held as decoded JSON. What is forwarded differs from what arrived
// only by the attributes export drops, so vendor telemetry stays uninterpreted (ADR-0015).
type Request struct {
	Signal Signal
	Body   map[string]any
}

// Decode reads one OTLP/HTTP JSON request body. Numbers stay as they were written, because a 64-bit
// integer travels as a string or a number and neither may lose a digit.
func Decode(signal Signal, raw []byte) (Request, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		return Request{}, fmt.Errorf("an OTLP %s request: %w", signal, err)
	}
	if body == nil {
		return Request{}, fmt.Errorf("an OTLP %s request holds nothing", signal)
	}
	if _, ok := body[signal.resourceKey()]; !ok && len(body) > 0 {
		return Request{}, fmt.Errorf("an OTLP %s request has no %s", signal, signal.resourceKey())
	}
	return Request{Signal: signal, Body: body}, nil
}

// Encode writes a request back as JSON.
func (r Request) Encode() ([]byte, error) { return json.Marshal(r.Body) }

// AccountAttribute reports an attribute that names a person or an organization. Claude Code's records
// carry `user.email`, `user.id`, `user.account_id`, `user.account_uuid`, and `organization.id`, and
// the whole of both namespaces is identity, so an export drops every key in them (D3).
func AccountAttribute(key string) bool {
	return strings.HasPrefix(key, "user.") || strings.HasPrefix(key, "organization.")
}

// WithoutAccount returns a copy of the request with every account attribute dropped, wherever an
// attribute list appears: on a resource, a scope, a span, an event, a link, a data point, an
// exemplar, or a log record. Nothing else changes.
func (r Request) WithoutAccount() Request {
	return Request{Signal: r.Signal, Body: dropAccount(r.Body).(map[string]any)}
}

func dropAccount(node any) any {
	switch value := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			if key == "attributes" || key == "filteredAttributes" {
				out[key] = keepAttributes(child)
				continue
			}
			out[key] = dropAccount(child)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = dropAccount(child)
		}
		return out
	}
	return node
}

func keepAttributes(list any) any {
	items, ok := list.([]any)
	if !ok {
		return dropAccount(list)
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if pair, ok := item.(map[string]any); ok {
			if key, _ := pair["key"].(string); AccountAttribute(key) {
				continue
			}
		}
		out = append(out, dropAccount(item))
	}
	return out
}

// Flatten turns a request into one record per span, data point, or log record, with the resource's
// and scope's attributes under the record's own, and the ropeshift tags read into the record's lane,
// work order, and generation.
func Flatten(r Request) []ports.TelemetryRecord {
	var out []ports.TelemetryRecord
	for _, resource := range list(r.Body[r.Signal.resourceKey()]) {
		resourceAttributes := attributes(object(resource["resource"])["attributes"])
		for _, scope := range list(resource[r.Signal.scopeKey()]) {
			scopeObject := object(scope["scope"])
			merged := merge(resourceAttributes, attributes(scopeObject["attributes"]))
			scopeName, _ := scopeObject["name"].(string)
			for _, item := range list(scope[r.Signal.itemKey()]) {
				switch r.Signal {
				case Traces:
					out = append(out, spanRecord(item, merged, scopeName))
				case Metrics:
					out = append(out, metricRecords(item, merged, scopeName)...)
				case Logs:
					out = append(out, logRecord(item, merged, scopeName))
				}
			}
		}
	}
	return out
}

func spanRecord(span map[string]any, base map[string]any, scope string) ports.TelemetryRecord {
	name, _ := span["name"].(string)
	start, end := nanos(span["startTimeUnixNano"]), nanos(span["endTimeUnixNano"])
	detail := map[string]any{
		"scope": scope, "trace_id": span["traceId"], "span_id": span["spanId"], "kind": integer(span["kind"]),
		"start": start / 1e6, "end": end / 1e6, "duration_ms": (end - start) / 1e6,
	}
	if parent, _ := span["parentSpanId"].(string); parent != "" {
		detail["parent_span_id"] = parent
	}
	if code := integer(object(span["status"])["code"]); code != 0 {
		detail["status"] = code
	}
	if links := list(span["links"]); len(links) > 0 {
		detail["links"] = len(links)
	}
	return tagged(ports.TelemetryRecord{
		Signal: ports.SignalSpan, Name: name, ObservedAt: end / 1e6, Attributes: merge(base, attributes(span["attributes"])), Detail: detail,
	})
}

// metricKinds are the data shapes a metric can hold, and the temporality each names.
var metricKinds = []string{"sum", "gauge", "histogram", "exponentialHistogram", "summary"}

func metricRecords(metric map[string]any, base map[string]any, scope string) []ports.TelemetryRecord {
	name, _ := metric["name"].(string)
	unit, _ := metric["unit"].(string)
	var out []ports.TelemetryRecord
	for _, kind := range metricKinds {
		data, ok := metric[kind].(map[string]any)
		if !ok {
			continue
		}
		temporality := ""
		switch integer(data["aggregationTemporality"]) {
		case 1:
			temporality = "delta"
		case 2:
			temporality = "cumulative"
		}
		for _, point := range list(data["dataPoints"]) {
			detail := map[string]any{"scope": scope, "type": kind, "unit": unit}
			if temporality != "" {
				detail["temporality"] = temporality
			}
			if monotonic, ok := data["isMonotonic"].(bool); ok {
				detail["monotonic"] = monotonic
			}
			v, has := pointValue(kind, point)
			if count, ok := point["count"]; ok {
				detail["count"] = integer(count)
			}
			record := ports.TelemetryRecord{
				Signal: ports.SignalMetric, Name: name, ObservedAt: nanos(point["timeUnixNano"]) / 1e6,
				Attributes: merge(base, attributes(point["attributes"])), Detail: detail,
			}
			if has {
				record.Value = &v
			}
			out = append(out, tagged(record))
		}
	}
	return out
}

func pointValue(kind string, point map[string]any) (float64, bool) {
	if kind == "sum" || kind == "gauge" {
		if v, ok := number(point["asDouble"]); ok {
			return v, true
		}
		return number(point["asInt"])
	}
	return number(point["sum"])
}

// The agent's request log, whose token figures are the second context source (D3).
const (
	apiRequestEvent = "api_request"
	apiRequestName  = "claude_code.api_request"
)

func logRecord(record map[string]any, base map[string]any, scope string) ports.TelemetryRecord {
	merged := merge(base, attributes(record["attributes"]))
	name, _ := record["eventName"].(string)
	body := anyValue(record["body"])
	if text, ok := body.(string); ok && name == "" && text != "" && !strings.ContainsAny(text, " \n\t") {
		name = text
	}
	if name == "" {
		name, _ = merged["event.name"].(string)
	}
	at := nanos(record["timeUnixNano"])
	if at == 0 {
		at = nanos(record["observedTimeUnixNano"])
	}
	detail := map[string]any{"scope": scope}
	if severity, _ := record["severityText"].(string); severity != "" {
		detail["severity"] = severity
	}
	if body != nil {
		detail["body"] = body
	}
	out := ports.TelemetryRecord{Signal: ports.SignalLog, Name: name, ObservedAt: at / 1e6, Attributes: merged, Detail: detail}
	if name == apiRequestName || merged["event.name"] == apiRequestEvent {
		context := 0.0
		for _, key := range []string{"input_tokens", "cache_read_tokens", "cache_creation_tokens"} {
			if v, ok := number(merged[key]); ok {
				context += v
			}
		}
		out.Value = &context
	}
	return tagged(out)
}

// tagged reads the ropeshift tags a lane's environment set, which arrive as resource attributes.
func tagged(record ports.TelemetryRecord) ports.TelemetryRecord {
	record.Lane, _ = record.Attributes[lanes.AttrLane].(string)
	record.WorkOrder, _ = record.Attributes[lanes.AttrWorkOrder].(string)
	switch generation := record.Attributes[lanes.AttrGeneration].(type) {
	case string:
		record.Generation, _ = strconv.ParseInt(generation, 10, 64)
	case int64:
		record.Generation = generation
	}
	return record
}

func list(node any) []map[string]any {
	items, _ := node.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func object(node any) map[string]any {
	m, _ := node.(map[string]any)
	return m
}

func merge(layers ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, layer := range layers {
		for key, value := range layer {
			out[key] = value
		}
	}
	return out
}

// attributes reads an OTLP key-value list.
func attributes(node any) map[string]any {
	out := map[string]any{}
	for _, pair := range list(node) {
		key, _ := pair["key"].(string)
		if key == "" {
			continue
		}
		out[key] = anyValue(pair["value"])
	}
	return out
}

// anyValue reads an OTLP AnyValue into a plain value: a string, an int64, a float64, a bool, a list,
// or a map. A 64-bit integer arrives as a string or a number, and both read as int64.
func anyValue(node any) any {
	value := object(node)
	switch {
	case value == nil:
		return nil
	case value["stringValue"] != nil:
		text, _ := value["stringValue"].(string)
		return text
	case value["intValue"] != nil:
		return integer(value["intValue"])
	case value["doubleValue"] != nil:
		v, _ := number(value["doubleValue"])
		return v
	case value["boolValue"] != nil:
		b, _ := value["boolValue"].(bool)
		return b
	case value["bytesValue"] != nil:
		text, _ := value["bytesValue"].(string)
		return text
	case value["arrayValue"] != nil:
		var out []any
		for _, item := range list(object(value["arrayValue"])["values"]) {
			out = append(out, anyValue(item))
		}
		return out
	case value["kvlistValue"] != nil:
		return attributes(object(value["kvlistValue"])["values"])
	}
	return nil
}

func integer(node any) int64 {
	switch value := node.(type) {
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return n
		}
		f, _ := value.Float64()
		return int64(f)
	case string:
		n, _ := strconv.ParseInt(value, 10, 64)
		return n
	case float64:
		return int64(value)
	case int64:
		return value
	}
	return 0
}

func number(node any) (float64, bool) {
	switch value := node.(type) {
	case json.Number:
		f, err := value.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(value, 64)
		return f, err == nil
	case float64:
		return value, true
	case int64:
		return float64(value), true
	}
	return 0, false
}

func nanos(node any) int64 {
	if n := integer(node); n != 0 {
		return n
	}
	f, _ := number(node)
	if f > math.MaxInt64 || f < 0 {
		return 0
	}
	return int64(f)
}

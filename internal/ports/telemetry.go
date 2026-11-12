package ports

import "context"

// Telemetry is where ropeshift's own spans and metrics go (ADR-0004). An emit never fails the work
// it describes and never blocks it: telemetry is lossy by design, and the ledger is the record. The
// worker's adapter keeps what it is given in the local store, and forwards it only to an exporter the
// workspace configured (NFR-PRIV-1). Nothing here carries a prompt, a response, or a message body.
type Telemetry interface {
	// Span records one finished span.
	Span(span Span)
	// Measure records one measurement of one metric.
	Measure(measurement Measurement)
}

// SpanKind is OpenTelemetry's span kind.
type SpanKind string

// The span kinds ropeshift emits.
const (
	SpanInternal SpanKind = "internal"
	SpanProducer SpanKind = "producer"
	SpanConsumer SpanKind = "consumer"
	SpanClient   SpanKind = "client"
)

// Attribute is one key and its value: a string, an int64, a float64, or a bool.
type Attribute struct {
	Key   string
	Value any
}

// Span is one finished operation. IDs are hex, and an empty parent starts a trace. Start and End
// are epoch milliseconds.
type Span struct {
	Name       string
	Kind       SpanKind
	TraceID    string
	SpanID     string
	ParentID   string
	Links      []SpanLink
	Start      int64
	End        int64
	Failed     bool
	Attributes []Attribute
}

// SpanLink joins a span to one it waited on across a queue or a parked period.
type SpanLink struct {
	TraceID string
	SpanID  string
}

// MetricKind is how a measurement adds up.
type MetricKind string

// The metric kinds. A counter and an up-down counter carry a change, a gauge a reading, and a
// histogram one observation.
const (
	MetricCounter   MetricKind = "counter"
	MetricUpDown    MetricKind = "updown"
	MetricGauge     MetricKind = "gauge"
	MetricHistogram MetricKind = "histogram"
)

// Measurement is one measurement of a metric, at epoch milliseconds At.
type Measurement struct {
	Name       string
	Kind       MetricKind
	Unit       string
	Value      float64
	At         int64
	Attributes []Attribute
}

// The signals the receiver stores, as a record names them.
const (
	SignalSpan   = "span"
	SignalMetric = "metric"
	SignalLog    = "log"
)

// TelemetryQuery selects records from the worker's local store. Empty fields select everything, and
// a zero Limit takes the store's default.
type TelemetryQuery struct {
	Signal string
	Name   string
	Lane   string
	Since  int64
	Limit  int
}

// TelemetryRecord is one span, data point, or log record, with its resource and scope attributes
// merged under its own. Value is a metric's value, or a histogram's sum.
type TelemetryRecord struct {
	ID         int64
	Signal     string
	Name       string
	Lane       string
	WorkOrder  string
	Generation int64
	ObservedAt int64
	ReceivedAt int64
	Value      *float64
	Attributes map[string]any
	Detail     map[string]any
}

// LaneTelemetry is what the local store says of one lane: its context at the last call, its tokens
// by type, and the cost its agent reported. Each figure comes from ropeshift's own metrics first, and
// from the agent's telemetry when ropeshift recorded none.
type LaneTelemetry struct {
	Lane          string
	ContextTokens int64
	ContextAt     int64
	Tokens        map[string]int64
	CostUSD       float64
	LastAt        int64
	// Agents are the tokens each roster agent spent inside the lane, by agent and then type, where the
	// driver reported which agent spent them. They are part of Tokens, not added to it (FR-AGENT-12).
	Agents map[string]map[string]int64
}

// TelemetryReader reads the worker's local store, for `ropeshift telemetry query` and `top`.
type TelemetryReader interface {
	Records(ctx context.Context, query TelemetryQuery) ([]TelemetryRecord, error)
	Lanes(ctx context.Context) ([]LaneTelemetry, error)
}

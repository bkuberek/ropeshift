package otel_test

import (
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/otel"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func TestTheEmitterWritesItsOwnTelemetryToTheStore(t *testing.T) {
	store := memoryStore(t)
	forward := &forwarded{}
	emitter := otel.NewEmitter(otel.EmitterConfig{
		Resource: []ports.Attribute{{Key: "service.name", Value: "ropeshift"}, {Key: lanes.AttrWorker, Value: "solo"}},
		Store:    store, Forward: forward, Now: func() time.Time { return time.UnixMilli(5000) },
	})
	lane := []ports.Attribute{{Key: lanes.AttrLane, Value: "lane-1"}, {Key: lanes.AttrWorkOrder, Value: "wo-1"}, {Key: lanes.AttrGeneration, Value: int64(2)}}
	emitter.Span(ports.Span{
		Name: ledger.SpanLane, TraceID: ledger.TraceID("wo-1"), SpanID: ledger.SpanID("lane-1"), Start: 1000, End: 4000,
		Attributes: append(lane, ports.Attribute{Key: ledger.AttrOutcome, Value: "completed"}),
	})
	emitter.Measure(ports.Measurement{Name: ledger.MetricContextTokens, Kind: ports.MetricGauge, Unit: "{token}", Value: 4200, At: 3000, Attributes: lane})
	emitter.Measure(ports.Measurement{Name: ledger.MetricCost, Kind: ports.MetricCounter, Unit: "USD", Value: 0.02, At: 3000, Attributes: lane})
	emitter.Measure(ports.Measurement{Name: ledger.MetricCost, Kind: ports.MetricCounter, Unit: "USD", Value: 0.03, At: 3500, Attributes: lane})
	emitter.Measure(ports.Measurement{
		Name: ledger.MetricTokens, Kind: ports.MetricCounter, Unit: "{token}", Value: 90, At: 3500,
		Attributes: append(lane, ports.Attribute{Key: ledger.AttrTokenType, Value: "output"}),
	})
	emitter.Measure(ports.Measurement{Name: ledger.MetricStall, Kind: ports.MetricHistogram, Unit: "s", Value: 70, At: 3600, Attributes: lane})
	if err := emitter.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(t.Context(), ports.TelemetryQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 6 {
		t.Fatalf("%d records: %+v", len(records), records)
	}
	for _, record := range records {
		if record.Lane != "lane-1" || record.WorkOrder != "wo-1" || record.Generation != 2 || record.Attributes[lanes.AttrWorker] != "solo" {
			t.Errorf("record %s is not tagged: %+v", record.Name, record)
		}
	}
	span, _ := store.Records(t.Context(), ports.TelemetryQuery{Signal: ports.SignalSpan})
	if len(span) != 1 || span[0].Detail["trace_id"] != ledger.TraceID("wo-1") || span[0].Detail["duration_ms"] != float64(3000) {
		t.Errorf("span %+v", span)
	}
	stall, _ := store.Records(t.Context(), ports.TelemetryQuery{Name: ledger.MetricStall})
	if len(stall) != 1 || stall[0].Value == nil || *stall[0].Value != 70 || stall[0].Detail["count"] != float64(1) {
		t.Errorf("stall %+v", stall)
	}
	summary, err := store.Lanes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != 1 || summary[0].ContextTokens != 4200 || summary[0].Tokens["output"] != 90 || summary[0].CostUSD < 0.0499 || summary[0].CostUSD > 0.0501 {
		t.Errorf("summary %+v", summary)
	}
	if len(forward.requests) != 2 {
		t.Fatalf("%d requests forwarded", len(forward.requests))
	}
	encoded, err := forward.requests[1].Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"aggregationTemporality":1`) || !strings.Contains(string(encoded), `"isMonotonic":true`) {
		t.Errorf("metrics %s", encoded)
	}
	if err := emitter.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(forward.requests) != 2 {
		t.Errorf("an empty flush forwarded %d more", len(forward.requests)-2)
	}
}

func TestTheEmitterDropsPastItsCap(t *testing.T) {
	emitter := otel.NewEmitter(otel.EmitterConfig{Store: memoryStore(t)})
	for range 9000 {
		emitter.Measure(ports.Measurement{Name: "m", Kind: ports.MetricCounter, Value: 1, At: 1})
	}
	if emitter.Dropped() != 9000-8192 {
		t.Errorf("dropped %d", emitter.Dropped())
	}
}

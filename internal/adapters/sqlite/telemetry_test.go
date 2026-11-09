package sqlite_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/ports"
)

func openTelemetry(t *testing.T) *sqlite.TelemetryStore {
	t.Helper()
	store, err := sqlite.OpenTelemetry(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store
}

func value(v float64) *float64 { return &v }

func metric(lane, name string, v float64, at int64, attributes map[string]any) ports.TelemetryRecord {
	if attributes == nil {
		attributes = map[string]any{}
	}
	return ports.TelemetryRecord{
		Signal: ports.SignalMetric, Name: name, Lane: lane, ObservedAt: at, Value: value(v), Attributes: attributes,
		Detail: map[string]any{"temporality": "delta"},
	}
}

func TestTelemetryRecordsReadBackNewestFirst(t *testing.T) {
	store := openTelemetry(t)
	records := []ports.TelemetryRecord{
		metric("lane-1", "ropeshift.lane.cost", 0.5, 1000, map[string]any{"user.email": "someone@example.com"}),
		{Signal: ports.SignalLog, Name: "claude_code.api_request", Lane: "lane-2", Generation: 2, ObservedAt: 2000, Attributes: map[string]any{"model": "m"}, Detail: map[string]any{}},
		{Signal: ports.SignalSpan, Name: "ropeshift.lane", Lane: "lane-1", WorkOrder: "wo-1", ObservedAt: 3000, Attributes: map[string]any{}, Detail: map[string]any{"trace_id": "ab"}},
	}
	if err := store.Append(t.Context(), 5000, records); err != nil {
		t.Fatal(err)
	}
	all, err := store.Records(t.Context(), ports.TelemetryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Name != "ropeshift.lane" || all[2].Name != "ropeshift.lane.cost" || all[2].ReceivedAt != 5000 {
		t.Fatalf("records %+v", all)
	}
	// The local store keeps what it received, account identifiers too (D3).
	if all[2].Attributes["user.email"] != "someone@example.com" || *all[2].Value != 0.5 {
		t.Errorf("record %+v", all[2])
	}
	for _, tt := range []struct {
		name  string
		query ports.TelemetryQuery
		want  int
	}{
		{"by signal", ports.TelemetryQuery{Signal: ports.SignalLog}, 1},
		{"by name", ports.TelemetryQuery{Name: "ropeshift.lane"}, 1},
		{"by lane", ports.TelemetryQuery{Lane: "lane-1"}, 2},
		{"since", ports.TelemetryQuery{Since: 2000}, 2},
		{"limited", ports.TelemetryQuery{Limit: 1}, 1},
	} {
		got, err := store.Records(t.Context(), tt.query)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tt.want {
			t.Errorf("%s: %d records", tt.name, len(got))
		}
	}
}

func TestLaneTelemetryPrefersRopeshiftsOwnFigures(t *testing.T) {
	store := openTelemetry(t)
	records := []ports.TelemetryRecord{
		metric("lane-1", "ropeshift.lane.context.tokens", 1000, 10, nil),
		metric("lane-1", "ropeshift.lane.context.tokens", 4000, 30, nil),
		metric("lane-1", "ropeshift.lane.tokens", 100, 11, map[string]any{"ropeshift.token.type": "input"}),
		metric("lane-1", "ropeshift.lane.tokens", 50, 12, map[string]any{"ropeshift.token.type": "input"}),
		metric("lane-1", "ropeshift.lane.tokens", 7, 12, map[string]any{"ropeshift.token.type": "output"}),
		metric("lane-1", "ropeshift.lane.cost", 0.25, 12, nil),
		metric("lane-1", "ropeshift.lane.cost", 0.5, 13, nil),
		// The agent's own figures for the same lane stay out, because ropeshift has its own.
		metric("lane-1", "claude_code.cost.usage", 9, 14, nil),
		// A lane ropeshift said nothing of reads the agent's telemetry.
		metric("lane-2", "claude_code.cost.usage", 0.1, 20, nil),
		metric("lane-2", "claude_code.cost.usage", 0.2, 21, nil),
		metric("lane-2", "claude_code.token.usage", 30, 21, map[string]any{"type": "cacheRead"}),
		{Signal: ports.SignalLog, Name: "claude_code.api_request", Lane: "lane-2", ObservedAt: 22, Value: value(1234), Attributes: map[string]any{}, Detail: map[string]any{}},
		// A cumulative series counts its last reading, not its sum.
		{Signal: ports.SignalMetric, Name: "claude_code.cost.usage", Lane: "lane-3", ObservedAt: 1, Value: value(0.1), Attributes: map[string]any{}, Detail: map[string]any{"temporality": "cumulative"}},
		{Signal: ports.SignalMetric, Name: "claude_code.cost.usage", Lane: "lane-3", ObservedAt: 2, Value: value(0.3), Attributes: map[string]any{}, Detail: map[string]any{"temporality": "cumulative"}},
	}
	if err := store.Append(t.Context(), 100, records); err != nil {
		t.Fatal(err)
	}
	lanes, err := store.Lanes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byLane := map[string]ports.LaneTelemetry{}
	for _, lane := range lanes {
		byLane[lane.Lane] = lane
	}
	one := byLane["lane-1"]
	if one.ContextTokens != 4000 || one.ContextAt != 30 || one.Tokens["input"] != 150 || one.Tokens["output"] != 7 || !near(one.CostUSD, 0.75) {
		t.Errorf("lane-1 %+v", one)
	}
	two := byLane["lane-2"]
	if two.ContextTokens != 1234 || !near(two.CostUSD, 0.3) || two.Tokens["cacheRead"] != 30 {
		t.Errorf("lane-2 %+v", two)
	}
	if three := byLane["lane-3"]; !near(three.CostUSD, 0.3) {
		t.Errorf("lane-3 %+v", three)
	}
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func TestRetentionDropsWhatIsOlderThanTheAge(t *testing.T) {
	store := openTelemetry(t)
	day := time.Hour.Milliseconds() * 24
	if err := store.Append(t.Context(), 1*day, []ports.TelemetryRecord{metric("lane-1", "old", 1, 1, nil)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(t.Context(), 8*day, []ports.TelemetryRecord{metric("lane-1", "new", 1, 2, nil)}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Retain(t.Context(), 8*day+1, 7*24*time.Hour, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	left, err := store.Records(t.Context(), ports.TelemetryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 || len(left) != 1 || left[0].Name != "new" {
		t.Errorf("deleted %d, left %+v", deleted, left)
	}
}

// TestRetentionHoldsTheStoreUnderItsSize writes past a small cap and reads the store's own size, so
// a bound that only counted rows could not pass.
func TestRetentionHoldsTheStoreUnderItsSize(t *testing.T) {
	store := openTelemetry(t)
	const limit = 512 << 10
	padding := strings.Repeat("x", 2000)
	for batch := range 40 {
		records := make([]ports.TelemetryRecord, 0, 50)
		for i := range 50 {
			records = append(records, metric("lane-1", fmt.Sprintf("m-%d", batch), 1, int64(batch*50+i), map[string]any{"pad": padding}))
		}
		if err := store.Append(t.Context(), int64(batch+1), records); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.Size(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if before <= limit {
		t.Fatalf("the store holds %d bytes, which tests nothing", before)
	}
	if _, err := store.Retain(t.Context(), 41, 365*24*time.Hour, limit); err != nil {
		t.Fatal(err)
	}
	after, err := store.Size(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after > limit {
		t.Errorf("%d bytes after retention, over %d", after, limit)
	}
	left, err := store.Records(t.Context(), ports.TelemetryQuery{Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(left) == 0 || left[0].Name != "m-39" {
		t.Fatalf("retention kept %d records, newest %+v", len(left), left)
	}
	if oldest := left[len(left)-1]; oldest.Name == "m-0" {
		t.Errorf("retention kept the oldest batch and dropped newer ones")
	}
}

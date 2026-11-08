package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/apitest"
)

func TestTelemetryCommandsReadTheLocalStore(t *testing.T) {
	store := fake.NewTelemetry()
	cost := 0.25
	store.Hold([]ports.TelemetryRecord{
		{ID: 1, Signal: ports.SignalMetric, Name: "ropeshift.lane.cost", Lane: "lane-1", ObservedAt: 1000, Value: &cost, Attributes: map[string]any{}, Detail: map[string]any{}},
		{ID: 2, Signal: ports.SignalLog, Name: "claude_code.api_request", Lane: "lane-2", ObservedAt: 2000, Attributes: map[string]any{"model": "m"}, Detail: map[string]any{}},
	}, []ports.LaneTelemetry{{Lane: "lane-1", ContextTokens: 4200, Tokens: map[string]int64{"input": 10, "output": 3}, CostUSD: 0.25}})
	s := &session{t: t, daemon: apitest.StartWith(t, apitest.Options{Telemetry: store})}
	var records api.TelemetryRecords
	if err := json.Unmarshal([]byte(s.must("telemetry", "query", "--signal", "metric", "--json")), &records); err != nil {
		t.Fatal(err)
	}
	if !records.Available || len(records.Records) != 1 || records.Records[0].Name != "ropeshift.lane.cost" || *records.Records[0].Value != 0.25 {
		t.Errorf("records %+v", records)
	}
	if text := s.must("telemetry", "query", "--lane", "lane-2"); !strings.Contains(text, "claude_code.api_request") || strings.Contains(text, "lane-1") {
		t.Errorf("query %q", text)
	}
	if text := s.must("telemetry", "lanes"); !strings.Contains(text, "context 4200") || !strings.Contains(text, "input 10, output 3") ||
		!strings.Contains(text, "$0.2500") {
		t.Errorf("lanes %q", text)
	}
	if code, _, _ := s.run("telemetry", "query", "--limit", "-1"); code != cli.ExitUsage {
		t.Errorf("a negative limit exited %d", code)
	}
}

func TestAPlaneWithNoStoreSaysSo(t *testing.T) {
	s := newSession(t)
	var lanes api.LaneTelemetryList
	if err := json.Unmarshal([]byte(s.must("telemetry", "lanes", "--json")), &lanes); err != nil {
		t.Fatal(err)
	}
	if lanes.Available || len(lanes.Lanes) != 0 {
		t.Errorf("lanes %+v", lanes)
	}
	if text := s.must("telemetry", "query"); !strings.Contains(text, "no telemetry store") {
		t.Errorf("query %q", text)
	}
}

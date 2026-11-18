package claudecode

import (
	"math"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// A message_delta, as a client run with partial messages writes it, carries the call's output beside a delta of its own.
func TestAMessageDeltaCarriesItsCallsOutput(t *testing.T) {
	raw := `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":410}},"session_id":"s","parent_tool_use_id":null,"uuid":"u"}`
	p, err := parseLine([]byte(raw), false)
	if err != nil {
		t.Fatal(err)
	}
	if p.useful || p.call == nil || p.call.start || p.call.usage.OutputTokens != 410 || p.call.parent != "" {
		t.Errorf("parsed %+v", p)
	}
}

// A subagent's call names its parent, and its context is not the lane's.
func TestASubagentsCallIsCountedAndItsContextIsNot(t *testing.T) {
	raw := `{"type":"stream_event","event":{"type":"message_start","message":{"id":"m","model":"claude-opus-4-5-20251101","usage":{"input_tokens":10,"cache_read_input_tokens":5000,"output_tokens":1}}},"parent_tool_use_id":"toolu_01SUB"}`
	p, err := parseLine([]byte(raw), false)
	if err != nil {
		t.Fatal(err)
	}
	if p.useful || p.call == nil || !p.call.start || p.call.parent != "toolu_01SUB" || p.call.model != "claude-opus-4-5-20251101" {
		t.Errorf("parsed %+v", p)
	}
}

// A turn's estimate starts again after the turn that ended, because the client's total is exact by then, and a figure is
// told only once it is higher than the last one told.
func TestATurnsEstimateEndsWithTheTurn(t *testing.T) {
	var turn turnCost
	start := streamCall{start: true, model: "claude-sonnet-4-5", usage: lanes.Usage{InputTokens: 1000, OutputTokens: 1}}
	first := turn.note(start, "")
	if want := (1000*3 + 1*15) / 1e6; math.Abs(first-want) > 1e-12 {
		t.Fatalf("a call of 1000 tokens in is estimated at %v, want %v", first, want)
	}
	if !turn.fresh(first) || turn.fresh(first) {
		t.Error("a figure was told twice, or never")
	}
	turn.reset()
	if turn.spent() != 0 || !turn.fresh(first) {
		t.Errorf("a turn that ended left %v spent", turn.spent())
	}
}

// A call that names no model is priced as its session's, and a session that names none is priced as the dearest family.
func TestACallWithNoModelIsPricedAsItsSessionOrTheDearestFamily(t *testing.T) {
	usage := lanes.Usage{OutputTokens: 1_000_000}
	var named, unnamed turnCost
	if got := named.note(streamCall{start: true, usage: usage}, "claude-haiku-4-5"); math.Abs(got-5) > 1e-9 {
		t.Errorf("a million tokens out on its session's Haiku cost %v, want 5", got)
	}
	if got := unnamed.note(streamCall{start: true, usage: usage}, ""); math.Abs(got-50) > 1e-9 {
		t.Errorf("a million tokens out on no model cost %v, want the dearest family's 50", got)
	}
}

func TestListPricesByFamily(t *testing.T) {
	tests := []struct {
		model string
		out   float64
	}{
		{"claude-haiku-4-5-20251001", 5},
		{"claude-sonnet-4-6", 15},
		{"claude-opus-4-8", 25},
		{"claude-opus-4-1-20250805", 75},
		{"claude-opus-4-20250514", 75},
		{"claude-fable-5-1", 50},
		{"something-new", 50},
		{"", 50},
	}
	for _, tt := range tests {
		if got := lanes.ListPrice(tt.model).Output; got != tt.out {
			t.Errorf("%q is priced at %v out, want %v", tt.model, got, tt.out)
		}
	}
}

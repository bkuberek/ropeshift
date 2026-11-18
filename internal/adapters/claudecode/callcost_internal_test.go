package claudecode

import (
	"math"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

func near(got, want float64) bool { return math.Abs(got-want) <= 1e-9*math.Max(1, math.Abs(want)) }

// A message_delta, as a client run with partial messages writes it, carries the call's output beside a delta of its own.
func TestAMessageDeltaCarriesItsCallsOutput(t *testing.T) {
	raw := `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":410}},"session_id":"s","parent_tool_use_id":null,"uuid":"u"}`
	p, err := parseLine([]byte(raw), false)
	if err != nil {
		t.Fatal(err)
	}
	if p.useful || p.call == nil || p.call.kind != callDelta || p.call.usage.OutputTokens != 410 || p.call.parent != "" {
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
	if p.useful || p.call == nil || p.call.kind != callStart || p.call.id != "m" || p.call.parent != "toolu_01SUB" || p.call.model != "claude-opus-4-5-20251101" {
		t.Errorf("parsed %+v", p)
	}
}

// An assistant message is a call with its id, model, and usage, whether it is the session's or a subagent's, and it reads
// the speed the usage names. A message with no id, or none of its usage, prices nothing, because nothing says which call
// it is or what it cost.
func TestAnAssistantMessageIsACallWithItsIDModelAndUsage(t *testing.T) {
	mine := `{"type":"assistant","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":6,"cache_read_input_tokens":900,"output_tokens":40,"speed":"fast"}},"parent_tool_use_id":null}`
	p, err := parseLine([]byte(mine), false)
	if err != nil {
		t.Fatal(err)
	}
	want := streamCall{kind: callMessage, id: "msg_1", model: "claude-opus-5-5", usage: lanes.Usage{InputTokens: 6, CacheReadTokens: 900, OutputTokens: 40, Speed: lanes.SpeedFast}}
	if p.call == nil || *p.call != want {
		t.Errorf("the session's message parsed %+v, want %+v", p.call, want)
	}
	theirs := `{"type":"assistant","message":{"id":"msg_2","model":"claude-haiku-4-5","content":[],"usage":{"input_tokens":2,"output_tokens":9}},"parent_tool_use_id":"toolu_01SUB"}`
	if p, err = parseLine([]byte(theirs), false); err != nil || p.call == nil || p.call.parent != "toolu_01SUB" || p.call.id != "msg_2" {
		t.Errorf("a subagent's message parsed %+v: %v", p.call, err)
	}
	for name, raw := range map[string]string{
		"no id":    `{"type":"assistant","message":{"model":"claude-opus-5-5","content":[],"usage":{"input_tokens":6,"output_tokens":40}}}`,
		"no usage": `{"type":"assistant","message":{"id":"msg_3","model":"claude-opus-5-5","content":[]}}`,
	} {
		if p, err = parseLine([]byte(raw), false); err != nil || p.call != nil {
			t.Errorf("a message with %s parsed %+v: %v", name, p.call, err)
		}
	}
}

// A call counts once however it is reported: the stream events and the assistant message of one call share its id, and the
// larger of each of their figures is the call's.
func TestACallReportedTwiceIsCountedOnce(t *testing.T) {
	var turn turnCost
	turn.note(streamCall{kind: callStart, id: "msg_1", model: "claude-sonnet-4-5", usage: lanes.Usage{InputTokens: 1000, OutputTokens: 1}}, "")
	turn.note(streamCall{kind: callDelta, usage: lanes.Usage{OutputTokens: 200}}, "")
	got := turn.note(streamCall{kind: callMessage, id: "msg_1", model: "claude-sonnet-4-5", usage: lanes.Usage{InputTokens: 1000, OutputTokens: 200}}, "")
	if want := (1000*3 + 200*15) / 1e6; !near(got, want) {
		t.Errorf("one call reported three ways is estimated at %v, want %v", got, want)
	}
	// A call with no id of its own is a call each time it starts, and a delta belongs to the latest of its caller.
	var anon turnCost
	anon.note(streamCall{kind: callStart, model: "claude-sonnet-4-5", usage: lanes.Usage{InputTokens: 1000}}, "")
	anon.note(streamCall{kind: callStart, model: "claude-sonnet-4-5", usage: lanes.Usage{InputTokens: 1000}}, "")
	if got = anon.note(streamCall{kind: callDelta, usage: lanes.Usage{OutputTokens: 100}}, ""); !near(got, (2*1000*3+100*15)/1e6) {
		t.Errorf("two calls with no ids are estimated at %v", got)
	}
}

// The count of web searches rides in the usage of a stream event and of an assistant message alike, and the larger count of one
// call's reports is the call's: a search is priced once however many lines say so, a call that is told of a second search
// later has two, and a call that made none costs nothing for them.
func TestWebSearchesOfACallAreCountedOnce(t *testing.T) {
	delta := `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":20,"cache_read_input_tokens":30000,"output_tokens":500,"server_tool_use":{"web_search_requests":2}}},"parent_tool_use_id":null}`
	p, err := parseLine([]byte(delta), false)
	if err != nil || p.call == nil || p.call.usage.ServerToolUse.WebSearchRequests != 2 {
		t.Fatalf("a message_delta that counts two searches parsed %+v: %v", p.call, err)
	}
	assistant := `{"type":"assistant","message":{"id":"msg_1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":20,"cache_read_input_tokens":30000,"output_tokens":500,"server_tool_use":{"web_search_requests":2}}},"parent_tool_use_id":null}`
	if p, err = parseLine([]byte(assistant), false); err != nil || p.call == nil || p.call.usage.ServerToolUse.WebSearchRequests != 2 {
		t.Fatalf("an assistant message that counts two searches parsed %+v: %v", p.call, err)
	}

	tokens := (20*3 + 30000*0.30 + 500*15) / 1e6
	var turn turnCost
	turn.note(streamCall{kind: callStart, id: "msg_1", model: "claude-sonnet-4-5", usage: lanes.Usage{InputTokens: 20, CacheReadTokens: 30000, OutputTokens: 1}}, "")
	if got := turn.note(*mustCall(t, delta, "msg_1"), ""); !near(got, tokens+0.02) {
		t.Errorf("a call whose delta counts two searches is estimated at %v, want %v", got, tokens+0.02)
	}
	if got := turn.note(*p.call, ""); !near(got, tokens+0.02) {
		t.Errorf("the assistant message of the same call, which says two again, estimates it at %v, want %v: a search counts once", got, tokens+0.02)
	}
	later := lanes.Usage{InputTokens: 20, CacheReadTokens: 30000, OutputTokens: 500, ServerToolUse: lanes.ServerToolUse{WebSearchRequests: 3}}
	if got := turn.note(streamCall{kind: callMessage, id: "msg_1", model: "claude-sonnet-4-5", usage: later}, ""); !near(got, tokens+0.03) {
		t.Errorf("a call told of a third search is estimated at %v, want %v", got, tokens+0.03)
	}
}

// mustCall parses a stream line and gives it the id of the call the stream's earlier `message_start` named, as a
// `message_delta` has none of its own.
func mustCall(t *testing.T, raw, id string) *streamCall {
	t.Helper()
	p, err := parseLine([]byte(raw), false)
	if err != nil || p.call == nil {
		t.Fatalf("parsed %+v: %v", p.call, err)
	}
	return p.call
}

// A turn's estimate starts again after the turn that ended, because the client's total is exact by then, and a figure is
// told only once it is higher than the last one told.
func TestATurnsEstimateEndsWithTheTurn(t *testing.T) {
	var turn turnCost
	start := streamCall{kind: callStart, id: "m", model: "claude-sonnet-4-5", usage: lanes.Usage{InputTokens: 1000, OutputTokens: 1}}
	first := turn.note(start, "")
	if want := (1000*3 + 1*15) / 1e6; !near(first, want) {
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
	if got := named.note(streamCall{kind: callStart, id: "a", usage: usage}, "claude-haiku-4-5"); !near(got, 5) {
		t.Errorf("a million tokens out on its session's Haiku cost %v, want 5", got)
	}
	if got := unnamed.note(streamCall{kind: callStart, id: "a", usage: usage}, ""); !near(got, 75) {
		t.Errorf("a million tokens out on no model cost %v, want the dearest family's 75", got)
	}
}

// A call whose usage says it ran in fast mode is priced at twice the standard rate, however the call is reported.
func TestAFastCallIsPricedAtTwiceTheStandardRate(t *testing.T) {
	var turn turnCost
	turn.note(streamCall{kind: callStart, id: "a", model: "claude-opus-5-5", usage: lanes.Usage{InputTokens: 1_000_000}}, "")
	got := turn.note(streamCall{kind: callMessage, id: "a", model: "claude-opus-5-5", usage: lanes.Usage{InputTokens: 1_000_000, Speed: lanes.SpeedFast}}, "")
	if !near(got, 10) {
		t.Errorf("a million tokens in on a fast call cost %v, want twice $5", got)
	}
}

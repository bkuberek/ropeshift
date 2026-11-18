package claudecode

import (
	"strconv"

	"ropeshift.com/ropeshift/internal/lanes"
)

// callKind says how a stream line reports an API call.
type callKind int

const (
	// callStart is a `message_start` stream event: the call's id and model, and the tokens it sent.
	callStart callKind = iota + 1
	// callDelta is a `message_delta` stream event: the call's output so far, for the latest call of its caller.
	callDelta
	// callMessage is an `assistant` message: the call's id, model, and usage, written once for each of its content blocks,
	// a subagent's included. It is the only report of a call on a session that writes no stream events.
	callMessage
)

// streamCall is one API call's usage as the stream reports it, in one of the three ways above. Every figure is the call's
// own, not the turn's, and a call of a subagent names its parent.
type streamCall struct {
	kind   callKind
	id     string
	parent string
	model  string
	usage  lanes.Usage
}

// openCall is a call the turn made: what it ran on, and the largest usage any report of it showed.
type openCall struct {
	model string
	usage lanes.Usage
}

// turnCost estimates what the turn in progress has spent, from the usage of each API call the stream reported, at
// list price. The client's own total arrives only with the `result` line that ends the turn, so a long turn of many
// calls would otherwise show no spend until it was over (FR-SPEND-5). The figure is a bound: the turn's end carries the
// exact total and replaces it.
//
// A call is one entry, found by its message id, however many lines report it: its stream events and its assistant messages
// each raise the figures of the one entry, so no call counts twice. A call that names no id, as a stream event of an older
// client does not, is told apart by the order of its caller's calls.
type turnCost struct {
	calls map[string]*openCall
	// latest is the key of the call each caller (the session itself, or one of its subagents' tool calls) last opened, which
	// its `message_delta` lines are about because they name no call of their own.
	latest map[string]string
	// anonymous counts the calls that named no id, and emitted is the highest running figure a caller was told, so that none
	// is told twice.
	anonymous int
	emitted   float64
}

// note takes one report of a call, and says what the turn has spent: the calls it has made at list price. The session's own
// model is the one a call that names none ran on.
func (t *turnCost) note(call streamCall, sessionModel string) float64 {
	if t.calls == nil {
		t.calls, t.latest = map[string]*openCall{}, map[string]string{}
	}
	key := call.id
	switch call.kind {
	case callStart:
		if key == "" {
			t.anonymous++
			key = call.parent + "#" + strconv.Itoa(t.anonymous)
		}
		t.latest[call.parent] = key
	case callDelta:
		var found bool
		if key, found = t.latest[call.parent]; !found {
			// A delta of a call whose start this reader never saw, as after an adoption in the middle of a call.
			t.anonymous++
			key = call.parent + "#" + strconv.Itoa(t.anonymous)
			t.latest[call.parent] = key
		}
	case callMessage:
		if key == "" {
			return t.spent()
		}
	}
	open := t.calls[key]
	if open == nil {
		open = &openCall{}
		t.calls[key] = open
	}
	if call.model != "" {
		open.model = call.model
	} else if open.model == "" {
		open.model = sessionModel
	}
	open.usage = open.usage.Max(call.usage)
	return t.spent()
}

// spent is what the calls of the turn so far cost.
func (t *turnCost) spent() float64 {
	var total float64
	for _, call := range t.calls {
		total += lanes.CallCost(call.model, call.usage)
	}
	return total
}

// fresh says a figure is higher than any told before it.
func (t *turnCost) fresh(running float64) bool {
	if running <= t.emitted {
		return false
	}
	t.emitted = running
	return true
}

// reset ends the turn: its exact total is the session's now.
func (t *turnCost) reset() { *t = turnCost{} }

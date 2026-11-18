package claudecode

import "ropeshift.com/ropeshift/internal/lanes"

// streamCall is one API call's usage as the stream reports it: a `message_start` names the call's model and the tokens
// it sent, and each `message_delta` after it carries the call's running output (and, on a client that reports them,
// the rest of its usage). Every figure is the call's own, not the turn's, and a call of a subagent names its parent.
type streamCall struct {
	start  bool
	parent string
	model  string
	usage  lanes.Usage
}

// openCall is a call that has not been followed by another of its caller.
type openCall struct {
	model string
	usage lanes.Usage
}

// turnCost estimates what the turn in progress has spent, from the usage of each API call the stream reported, at
// list price. The client's own total arrives only with the `result` line that ends the turn, so a long turn of many
// calls would otherwise show no spend until it was over (FR-SPEND-5). The figure is a bound: the turn's end carries the
// exact total and replaces it.
type turnCost struct {
	// settled is what the calls that ended cost, and open is the call each caller (the session itself, or one of its
	// subagents' tool calls) is on.
	settled float64
	open    map[string]*openCall
	// emitted is the highest running figure a caller was told, so that none is told twice.
	emitted float64
}

// note takes one call's report, and says what the turn has spent beyond what its session's total held before it. The
// session's own model is the one a call that names none ran on.
func (t *turnCost) note(call streamCall, sessionModel string) float64 {
	if t.open == nil {
		t.open = map[string]*openCall{}
	}
	current := t.open[call.parent]
	switch {
	case call.start:
		if current != nil {
			t.settled += lanes.ListPrice(current.model).Cost(current.usage)
		}
		model := call.model
		if model == "" {
			model = sessionModel
		}
		t.open[call.parent] = &openCall{model: model, usage: call.usage}
	case current != nil:
		current.usage = current.usage.Max(call.usage)
	default:
		// A report of a call whose start this reader never saw, as after an adoption in the middle of a call, is priced
		// as its session's model would.
		t.open[call.parent] = &openCall{model: sessionModel, usage: call.usage}
	}
	return t.spent()
}

// spent is what the calls of the turn so far cost, the open ones included.
func (t *turnCost) spent() float64 {
	total := t.settled
	for _, call := range t.open {
		total += lanes.ListPrice(call.model).Cost(call.usage)
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

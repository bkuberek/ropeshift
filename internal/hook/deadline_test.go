package hook

import (
	"testing"
	"time"
)

// A hook keeps its real bounds unless a test build raised its deadline, and a deadline below the real
// one lowers nothing.
func TestOnlyARaisedDeadlineChangesAHooksBounds(t *testing.T) {
	for _, raised := range []time.Duration{0, time.Millisecond, hardDeadline} {
		cfg := config{raised: raised}
		for _, bound := range []time.Duration{dialDeadline, ackDeadline, peekDeadline, groundAck, hardDeadline} {
			if got := cfg.within(bound); got != bound {
				t.Errorf("raised to %v, a bound of %v became %v", raised, bound, got)
			}
		}
	}
}

// A raised deadline replaces the real one, and every bound inside it grows in proportion, so what a
// hook does before its deadline still fits inside it.
func TestARaisedDeadlineKeepsTheBoundsInsideIt(t *testing.T) {
	cfg := config{raised: 2 * time.Second}
	if got := cfg.within(hardDeadline); got != cfg.raised {
		t.Errorf("the deadline is %v, raised to %v", got, cfg.raised)
	}
	for _, bound := range []time.Duration{dialDeadline, ackDeadline, peekDeadline, groundAck} {
		if got := cfg.within(bound); got <= bound {
			t.Errorf("a bound of %v stayed at %v under a raised deadline", bound, got)
		}
	}
	if cfg.within(dialDeadline)+cfg.within(groundAck) >= cfg.within(hardDeadline) {
		t.Error("a re-grounding hook can pass its raised deadline")
	}
	if cfg.within(dialDeadline)+cfg.within(peekDeadline) >= cfg.within(hardDeadline) {
		t.Error("a look at a gated call can pass its raised deadline")
	}
}

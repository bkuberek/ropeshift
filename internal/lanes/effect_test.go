package lanes_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// An input that no session took did not happen, so a later attempt may be its first delivery. One that
// failed any other way may have reached a session, and nothing else is a delivery that never was.
func TestUndelivered(t *testing.T) {
	tests := []struct {
		name   string
		kind   lanes.EffectKind
		state  lanes.EffectState
		detail string
		want   bool
	}{
		{name: "no session took the input", kind: lanes.EffectInput, state: lanes.EffectFailed, detail: lanes.DetailNotDelivered + ": lane/1: no such session", want: true},
		{name: "the input failed another way", kind: lanes.EffectInput, state: lanes.EffectFailed, detail: "lane/1: the pane went away"},
		{name: "the input has no detail", kind: lanes.EffectInput, state: lanes.EffectFailed},
		{name: "the input was delivered", kind: lanes.EffectInput, state: lanes.EffectCompleted},
		{name: "the input is on its way", kind: lanes.EffectInput, state: lanes.EffectStarted, detail: lanes.DetailNotDelivered},
		{name: "a crash left the input unknown", kind: lanes.EffectInput, state: lanes.EffectUnknown, detail: lanes.DetailNotDelivered},
		{name: "a stop with the same words", kind: lanes.EffectStop, state: lanes.EffectFailed, detail: lanes.DetailNotDelivered},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effect := lanes.NewEffect(test.kind, "lane", 1, "", "label")
			effect.State, effect.Detail = test.state, test.detail
			if got := effect.Undelivered(); got != test.want {
				t.Errorf("undelivered %v, want %v", got, test.want)
			}
		})
	}
}

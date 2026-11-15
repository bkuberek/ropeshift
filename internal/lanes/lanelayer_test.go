package lanes

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// A lane's layer can add a gate, so the gates are not among the keys a route refuses.
func TestLaneLayerKeysLeaveOutTheGates(t *testing.T) {
	for _, key := range LaneLayerKeys() {
		if slices.Equal(key, []string{"agent", "gates"}) {
			t.Errorf("a route refuses %v", key)
		}
	}
}

// CheckLaneLayer compares two profiles however they were built, so it still refuses a profile that leaves
// out a gate of the one below it, though a profile resolved from layers never does.
func TestCheckLaneLayerStillRefusesADroppedGate(t *testing.T) {
	below := Profile{Agent: AgentSettings{Gates: []string{"Bash(git push *)", "Edit"}}}
	tests := []struct {
		name  string
		gates []string
		gate  string
	}{
		{name: "none left", gates: nil, gate: "Bash(git push *)"},
		{name: "one left out", gates: []string{"Bash(git push *)"}, gate: "Edit"},
		{name: "kept in another order", gates: []string{"Edit", "Bash(git push *)"}},
		{name: "one added", gates: []string{"Bash(git push *)", "Edit", "Read"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckLaneLayer(below, Profile{Agent: AgentSettings{Gates: tt.gates}})
			if tt.gate == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrBadProfile) || !strings.Contains(err.Error(), `"`+tt.gate+`"`) {
				t.Errorf("got %v, want a refusal that names %q", err, tt.gate)
			}
		})
	}
}

package worker

import (
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
)

// Both halves of the rule guard one write: neither the state nor the flag alone lets a lane park
// from where the table gives it no edge.
func TestWhereALaneMayPark(t *testing.T) {
	tests := []struct {
		name  string
		state lanes.ActualState
		past  bool
		want  bool
	}{
		{"a lane at rest", lanes.Idle, false, true},
		{"a lane in a turn", lanes.Working, false, true},
		{"a compaction with a bound behind it", lanes.Compacting, true, true},
		{"a compaction with none", lanes.Compacting, false, false},
		{"a lane at rest with the flag", lanes.Idle, true, true},
		{"a lane in backoff with the flag", lanes.Backoff, true, false},
		{"a lane handing off with the flag", lanes.HandingOff, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parksFrom(tt.state, lanes.ParkRequest{PastCompaction: tt.past}); got != tt.want {
				t.Errorf("got %v", got)
			}
		})
	}
}

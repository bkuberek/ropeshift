package lanes

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestLaneLayerFloorsAreWhatTheWorkerAccepts ties the floors a route accepts to the rule the worker holds a
// lane's layer to: a layer at its floor passes CheckLaneLayer whatever the layers below it say, and no
// value past the floor does, so the route and the worker cannot come to disagree.
func TestLaneLayerFloorsAreWhatTheWorkerAccepts(t *testing.T) {
	set := func(profile Profile, path []string, value int64) Profile {
		switch strings.Join(path, ".") {
		case "review.max_review_rounds":
			profile.Review.MaxReviewRounds = value
		case "resources.ports_per_lane":
			profile.Resources.PortsPerLane = value
		default:
			t.Fatalf("no floor is known for %v", path)
		}
		return profile
	}
	floors := LaneLayerFloors()
	if len(floors) != 2 {
		t.Fatalf("floors %+v", floors)
	}
	for _, floor := range floors {
		for _, below := range []int64{-3, 0, 1, 2, 3, 4, 10, 10000} {
			lower := set(Profile{}, floor.Path, below)
			if err := CheckLaneLayer(lower, set(lower, floor.Path, floor.Value)); err != nil {
				t.Errorf("%v at %d over %d: %v", floor.Path, floor.Value, below, err)
			}
		}
		// One past the floor raises the setting over layers that sit at it.
		over := set(Profile{}, floor.Path, floor.Value)
		if err := CheckLaneLayer(over, set(over, floor.Path, floor.Value+1)); !errors.Is(err, ErrBadProfile) {
			t.Errorf("%v at %d over %d: %v", floor.Path, floor.Value+1, floor.Value, err)
		}
	}
}

// TestLaneLayerKeysAreTheSettingsNoLayerChanges keeps the gates, the review bound, and the ports out of the
// keys a route refuses outright: a lane's layer may add a gate, and the other two have a floor.
func TestLaneLayerKeysAreTheSettingsNoLayerChanges(t *testing.T) {
	want := [][]string{{"hooks"}, {"agent", "credentials"}, {"agent", "instructions_file"}, {"worktree", "root"}, {"worktree", "branch_prefix"}}
	if got := LaneLayerKeys(); !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Errorf("keys %v, want %v", got, want)
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

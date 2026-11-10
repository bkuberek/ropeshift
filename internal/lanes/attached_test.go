package lanes_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
)

// A marker says someone is attached only while the worker keeps touching it, so the hook needs no
// word from the worker or from tmux, and a worker that stopped or a read that failed reads as nobody.
func TestAnAttachedMarkerIsFreshOnlyForAMoment(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name     string
		modified time.Time
		want     bool
	}{
		{"touched just now", now, true},
		{"touched a second ago", now.Add(-time.Second), true},
		{"touched at the bound", now.Add(-lanes.AttachedStale), true},
		{"touched past the bound", now.Add(-lanes.AttachedStale - time.Millisecond), false},
		{"touched long ago", now.Add(-time.Hour), false},
		{"touched a moment ahead, by a clock a little off", now.Add(time.Second), true},
		{"touched far ahead, by a clock that jumped", now.Add(time.Hour), false},
		{"never touched", time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lanes.Attached(tt.modified, now); got != tt.want {
				t.Errorf("Attached(%v) = %v, want %v", tt.modified, got, tt.want)
			}
		})
	}
}

func TestACompiledPolicyCarriesTheAttachedMarker(t *testing.T) {
	policy := lanes.CompilePolicy(lanes.PolicySpec{
		Worktree: "/work/lane-1", QuestionTool: lanes.QuestionToolTerminalIfAttached, AttachedMarker: "/state/lane-1/attached-2",
	})
	if policy.AttachedMarker != "/state/lane-1/attached-2" || policy.QuestionTool != lanes.QuestionToolTerminalIfAttached {
		t.Errorf("policy %+v", policy)
	}
}

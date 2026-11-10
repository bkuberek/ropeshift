package lanes_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

func TestABaselineWithNoRoomIsOversized(t *testing.T) {
	policy := lanes.ContextPolicy{RoleThresholds: lanes.RoleThresholds{HandoffTokens: 180000}, MinHeadroom: 40000}
	tests := []struct {
		name     string
		policy   lanes.ContextPolicy
		baseline int64
		want     bool
	}{
		{"plenty of room", policy, 30000, false},
		{"exactly the headroom", policy, 140000, false},
		{"one token past it", policy, 140001, true},
		{"past the handoff itself", policy, 190000, true},
		{"nothing measured yet", policy, 0, false},
		{"no handoff to protect", lanes.ContextPolicy{MinHeadroom: 40000}, 190000, false},
		{"no headroom asked for", lanes.ContextPolicy{RoleThresholds: lanes.RoleThresholds{HandoffTokens: 180000}}, 179000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.Oversized(tt.baseline); got != tt.want {
				t.Errorf("oversized %v", got)
			}
		})
	}
}

// TestAThresholdActsByLaneState is the design's table: each threshold against each state a lane can
// be in, with what a lane can and cannot do from there.
func TestAThresholdActsByLaneState(t *testing.T) {
	policy := lanes.ContextPolicy{
		RoleThresholds: lanes.RoleThresholds{WarnTokens: 120, CompactTokens: 150, HandoffTokens: 180}, MinHeadroom: 40,
	}
	const (
		under     = int64(149)
		atCompact = int64(150)
		atHand    = int64(180)
		room      = int64(30)
		noRoom    = int64(141)
	)
	tests := []struct {
		name   string
		policy lanes.ContextPolicy
		facts  lanes.ContextFacts
		want   lanes.ContextAct
	}{
		{"idle under every threshold", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: under, Baseline: room, CanCompact: true}, lanes.ContextWait},
		{"idle at the compact threshold", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: atCompact, Baseline: room, CanCompact: true}, lanes.ContextCompact},
		{"idle at the handoff threshold", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: atHand, Baseline: room, CanCompact: true}, lanes.ContextHandOff},
		{"idle after a compaction that failed", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: atCompact, Baseline: room, CanCompact: true, CompactFailed: true}, lanes.ContextHandOff},
		{"idle with no way to compact", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: atCompact, Baseline: room}, lanes.ContextHandOff},
		{"idle waiting on a compaction's next call", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: atHand, Baseline: room, CanCompact: true, CompactPending: true}, lanes.ContextWait},
		{"idle with an oversized pack, at handoff", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: atHand, Baseline: noRoom, CanCompact: true}, lanes.ContextCompact},
		{"idle with an oversized pack and no compaction left", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: atHand, Baseline: noRoom, CanCompact: true, CompactFailed: true}, lanes.ContextWait},
		{"idle with an oversized pack and no way to compact", policy, lanes.ContextFacts{State: lanes.Idle, Tokens: atHand, Baseline: noRoom}, lanes.ContextWait},
		{"working under every threshold", policy, lanes.ContextFacts{State: lanes.Working, Tokens: under, Baseline: room, CanCompact: true}, lanes.ContextWait},
		{"working at the compact threshold", policy, lanes.ContextFacts{State: lanes.Working, Tokens: atCompact, Baseline: room, CanCompact: true}, lanes.ContextWait},
		{"working at handoff", policy, lanes.ContextFacts{State: lanes.Working, Tokens: atHand, Baseline: room, CanCompact: true}, lanes.ContextInstruct},
		{"working at handoff, with no way to compact", policy, lanes.ContextFacts{State: lanes.Working, Tokens: atHand, Baseline: room}, lanes.ContextInstruct},
		{"working over compact, with no way to compact", policy, lanes.ContextFacts{State: lanes.Working, Tokens: atCompact, Baseline: room}, lanes.ContextInstruct},
		{"working after a compaction that failed", policy, lanes.ContextFacts{State: lanes.Working, Tokens: atCompact, Baseline: room, CanCompact: true, CompactFailed: true}, lanes.ContextInstruct},
		{"working with an oversized pack", policy, lanes.ContextFacts{State: lanes.Working, Tokens: atHand, Baseline: noRoom, CanCompact: true}, lanes.ContextWait},
		{"working on a compaction's next call", policy, lanes.ContextFacts{State: lanes.Working, Tokens: atHand, Baseline: room, CompactPending: true}, lanes.ContextWait},
		{"compacting past every threshold", policy, lanes.ContextFacts{State: lanes.Compacting, Tokens: atHand, Baseline: room, CanCompact: true}, lanes.ContextWait},
		{"waiting on a decision", policy, lanes.ContextFacts{State: lanes.NeedsDecision, Tokens: atHand, Baseline: room}, lanes.ContextWait},
		{"already handing off", policy, lanes.ContextFacts{State: lanes.HandingOff, Tokens: atHand, Baseline: room}, lanes.ContextWait},
		{"parking", policy, lanes.ContextFacts{State: lanes.Parking, Tokens: atHand, Baseline: room}, lanes.ContextWait},
		{"a status line reading zero is unknown", policy, lanes.ContextFacts{State: lanes.Working, Tokens: 0, Baseline: room}, lanes.ContextWait},
		{"no thresholds at all", lanes.ContextPolicy{}, lanes.ContextFacts{State: lanes.Idle, Tokens: atHand, CanCompact: true}, lanes.ContextWait},
		{"a compact threshold alone", lanes.ContextPolicy{RoleThresholds: lanes.RoleThresholds{CompactTokens: 150}}, lanes.ContextFacts{State: lanes.Idle, Tokens: atHand, CanCompact: true}, lanes.ContextCompact},
		{"a handoff threshold alone", lanes.ContextPolicy{RoleThresholds: lanes.RoleThresholds{HandoffTokens: 180}}, lanes.ContextFacts{State: lanes.Idle, Tokens: atHand, CanCompact: true}, lanes.ContextHandOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.Decide(tt.facts); got != tt.want {
				t.Errorf("acts %q, want %q", got, tt.want)
			}
		})
	}
}

// A compaction counts only from the next call's usage, because the end of a compaction does not
// show that context fell.
func TestACompactionCountsOnlyFromTheNextCall(t *testing.T) {
	policy := lanes.ContextPolicy{RoleThresholds: lanes.RoleThresholds{CompactTokens: 150}}
	for tokens, want := range map[int64]bool{0: false, 149: true, 150: false, 400: false} {
		if got := policy.Compacted(tokens); got != want {
			t.Errorf("%d tokens read compacted %v", tokens, got)
		}
	}
}

func TestAPolicyReadsItsRole(t *testing.T) {
	settings := lanes.ContextSettings{
		MinHeadroomTokens: 7,
		Roles:             map[string]lanes.RoleThresholds{lanes.RoleWriter: {WarnTokens: 1, CompactTokens: 2, HandoffTokens: 3}},
	}
	got := lanes.PolicyFor(settings, lanes.RoleWriter)
	if got.WarnTokens != 1 || got.CompactTokens != 2 || got.HandoffTokens != 3 || got.MinHeadroom != 7 {
		t.Errorf("policy %+v", got)
	}
	if none := lanes.PolicyFor(settings, lanes.RoleOrchestrator); none.HandoffTokens != 0 || none.MinHeadroom != 7 {
		t.Errorf("a role with no thresholds reads %+v", none)
	}
}

// A planning lane runs on the writer's thresholds until a profile gives the planner its own, because
// a role with no thresholds would never compact or hand off.
func TestAPlannerWithNoThresholdsOfItsOwnReadsTheWriters(t *testing.T) {
	writer := lanes.RoleThresholds{WarnTokens: 1, CompactTokens: 2, HandoffTokens: 3}
	planner := lanes.RoleThresholds{WarnTokens: 4, CompactTokens: 5, HandoffTokens: 6}
	tests := []struct {
		name  string
		roles map[string]lanes.RoleThresholds
		want  lanes.RoleThresholds
	}{
		{"the writer's when the profile names no planner", map[string]lanes.RoleThresholds{lanes.RoleWriter: writer}, writer},
		{"its own when the profile names one", map[string]lanes.RoleThresholds{lanes.RoleWriter: writer, lanes.RolePlanner: planner}, planner},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := lanes.ContextSettings{Roles: tt.roles}
			if got := settings.ThresholdsFor(lanes.RolePlanner); got != tt.want {
				t.Errorf("thresholds %+v, want %+v", got, tt.want)
			}
			if got := lanes.PolicyFor(settings, lanes.RolePlanner); got.RoleThresholds != tt.want {
				t.Errorf("policy %+v, want %+v", got.RoleThresholds, tt.want)
			}
		})
	}
	if got := (lanes.ContextSettings{Roles: map[string]lanes.RoleThresholds{lanes.RoleWriter: writer}}).ThresholdsFor(lanes.RoleOrchestrator); got != (lanes.RoleThresholds{}) {
		t.Errorf("an orchestrator the profile never named reads the writer's: %+v", got)
	}
}

// A review lane runs on the writer's thresholds until a profile gives the reviewer its own, for the
// same reason a planner does.
func TestAReviewerWithNoThresholdsOfItsOwnReadsTheWriters(t *testing.T) {
	writer := lanes.RoleThresholds{WarnTokens: 1, CompactTokens: 2, HandoffTokens: 3}
	reviewer := lanes.RoleThresholds{WarnTokens: 7, CompactTokens: 8, HandoffTokens: 9}
	tests := []struct {
		name  string
		roles map[string]lanes.RoleThresholds
		want  lanes.RoleThresholds
	}{
		{"the writer's when the profile names no reviewer", map[string]lanes.RoleThresholds{lanes.RoleWriter: writer}, writer},
		{"its own when the profile names one", map[string]lanes.RoleThresholds{lanes.RoleWriter: writer, lanes.RoleReviewer: reviewer}, reviewer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := lanes.ContextSettings{Roles: tt.roles}
			if got := settings.ThresholdsFor(lanes.RoleReviewer); got != tt.want {
				t.Errorf("thresholds %+v, want %+v", got, tt.want)
			}
			if got := lanes.PolicyFor(settings, lanes.RoleReviewer); got.RoleThresholds != tt.want {
				t.Errorf("policy %+v, want %+v", got.RoleThresholds, tt.want)
			}
		})
	}
}

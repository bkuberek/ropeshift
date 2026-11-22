package lanes_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// A result holds for good only when it passed every check a probe runs now: a check that failed, one no session
// reached, one the result does not hold at all, and a probe that could not run each leave the version to be probed
// again, and a check the probe no longer runs does not.
func TestAResultHoldsOnlyWhenItPassedEveryCheckAProbeRuns(t *testing.T) {
	t.Parallel()
	named := []lanes.Surface{"a", "b"}
	for _, tt := range []struct {
		name  string
		q     lanes.Qualification
		named []lanes.Surface
		want  bool
	}{
		{"every named check passed", lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed, "b": lanes.CheckPassed}}, named, true},
		{"one failed", lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed, "b": lanes.CheckFailed}}, named, false},
		{"one unobserved", lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed, "b": lanes.CheckUnobserved}}, named, false},
		{"one the result does not hold", lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed}}, named, false},
		{"a check the probe no longer runs is not asked for", lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed, "b": lanes.CheckPassed, "c": lanes.CheckUnobserved}}, named, true},
		{"a degraded result", lanes.Qualification{Degraded: true, Checks: map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed, "b": lanes.CheckPassed}}, named, false},
		{"a probe that could not run", lanes.Unprobed("d", "1.0.0", "", nil), named, false},
		{"no check named", lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed}}, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.q.PassedAll(tt.named); got != tt.want {
				t.Errorf("PassedAll(%v) = %v, want %v", tt.named, got, tt.want)
			}
		})
	}
}

package lanes_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// A result is complete only when it holds checks and every one passed: a check that failed or was
// never observed, and a probe that could not run and holds no check, each leave the version to be probed again.
func TestAResultWithEveryCheckPassedIsAllPassed(t *testing.T) {
	t.Parallel()
	passed := map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed, "b": lanes.CheckPassed}
	failed := map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed, "b": lanes.CheckFailed}
	unobserved := map[lanes.Surface]lanes.CheckResult{"a": lanes.CheckPassed, "b": lanes.CheckUnobserved}
	for _, tt := range []struct {
		name string
		q    lanes.Qualification
		want bool
	}{
		{"every check passed", lanes.Qualification{Checks: passed}, true},
		{"one check failed", lanes.Qualification{Checks: failed}, false},
		{"one check unobserved", lanes.Qualification{Checks: unobserved}, false},
		{"a probe that could not run", lanes.Unprobed("d", "1.0.0", "", nil), false},
		{"no checks", lanes.Qualification{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.q.AllPassed(); got != tt.want {
				t.Errorf("AllPassed() = %v, want %v", got, tt.want)
			}
		})
	}
}

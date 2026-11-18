package claudecode

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// A headless generation starts with the client's own cap only where the version's check of the flag passed, and a
// budget of none, a pane, or a version that failed the check or never ran it starts without.
func TestTheBudgetFlagGoesOnlyToAHeadlessGenerationOnAVersionThatObservedIt(t *testing.T) {
	passed := UseOf(lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{SurfaceMaxBudget: lanes.CheckPassed}})
	failed := UseOf(lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{SurfaceMaxBudget: lanes.CheckFailed}})
	unobserved := UseOf(lanes.Qualification{Checks: map[lanes.Surface]lanes.CheckResult{SurfaceMaxBudget: lanes.CheckUnobserved}})
	tests := []struct {
		name string
		use  Use
		spec ports.SessionSpec
		want []string
	}{
		{"observed, headless", passed, ports.SessionSpec{MaxBudgetUSD: 27.5}, []string{"--max-budget-usd", "27.5000"}},
		{"observed, a run mode of none says headless", passed, ports.SessionSpec{RunMode: ports.RunModeHeadless, MaxBudgetUSD: 0.125}, []string{"--max-budget-usd", "0.1250"}},
		{"observed, no budget", passed, ports.SessionSpec{}, nil},
		{"observed, a pane has no such flag", passed, ports.SessionSpec{RunMode: ports.RunModeInteractive, MaxBudgetUSD: 27.5}, nil},
		{"failed", failed, ports.SessionSpec{MaxBudgetUSD: 27.5}, nil},
		{"unobserved", unobserved, ports.SessionSpec{MaxBudgetUSD: 27.5}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := budgetArgs(tt.use, tt.spec); !slices.Equal(got, tt.want) {
				t.Errorf("args %v, want %v", got, tt.want)
			}
		})
	}
}

// A version that never passed the budget check is not degraded for it: the flag is a backstop, and the worker holds the
// lane to its limit either way.
func TestAVersionWithNoBudgetFlagIsNotDegraded(t *testing.T) {
	q := lanes.Qualification{Driver: DriverName, Version: "2.1.400", Source: lanes.QualifiedByProbe, Checks: map[lanes.Surface]lanes.CheckResult{}}
	for _, surface := range Surfaces {
		q.Checks[surface] = lanes.CheckPassed
	}
	for _, result := range []lanes.CheckResult{lanes.CheckFailed, lanes.CheckUnobserved} {
		q.Checks[SurfaceMaxBudget] = result
		if got := Judge(q, true); got.Degraded {
			t.Errorf("a version whose budget check is %s runs degraded", result)
		}
	}
}

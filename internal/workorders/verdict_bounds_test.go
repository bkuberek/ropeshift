package workorders

import (
	"errors"
	"strings"
	"testing"
)

// TestVerdictReasonsOverTheirBoundAreRefused holds that a verdict's reasons are at most 4 KiB, as a note's are.
func TestVerdictReasonsOverTheirBoundAreRefused(t *testing.T) {
	verdict := func(reasons string) CriterionVerdict {
		return CriterionVerdict{Criterion: "c1", Verdict: VerdictPass, Reasons: reasons}
	}
	if err := verdict(strings.Repeat("x", 4096)).Validate(); err != nil {
		t.Errorf("reasons of 4096 bytes: %v", err)
	}
	err := verdict(strings.Repeat("x", 4097)).Validate()
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "4096") {
		t.Errorf("reasons of 4097 bytes: %v, want ErrInvalid naming the bound", err)
	}
	report := Report{Outcome: OutcomeCompleted, Verdicts: []CriterionVerdict{verdict(strings.Repeat("x", 4097))}}
	if err := report.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a report with such a verdict: %v", err)
	}
}

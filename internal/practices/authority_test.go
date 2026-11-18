package practices_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/practices"
)

// The merge authorities the `ci` practice reads are the values of the repository record's merge authority: the
// no-CI recipe holds at the two levels where a person merges, and at no other.
func TestTheCIRecipeHoldsAtTheLedgersPersonAuthorities(t *testing.T) {
	statusAt := func(authority ledger.MergeAuthority) practices.Status {
		in := practices.Input{
			Paths: []string{".pre-commit-config.yaml"}, MergeAuthority: string(authority),
			Read: func(string) (string, bool) { return "", false },
		}
		for _, p := range practices.Evaluate(in).Practices {
			if p.ID == practices.CI {
				return p.Status
			}
		}
		t.Fatal("the result holds no status for ci")
		return ""
	}
	for _, authority := range []ledger.MergeAuthority{ledger.PersonMerges, ledger.PersonApproves} {
		if got := statusAt(authority); got != practices.StatusMet {
			t.Errorf("ci at %s is %s, want met by the no-CI recipe", authority, got)
		}
	}
	if got := statusAt(ledger.ReviewApproves); got != practices.StatusFinding {
		t.Errorf("ci at %s is %s, want a finding", ledger.ReviewApproves, got)
	}
}

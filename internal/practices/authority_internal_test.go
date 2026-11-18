package practices

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The merge authorities the `ci` practice reads are the values of the repository record's merge authority.
func TestMergeAuthorityConstantsEqualTheLedgers(t *testing.T) {
	if mergeAuthorityPersonMerges != string(ledger.PersonMerges) {
		t.Errorf("person merges is %q, the ledger's is %q", mergeAuthorityPersonMerges, ledger.PersonMerges)
	}
	if mergeAuthorityPersonApproves != string(ledger.PersonApproves) {
		t.Errorf("person approves is %q, the ledger's is %q", mergeAuthorityPersonApproves, ledger.PersonApproves)
	}
}

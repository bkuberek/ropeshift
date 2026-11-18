package control

import (
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// consolidationNote is what the pull request of an adoption branch that imports a design repository's history says
// of its merge (FR-ADOPT-13): at person_merges, that the person merges it with a merge commit, and at the levels
// where ropeshift merges, that ropeshift does, whatever the repository's merge method. Any other branch says nothing.
func consolidationNote(w workorders.WorkOrder, record ledger.Repository) string {
	if !w.Restructure.Consolidates() {
		return ""
	}
	if record.Level() == ledger.PersonMerges {
		return "\nThis branch imports a design repository's history in a merge commit. At person_merges you merge it yourself: " +
			"merge it with a merge commit, not a squash or a rebase. " + planning.MergeCommitKeepsHistory + "\n"
	}
	return "\nThis branch imports a design repository's history in a merge commit, so ropeshift merges it with a merge commit, " +
		"whatever the repository's merge method. " + planning.MergeCommitKeepsHistory + "\n"
}

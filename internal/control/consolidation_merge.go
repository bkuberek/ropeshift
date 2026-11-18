package control

import (
	"fmt"
	"slices"
	"strings"

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

// mergeCommitRefusals are words a forge or a push gives when a base takes no merge commit: a required linear
// history, or merge commits turned off.
var mergeCommitRefusals = []string{"linear history", "must not contain merge commits", "merge commits are not allowed", "required_linear_history"}

// mergeCommitRefused is what a held merge of a consolidating adoption branch adds to the refusal when the base takes
// no merge commit, which is the only merge this branch is given: why, and what a member can do. Any other refusal,
// and any other branch, adds nothing.
func mergeCommitRefused(w workorders.WorkOrder, base, detail string) string {
	if !w.Restructure.Consolidates() {
		return ""
	}
	said := strings.ToLower(detail)
	if !slices.ContainsFunc(mergeCommitRefusals, func(words string) bool { return strings.Contains(said, words) }) {
		return ""
	}
	return fmt.Sprintf(". The base %s requires a linear history or takes no merge commit, and this branch imports a design repository's history, "+
		"which only a merge commit keeps, so ropeshift merges it no other way: allow merge commits on %s, or merge the branch yourself with a merge commit", base, base)
}

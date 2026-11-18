package lanes

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/workorders"
)

// FR-ADOPT-2: the adopt brief tells the session that the inventory lists open pull requests, that a pull request
// it keeps continues its head branch, and that each one left out goes in [[left_out]] as pull:<number>.
func TestAnAdoptBriefTellsTheSessionWhatToDoWithOpenPullRequests(t *testing.T) {
	brief := Brief(BriefSpec{WorkOrder: "wo", Title: "Adopt the project", Kind: workorders.KindPlan, Adopt: true})
	for _, want := range []string{
		"under pull_requests", "more_pull_requests", "has an omitted entry that says why",
		"A pull request they keep is its head branch", "continues",
		`ref = "pull:<number>"`, `ref = "branch:<name>"`,
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("the adopt brief says nothing of %q", want)
		}
	}
	if strings.Contains(brief, "does not read yet") {
		t.Errorf("the adopt brief still says the inventory reads nothing of pull requests")
	}
}

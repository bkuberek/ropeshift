package control

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ports"
)

type lanesRequests struct {
	ports.ApprovalReader
	rows []authority.Approval
}

func (r lanesRequests) OfLane(string) ([]authority.Approval, error) { return r.rows, nil }

// A decision nobody spent is owed to whichever session comes next, because a generation that parks
// before it makes the call leaves it covered, and the session after it has no other way to hear.
// How a request came out belongs to the generation that asked.
func TestWhatAResumeOwesOfALanesRequests(t *testing.T) {
	spent := authority.Approval{Generation: 2, Subject: "Bash", Input: `{"command":"a"}`, Summary: "a", State: authority.ApprovalUsed}
	owed := authority.Approval{Generation: 1, Subject: "Bash", Input: `{"command":"b"}`, Summary: "b", State: authority.ApprovalApproved}
	denied := authority.Approval{Generation: 2, Subject: "Bash", Input: `{"command":"c"}`, Summary: "c", State: authority.ApprovalDenied}
	older := authority.Approval{Generation: 1, Subject: "Bash", Input: `{"command":"d"}`, Summary: "d", State: authority.ApprovalDenied}
	outcomes, calls, err := approvalOutcomes(lanesRequests{rows: []authority.Approval{spent, owed, denied, older}}, "lane-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{owed.Repeat()}; !slices.Equal(calls, want) {
		t.Errorf("calls %q, want %q", calls, want)
	}
	if want := []string{denied.Outcome()}; !slices.Equal(outcomes, want) {
		t.Errorf("outcomes %q, want %q", outcomes, want)
	}
}

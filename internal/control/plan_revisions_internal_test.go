package control

import (
	"encoding/json"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/planning"
)

// approvalShowing is a plan approval decided at a moment, with the decisions it recorded as shown, or none recorded
// when shown is nil.
func approvalShowing(t *testing.T, decidedAt int64, shown []string) authority.Approval {
	t.Helper()
	detail := planning.PlanDetail{Title: "Factory"}
	if shown != nil {
		detail.Shown = &planning.ShownDecisions{IDs: shown}
	}
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	return authority.Approval{ID: "a1", Kind: authority.KindPlan, DecidedAt: decidedAt, Detail: string(data)}
}

func TestAnApprovalShowsTheDecisionsItRecordedWhateverTimeTheyWereListedAt(t *testing.T) {
	// d1 and d2 were listed in the millisecond of the approval, and d3 by a clock that was set back behind it.
	open := planning.Revision{ID: "r1", PlanID: "factory"}.Extend("d1", 10, nil).Extend("d2", 10, nil).Extend("d3", 5, nil)

	shown, later := decisionsShown(open, approvalShowing(t, 10, []string{"d1"}))
	if !slices.Equal(shown, []string{"d1"}) || !slices.Equal(later, []string{"d2", "d3"}) {
		t.Errorf("an approval that recorded d1 showed %v and left %v, want d1 shown and d2 and d3 later", shown, later)
	}
	shown, later = decisionsShown(open, approvalShowing(t, 10, []string{"d1", "d2", "d3"}))
	if len(shown) != 3 || len(later) != 0 {
		t.Errorf("an approval that recorded every decision showed %v and left %v", shown, later)
	}
	shown, later = decisionsShown(open, approvalShowing(t, 10, []string{}))
	if len(shown) != 0 || !slices.Equal(later, []string{"d1", "d2", "d3"}) {
		t.Errorf("an approval that recorded none showed %v and left %v, want every decision later: it is not an approval that recorded nothing", shown, later)
	}
}

func TestAnApprovalRecordedBeforeItKeptWhatItShowedIsReadByTheClock(t *testing.T) {
	open := planning.Revision{ID: "r1", PlanID: "factory"}.Extend("d1", 10, nil).Extend("d2", 20, nil).Extend("d3", 30, nil)

	for _, test := range []struct {
		at           int64
		shown, later []string
	}{
		{5, nil, []string{"d1", "d2", "d3"}},
		{10, []string{"d1"}, []string{"d2", "d3"}},
		{25, []string{"d1", "d2"}, []string{"d3"}},
		{30, []string{"d1", "d2", "d3"}, nil},
	} {
		shown, later := decisionsShown(open, approvalShowing(t, test.at, nil))
		if !slices.Equal(shown, test.shown) || !slices.Equal(later, test.later) {
			t.Errorf("an approval at %d with no list showed %v and left %v, want %v and %v", test.at, shown, later, test.shown, test.later)
		}
	}
	if shown, later := decisionsShown(open, authority.Approval{ID: "a2", Kind: authority.KindPlan, DecidedAt: 20}); !slices.Equal(shown, []string{"d1", "d2"}) || !slices.Equal(later, []string{"d3"}) {
		t.Errorf("an approval with no detail at all showed %v and left %v", shown, later)
	}
}

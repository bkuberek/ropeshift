package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A lane the control plane withholds, which a worker too old to keep a delegate's layer restricted would run
// unrestricted, is offered its claim and nothing of what the lane reads, practices findings included.
func TestAWithheldLeaseCarriesNoPracticesFindings(t *testing.T) {
	lease := ledger.DesiredLease{
		WorkOrder: "wo", ClaimID: "c", Fence: 1, State: "claimed", Lane: "lane-1", Kind: "plan",
		Delegated: &ledger.DelegatedLayer{Delegate: "member-1/dlg_1", PolicyVersion: 2, Restricted: true},
		Practices: []ledger.PracticesSummary{{Repository: "repo-1", Name: "widgets", Checked: true}},
	}
	for _, protocol := range []int{0, ledger.ProtocolDelegatedLayers - 1} {
		if got := offeredLease(lease, protocol); got.Practices != nil || got.Lane != "" {
			t.Errorf("a worker on protocol %d is offered %+v", protocol, got)
		}
	}
	if got := offeredLease(lease, ledger.ProtocolVersion); len(got.Practices) != 1 {
		t.Errorf("a worker that keeps the layer restricted is offered %+v", got)
	}
}

// Only planning work reads the findings: no other kind of work order does, though the project holds them.
func TestOnlyPlanningWorkIsToldThePracticesFindings(t *testing.T) {
	for kind, want := range map[workorders.Kind]bool{
		workorders.KindPlan: true, workorders.KindChange: false, workorders.KindRework: false,
		workorders.KindReview: false, workorders.KindContractReview: false,
	} {
		if got := carriesPractices(kind); got != want {
			t.Errorf("carriesPractices(%s) = %v, want %v", kind, got, want)
		}
	}
}

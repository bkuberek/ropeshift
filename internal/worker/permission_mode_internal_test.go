package worker

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A review lane and an orchestrator run in the more careful of their profile's mode and `default`, an assistant in
// `default`, and every other lane in its profile's mode.
func TestEachRoleRunsInItsPermissionMode(t *testing.T) {
	t.Parallel()
	careful := map[string]string{"plan": "plan", "dontAsk": "dontAsk", "default": "default", "acceptEdits": "default", "auto": "default"}
	for mode, held := range careful {
		var profile lanes.Profile
		profile.Agent.PermissionMode = mode
		roles := []struct {
			name  string
			lease ledger.DesiredLease
			want  string
		}{
			{"a change lane", ledger.DesiredLease{WorkOrder: "wo", Kind: string(workorders.KindChange)}, mode},
			{"a planning lane", ledger.DesiredLease{WorkOrder: "wo", Kind: string(workorders.KindPlan)}, mode},
			{"a review lane", ledger.DesiredLease{WorkOrder: "wo", Kind: string(workorders.KindReview)}, held},
			{"an orchestrator", ledger.DesiredLease{}, held},
			{"an assistant", ledger.DesiredLease{Kind: ledger.RunKindAssistant}, "default"},
		}
		for _, role := range roles {
			run := &laneRun{lease: role.lease}
			if got := run.permissionMode(profile); got != role.want {
				t.Errorf("%s under %s runs in %q, want %q", role.name, mode, got, role.want)
			}
		}
	}
}

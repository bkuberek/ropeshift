package control_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A lane that was dispatched and has not reported has no row, and no breaker to reset. A reset of it is a move the work
// order cannot make, as a reset of a lane that did not trip is, and not a lane the ledger cannot find.
func TestAResetOfALaneThatHasNotReportedIsRefusedAsAMove(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	r.ready("pa", "wo")
	if err := r.claim("pa", "wo"); err != nil {
		t.Fatal(err)
	}
	leases := control.NewLeases(r.stores["pa"])
	if _, err := leases.Dispatch(ctx, "pa", "wo", "lane-wo", projectMember); err != nil {
		t.Fatal(err)
	}
	err := leases.ResetLane(ctx, "pa", "wo", projectMember)
	if !errors.Is(err, workorders.ErrIllegalMove) || errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("a reset of a lane that has not reported answered %v, want an illegal move", err)
	}
	if !strings.Contains(err.Error(), "the lane of work order wo has not reported, and only a tripped lane is reset") {
		t.Errorf("the reset was refused with %q", err)
	}
}

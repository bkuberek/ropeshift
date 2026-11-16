package contract

import "testing"

// TestOnlyTheTransactionsThatMakeAnEstimateMayReadOne holds the blind store's leave to the transactions where an
// estimate is made, so a retry, a contract review of a plan, and intake's settling and proposing, which make none,
// are held blind.
func TestOnlyTheTransactionsThatMakeAnEstimateMayReadOne(t *testing.T) {
	for _, op := range []string{"work_order.create", "work_order.record", "intake.item", "intake.plan_version", "decision.overturn"} {
		if !makers(op) {
			t.Errorf("%s makes an estimate and may read one", op)
		}
	}
	for _, op := range []string{
		"work_order.retry", "work_order.review_plan", "work_order.move", "intake.settle", "intake.propose", "intake.file", "intake.plan",
		"claim", "lapse", "lane.end", "acceptance.accept", "integration.merged", "read",
	} {
		if makers(op) {
			t.Errorf("%s makes no estimate and may not read one", op)
		}
	}
}

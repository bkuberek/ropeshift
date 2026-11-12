package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// A failed lane leaves its work failed, and a member's retry returns it to ready with one more attempt,
// because a member is held to no recovery policy (ADR-0014).
func TestAMemberRetriesFailedWork(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Running("wo", "Try the flaky call").End(workorders.OutcomeFailed, workorders.ReasonCrashed)
	ctx := t.Context()
	row, err := d.Client.WorkOrder(ctx, "wo")
	if err != nil {
		t.Fatal(err)
	}
	if row.State != workorders.Failed {
		t.Fatalf("a failed lane left %s", row.State)
	}
	_, err = d.Client.Retry(ctx, "wo", api.Retry{})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	retried, err := d.Client.Retry(ctx, "wo", api.Retry{Reason: "the network came back"})
	if err != nil {
		t.Fatal(err)
	}
	if retried.State != workorders.Ready || retried.WorkOrder != "wo" {
		t.Errorf("answered %+v", retried)
	}
	if row, err = d.Client.WorkOrder(ctx, "wo"); err != nil {
		t.Fatal(err)
	}
	if row.State != workorders.Ready || row.MaxAttempts != row.Attempts+1 {
		t.Errorf("the record reads %s with %d of %d attempts", row.State, row.Attempts, row.MaxAttempts)
	}
	_, err = d.Client.Retry(ctx, "wo", api.Retry{Reason: "again"})
	refusedWith(t, err, http.StatusConflict, api.CodeIllegalMove)
}

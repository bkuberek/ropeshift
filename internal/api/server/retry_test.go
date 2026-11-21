package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// A failed lane leaves its work failed, and a member's retry returns it to ready with one more attempt,
// because a member is held to no recovery policy (ADR-0014).
func TestAMemberRetriesFailedWork(t *testing.T) {
	t.Parallel()
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

// The words the web UI sends for a retry whose note a member left empty are the member's, and the retry names the lane
// whose failure it answers, so what that lane wrote stays the lane's and never becomes the member's reason.
func TestARetryNamesTheFailedLaneAndKeepsTheMembersWords(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	const said = "It gave up on the flaky call"
	w.Running("wo", "Try the flaky call").Finish(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: said}})
	ctx := t.Context()
	const fixed = "Retried after the recorded failure"
	if _, err := d.Client.Retry(ctx, "wo", api.Retry{Reason: fixed}); err != nil {
		t.Fatal(err)
	}
	page, err := d.Client.Audit(ctx, api.AuditQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]workorders.Change{}
	var retried []byte
	for _, event := range page.Events {
		if event.SubjectID != "wo" {
			continue
		}
		var change workorders.Change
		if err := json.Unmarshal(event.Data, &change); err != nil {
			t.Fatal(err)
		}
		changes[event.Type] = change
		if event.Type == workorders.EventType(workorders.TriggerRetried) {
			retried = event.Data
		}
	}
	failed, ok := changes[workorders.EventType(workorders.TriggerAttemptsExhausted)]
	if !ok || failed.Lane == "" {
		t.Fatalf("no lane end names the lane: %+v", changes)
	}
	got := changes[workorders.EventType(workorders.TriggerRetried)]
	if got.Reason != fixed || got.Lane != failed.Lane {
		t.Errorf("the retry recorded reason %q for lane %q, want %q for the failed lane %q", got.Reason, got.Lane, fixed, failed.Lane)
	}
	if strings.Contains(string(retried), said) {
		t.Errorf("the retry carries the lane's words as the member's: %s", retried)
	}
}

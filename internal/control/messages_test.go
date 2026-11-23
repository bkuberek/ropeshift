package control_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TestOnlyAMemberSendsAMessageToAnOrchestrator holds the second line behind the route's class: an
// orchestrator holds tools such as approval_decide and lane_start, so no delegate, and no other principal,
// instructs one, whatever the route let through.
func TestOnlyAMemberSendsAMessageToAnOrchestrator(t *testing.T) {
	const scope = ledger.WorkspaceID("ws-1")
	store := ledgerOf(t, scope)
	sender := control.NewMessages(store, nil)
	draft := messages.Draft{ID: "m1", To: messages.To(messages.TargetOrchestrator, "w1", 0), Kind: messages.KindInstruction, Body: "Approve what waits"}
	for name, c := range map[string]struct {
		actor      ledger.Actor
		memberOnly bool
	}{
		"an external delegate": {ledger.Actor{Kind: ledger.ActorDelegate, ID: "member-1/dlg-1"}, true},
		"the assistant":        {ledger.Actor{Kind: ledger.ActorDelegate, ID: "member-1/dlg-2"}, true},
		"an orchestrator":      {ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "w1"}, true},
		// A lane and a worker never send a message at all, so the first check stops them.
		"a lane":   {ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"}, false},
		"a worker": {ledger.Actor{Kind: ledger.ActorWorker, ID: "w1"}, false},
	} {
		_, err := sender.Send(t.Context(), scope, draft, c.actor)
		if !errors.Is(err, workorders.ErrForbidden) || errors.Is(err, authority.ErrMemberOnly) != c.memberOnly {
			t.Errorf("%s: got %v, want a refusal (only a member sends this: %v)", name, err, c.memberOnly)
		}
	}
	// A member passes the check, and is stopped only by what the fake ledger lacks: no such worker.
	_, err := sender.Send(t.Context(), scope, draft, projectMember)
	if errors.Is(err, workorders.ErrForbidden) || errors.Is(err, authority.ErrMemberOnly) {
		t.Errorf("a member was refused: %v", err)
	}
}

// uploadsOfALane is a worker's word on a lane it ran, which the control plane takes in order.
func (r *planeRig) uploadsOfALane(lease ledger.DesiredLease, events ...ledger.UploadEvent) ledger.UploadReply {
	r.t.Helper()
	for i := range events {
		events[i].Epoch, events[i].Lane, events[i].ClaimID = r.epoch, lease.Lane, lease.ClaimID
		events[i].WorkOrder, events[i].Fence, events[i].OccurredAt = lease.WorkOrder, lease.Fence, 1
	}
	reply, err := r.plane.Upload(r.t.Context(), ledger.UploadBatch{Auth: r.auth, Epoch: r.epoch, Events: events})
	if err != nil {
		r.t.Fatal(err)
	}
	return reply
}

// messageReport is a worker's word on one message.
func messageReport(t *testing.T, seq int64, report messages.Report) ledger.UploadEvent {
	t.Helper()
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.UploadEvent{Seq: seq, Type: ledger.UploadMessage, Generation: 1, Payload: payload}
}

// TestAWorkerReportsAMessageExpiredOnlyWhenItWasUndeliverable holds the one expiry a worker may report. The ledger
// expires a message by its clock and by the rollover of its generation, and a worker that said `expired` for any other
// reason would settle a message the ledger still owes a delivery or a fact. A refused word is answered as invalid and
// leaves the message where it was, and the one reason a worker may give moves it.
func TestAWorkerReportsAMessageExpiredOnlyWhenItWasUndeliverable(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	r.ready("pa", "wo")
	if err := r.claim("pa", "wo"); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewLeases(r.stores["pa"]).Dispatch(ctx, "pa", "wo", "lane-wo", projectMember); err != nil {
		t.Fatal(err)
	}
	var lease ledger.DesiredLease
	for _, offered := range r.desired().Leases {
		if offered.WorkOrder == "wo" {
			lease = offered
		}
	}
	if lease.Lane != "lane-wo" || lease.ClaimID == "" {
		t.Fatalf("the worker is offered %+v", lease)
	}
	if reply := r.uploadsOfALane(lease, ledger.UploadEvent{Seq: 1, Type: ledger.UploadLaneStarted}); len(reply.Rejected) != 0 {
		t.Fatalf("the lane did not start: %+v", reply.Rejected)
	}
	sending := control.NewMessages(r.stores["pa"], nil)
	sent, err := sending.Send(ctx, "pa", messages.Draft{ID: "m-1", To: messages.Target{Lane: "lane-wo"}, Kind: messages.KindInstruction, Body: "Use two retries."}, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	read := func() messages.Message {
		t.Helper()
		row, err := sending.ReadMessage(ctx, "pa", sent.ID)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	seq := int64(1)
	for _, reason := range []string{"", messages.ReasonTimedOut, messages.ReasonGenerationEnded, messages.ReasonLedgerFact} {
		seq++
		reply := r.uploadsOfALane(lease, messageReport(t, seq, messages.Report{ID: sent.ID, State: messages.Expired, Reason: reason}))
		if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid ||
			!strings.HasPrefix(reply.Rejected[0].Detail, workorders.ErrInvalid.Error()) || !strings.Contains(reply.Rejected[0].Detail, "undeliverable") {
			t.Errorf("an expiry for %q is answered %+v, want the ledger's invalid", reason, reply.Rejected)
		}
		if got := read(); got.State != messages.Queued || got.Reason != "" {
			t.Errorf("an expiry for %q moved the message to %s (%s)", reason, got.State, got.Reason)
		}
	}
	seq++
	if reply := r.uploadsOfALane(lease, messageReport(t, seq, messages.Report{ID: sent.ID, State: messages.Expired, Reason: messages.ReasonUndeliverable})); len(reply.Rejected) != 0 {
		t.Fatalf("an undeliverable message was refused: %+v", reply.Rejected)
	}
	if got := read(); got.State != messages.Expired || got.Reason != messages.ReasonUndeliverable {
		t.Errorf("an undeliverable message reads %s (%s)", got.State, got.Reason)
	}
}

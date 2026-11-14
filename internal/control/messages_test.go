package control_test

import (
	"errors"
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

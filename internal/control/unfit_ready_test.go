package control_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// record records work in a project, which waits in contract review until it passes.
func (r *planeRig) record(project ledger.WorkspaceID, order workorders.WorkOrder) {
	r.t.Helper()
	_, err := control.NewWorkOrders(r.stores[project]).Record(r.t.Context(), project, control.LedgerWork{
		Order: order, Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, projectMember)
	if err != nil {
		r.t.Fatal(err)
	}
}

// madeReadyEarlier moves work from contract review into ready with no definition of ready, as a build
// that checked less of a work order's agent did.
func (r *planeRig) madeReadyEarlier(project ledger.WorkspaceID, id string) {
	r.t.Helper()
	err := r.stores[project].Write(r.t.Context(), project, "test.ready_earlier", func(tx ports.WriteTx) error {
		won, err := tx.WorkOrders().Move(id, workorders.ContractReview, workorders.Ready)
		if err == nil && !won {
			err = workorders.ErrIllegalMove
		}
		if err != nil {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerReadyPassed), id, workorders.Change{Trigger: workorders.TriggerReadyPassed}, projectMember)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// sentBack is the state of a work order and the reason of the last move that sent it back to review.
func (r *planeRig) sentBack(project ledger.WorkspaceID, id string) (workorders.State, string) {
	r.t.Helper()
	var state workorders.State
	var reason string
	err := r.stores[project].Read(r.t.Context(), project, func(tx ports.ReadTx) error {
		order, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		state = order.State
		events, err := control.ReadAllEvents(tx.Events)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Subject.ID != id || event.Type != workorders.EventType(workorders.TriggerBackToReview) {
				continue
			}
			var change workorders.Change
			if err := json.Unmarshal(event.Data, &change); err != nil {
				return err
			}
			reason = change.Reason
		}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return state, reason
}

// Ready work whose agent would take the shell from its lane, such as work an earlier build made ready, is
// refused its claim with the problem named, and goes back to contract review with that readiness failure
// as the reason, so the member and the orchestrator see why and can replan it.
func TestAClaimOnReadyWorkWithAnUnfitAgentSendsItBackToReview(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	r.record("pa", workorders.WorkOrder{ID: "wo-1", Kind: workorders.KindChange, Title: "T", Agent: "reasoning"})
	r.madeReadyEarlier("pa", "wo-1")
	err := r.claim("pa", "wo-1")
	if !errors.Is(err, workorders.ErrIneligible) || !strings.Contains(err.Error(), `"reasoning"`) || !strings.Contains(err.Error(), "denies the shell") {
		t.Fatalf("the claim: %v", err)
	}
	state, reason := r.sentBack("pa", "wo-1")
	if state != workorders.ContractReview || !strings.HasPrefix(reason, "agent: ") || !strings.Contains(reason, "denies the shell") {
		t.Errorf("the work stands in %s, sent back because %q", state, reason)
	}
}

// A roster change that leaves ready work's agent unfit for it, such as one that takes the shell from the
// agent rework moves into, sends that work back to contract review with the readiness failure as the reason.
func TestARosterChangeSendsReadyWorkWithAnUnfitAgentBackToReview(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	rosters := control.NewRoster(r.state.Installation, r.state)
	target := control.RosterTarget{Scope: roster.ScopeProject, Store: r.stores["pa"], Project: "pa"}
	for _, change := range []roster.Change{
		{Op: roster.OpAdd, Agent: "porter", From: "implementation"},
		{Op: roster.OpSet, Agent: "porter", Fields: map[string]string{"effort": roster.EffortHigh}},
	} {
		if _, err := rosters.Change(t.Context(), target, change, projectMember); err != nil {
			t.Fatalf("%+v: %v", change, err)
		}
	}
	// Rework with nothing named runs one tier above the medium change default, which is porter now.
	r.record("pa", workorders.WorkOrder{ID: "wo-r", Kind: workorders.KindRework, Title: "T"})
	if _, err := control.NewReviews(r.stores["pa"]).Pass(t.Context(), "pa", "wo-r", projectMember); err != nil {
		t.Fatal(err)
	}
	if state, _ := r.sentBack("pa", "wo-r"); state != workorders.Ready {
		t.Fatalf("the rework stands in %s", state)
	}
	strip := roster.Change{Op: roster.OpSet, Agent: "porter", Fields: map[string]string{"tools.allow": "read,search,edit,web,context", "tools.deny": "shell,delegate"}}
	if _, err := rosters.Change(t.Context(), target, strip, projectMember); err != nil {
		t.Fatal(err)
	}
	state, reason := r.sentBack("pa", "wo-r")
	if state != workorders.ContractReview || !strings.HasPrefix(reason, "agent: ") || !strings.Contains(reason, `"porter"`) {
		t.Errorf("the rework stands in %s, sent back because %q", state, reason)
	}
}

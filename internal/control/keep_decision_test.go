package control_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// anOrchestratorsOneWayDecision has an unattended orchestrator record a one-way decision on wo-a, which a member
// accepted the one-way acknowledgement for, and returns it.
func anOrchestratorsOneWayDecision(t *testing.T, r *planeRig) questions.Decision {
	t.Helper()
	acceptOneWay(t, r)
	call, _ := ladderAtRungThree(t, r, questions.CategoryArchitecture)
	decision, err := decideOneWay(r, call)
	if err != nil {
		t.Fatalf("the orchestrator's one-way decision: %v", err)
	}
	return decision
}

func workOrderCount(t *testing.T, r *planeRig) int {
	t.Helper()
	var count int
	err := r.stores["pa"].Read(t.Context(), "pa", func(tx ports.ReadTx) error {
		orders, err := tx.WorkOrders().List()
		count = len(orders)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// TestAMemberKeepsAnOrchestratorsOneWayDecision holds Keep to what the review of such a decision asks: it records the
// same decision as the member's, in its place and under the name the old one's ID gives it, and the decision it
// replaces ends as replaced. It binds nothing anew, so the work it reaches stands as it was, and a second Keep, which a
// retry makes, finds the first.
func TestAMemberKeepsAnOrchestratorsOneWayDecision(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	old := anOrchestratorsOneWayDecision(t, r)
	ruled := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{})
	orders := workOrderCount(t, r)

	kept, err := ruled.Keep(ctx, "pa", old.ID, projectMember)
	if err != nil {
		t.Fatalf("keeping the decision: %v", err)
	}
	if kept.ID != questions.KeptDecisionID(old.ID) || kept.Supersedes != old.ID || kept.DecidedBy != projectMember.ID {
		t.Errorf("the kept decision is %+v, want %s by the member, replacing %s", kept, questions.KeptDecisionID(old.ID), old.ID)
	}
	if old.QuestionID == "" || kept.QuestionID != old.QuestionID {
		t.Errorf("the kept decision answers question %q, want the question %q the decision it replaces answered", kept.QuestionID, old.QuestionID)
	}
	if !kept.SameContent(old) || kept.Status != questions.DecisionDecided || kept.Reversibility != questions.OneWay {
		t.Errorf("the kept decision is %+v, want the content of %+v", kept, old)
	}
	var replaced questions.Decision
	err = r.stores["pa"].Read(ctx, "pa", func(tx ports.ReadTx) (err error) {
		replaced, err = tx.Questions().Decision(old.ID)
		return err
	})
	if err != nil || replaced.Status != questions.DecisionReplaced {
		t.Errorf("the decision it replaced reads %+v: %v, want replaced", replaced, err)
	}
	if got := workOrderCount(t, r); got != orders {
		t.Errorf("keeping a decision left %d work orders, from %d: it binds nothing anew", got, orders)
	}
	again, err := ruled.Keep(ctx, "pa", old.ID, projectMember)
	if err != nil || again.ID != kept.ID {
		t.Errorf("a second Keep read %+v: %v, want the first, %s", again, err, kept.ID)
	}
}

// TestKeepRefusesWhatIsNotAMembersToKeep holds Keep to a member: the worker's orchestrator, a lane, and a member's
// delegate, which is how the assistant calls, are refused, and the decision stands as it was.
func TestKeepRefusesWhatIsNotAMembersToKeep(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	old := anOrchestratorsOneWayDecision(t, r)
	ruled := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{})
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: "worker-1"},
		{Kind: ledger.ActorLane, ID: "lane-1"},
		authority.DelegateActor(projectMember.ID, authority.DelegateIDPrefix+"x"),
	} {
		if _, err := ruled.Keep(t.Context(), "pa", old.ID, actor); !errors.Is(err, workorders.ErrForbidden) {
			t.Errorf("a Keep by %s: %v, want %v", actor.Kind, err, workorders.ErrForbidden)
		}
	}
	var held questions.Decision
	err := r.stores["pa"].Read(t.Context(), "pa", func(tx ports.ReadTx) (err error) {
		held, err = tx.Questions().Decision(old.ID)
		return err
	})
	if err != nil || held.Status != questions.DecisionDecided {
		t.Errorf("after the refusals the decision reads %+v: %v, want it in force", held, err)
	}
}

// TestKeepNeedsADecisionInForce holds Keep to a decision that stands: one the ledger does not hold is not found, and
// one a member overturned is ended, which a Keep never brings back.
func TestKeepNeedsADecisionInForce(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	old := anOrchestratorsOneWayDecision(t, r)
	ruled := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{})
	if _, err := ruled.Keep(ctx, "pa", "no-such-decision", projectMember); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a Keep of a decision nobody recorded: %v, want %v", err, ports.ErrNotFound)
	}
	if _, err := ruled.Overturn(ctx, "pa", old.ID, questions.Overturn{DecisionID: "overturned-a", Statement: "One repository."}, projectMember); err != nil {
		t.Fatal(err)
	}
	if _, err := ruled.Keep(ctx, "pa", old.ID, projectMember); !errors.Is(err, questions.ErrDecisionEnded) {
		t.Errorf("a Keep of an overturned decision: %v, want %v", err, questions.ErrDecisionEnded)
	}
}

// TestASecondKeepAfterAnOverturnIsRefused holds that a retry of Keep finds the decision the first Keep recorded only
// while it stands: once a member overturned the kept decision, a Keep of the original is a Keep of a decision that
// ended, and returns neither the overturned decision nor a new one.
func TestASecondKeepAfterAnOverturnIsRefused(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	old := anOrchestratorsOneWayDecision(t, r)
	ruled := control.NewQuestions(r.stores["pa"], questions.BatchPolicy{})
	kept, err := ruled.Keep(ctx, "pa", old.ID, projectMember)
	if err != nil {
		t.Fatalf("keeping the decision: %v", err)
	}
	if _, err := ruled.Overturn(ctx, "pa", kept.ID, questions.Overturn{DecisionID: "overturned-kept", Statement: "One repository."}, projectMember); err != nil {
		t.Fatal(err)
	}
	if again, err := ruled.Keep(ctx, "pa", old.ID, projectMember); !errors.Is(err, questions.ErrDecisionEnded) {
		t.Errorf("a second Keep after the kept decision was overturned read %+v: %v, want %v", again, err, questions.ErrDecisionEnded)
	}
}

// TestAKeptDecisionIDFitsWhatAnIDMayBe holds the name Keep gives the decision it records to the rule for IDs, for an ID
// of any length: the plain form while it fits, and a digest of the ID, under a prefix the plain form never makes, when
// it does not.
func TestAKeptDecisionIDFitsWhatAnIDMayBe(t *testing.T) {
	plain := questions.KeptDecisionID("d-1")
	if plain != "kept-d-1" {
		t.Errorf("the kept ID of d-1 is %q", plain)
	}
	long, other := questions.KeptDecisionID(strings.Repeat("a", ledger.MaxRecordID)), questions.KeptDecisionID(strings.Repeat("a", ledger.MaxRecordID-1))
	if !strings.HasPrefix(long, "kept_") || questions.CheckDecisionID(long) != nil {
		t.Errorf("the kept ID of a longest ID is %q, want a digest under kept_ that is an ID", long)
	}
	if long == other {
		t.Errorf("two IDs share the kept ID %q", long)
	}
}

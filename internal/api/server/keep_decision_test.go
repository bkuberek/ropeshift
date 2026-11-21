package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestAMemberKeepsAnOrchestratorsOneWayDecisionWithoutRestatingIt holds the Keep route to what the review asks of it: a
// member names the decision and nothing else, and the decision is recorded again as theirs, with its content, in its
// place. It leaves Needs you, a second call finds the first, and the decision it replaced reads as replaced.
func TestAMemberKeepsAnOrchestratorsOneWayDecisionWithoutRestatingIt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	d, w, _ := aWorld(t)
	orchestrator, oneWay := anUnattendedOrchestrator(t, d, w)
	decision := oneWay("orchestrator-kept", "One repository per service.")
	if _, err := w.Questions.RecordDecision(ctx, apitest.Scope, decision, orchestrator); err != nil {
		t.Fatal(err)
	}
	if _, waiting := reviews(t, d)["orchestrator-kept"]; !waiting {
		t.Fatal("Needs you holds no review of the orchestrator's decision")
	}

	kept, err := d.Client.KeepDecision(ctx, "orchestrator-kept")
	if err != nil {
		t.Fatalf("keeping the decision: %v", err)
	}
	if kept.ID != questions.KeptDecisionID("orchestrator-kept") || kept.Supersedes != "orchestrator-kept" || kept.DecidedBy == orchestrator.ID {
		t.Errorf("the kept decision is %+v, want %s by the member, replacing the orchestrator's", kept, questions.KeptDecisionID("orchestrator-kept"))
	}
	if kept.Status != decision.Status || kept.Statement != decision.Statement || kept.Rationale != decision.Rationale || kept.Scope != decision.Scope ||
		kept.Reversibility != questions.OneWay || len(kept.Citations) != 1 || len(kept.Categories) != 1 {
		t.Errorf("the kept decision is %+v, want the content of %+v", kept, decision)
	}
	replaced, err := d.Client.Decision(ctx, "orchestrator-kept")
	if err != nil || replaced.Status != questions.DecisionReplaced {
		t.Errorf("the decision it replaced reads %+v: %v, want replaced", replaced, err)
	}
	if left := reviews(t, d); len(left) != 0 {
		t.Errorf("after Keep, Needs you still shows %+v", left)
	}
	if again, err := d.Client.KeepDecision(ctx, "orchestrator-kept"); err != nil || again.ID != kept.ID {
		t.Errorf("a second Keep read %+v: %v, want the first", again, err)
	}
	if _, err := d.Client.KeepDecision(ctx, "no-such-decision"); err == nil {
		t.Error("a Keep of a decision nobody recorded was accepted")
	}
}

// TestKeepIsAMemberOnlyRoute holds Keep to a member's own session: an outside delegate and the assistant are refused as
// member-only, whatever the decision is, and the decision stays as it was, with no card and no new record.
func TestKeepIsAMemberOnlyRoute(t *testing.T) {
	t.Parallel()
	d := startTalk(t)
	ctx := t.Context()
	if _, err := d.client.RecordDecision(ctx, api.RecordDecision{
		ID: "member-one-way", Status: questions.DecisionDecided, Statement: "Store money as cents.", Rationale: "Rounding is a bug.",
		Reversibility: questions.OneWay, Categories: []string{questions.CategoryArchitecture},
	}); err != nil {
		t.Fatal(err)
	}
	_, assistant, _ := d.assistant(t)
	external := asDelegate(t, d.socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	delegates := map[string]*api.ProjectClient{"external delegate": external, "assistant": assistant}
	for name, delegate := range delegates {
		// A credential's first use is recorded, so the count starts after it.
		if _, err := delegate.WorkOrders(ctx, ""); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	before := revisionOf(t, d.Daemon)
	for name, delegate := range delegates {
		_, err := delegate.KeepDecision(ctx, "member-one-way")
		if err == nil {
			t.Errorf("a Keep by the %s was accepted", name)
			continue
		}
		refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	}
	if got := revisionOf(t, d.Daemon); got != before {
		t.Errorf("a refused Keep moved the ledger by %d", got-before)
	}
	held, err := d.client.Decision(ctx, "member-one-way")
	if err != nil || held.Status != questions.DecisionDecided {
		t.Errorf("after the refusals the decision reads %+v: %v, want it in force", held, err)
	}
	if _, err := d.client.Decision(ctx, questions.KeptDecisionID("member-one-way")); err == nil {
		t.Error("a refused Keep recorded a decision")
	}
}

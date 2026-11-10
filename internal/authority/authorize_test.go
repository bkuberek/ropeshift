package authority_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

func TestAMemberMayDoEverything(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "any"}
	for _, action := range authority.Actions() {
		if !authority.Allowed(member, action) {
			t.Errorf("a member may not %s", action)
		}
	}
}

// No principal but a member keeps a person near automated use. An action added to a row of the table
// would hand an agent the window that holds it back.
func TestNoAgentHoldsItsOwnAutonomy(t *testing.T) {
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		for _, action := range []authority.Action{authority.ActionSetAutonomy, authority.ActionOpenWindow, authority.ActionAcknowledgeRisk, authority.ActionPassAutonomy} {
			if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, action) {
				t.Errorf("%s may %s", kind, action)
			}
		}
	}
}

// Canceling throws away a claim and whatever a lane was doing, and a person answers for that.
func TestOnlyAMemberCancelsWork(t *testing.T) {
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, authority.ActionCancelWork) {
			t.Errorf("%s may cancel work", kind)
		}
	}
}

func TestOtherPrincipalsHoldNarrowScopes(t *testing.T) {
	tests := []struct {
		name   string
		kind   ledger.ActorKind
		action authority.Action
		want   bool
	}{
		{"an orchestrator may decide within policy", ledger.ActorOrchestrator, authority.ActionRecordDecision, true},
		{"an orchestrator may work a question", ledger.ActorOrchestrator, authority.ActionTakeQuestion, true},
		{"an orchestrator cannot pass a review", ledger.ActorOrchestrator, authority.ActionPassReview, false},
		{"an orchestrator cannot reset a breaker", ledger.ActorOrchestrator, authority.ActionResetLane, false},
		{"an orchestrator cannot force an archive", ledger.ActorOrchestrator, authority.ActionForceArchive, false},
		{"an orchestrator cannot give a human's answer", ledger.ActorOrchestrator, authority.ActionAnswerQuestion, false},
		{"an orchestrator cannot decide outside the policy", ledger.ActorOrchestrator, authority.ActionDecideUnbound, false},
		{"an orchestrator cannot overturn a decision", ledger.ActorOrchestrator, authority.ActionOverturn, false},
		{"an orchestrator cannot change a policy", ledger.ActorOrchestrator, authority.ActionPublishPolicy, false},
		{"an orchestrator cannot change a repository", ledger.ActorOrchestrator, authority.ActionSetRepository, false},
		{"a tick cannot change a repository", ledger.ActorSystem, authority.ActionSetRepository, false},
		{"a lane cannot change a repository", ledger.ActorLane, authority.ActionSetRepository, false},
		{"a worker cannot change a repository", ledger.ActorWorker, authority.ActionSetRepository, false},
		{"a tick may make the solo join token", ledger.ActorSystem, authority.ActionCreateJoinToken, true},
		{"the control plane may accept reviewed work by rule", ledger.ActorSystem, authority.ActionAcceptReviewed, true},
		{"an orchestrator cannot accept reviewed work", ledger.ActorOrchestrator, authority.ActionAcceptReviewed, false},
		{"a lane cannot accept reviewed work", ledger.ActorLane, authority.ActionAcceptReviewed, false},
		{"a worker cannot accept reviewed work", ledger.ActorWorker, authority.ActionAcceptReviewed, false},
		{"a tick decides nothing", ledger.ActorSystem, authority.ActionRecordDecision, false},
		{"a lane decides nothing", ledger.ActorLane, authority.ActionRecordDecision, false},
		{"a worker decides nothing", ledger.ActorWorker, authority.ActionRecordDecision, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := authority.Allowed(ledger.Actor{Kind: tt.kind, ID: "x"}, tt.action); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// A plan sets scope, and scope stays with a person (FR-PLAN-12). No agent and no tick approves one.
func TestOnlyAMemberApprovesAPlan(t *testing.T) {
	if !authority.Allowed(ledger.Actor{Kind: ledger.ActorMember, ID: "m"}, authority.ActionApprovePlan) {
		t.Error("a member may not approve a plan")
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, authority.ActionApprovePlan) {
			t.Errorf("%s may approve a plan", kind)
		}
	}
}

// Where work starts, what it merges with, and whether ropeshift integrates it are the person's setting,
// and learning cannot widen them (FR-REPO-1). The action is its own, so a role that may publish a
// policy does not thereby change a repository.
func TestOnlyAMemberChangesARepository(t *testing.T) {
	if !authority.Allowed(ledger.Actor{Kind: ledger.ActorMember, ID: "m"}, authority.ActionSetRepository) {
		t.Error("a member may not change a repository")
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, authority.ActionSetRepository) {
			t.Errorf("%s may change a repository", kind)
		}
	}
	if authority.ActionSetRepository == authority.ActionPublishPolicy {
		t.Error("changing a repository shares an action with publishing a policy")
	}
	listed := false
	for _, action := range authority.Actions() {
		listed = listed || action == authority.ActionSetRepository
	}
	if !listed {
		t.Error("Actions() does not list the action, so the member test never holds it to the function")
	}
}

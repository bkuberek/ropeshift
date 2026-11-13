package authority_test

import (
	"slices"
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
		{"an orchestrator is no second principal and cannot accept", ledger.ActorOrchestrator, authority.ActionAcceptLaneChecks, false},
		{"an orchestrator cannot accept work", ledger.ActorOrchestrator, authority.ActionAccept, false},
		{"an orchestrator cannot send work back", ledger.ActorOrchestrator, authority.ActionRework, false},
		{"an orchestrator cannot accept reviewed work", ledger.ActorOrchestrator, authority.ActionAcceptReviewed, false},
		{"a lane cannot accept reviewed work", ledger.ActorLane, authority.ActionAcceptReviewed, false},
		{"a worker cannot accept reviewed work", ledger.ActorWorker, authority.ActionAcceptReviewed, false},
		{"the control plane records a merge a person made, by rule", ledger.ActorSystem, authority.ActionRecordMerge, true},
		{"a worker cannot end work by saying it merged", ledger.ActorWorker, authority.ActionRecordMerge, false},
		{"an orchestrator cannot record a merge", ledger.ActorOrchestrator, authority.ActionRecordMerge, false},
		{"a lane cannot record a merge", ledger.ActorLane, authority.ActionRecordMerge, false},
		{"the control plane asks for a merge by the integration rule", ledger.ActorSystem, authority.ActionMerge, true},
		{"an orchestrator cannot merge", ledger.ActorOrchestrator, authority.ActionMerge, false},
		{"a lane cannot merge", ledger.ActorLane, authority.ActionMerge, false},
		{"a worker cannot ask itself to merge", ledger.ActorWorker, authority.ActionMerge, false},
		{"an orchestrator cannot decide a merge", ledger.ActorOrchestrator, authority.ActionDecideMerge, false},
		{"a tick cannot decide a merge", ledger.ActorSystem, authority.ActionDecideMerge, false},
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

// TestOnlyAMemberManagesAProject holds ADR-0021's lifecycle to members: no agent, worker, or tick creates,
// renames, archives, restores, removes, or imports a project.
func TestOnlyAMemberManagesAProject(t *testing.T) {
	if !authority.Allowed(ledger.Actor{Kind: ledger.ActorMember, ID: "m"}, authority.ActionManageProject) {
		t.Error("a member may not manage a project")
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, authority.ActionManageProject) {
			t.Errorf("%s may manage a project", kind)
		}
	}
	if !slices.Contains(authority.Actions(), authority.ActionManageProject) {
		t.Error("Actions() does not list the action, so the member test never holds it to the function")
	}
}

// TestOnlyAMemberRegistersARepository holds ADR-0022's registration to members: no agent, worker, or tick
// adds a repository to a project or takes one out.
func TestOnlyAMemberRegistersARepository(t *testing.T) {
	if !authority.Allowed(ledger.Actor{Kind: ledger.ActorMember, ID: "m"}, authority.ActionRegisterRepository) {
		t.Error("a member may not register a repository")
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, authority.ActionRegisterRepository) {
			t.Errorf("%s may register a repository", kind)
		}
	}
	if !slices.Contains(authority.Actions(), authority.ActionRegisterRepository) {
		t.Error("Actions() does not list the action, so the member test never holds it to the function")
	}
}

// An orchestrator recovers stuck work only through a recovery act its policy bounds. Retrying outside one,
// deciding a recovery request, and resetting a breaker outright stay a member's (ADR-0014).
func TestAnOrchestratorRecoversOnlyWithinItsPolicy(t *testing.T) {
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "w1"}
	if !authority.Allowed(orchestrator, authority.ActionRecover) {
		t.Error("an orchestrator cannot take a recovery act")
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		for _, action := range []authority.Action{authority.ActionDecideRecovery, authority.ActionPassRecovery, authority.ActionRetryWork, authority.ActionResetLane} {
			if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, action) {
				t.Errorf("%s may %s", kind, action)
			}
		}
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, authority.ActionRecover) {
			t.Errorf("%s may take a recovery act", kind)
		}
	}
}

// TestOnlyAMemberChangesTheRoster holds ADR-0025's consent operation to members until confirmation cards
// arrive: no agent, worker, or tick changes the agent roster or its routing guide (FR-AGENT-7).
func TestOnlyAMemberChangesTheRoster(t *testing.T) {
	if !authority.Allowed(ledger.Actor{Kind: ledger.ActorMember, ID: "m"}, authority.ActionChangeRoster) {
		t.Error("a member may not change the roster")
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, authority.ActionChangeRoster) {
			t.Errorf("%s may change the roster", kind)
		}
	}
	if !slices.Contains(authority.Actions(), authority.ActionChangeRoster) {
		t.Error("Actions() does not list the action, so the member test never holds it to the function")
	}
}

// TestOnlyAMemberSetsTheLaneCaps holds a machine's lane caps to a person: no agent, worker, or tick
// changes how many lanes the machine or a sign-in runs (FR-PROJ-7).
func TestOnlyAMemberSetsTheLaneCaps(t *testing.T) {
	if !authority.Allowed(ledger.Actor{Kind: ledger.ActorMember, ID: "m"}, authority.ActionSetLaneCaps) {
		t.Error("a member may not set the lane caps")
	}
	for _, kind := range []ledger.ActorKind{ledger.ActorOrchestrator, ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		if authority.Allowed(ledger.Actor{Kind: kind, ID: "x"}, authority.ActionSetLaneCaps) {
			t.Errorf("%s may set the lane caps", kind)
		}
	}
	if !slices.Contains(authority.Actions(), authority.ActionSetLaneCaps) {
		t.Error("Actions() does not list the action, so the member test never holds it to the function")
	}
}

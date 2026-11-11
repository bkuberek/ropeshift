package authority

import "ropeshift.com/ropeshift/internal/ledger"

// Action is something a caller asks to do.
type Action string

// The actions a check names.
const (
	// ActionAccept accepts work whose criteria a forge covered, so no person need stand behind
	// it. The tick takes this one.
	ActionAccept Action = "acceptance.accept"
	// ActionAcceptLaneChecks accepts work only its lane vouches for. No agent is a second principal any more
	// (ADR-0014), so it is a member's alone.
	ActionAcceptLaneChecks Action = "acceptance.lane_checks"
	// ActionAcceptUnchecked accepts a criterion no check maps to. Only a person answers for that.
	ActionAcceptUnchecked Action = "acceptance.unchecked"
	// ActionAcceptReviewed accepts work a review passed on every criterion, at a repository whose merge
	// authority leaves that to the review. The control plane takes it by rule, as a system actor, and
	// names the review it rests on.
	ActionAcceptReviewed Action = "acceptance.reviewed"
	ActionAddCategory    Action = "category.add"
	ActionAnswerQuestion Action = "question.answer"
	ActionArchiveLane    Action = "lane.archive"
	// ActionCancelWork ends a work order for good. It throws away a claim and whatever a lane was
	// doing, so no row below may ever grant it.
	ActionCancelWork Action = "work_order.cancel"
	// ActionAddPackItem adds a record to a work order's next context pack.
	ActionAddPackItem     Action = "pack.add_item"
	ActionChangeFleet     Action = "fleet.change"
	ActionCreateJoinToken Action = "worker.join_token"
	// ActionDecideUnbound decides outside the decision policy: a one-way choice, or with no
	// statement about the decisions it overlaps. Only a person answers for that.
	ActionDecideUnbound   Action = "decision.unbound"
	ActionDismissQuestion Action = "question.dismiss"
	ActionDispatchLane    Action = "lane.dispatch"
	ActionForceArchive    Action = "lane.force_archive"
	ActionOverturn        Action = "decision.overturn"
	ActionPassReview      Action = "review.pass"
	ActionPublishPolicy   Action = "policy.publish"
	ActionReassign        Action = "work_order.reassign"
	ActionRecordDecision  Action = "decision.record"
	ActionResetLane       Action = "lane.reset"
	// ActionRespawnLane asks for a running lane's next generation.
	ActionRespawnLane Action = "lane.respawn"
	// ActionResumeLane is a person waking a parked lane, which no automatic step can do.
	ActionResumeLane Action = "lane.resume"
	// ActionRework sends completed work back for another attempt.
	ActionRework   Action = "acceptance.rework"
	ActionSendBack Action = "review.send_back"
	// ActionSendMessage sends a lane a message, which grants nothing (FR-MSG-6).
	ActionSendMessage   Action = "message.send"
	ActionSetPassphrase Action = "member.set_passphrase"
	ActionTakeQuestion  Action = "question.take"
	// ActionUnblock takes work out of `blocked`, where a lane left it because no answer would help.
	ActionUnblock Action = "work_order.unblock"
	// ActionDecideApproval decides a request. An orchestrator reaches only what a member delegated.
	ActionDecideApproval Action = "approval.decide"
	// ActionApprovePlan approves a plan version. A plan sets scope, and scope stays with a person, so
	// no row below may ever grant it: not an orchestrator, not the planning lane, and not a tick.
	ActionApprovePlan Action = "plan.approve"
	// ActionDecideEffect says what became of an effect nothing can observe. Only a person can know, so
	// no row below may ever grant it.
	ActionDecideEffect Action = "effect.decide"
	// ActionPassAutonomy claims or respawns whatever a worker's level says: a person being there is
	// what a level stands in for, so a member needs no window and no agent has this.
	ActionPassAutonomy Action = "autonomy.pass"
	// ActionSetAutonomy chooses a worker's level, ActionOpenWindow opens or closes a run window, and
	// ActionAcknowledgeRisk stands behind the stated risk. These are what keep a person near automated
	// use, so no agent has them, and no row below may ever grant one.
	ActionSetAutonomy     Action = "autonomy.set"
	ActionOpenWindow      Action = "autonomy.window"
	ActionAcknowledgeRisk Action = "autonomy.acknowledge"
	// ActionSetRepository changes the settings of a repository's record: the base branch lanes start
	// from, the merge method, what dependent work starts from, and whether integration is on. How much
	// ropeshift may merge is the person's choice, so no agent has this, and no row below may ever grant it.
	ActionSetRepository Action = "repository.set"
	// ActionRunOrchestrator starts or stops a worker's orchestrator. No agent may ask for one: the
	// first passphrase needs no session, which is safe only while no agent runs unasked.
	ActionRunOrchestrator Action = "orchestrator.run"
	// ActionManageProject creates, renames, archives, restores, removes, or imports a project of the
	// installation (ADR-0021). A project holds every record of a product, so no agent has this, and no row
	// below may ever grant it.
	ActionManageProject Action = "project.manage"
)

// Actions lists every action, so a test can hold each one to the function.
func Actions() []Action {
	return []Action{
		ActionAccept, ActionAcceptLaneChecks, ActionAcceptUnchecked, ActionAcceptReviewed, ActionAddCategory, ActionAddPackItem, ActionAnswerQuestion,
		ActionArchiveLane, ActionCancelWork, ActionChangeFleet, ActionCreateJoinToken, ActionDecideUnbound, ActionDismissQuestion,
		ActionDispatchLane, ActionForceArchive, ActionOverturn, ActionPassReview, ActionPublishPolicy,
		ActionReassign, ActionRecordDecision, ActionResetLane, ActionRespawnLane, ActionResumeLane, ActionRework, ActionSendBack,
		ActionSendMessage, ActionSetPassphrase, ActionTakeQuestion, ActionDecideApproval, ActionRunOrchestrator,
		ActionSetAutonomy, ActionOpenWindow, ActionAcknowledgeRisk, ActionPassAutonomy, ActionUnblock, ActionDecideEffect, ActionApprovePlan,
		ActionSetRepository, ActionManageProject,
	}
}

// granted is what each principal other than a member may do. A kind with no row may do
// none of these.
var granted = map[ledger.ActorKind]map[Action]bool{
	ledger.ActorOrchestrator: {
		ActionAddPackItem: true, ActionArchiveLane: true, ActionDecideApproval: true, ActionDismissQuestion: true,
		ActionDispatchLane: true, ActionReassign: true, ActionRecordDecision: true, ActionRespawnLane: true,
		ActionSendBack: true, ActionSendMessage: true, ActionTakeQuestion: true,
	},
	ledger.ActorSystem: {ActionAccept: true, ActionAcceptReviewed: true, ActionCreateJoinToken: true, ActionRework: true},
}

// Allowed is the one authorization function. Every check passes through it. There are no
// roles in the first release, so it returns true for any member, and optional roles arrive
// here with no change to the data model (FR-AUTH-9).
func Allowed(actor ledger.Actor, action Action) bool {
	if actor.Kind == ledger.ActorMember {
		return true
	}
	return granted[actor.Kind][action]
}

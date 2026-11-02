package authority

import "ropeshift.dev/ropeshift/internal/ledger"

// Action is something a caller asks to do.
type Action string

// The actions a check names.
const (
	// ActionAccept accepts work whose criteria a forge covered, so no person need stand behind
	// it. The tick takes this one.
	ActionAccept Action = "acceptance.accept"
	// ActionAcceptLaneChecks accepts work only its lane vouches for, as a second principal.
	ActionAcceptLaneChecks Action = "acceptance.lane_checks"
	// ActionAcceptUnchecked accepts a criterion no check maps to. Only a person answers for that.
	ActionAcceptUnchecked Action = "acceptance.unchecked"
	ActionAddCategory     Action = "category.add"
	ActionAnswerQuestion  Action = "question.answer"
	ActionArchiveLane     Action = "lane.archive"
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
	// ActionPassAutonomy claims or respawns whatever a worker's level says: a person being there is
	// what a level stands in for, so a member needs no window and no agent has this.
	ActionPassAutonomy Action = "autonomy.pass"
	// ActionSetAutonomy chooses a worker's level, ActionOpenWindow opens or closes a run window, and
	// ActionAcknowledgeRisk stands behind the stated risk. These are what keep a person near automated
	// use, so no agent has them, and no row below may ever grant one.
	ActionSetAutonomy     Action = "autonomy.set"
	ActionOpenWindow      Action = "autonomy.window"
	ActionAcknowledgeRisk Action = "autonomy.acknowledge"
	// ActionRunOrchestrator starts or stops a worker's orchestrator. No agent may ask for one: the
	// first passphrase needs no session, which is safe only while no agent runs unasked.
	ActionRunOrchestrator Action = "orchestrator.run"
)

// Actions lists every action, so a test can hold each one to the function.
func Actions() []Action {
	return []Action{
		ActionAccept, ActionAcceptLaneChecks, ActionAcceptUnchecked, ActionAddCategory, ActionAddPackItem, ActionAnswerQuestion,
		ActionArchiveLane, ActionChangeFleet, ActionCreateJoinToken, ActionDecideUnbound, ActionDismissQuestion,
		ActionDispatchLane, ActionForceArchive, ActionOverturn, ActionPassReview, ActionPublishPolicy,
		ActionReassign, ActionRecordDecision, ActionResetLane, ActionRespawnLane, ActionResumeLane, ActionRework, ActionSendBack,
		ActionSendMessage, ActionSetPassphrase, ActionTakeQuestion, ActionDecideApproval, ActionRunOrchestrator,
		ActionSetAutonomy, ActionOpenWindow, ActionAcknowledgeRisk, ActionPassAutonomy, ActionUnblock,
	}
}

// granted is what each principal other than a member may do. A kind with no row may do
// none of these.
var granted = map[ledger.ActorKind]map[Action]bool{
	ledger.ActorOrchestrator: {
		ActionAccept: true, ActionAcceptLaneChecks: true, ActionAddPackItem: true, ActionArchiveLane: true, ActionDecideApproval: true, ActionDismissQuestion: true,
		ActionDispatchLane: true, ActionReassign: true, ActionRecordDecision: true, ActionRespawnLane: true, ActionRework: true,
		ActionSendBack: true, ActionSendMessage: true, ActionTakeQuestion: true,
	},
	ledger.ActorSystem: {ActionAccept: true, ActionCreateJoinToken: true, ActionRework: true},
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

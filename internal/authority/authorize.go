package authority

import "ropeshift.dev/ropeshift/internal/ledger"

// Action is something a caller asks to do.
type Action string

// The actions a check names.
const (
	ActionAddCategory     Action = "category.add"
	ActionAnswerQuestion  Action = "question.answer"
	ActionArchiveLane     Action = "lane.archive"
	ActionChangeFleet     Action = "fleet.change"
	ActionCreateJoinToken Action = "worker.join_token"
	ActionDismissQuestion Action = "question.dismiss"
	ActionDispatchLane    Action = "lane.dispatch"
	ActionForceArchive    Action = "lane.force_archive"
	ActionPassReview      Action = "review.pass"
	ActionReassign        Action = "work_order.reassign"
	ActionRecordDecision  Action = "decision.record"
	ActionResetLane       Action = "lane.reset"
	ActionSendBack        Action = "review.send_back"
	ActionSetPassphrase   Action = "member.set_passphrase"
	ActionTakeQuestion    Action = "question.take"
)

// Actions lists every action, so a test can hold each one to the function.
func Actions() []Action {
	return []Action{
		ActionAddCategory, ActionAnswerQuestion, ActionArchiveLane, ActionChangeFleet, ActionCreateJoinToken,
		ActionDismissQuestion, ActionDispatchLane, ActionForceArchive, ActionPassReview, ActionReassign,
		ActionRecordDecision, ActionResetLane, ActionSendBack, ActionSetPassphrase, ActionTakeQuestion,
	}
}

// granted is what each principal other than a member may do. A kind with no row may do
// none of these.
var granted = map[ledger.ActorKind]map[Action]bool{
	ledger.ActorOrchestrator: {
		ActionArchiveLane: true, ActionDismissQuestion: true, ActionDispatchLane: true, ActionReassign: true,
		ActionRecordDecision: true, ActionSendBack: true, ActionTakeQuestion: true,
	},
	ledger.ActorSystem: {ActionCreateJoinToken: true},
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

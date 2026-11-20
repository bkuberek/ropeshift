package authority

// OperationClass says what a delegate's call to an operation does (ADR-0024). A member runs every
// operation, whatever its class.
type OperationClass string

// The classes, from the least strict to the strictest.
const (
	// ClassOperational runs at once as the delegate.
	ClassOperational OperationClass = "operational"
	// ClassConsent runs nothing: it records a confirmation card that runs only when a member confirms it,
	// unless the delegate policy in force exempts the operation.
	ClassConsent OperationClass = "consent"
	// ClassMemberOnly is refused to a delegate outright: no card stands in for it.
	ClassMemberOnly OperationClass = "member_only"
)

// rank orders the classes, so a call that resolves to several takes the strictest.
func (c OperationClass) rank() int {
	switch c {
	case ClassOperational:
		return 0
	case ClassMemberOnly:
		return 2
	}
	return 1
}

// Operation is one changing operation of the API. ID names it in a card and in the delegate policy, and
// Label is the short imperative phrase a person reads. A consent operation says whether the delegate
// policy may exempt it, and why when it may not. A one-way category always makes a card of an operation
// whose OneWay says so, whatever the policy. Actions are what its use case checks, from which a
// delegate's grants are read, and Installation marks an operation on the installation's own records,
// which no delegate reaches.
type Operation struct {
	ID           string         `json:"id"`
	Label        string         `json:"label"`
	Class        OperationClass `json:"class"`
	Exemptable   bool           `json:"exemptable"`
	NeverReason  string         `json:"never_reason,omitempty"`
	OneWay       bool           `json:"-"`
	Actions      []Action       `json:"-"`
	Installation bool           `json:"-"`
}

// The operation IDs code names. The table below holds every one.
const (
	OpProjectCreate                       = "project.create"
	OpProjectImport                       = "project.import"
	OpProjectRename                       = "project.rename"
	OpProjectArchive                      = "project.archive"
	OpProjectRestore                      = "project.restore"
	OpProjectRemove                       = "project.remove"
	OpInstallationBackup                  = "installation.backup"
	OpInstallationSpendPolicyPublish      = "installation_spend_policy.publish"
	OpSpendRaise                          = "spend.raise"
	OpSpendRaiseProject                   = "spend.raise_project"
	OpSpendResume                         = "spend.resume"
	OpSpendBlock                          = "spend.block"
	OpSpendReplan                         = "spend.replan"
	OpWorkerJoinToken                     = "worker.join_token"
	OpWorkerJoinTokenRevoke               = "worker.join_token_revoke"
	OpAutonomySet                         = "autonomy.set"
	OpAutonomyAcknowledge                 = "autonomy.acknowledge"
	OpAutonomyAcknowledgeOneWay           = "autonomy.acknowledge_one_way"
	OpAutonomyWindow                      = "autonomy.window"
	OpWorkerLaneCaps                      = "worker.lane_caps"
	OpTemplateWrite                       = "template.write"
	OpRepositoryCreateRemote              = "repository.create_remote"
	OpOrchestratorRun                     = "orchestrator.run"
	OpOrchestratorSchedule                = "orchestrator.schedule"
	OpOrchestratorUnschedule              = "orchestrator.unschedule"
	OpRosterChange                        = "roster.change"
	OpSessionEnd                          = "session.end"
	OpSessionEndAll                       = "session.end_all"
	OpPassphraseSet                       = "passphrase.set"
	OpDelegateCreate                      = "delegate.create"
	OpDelegateList                        = "delegate.list"
	OpDelegateRevoke                      = "delegate.revoke"
	OpDelegateRemove                      = "delegate.remove"
	OpProjectBackup                       = "project.backup"
	OpWorkOrderCreate                     = "work_order.create"
	OpWorkOrderCreateByAssistant          = "work_order.create_by_assistant"
	OpWorkOrderReviewPlan                 = "work_order.review_plan"
	OpWorkOrderReview                     = "work_order.review"
	OpWorkOrderClaim                      = "work_order.claim"
	OpLaneStart                           = "lane.start"
	OpLaneResume                          = "lane.resume"
	OpLaneReset                           = "lane.reset"
	OpLaneRestart                         = "lane.restart"
	OpLaneArchive                         = "lane.archive"
	OpWorkOrderReplan                     = "work_order.replan"
	OpWorkOrderRetry                      = "work_order.retry"
	OpWorkOrderRetryExtra                 = "work_order.retry_extra"
	OpWorkOrderAccept                     = "work_order.accept"
	OpWorkOrderAcceptUnverified           = "work_order.accept_unverified"
	OpWorkOrderRework                     = "work_order.rework"
	OpWorkOrderReworkWithoutFailedVerdict = "work_order.rework_without_failed_verdict"
	OpWorkOrderCancel                     = "work_order.cancel"
	OpWorkOrderReassign                   = "work_order.reassign"
	OpWorkOrderSendBack                   = "work_order.send_back"
	OpWorkOrderResolve                    = "work_order.resolve"
	OpWorkOrderWrite                      = "work_order.write"
	OpWorkOrderRelease                    = "work_order.release"
	OpFleetPause                          = "fleet.pause"
	OpFleetRun                            = "fleet.run"
	OpFleetCaps                           = "fleet.caps"
	OpPlanStartReview                     = "plan.start_review"
	OpPlanStartReviewByAssistant          = "plan.start_review_by_assistant"
	OpPlanConfirmRevision                 = "plan.confirm_revision"
	OpPlanRevise                          = "plan.revise"
	OpPlanRepositoryRetry                 = "plan.repository_retry"
	OpPlanRepositorySkip                  = "plan.repository_skip"
	OpWorkOrderDecisionRework             = "work_order.decision_rework"
	OpWorkOrderCIWait                     = "work_order.ci_wait"
	OpWorkOrderCIRerun                    = "work_order.ci_rerun"
	OpQuestionRecord                      = "question.record"
	OpQuestionDismiss                     = "question.dismiss"
	OpQuestionAnswer                      = "question.answer"
	OpQuestionAnswerBatch                 = "question.answer_batch"
	OpQuestionMerge                       = "question.merge"
	OpDecisionAssume                      = "decision.assume"
	OpDecisionRecord                      = "decision.record"
	OpDecisionOverturn                    = "decision.overturn"
	OpPolicyPublish                       = "policy.publish"
	OpDelegatePolicyPublish               = "delegate_policy.publish"
	OpCategoryAdd                         = "category.add"
	OpRepositoryRegister                  = "repository.register"
	OpRepositoryRegisterManifest          = "repository.register_manifest"
	OpAdoptionPlan                        = "adoption.plan"
	OpAdoptionRestructure                 = "adoption.restructure"
	OpPracticesCheck                      = "practices.check"
	OpPracticesAcknowledge                = "practices.acknowledge"
	OpPracticesWithdraw                   = "practices.withdraw"
	OpRepositorySet                       = "repository.set"
	OpRepositoryLowerMergeAuthority       = "repository.lower_merge_authority"
	OpRepositoryReviewTargetBranch        = "repository.review_target_branch"
	OpRepositoryRaiseMergeAuthority       = "repository.raise_merge_authority"
	OpRepositoryReviewTargetPullRequest   = "repository.review_target_pull_request"
	OpRepositoryForge                     = "repository.forge"
	OpRepositoryRemove                    = "repository.remove"
	OpApprovalDecideDelegated             = "approval.decide_delegated"
	OpApprovalDecide                      = "approval.decide"
	OpCardConfirm                         = "card.confirm"
	OpEffectDecide                        = "effect.decide"
	OpMessageSend                         = "message.send"
	OpMessageSendByAssistant              = "message.send_by_assistant"
	OpMessageOrchestrator                 = "message.send_orchestrator"
	OpPaneInput                           = "pane.input"
	OpConversationCreate                  = "conversation.create"
	OpConversationOpen                    = "conversation.open"
	OpConversationStop                    = "conversation.stop"
	OpConversationRename                  = "conversation.rename"
	OpConversationDelete                  = "conversation.delete"
	OpConversationCompact                 = "conversation.compact"
	OpConversationClear                   = "conversation.clear"
	OpConversationMessage                 = "conversation.message"
)

// The reasons a consent operation is never exempt.
const (
	neverManagement  = "Member and worker management stays with a member."
	neverAutonomy    = "Autonomy is policy, which only a member changes."
	neverRoster      = "The agent roster and its routing guide are changed only on a member's word (ADR-0025)."
	neverPolicy      = "Policy is changed only on a member's word."
	neverSpend       = "Spending past a hard limit is a person's decision."
	neverRaise       = "Raising merge authority leaves more to ropeshift, which only a member allows."
	neverPullReq     = "Moving review to a pull request changes where work lands, which only a member allows."
	neverAcknowledge = "An acknowledgement changes what members see as open, which only a member decides."
	neverApproval    = "A delegate decides a request only within the delegation policy; every other request needs a member."
	// neverUnlisted is why every other consent operation asks: a delegate policy exempts only a fixed list
	// of reversible acts (ADR-0024).
	neverUnlisted = "A delegate policy exempts only accepting verified work, rework after a failed verdict, creating work, and starting a contract review."
)

func operational(id, label string, actions ...Action) Operation {
	return Operation{ID: id, Label: label, Class: ClassOperational, Actions: actions}
}

// exemptable is a consent operation of the fixed list a delegate policy may exempt: a reversible act.
func exemptable(id, label string, actions ...Action) Operation {
	return Operation{ID: id, Label: label, Class: ClassConsent, Exemptable: true, Actions: actions}
}

// consent is every other consent operation: it always asks, because no delegate policy may exempt it.
func consent(id, label string, actions ...Action) Operation {
	return never(id, label, neverUnlisted, actions...)
}

func never(id, label, reason string, actions ...Action) Operation {
	return Operation{ID: id, Label: label, Class: ClassConsent, NeverReason: reason, Actions: actions}
}

func memberOnly(id, label string, actions ...Action) Operation {
	return Operation{ID: id, Label: label, Class: ClassMemberOnly, Actions: actions}
}

func oneWay(op Operation) Operation {
	op.OneWay = true
	return op
}

func onInstallation(ops ...Operation) []Operation {
	for i := range ops {
		ops[i].Installation = true
	}
	return ops
}

// operations is the class table. A changing route names one or more of these, and a route that resolves
// to none reads as an unnamed consent operation. Every read is operational and is not listed.
var operations = append(onInstallation(
	operational(OpProjectCreate, "Create a project", ActionManageProject),
	operational(OpProjectImport, "Import a project", ActionManageProject),
	consent(OpProjectRename, "Rename a project", ActionManageProject),
	consent(OpProjectArchive, "Archive a project", ActionManageProject),
	consent(OpProjectRestore, "Restore a project", ActionManageProject),
	consent(OpProjectRemove, "Remove a project", ActionManageProject),
	memberOnly(OpInstallationBackup, "Back up the installation", ActionBackupInstallation),
	// The installation's spend policy sets what the orchestrator runs on and what it may spend for every project, so
	// publishing it is the installation role's action (ADR-0033) and not the project's policy action.
	never(OpInstallationSpendPolicyPublish, "Publish the installation's spend policy", neverPolicy, ActionPublishInstallationPolicy),
	never(OpWorkerJoinToken, "Make a worker join token", neverManagement, ActionCreateJoinToken),
	never(OpWorkerJoinTokenRevoke, "Revoke a worker join token", neverManagement, ActionCreateJoinToken),
	never(OpAutonomySet, "Set a worker's level", neverAutonomy, ActionSetAutonomy),
	never(OpAutonomyAcknowledge, "Acknowledge the stated risk", neverAutonomy, ActionAcknowledgeRisk),
	never(OpAutonomyAcknowledgeOneWay, "Accept the one-way acknowledgement", neverAutonomy, ActionAcknowledgeRisk),
	never(OpAutonomyWindow, "Open or close a run window", neverAutonomy, ActionOpenWindow),
	// A worker's lane caps live in the user's profile on its machine and set what that machine admits. No
	// ledger record holds them for a card to bind to, so a delegate's call is refused, not carded.
	memberOnly(OpWorkerLaneCaps, "Set a worker's lane caps", ActionSetLaneCaps),
	// A template write puts files at a path a caller chooses on a machine, which a delegate could choose, and
	// no ledger record holds them for a card to bind to, as for worker.lane_caps and
	// repository.register_manifest, so a delegate's call is refused, not carded.
	memberOnly(OpTemplateWrite, "Write a project's starting files", ActionWriteTemplates),
	// Creating a project's repository on a forge acts under the person's own forge account, outside this machine, and no
	// ledger record holds it for a card to bind to. It rides on a template write, which is member only too, so a card for the
	// remote alone would bind to nothing: a delegate's call is refused, not carded.
	memberOnly(OpRepositoryCreateRemote, "Create a project's repository on a forge", ActionCreateRemotes),
	operational(OpOrchestratorRun, "Start or stop an orchestrator", ActionRunOrchestrator),
	// A start scheduled for a sign-in's usage reset starts an agent later, as the member who scheduled it, with no
	// one watching, so a delegate's call is refused.
	memberOnly(OpOrchestratorSchedule, "Schedule an orchestrator's start for its usage reset", ActionRunOrchestrator),
	memberOnly(OpOrchestratorUnschedule, "Cancel an orchestrator's scheduled start", ActionRunOrchestrator),
	memberOnly(OpSessionEnd, "Sign out", ActionManageSessions),
	memberOnly(OpSessionEndAll, "Sign out all sessions", ActionManageSessions),
	memberOnly(OpPassphraseSet, "Set the passphrase", ActionSetPassphrase),
	memberOnly(OpDelegateCreate, "Create a delegate", ActionManageSessions),
	memberOnly(OpDelegateList, "List delegates", ActionManageSessions),
	memberOnly(OpDelegateRevoke, "Revoke a delegate", ActionManageSessions),
	memberOnly(OpDelegateRemove, "Remove a delegate", ActionManageSessions),
),
	never(OpRosterChange, "Change the agent roster", neverRoster, ActionChangeRoster),
	memberOnly(OpProjectBackup, "Back up the project", ActionBackupProject),
	// An orchestrator holds tools such as approval_decide and lane_start, and its wake states the title of
	// work to claim, so an outside delegate's call that creates a work order is a card, as a question it
	// records is. The assistant acts in a member's own conversation, so its call runs at once, and a wake
	// states what it wrote as a delegate's text.
	exemptable(OpWorkOrderCreate, "Create work"),
	operational(OpWorkOrderCreateByAssistant, "Create work"),
	// Passing a review makes work claimable, planning work's included, so it is a member's word whoever asks.
	consent(OpWorkOrderReviewPlan, "Review planning work", ActionPassReview),
	consent(OpWorkOrderReview, "Pass a review", ActionPassReview),
	operational(OpWorkOrderClaim, "Claim work"),
	operational(OpLaneStart, "Start a lane", ActionDispatchLane),
	operational(OpLaneResume, "Resume a lane", ActionResumeLane),
	operational(OpLaneReset, "Reset a lane", ActionResetLane),
	operational(OpLaneRestart, "Restart a lane", ActionRespawnLane),
	operational(OpLaneArchive, "Archive a lane", ActionArchiveLane),
	operational(OpWorkOrderReplan, "Replan work", ActionUnblock),
	operational(OpWorkOrderRetry, "Retry work", ActionRetryWork),
	consent(OpWorkOrderRetryExtra, "Retry work with an extra attempt", ActionRetryWork),
	// A delegate policy may exempt an acceptance only of work whose review has ruled that every criterion
	// passed, on a check the lane did not write, with no verdict of the attempt failed or unjudged and every
	// check its base requires green, and a rework only once a verdict of the attempt failed. Anything short of
	// that is an operation of its own, which asks.
	exemptable(OpWorkOrderAccept, "Accept work", ActionAccept),
	consent(OpWorkOrderAcceptUnverified, "Accept unverified work", ActionAcceptLaneChecks, ActionAcceptUnchecked),
	exemptable(OpWorkOrderRework, "Send work to rework", ActionRework),
	consent(OpWorkOrderReworkWithoutFailedVerdict, "Send work to rework with no failed verdict", ActionRework),
	consent(OpWorkOrderCancel, "Cancel work", ActionCancelWork),
	consent(OpWorkOrderReassign, "Reassign work", ActionReassign),
	consent(OpWorkOrderSendBack, "Send work back to planning", ActionSendBack),
	consent(OpWorkOrderResolve, "Resolve blocked work", ActionUnblock),
	consent(OpWorkOrderWrite, "Write under a claim"),
	consent(OpWorkOrderRelease, "Release a claim"),
	operational(OpFleetPause, "Pause the fleet", ActionChangeFleet),
	consent(OpFleetRun, "Run the fleet", ActionChangeFleet),
	consent(OpFleetCaps, "Set the fleet's caps", ActionChangeFleet),
	// A contract review is a work order, which the same rule holds.
	exemptable(OpPlanStartReview, "Start a contract review", ActionStartReview),
	operational(OpPlanStartReviewByAssistant, "Start a contract review", ActionStartReview),
	// A plan sets scope, and scope stays with a person: confirming that a plan's approved version stands against the
	// decisions made since, and starting the session that revises it, are a member's alone (FR-PLAN-12).
	memberOnly(OpPlanConfirmRevision, "Confirm a plan revision", ActionApprovePlan),
	memberOnly(OpPlanRevise, "Revise a plan", ActionApprovePlan),
	// Retrying a repository creation runs the creation again on the daemon's worker, under the member's forge account,
	// as approving the plan did, and skipping one leaves it undone: both act on an approved plan's registration, which no
	// card binds to, so a delegate's call is refused, not carded.
	memberOnly(OpPlanRepositoryRetry, "Retry creating a plan's repository", ActionApprovePlan, ActionRegisterManifest, ActionCreateRemotes),
	memberOnly(OpPlanRepositorySkip, "Skip creating a plan's repository", ActionApprovePlan),
	// Rework for the decisions a plan's revision lists costs a lane and a review apiece, so a member chooses it.
	memberOnly(OpWorkOrderDecisionRework, "Rework work for a decision", ActionApprovePlan),
	// Waiting longer for CI and rerunning it answer an item that asks a person, and start no agent, so a delegate's
	// call is refused, as the item is the member's.
	memberOnly(OpWorkOrderCIWait, "Wait longer for CI", ActionAnswerCI),
	memberOnly(OpWorkOrderCIRerun, "Rerun CI", ActionAnswerCI),
	consent(OpQuestionRecord, "Record a question"),
	consent(OpQuestionDismiss, "Dismiss a question", ActionDismissQuestion),
	oneWay(consent(OpQuestionAnswer, "Answer a question", ActionAnswerQuestion)),
	// A batch answered in one request holds answers of every reversibility, which a delegate's policy cannot weigh
	// as one, and each is a decision that binds work (ADR-0030). A member answers it, with the rationale each own
	// answer states, so a delegate's call is refused, not carded.
	memberOnly(OpQuestionAnswerBatch, "Answer a batch of questions", ActionAnswerQuestion),
	// A member merges a question into the one that stays in an orchestrator's place (FR-QD-3). An orchestrator merges
	// through its own acts, and a delegate's call is refused, because the lanes of one question move to another.
	memberOnly(OpQuestionMerge, "Merge a question into another", ActionMergeQuestion),
	// Recording a decision is a member's consent whoever drafts it, the assistant included (ADR-0024). An
	// orchestrator's decisions within policy reach the ledger through its own acts, which no class reads.
	consent(OpDecisionAssume, "Record a decision within policy", ActionRecordDecision),
	oneWay(consent(OpDecisionRecord, "Record a decision", ActionRecordDecision, ActionDecideUnbound)),
	consent(OpDecisionOverturn, "Overturn a decision", ActionOverturn),
	never(OpPolicyPublish, "Publish a policy", neverPolicy, ActionPublishPolicy),
	// A work order's hard limit stops spending more than twice what its estimate expects, so raising it, and
	// resuming a lane the limit parked, are a person's word whoever asks (ADR-0037, FR-SPEND-8): an orchestrator
	// holds neither, and no delegate policy exempts them.
	never(OpSpendRaise, "Raise a work order's spend limit", neverSpend, ActionRaiseSpendLimit),
	never(OpSpendRaiseProject, "Raise the project's daily or weekly spend limit", neverSpend, ActionRaiseSpendLimit),
	never(OpSpendResume, "Resume a lane a spend limit parked", neverSpend, ActionRaiseSpendLimit),
	// Blocking a spend stop and sending it to replanning are the other two ways a member ends one, and they are held
	// to the same action, so whoever may lift a limit decides what becomes of the work it stopped.
	never(OpSpendBlock, "Block work a spend limit parked", neverSpend, ActionRaiseSpendLimit),
	never(OpSpendReplan, "Send work a spend limit parked to replanning", neverSpend, ActionRaiseSpendLimit),
	memberOnly(OpDelegatePolicyPublish, "Publish the delegate policy", ActionPublishPolicy),
	never(OpCategoryAdd, "Add a category", neverPolicy, ActionAddCategory),
	consent(OpRepositoryRegister, "Register a repository", ActionRegisterRepository),
	// A manifest names paths on the machine in a file an agent can write, and its card would show the text
	// and not where each path leads, so registering from one is a member's alone.
	memberOnly(OpRepositoryRegisterManifest, "Register from a repository manifest", ActionRegisterRepository, ActionRegisterManifest),
	// Planning an adoption reads the paths of clones on the machine, as registering from a manifest does, and a
	// restructuring changes the layout of the planning repository, so each is a member's alone.
	memberOnly(OpAdoptionPlan, "Plan an adoption", ActionRegisterRepository, ActionPlanAdoption),
	memberOnly(OpAdoptionRestructure, "Restructure by adopting", ActionRestructure),
	// Running the practices check reads a repository and records what it found, and no decision reads the result, so a
	// delegate's call runs at once.
	operational(OpPracticesCheck, "Run the practices check"),
	// An acknowledgement changes what members see as open, so a delegate's call is a card a member confirms, and
	// no delegate policy exempts either operation.
	never(OpPracticesAcknowledge, "Acknowledge a practices finding", neverAcknowledge, ActionAcknowledgePractices),
	never(OpPracticesWithdraw, "Withdraw a finding's acknowledgement", neverAcknowledge, ActionWithdrawPractices),
	operational(OpRepositorySet, "Change a repository's settings", ActionSetRepository),
	operational(OpRepositoryLowerMergeAuthority, "Lower merge authority", ActionSetRepository),
	operational(OpRepositoryReviewTargetBranch, "Review on the branch", ActionSetRepository),
	never(OpRepositoryRaiseMergeAuthority, "Raise merge authority", neverRaise, ActionSetRepository, ActionRaiseMergeAuthority),
	never(OpRepositoryReviewTargetPullRequest, "Review through a pull request", neverPullReq, ActionSetRepository, ActionReviewPullRequest),
	consent(OpRepositoryForge, "Change forge settings", ActionSetRepository),
	consent(OpRepositoryRemove, "Remove a repository", ActionRegisterRepository),
	operational(OpApprovalDecideDelegated, "Decide a delegated request", ActionDecideApproval),
	// A tool call's request needs ActionDecideApproval alone, which the delegated operation grants, and
	// CheckDelegated holds a delegate's decision of one to the delegation policy. The other kinds need an
	// action of their own, which no delegate holds.
	never(OpApprovalDecide, "Decide a request", neverApproval, ActionDecideApproval, ActionApprovePlan, ActionDecideRecovery, ActionDecideMerge),
	memberOnly(OpCardConfirm, "Confirm a card", ActionConfirmCard),
	consent(OpEffectDecide, "Decide an effect", ActionDecideEffect),
	// A message steers what a lane does next, so an outside delegate's message to a lane is a card, as its
	// work is. The assistant acts in a member's own conversation, so its message goes at once.
	consent(OpMessageSend, "Send a message", ActionSendMessage),
	operational(OpMessageSendByAssistant, "Send a message", ActionSendMessage),
	// An orchestrator holds tools such as approval_decide and lane_start, so a message to one is a member's
	// alone: a delegate, the assistant included, cannot instruct it. Its reply tool is not a message.
	memberOnly(OpMessageOrchestrator, "Send a message to an orchestrator", ActionMessageOrchestrator),
	consent(OpPaneInput, "Type into a pane"),
	memberOnly(OpConversationCreate, "Start a conversation", ActionConverse),
	memberOnly(OpConversationOpen, "Open a conversation", ActionConverse),
	memberOnly(OpConversationStop, "Stop a conversation's session", ActionConverse),
	memberOnly(OpConversationRename, "Rename a conversation", ActionConverse),
	memberOnly(OpConversationDelete, "Delete a conversation", ActionConverse),
	memberOnly(OpConversationCompact, "Compact a conversation's context", ActionConverse),
	memberOnly(OpConversationClear, "Clear a conversation's context", ActionConverse),
	memberOnly(OpConversationMessage, "Message the assistant", ActionConverse),
)

// Operations lists the class table in its order.
func Operations() []Operation {
	out := make([]Operation, len(operations))
	copy(out, operations)
	return out
}

// OperationOf finds one operation by its ID.
func OperationOf(id string) (Operation, bool) {
	for _, op := range operations {
		if op.ID == id {
			return op, true
		}
	}
	return Operation{}, false
}

// Strictest is the class of a call that resolves to several operations.
func Strictest(ops []Operation) OperationClass {
	class := ClassOperational
	for _, op := range ops {
		if op.Class.rank() > class.rank() {
			class = op.Class
		}
	}
	return class
}

// DelegateMayRun says an operation is one a delegate's call can run as the delegate: an operational or
// exemptable consent operation of a project. A delegate reaches no installation route.
func (o Operation) DelegateMayRun() bool {
	return !o.Installation && (o.Class == ClassOperational || (o.Class == ClassConsent && o.Exemptable))
}

// delegateGrants are the actions an operation a delegate may run needs. A delegate holds nothing an
// installation operation needs, and a never-exempt or member-only operation needs something it lacks.
var delegateGrants = func() map[Action]bool {
	grants := map[Action]bool{}
	for _, op := range operations {
		if !op.DelegateMayRun() {
			continue
		}
		for _, action := range op.Actions {
			grants[action] = true
		}
	}
	return grants
}()

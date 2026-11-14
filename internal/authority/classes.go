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
	OpProjectCreate                     = "project.create"
	OpProjectImport                     = "project.import"
	OpProjectRename                     = "project.rename"
	OpProjectArchive                    = "project.archive"
	OpProjectRestore                    = "project.restore"
	OpProjectRemove                     = "project.remove"
	OpInstallationBackup                = "installation.backup"
	OpWorkerJoinToken                   = "worker.join_token"
	OpAutonomySet                       = "autonomy.set"
	OpAutonomyAcknowledge               = "autonomy.acknowledge"
	OpAutonomyWindow                    = "autonomy.window"
	OpWorkerLaneCaps                    = "worker.lane_caps"
	OpOrchestratorRun                   = "orchestrator.run"
	OpRosterChange                      = "roster.change"
	OpSessionEnd                        = "session.end"
	OpSessionEndAll                     = "session.end_all"
	OpPassphraseSet                     = "passphrase.set"
	OpDelegateCreate                    = "delegate.create"
	OpDelegateList                      = "delegate.list"
	OpDelegateRevoke                    = "delegate.revoke"
	OpDelegateRemove                    = "delegate.remove"
	OpProjectBackup                     = "project.backup"
	OpWorkOrderCreate                   = "work_order.create"
	OpWorkOrderCreateByAssistant        = "work_order.create_by_assistant"
	OpWorkOrderReviewPlan               = "work_order.review_plan"
	OpWorkOrderReview                   = "work_order.review"
	OpWorkOrderClaim                    = "work_order.claim"
	OpLaneStart                         = "lane.start"
	OpLaneResume                        = "lane.resume"
	OpLaneReset                         = "lane.reset"
	OpLaneRestart                       = "lane.restart"
	OpLaneArchive                       = "lane.archive"
	OpWorkOrderReplan                   = "work_order.replan"
	OpWorkOrderRetry                    = "work_order.retry"
	OpWorkOrderRetryExtra               = "work_order.retry_extra"
	OpWorkOrderAccept                   = "work_order.accept"
	OpWorkOrderRework                   = "work_order.rework"
	OpWorkOrderCancel                   = "work_order.cancel"
	OpWorkOrderReassign                 = "work_order.reassign"
	OpWorkOrderSendBack                 = "work_order.send_back"
	OpWorkOrderResolve                  = "work_order.resolve"
	OpWorkOrderWrite                    = "work_order.write"
	OpWorkOrderRelease                  = "work_order.release"
	OpFleetPause                        = "fleet.pause"
	OpFleetRun                          = "fleet.run"
	OpFleetCaps                         = "fleet.caps"
	OpPlanStartReview                   = "plan.start_review"
	OpPlanStartReviewByAssistant        = "plan.start_review_by_assistant"
	OpQuestionRecord                    = "question.record"
	OpQuestionDismiss                   = "question.dismiss"
	OpQuestionAnswer                    = "question.answer"
	OpDecisionAssume                    = "decision.assume"
	OpDecisionRecord                    = "decision.record"
	OpDecisionOverturn                  = "decision.overturn"
	OpPolicyPublish                     = "policy.publish"
	OpDelegatePolicyPublish             = "delegate_policy.publish"
	OpCategoryAdd                       = "category.add"
	OpRepositoryRegister                = "repository.register"
	OpRepositorySet                     = "repository.set"
	OpRepositoryLowerMergeAuthority     = "repository.lower_merge_authority"
	OpRepositoryReviewTargetBranch      = "repository.review_target_branch"
	OpRepositoryRaiseMergeAuthority     = "repository.raise_merge_authority"
	OpRepositoryReviewTargetPullRequest = "repository.review_target_pull_request"
	OpRepositoryForge                   = "repository.forge"
	OpRepositoryRemove                  = "repository.remove"
	OpApprovalDecideDelegated           = "approval.decide_delegated"
	OpApprovalDecide                    = "approval.decide"
	OpCardConfirm                       = "card.confirm"
	OpEffectDecide                      = "effect.decide"
	OpMessageSend                       = "message.send"
	OpMessageOrchestrator               = "message.send_orchestrator"
	OpPaneInput                         = "pane.input"
	OpConversationCreate                = "conversation.create"
	OpConversationOpen                  = "conversation.open"
	OpConversationStop                  = "conversation.stop"
	OpConversationRename                = "conversation.rename"
	OpConversationDelete                = "conversation.delete"
	OpConversationCompact               = "conversation.compact"
	OpConversationClear                 = "conversation.clear"
	OpConversationMessage               = "conversation.message"
)

// The reasons a consent operation is never exempt.
const (
	neverManagement = "Member and worker management stays with a member."
	neverAutonomy   = "Autonomy is policy, which only a member changes."
	neverRoster     = "The agent roster and its routing guide are changed only on a member's word (ADR-0025)."
	neverPolicy     = "Policy is changed only on a member's word."
	neverRaise      = "Raising merge authority leaves more to ropeshift, which only a member allows."
	neverPullReq    = "Moving review to a pull request changes where work lands, which only a member allows."
	neverApproval   = "A delegate decides a request only within the delegation policy; every other request needs a member."
)

func operational(id, label string, actions ...Action) Operation {
	return Operation{ID: id, Label: label, Class: ClassOperational, Actions: actions}
}

func consent(id, label string, actions ...Action) Operation {
	return Operation{ID: id, Label: label, Class: ClassConsent, Exemptable: true, Actions: actions}
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
	never(OpWorkerJoinToken, "Make a worker join token", neverManagement, ActionCreateJoinToken),
	never(OpAutonomySet, "Set a worker's level", neverAutonomy, ActionSetAutonomy),
	never(OpAutonomyAcknowledge, "Acknowledge the stated risk", neverAutonomy, ActionAcknowledgeRisk),
	never(OpAutonomyWindow, "Open or close a run window", neverAutonomy, ActionOpenWindow),
	// A worker's lane caps live in the user's profile on its machine and set what that machine admits. No
	// ledger record holds them for a card to bind to, so a delegate's call is refused, not carded.
	memberOnly(OpWorkerLaneCaps, "Set a worker's lane caps", ActionSetLaneCaps),
	operational(OpOrchestratorRun, "Start or stop an orchestrator", ActionRunOrchestrator),
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
	consent(OpWorkOrderCreate, "Create work"),
	operational(OpWorkOrderCreateByAssistant, "Create work"),
	operational(OpWorkOrderReviewPlan, "Review planning work", ActionPassReview),
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
	consent(OpWorkOrderAccept, "Accept work", ActionAccept, ActionAcceptLaneChecks, ActionAcceptUnchecked),
	consent(OpWorkOrderRework, "Send work to rework", ActionRework),
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
	consent(OpPlanStartReview, "Start a contract review", ActionPassReview),
	operational(OpPlanStartReviewByAssistant, "Start a contract review", ActionPassReview),
	consent(OpQuestionRecord, "Record a question"),
	consent(OpQuestionDismiss, "Dismiss a question", ActionDismissQuestion),
	oneWay(consent(OpQuestionAnswer, "Answer a question", ActionAnswerQuestion)),
	operational(OpDecisionAssume, "Record a decision within policy", ActionRecordDecision),
	oneWay(consent(OpDecisionRecord, "Record a decision", ActionRecordDecision, ActionDecideUnbound)),
	consent(OpDecisionOverturn, "Overturn a decision", ActionOverturn),
	never(OpPolicyPublish, "Publish a policy", neverPolicy, ActionPublishPolicy),
	memberOnly(OpDelegatePolicyPublish, "Publish the delegate policy", ActionPublishPolicy),
	never(OpCategoryAdd, "Add a category", neverPolicy, ActionAddCategory),
	consent(OpRepositoryRegister, "Register a repository", ActionRegisterRepository),
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
	operational(OpMessageSend, "Send a message", ActionSendMessage),
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

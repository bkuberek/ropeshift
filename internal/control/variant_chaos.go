//go:build chaos

package control

import (
	"sync/atomic"
)

// Sabotage is a broken variant of parking and waking, used only to prove that a check can
// fail. The chaos build holds these seams, and a normal build has none.
type Sabotage struct {
	// NoParkedEvaluation commits `parked` and evaluates nothing.
	NoParkedEvaluation bool
	// FiresBeforeParked fires with the work order in any state, which spends a wake during
	// parking.
	FiresBeforeParked bool
	// EarlyMs evaluates ahead of the database's time.
	EarlyMs int64
	// FirstOnly stops at the first wake an event fires.
	FirstOnly bool
	// UnreadableStops turns an expression this build cannot read into a failure.
	UnreadableStops bool
	// KeepsNextEval never moves `next_eval_at`.
	KeepsNextEval bool
	// KeepsWakesOpen retires nothing when a lane ends.
	KeepsWakesOpen bool
	// ResumesAnyClaim starts a lane under a claim that is another worker's, or another lane's.
	ResumesAnyClaim bool
	// RefusesResumeRetry answers a retried resume as a stranger's, which a lost answer needs.
	RefusesResumeRetry bool
	// RefusesParkedRepeat answers a repeated parked commit as a stranger's.
	RefusesParkedRepeat bool
	// LeavesApprovalWakes decides a request and tells nothing that waits on it.
	LeavesApprovalWakes bool
	// ReadsOnePage stops a wake's read of a scope at its first page.
	ReadsOnePage bool
	// LeavesMessages ends a generation and decides nothing about what it left.
	LeavesMessages bool
	// ExpiresSettled lets a stale tick move a message a signal settled.
	ExpiresSettled bool
	// ReadsAnyLane lets a worker speak for a message to a lane it does not name.
	ReadsAnyLane bool
	// LeavesAnswers closes a question and tells no lane.
	LeavesAnswers bool
	// SignalsAnyKey takes a worker's word on any sign-in, and SignalsAnyKind any word at all.
	SignalsAnyKey, SignalsAnyKind bool
	// DropsPackEvent stores a pack the log never hears of.
	DropsPackEvent bool
	// IgnoresAutonomy grants a claim whatever a worker's level and window say.
	IgnoresAutonomy bool
	// DropsAutonomyEvent keeps a change of autonomy the log never hears of.
	DropsAutonomyEvent bool
	// PassesAnyVersion lets a review pass a contract version it never read.
	PassesAnyVersion bool
	// ReleasesReviews sends what a review asks up the ladder while it still reads.
	ReleasesReviews bool
	// TakesInUnaccepted reads a plan's commit before anyone accepted it.
	TakesInUnaccepted bool
	// MovesPlanCursor moves a source to the commit of an accepted plan.
	MovesPlanCursor bool
	// KeepsHoldTime names no holder and leaves the time a hold ran to, which no wake reads until then.
	KeepsHoldTime bool
	// RebuildsPack gives a claim that brings a lane back a new pack.
	RebuildsPack bool
	// KeepsParkWanted leaves what a policy asked of a lane that parked.
	KeepsParkWanted bool
	// IgnoresPins tells only the lanes a decision's scope reaches, and renews no pack.
	IgnoresPins bool
	// BindsNoPlan lets a plan scope bind no work order, which is how a decision about a plan reached nobody.
	BindsNoPlan bool
	// StopsAtOnePage reads the decisions in force one page, and no further.
	StopsAtOnePage bool
	// AcceptsAnyScope records a decision whatever its scope says.
	AcceptsAnyScope bool
	// OverlapsOneSide lets a scope overlap a scope of a narrower kind and miss one of a wider kind.
	OverlapsOneSide bool
	// BindsEveryContract lets a contract scope bind the work of every contract.
	BindsEveryContract bool
	// KeepsReadyWork leaves ready work ready under a decision that binds it.
	KeepsReadyWork bool
	// HoldsNothing returns the work to review and leaves it to the next tick.
	HoldsNothing bool
	// KeepsStandingHold leaves a hold that stands as it is, and holds the work for the decision under no hold of its own.
	KeepsStandingHold bool
	// HoldsReversible holds work in review for a decision that can be taken back.
	HoldsReversible bool
	// TellsNobody binds work and tells none of its lanes.
	TellsNobody bool
	// RenewsNoPack leaves the pack of a lane as it was.
	RenewsNoPack bool
	// ParksNothing never asks a lane to park, whatever the lane policy says.
	ParksNothing bool
	// ParksAnyDecision asks a lane to park for a decision the policy only tells it of, by reading every
	// decision as a one-way one.
	ParksAnyDecision bool
	// ParksAskingLanes asks the lane that asked a question to park for the decision that answers it.
	ParksAskingLanes bool
	// ReworksNothing starts no rework of finished work a one-way decision binds.
	ReworksNothing bool
	// ReworksReversible reworks finished work a reversible decision binds, which a member does in one action.
	ReworksReversible bool
	// ReworksEachDecision starts a rework for every decision, and merges none.
	ReworksEachDecision bool
	// ReworksWholeChain reworks every finished work order of a chain of rework, not its tip alone.
	ReworksWholeChain bool
	// SparesAskersOnOverturn leaves the lane that asked the question running when the decision that answered it is
	// overturned, as it is when the decision answers it.
	SparesAskersOnOverturn bool
	// BranchesTheChain starts a rework beside a rework a lane has, which the chain of rework then forks at.
	BranchesTheChain bool
	// ReworksAnyKind lets a decision move and rework a contract review and a review.
	ReworksAnyKind bool
	// MarksNoRevision never opens or extends a plan's revision, whatever a member decides.
	MarksNoRevision bool
	// MarksAnyActor lets a decision an orchestrator records mark a plan as needing revision.
	MarksAnyActor bool
	// MarksNoOverturn leaves the revision of a plan alone when a member overturns a decision inside it.
	MarksNoOverturn bool
	// ExtendsNothing leaves a decision off the revision that stands.
	ExtendsNothing bool
	// ListsNoFinishedWork lists no finished work on a revision, whatever the decision reached.
	ListsNoFinishedWork bool
	// ListsOneWayFinishedWork lists the finished work a one-way decision reworked, beside the rework.
	ListsOneWayFinishedWork bool
	// WaitsOnNoRevision lets work of a plan pass its contract review while the plan's revision is open.
	WaitsOnNoRevision bool
	// WaitsPlanWide holds every work order of a plan for its open revision, and not the work its decisions bind.
	WaitsPlanWide bool
	// ConfirmsAnyActor lets any actor confirm a revision.
	ConfirmsAnyActor bool
	// KeepsWaitingWork leaves the work that waited in review as it was when a revision is settled.
	KeepsWaitingWork bool
	// KeepsDecisionHolds leaves a hold a person's one-way decision put on work when a revision is settled.
	KeepsDecisionHolds bool
	// KeepsRevisionOnApproval leaves a revision open when a member approves a later version.
	KeepsRevisionOnApproval bool
	// MarksNoAssumption opens no revision for a member's assumption.
	MarksNoAssumption bool
	// ReusesAnyWorkOrder returns the work order that has the ID of a revising session, whatever its kind.
	ReusesAnyWorkOrder bool
	// ReworksUnlistedWork makes rework for a decision of finished work its revision does not list.
	ReworksUnlistedWork bool
	// ClosesLaterDecisions closes a revision for every decision it lists when a member approves a later version, the
	// ones recorded after the approval included.
	ClosesLaterDecisions bool
	// NarrowsRevisionInPlace rewrites a revision with the decisions recorded after an approval, which leaves the
	// approval no revision to show.
	NarrowsRevisionInPlace bool
	// ShowsByTime splits a revision at an approval by the moments its decisions were recorded at, which the
	// decisions the approval recorded as shown do not decide.
	ShowsByTime bool
	// ReleasesBoundWork lets a revision that stays open release the work its decisions bind when a member settles
	// the plan.
	ReleasesBoundWork bool
	// BriefsNoDecisions starts a revising session with a brief that lists no decision.
	BriefsNoDecisions bool
	// DecidesReplacedDrafts has plan approval decide a draft again when a decision outside the plan replaced or
	// overturned the one an earlier version derived for it, as if no earlier version had decided it.
	DecidesReplacedDrafts bool
	// AnswersReplaceNothing leaves the decision an answer names in force.
	AnswersReplaceNothing bool
	// BindsAnswersTwice applies what an answer binds a second time, after the answer is recorded.
	BindsAnswersTwice bool
	// DropsAskedRecommendation records a lane's question without the recommendation the lane gave it.
	DropsAskedRecommendation bool
	// LeavesUnjudged leaves a question no orchestrator took on the ladder, so no member sees it.
	LeavesUnjudged bool
	// KeepsFiredStart keeps a scheduled start after it started the orchestrator, so the next reset starts it again.
	KeepsFiredStart bool
	// RewritesRecommendation has escalation replace a question's recommendation with the escalation's own, even
	// when it states none.
	RewritesRecommendation bool
	// LeavesParkedLanes cancels work and leaves its parked lane reading parked for good.
	LeavesParkedLanes bool
	// EndsAnyCanceledLane takes any worker's word that a canceled lane ended.
	EndsAnyCanceledLane bool
	// ForgetsAttempts sends with nothing on record, so a repeat leaves no trace.
	ForgetsAttempts bool
	// RecordsBeforeSending says a notice was delivered before it goes, so a kill loses it.
	RecordsBeforeSending bool
}

var sabotage atomic.Pointer[Sabotage]

// Sabotaged installs a variant for this process.
func Sabotaged(s Sabotage) { sabotage.Store(&s) }

func current() Sabotage {
	if s := sabotage.Load(); s != nil {
		return *s
	}
	return Sabotage{}
}

func skipsParkedEvaluation() bool { return current().NoParkedEvaluation }

func firesBeforeParked() bool { return current().FiresBeforeParked }

func evaluatedAt(now int64) int64 { return now + current().EarlyMs }

func firesFirstOnly() bool { return current().FirstOnly }

func leavesApprovalWakes() bool { return current().LeavesApprovalWakes }

func stopsOnUnreadable() bool { return current().UnreadableStops }

func keepsNextEval() bool { return current().KeepsNextEval }

func keepsWakesOpen() bool { return current().KeepsWakesOpen }

func resumesAnyClaim() bool { return current().ResumesAnyClaim }

func refusesResumeRetry() bool { return current().RefusesResumeRetry }

func refusesParkedRepeat() bool { return current().RefusesParkedRepeat }

func readsOnePage() bool { return current().ReadsOnePage }

func leavesMessages() bool { return current().LeavesMessages }

func expiresSettled() bool { return current().ExpiresSettled }

func readsAnyLane() bool { return current().ReadsAnyLane }

func leavesAnswers() bool { return current().LeavesAnswers }

func signalsAnyKey() bool { return current().SignalsAnyKey }

func signalsAnyKind() bool { return current().SignalsAnyKind }

func dropsPackEvent() bool { return current().DropsPackEvent }

func ignoresAutonomy() bool { return current().IgnoresAutonomy }

func dropsAutonomyEvent() bool { return current().DropsAutonomyEvent }

func passesAnyVersion() bool { return current().PassesAnyVersion }

func releasesReviews() bool { return current().ReleasesReviews }

func takesInUnaccepted() bool { return current().TakesInUnaccepted }

func movesPlanCursor() bool { return current().MovesPlanCursor }

func keepsHoldTime() bool { return current().KeepsHoldTime }

func rebuildsPack() bool { return current().RebuildsPack }

func keepsParkWanted() bool { return current().KeepsParkWanted }

func ignoresPins() bool { return current().IgnoresPins }

func bindsNoPlan() bool { return current().BindsNoPlan }

func stopsAtOnePage() bool { return current().StopsAtOnePage }

func overlapsOneSide() bool { return current().OverlapsOneSide }

func acceptsAnyScope() bool { return current().AcceptsAnyScope }

func bindsEveryContract() bool { return current().BindsEveryContract }

func keepsReadyWork() bool { return current().KeepsReadyWork }

func holdsNothing() bool { return current().HoldsNothing }

func keepsStandingHold() bool { return current().KeepsStandingHold }

func holdsReversible() bool { return current().HoldsReversible }

func tellsNobody() bool { return current().TellsNobody }

func renewsNoPack() bool { return current().RenewsNoPack }

func parksNothing() bool { return current().ParksNothing }

func parksAnyDecision() bool { return current().ParksAnyDecision }

func parksAskingLanes() bool { return current().ParksAskingLanes }

func reworksNothing() bool { return current().ReworksNothing }

func reworksReversible() bool { return current().ReworksReversible }

func reworksEachDecision() bool { return current().ReworksEachDecision }

func reworksWholeChain() bool { return current().ReworksWholeChain }

func sparesAskersOnOverturn() bool { return current().SparesAskersOnOverturn }

func branchesTheChain() bool { return current().BranchesTheChain }

func reworksAnyKind() bool { return current().ReworksAnyKind }

func marksNoRevision() bool { return current().MarksNoRevision }

func marksAnyActor() bool { return current().MarksAnyActor }

func marksNoOverturn() bool { return current().MarksNoOverturn }

func extendsNothing() bool { return current().ExtendsNothing }

func listsNoFinishedWork() bool { return current().ListsNoFinishedWork }

func listsOneWayFinishedWork() bool { return current().ListsOneWayFinishedWork }

func waitsOnNoRevision() bool { return current().WaitsOnNoRevision }

func waitsPlanWide() bool { return current().WaitsPlanWide }

func confirmsAnyActor() bool { return current().ConfirmsAnyActor }

func keepsWaitingWork() bool { return current().KeepsWaitingWork }

func keepsDecisionHolds() bool { return current().KeepsDecisionHolds }

func keepsRevisionOnApproval() bool { return current().KeepsRevisionOnApproval }

func marksNoAssumption() bool { return current().MarksNoAssumption }

func reusesAnyWorkOrder() bool { return current().ReusesAnyWorkOrder }

func reworksUnlistedWork() bool { return current().ReworksUnlistedWork }

func closesLaterDecisions() bool { return current().ClosesLaterDecisions }

func narrowsRevisionInPlace() bool { return current().NarrowsRevisionInPlace }

func showsByTime() bool { return current().ShowsByTime }

func releasesBoundWork() bool { return current().ReleasesBoundWork }

func briefsNoDecisions() bool { return current().BriefsNoDecisions }

func decidesReplacedDrafts() bool { return current().DecidesReplacedDrafts }

func answersReplaceNothing() bool { return current().AnswersReplaceNothing }

func bindsAnswersTwice() bool { return current().BindsAnswersTwice }

func dropsAskedRecommendation() bool { return current().DropsAskedRecommendation }

func leavesUnjudged() bool { return current().LeavesUnjudged }

func keepsFiredStart() bool { return current().KeepsFiredStart }

func rewritesRecommendation() bool { return current().RewritesRecommendation }

func leavesParkedLanes() bool { return current().LeavesParkedLanes }

func endsAnyCanceledLane() bool { return current().EndsAnyCanceledLane }

func forgetsAttempts() bool { return current().ForgetsAttempts }

func recordsBeforeSending() bool { return current().RecordsBeforeSending }

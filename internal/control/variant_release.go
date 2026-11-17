//go:build !chaos

package control

// skipsParkedEvaluation is false: a normal build evaluates a wake in the parked commit.
func skipsParkedEvaluation() bool { return false }

// firesBeforeParked is false: a normal build fires only for a parked work order.
func firesBeforeParked() bool { return false }

// evaluatedAt is the time as the database read it.
func evaluatedAt(now int64) int64 { return now }

// firesFirstOnly is false: one signal reaches every lane that names it.
func firesFirstOnly() bool { return false }

// leavesApprovalWakes is false: a decision reaches whatever waits on it, in its own write.
func leavesApprovalWakes() bool { return false }

// stopsOnUnreadable is false: what a build cannot read stops nothing.
func stopsOnUnreadable() bool { return false }

// keepsNextEval is false: a false evaluation moves `next_eval_at` on.
func keepsNextEval() bool { return false }

// keepsWakesOpen is false: ending a lane retires its wake.
func keepsWakesOpen() bool { return false }

// resumesAnyClaim is false: a lane resumes only under its own worker's claim on it.
func resumesAnyClaim() bool { return false }

// refusesResumeRetry is false: a retried resume returns the generation it started.
func refusesResumeRetry() bool { return false }

// refusesParkedRepeat is false: a repeated parked commit is done.
func refusesParkedRepeat() bool { return false }

// readsOnePage is false: a wake reads on past one read's bound.
func readsOnePage() bool { return false }

// leavesMessages is false: the end of a generation decides what it left.
func leavesMessages() bool { return false }

// expiresSettled is false: a clock never moves a message a signal settled.
func expiresSettled() bool { return false }

// readsAnyLane is false: a worker speaks only for messages to the lane it names.
func readsAnyLane() bool { return false }

// leavesAnswers is false: the write that closes a question tells its lanes.
func leavesAnswers() bool { return false }

// signalsAnyKey is false: a worker speaks only for its own sign-in.
func signalsAnyKey() bool { return false }

// signalsAnyKind is false: a worker says only what a sign-in can say.
func signalsAnyKind() bool { return false }

// dropsPackEvent is false: every pack is on the log.
func dropsPackEvent() bool { return false }

// ignoresAutonomy is false: a claim no member made reads the level and the window.
func ignoresAutonomy() bool { return false }

// dropsAutonomyEvent is false: every change of autonomy is on the log.
func dropsAutonomyEvent() bool { return false }

// passesAnyVersion is false: a review passes only the versions it read.
func passesAnyVersion() bool { return false }

// releasesReviews is false: what a review asks waits for it to end.
func releasesReviews() bool { return false }

// takesInUnaccepted is false: nothing a lane wrote is a record until someone accepts it.
func takesInUnaccepted() bool { return false }

// movesPlanCursor is false: a commit a person accepted is not where a source stands.
func movesPlanCursor() bool { return false }

// keepsHoldTime is false: a hold that ends has ended, so a wake reads its question now.
func keepsHoldTime() bool { return false }

// rebuildsPack is false: a lane keeps its pack.
func rebuildsPack() bool { return false }

// keepsParkWanted is false: a parked commit answers what a policy asked.
func keepsParkWanted() bool { return false }

// ignoresPins is false: a notice finds every lane whose pack holds what changed.
func ignoresPins() bool { return false }

// bindsNoPlan is false: a plan scope binds every work order under the plan's contracts.
func bindsNoPlan() bool { return false }

// stopsAtOnePage is false: the decisions in force are read to their last page.
func stopsAtOnePage() bool { return false }

// overlapsOneSide is false: a scope overlaps one that is inside it and one that holds it.
func overlapsOneSide() bool { return false }

// acceptsAnyScope is false: a decision's scope is in the grammar, whoever records it.
func acceptsAnyScope() bool { return false }

// A decision that binds work acts on exactly what its scope names, once, as the lane policy says: none of these
// breaks it in a normal build.
func bindsEveryContract() bool     { return false }
func keepsReadyWork() bool         { return false }
func holdsNothing() bool           { return false }
func keepsStandingHold() bool      { return false }
func holdsReversible() bool        { return false }
func tellsNobody() bool            { return false }
func renewsNoPack() bool           { return false }
func parksNothing() bool           { return false }
func parksAnyDecision() bool       { return false }
func parksAskingLanes() bool       { return false }
func reworksNothing() bool         { return false }
func reworksReversible() bool      { return false }
func reworksEachDecision() bool    { return false }
func reworksWholeChain() bool      { return false }
func sparesAskersOnOverturn() bool { return false }
func branchesTheChain() bool       { return false }
func reworksAnyKind() bool         { return false }

// A plan needs revision when a member decides inside its approved version, and each of these is what a normal build
// does the other way: none of them breaks it.
func marksNoRevision() bool         { return false }
func marksAnyActor() bool           { return false }
func marksNoOverturn() bool         { return false }
func extendsNothing() bool          { return false }
func listsNoFinishedWork() bool     { return false }
func listsOneWayFinishedWork() bool { return false }
func waitsOnNoRevision() bool       { return false }
func waitsPlanWide() bool           { return false }
func confirmsAnyActor() bool        { return false }
func keepsWaitingWork() bool        { return false }
func keepsDecisionHolds() bool      { return false }
func keepsRevisionOnApproval() bool { return false }
func marksNoAssumption() bool       { return false }
func reusesAnyWorkOrder() bool      { return false }
func reworksUnlistedWork() bool     { return false }
func closesLaterDecisions() bool    { return false }
func narrowsRevisionInPlace() bool  { return false }
func showsByTime() bool             { return false }
func releasesBoundWork() bool       { return false }
func briefsNoDecisions() bool       { return false }

// A replaced draft stays replaced, and an answer that names a decision replaces it, once: none of these breaks it in
// a normal build.
func decidesReplacedDrafts() bool { return false }
func answersReplaceNothing() bool { return false }
func bindsAnswersTwice() bool     { return false }

// leavesParkedLanes is false: a cancel ends a lane with no process, which no worker will report.
func leavesParkedLanes() bool { return false }

// endsAnyCanceledLane is false: only the worker that ran a lane says it ended.
func endsAnyCanceledLane() bool { return false }

// forgetsAttempts is false: an attempt goes on record before it is made.
func forgetsAttempts() bool { return false }

// recordsBeforeSending is false: a delivery goes on record once a channel took it.
func recordsBeforeSending() bool { return false }

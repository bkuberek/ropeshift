package contract

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// RevisionChecks are what a decision a member makes inside an approved plan does to the plan (ADR-0030, FR-PLAN-11):
// the plan needs revision until a member confirms the approved version or approves a later one, and the plan's
// unstarted work waits in contract review meanwhile. They run on the world of the mid-plan scenario.
var RevisionChecks = []Check{
	{"PRV1", "a member's decision inside an approved plan opens one revision, and each later decision extends it", checkRevisionOpensAndExtends},
	{"PRV2", "an orchestrator's decision, and a decision that binds no work of an approved plan, open no revision", checkRevisionOnlyAMemberMarks},
	{"PRV3", "a reversible decision lists the finished work it reached on the revision, and a one-way decision reworks it and lists none", checkRevisionListsFinishedWork},
	{"PRV4", "unstarted work of a plan waits in contract review while its revision is open, and Confirm releases it", checkRevisionHoldsWorkUntilConfirmed},
	{"PRV5", "Confirm releases work a person's one-way decision held, whose review has not been passed since", checkRevisionConfirmReleasesHeldWork},
	{"PRV6", "only a member confirms a revision, and a plan with none open has nothing to confirm", checkRevisionConfirmIsAMembers},
	{"PRV7", "approving a later version closes the revision approved, naming the approval, and releases the waiting work", checkRevisionClosesOnApproval},
	{"PRV8", "a member's overturn of a decision opens a revision that names the decision that replaced it", checkRevisionOpensOnOverturn},
	{"PRV9", "revising starts a planning session whose brief lists the decisions with their statements and scopes", checkRevisionStartsASession},
	{"PRV10", "a member makes the rework of finished work a reversible decision reached and its revision lists, once, naming the decisions", checkRevisionDecisionRework},
	{"PRV11", "a decision answered in a batch of the plan review opens one revision for the batch", checkRevisionOneForABatch},
	{"PRV12", "only work a decision of the revision binds waits for it, and work of the plan that none binds passes its review", checkRevisionWaitsForBoundWorkOnly},
	{"PRV13", "a revising session's branch closes the revision once intake records the version a member approved, and approving the branch closes nothing before", checkRevisionClosesOnBranchApproval},
	{"PRV14", "a branch's approval closes the revision only for the decisions recorded before it, and a new revision holds the work of the later ones", checkRevisionBranchSplitsAtItsApproval},
	{"PRV15", "a member's own assumption inside an approved plan opens a revision, as a decision does", checkRevisionOpensOnAssumption},
	{"PRV16", "rework for a decision is refused for finished work the revision does not list", checkRevisionReworkOfUnlistedWork},
	{"PRV17", "a one-way decision recorded after a branch's approval keeps the hold on its work when intake records the version", checkRevisionBranchKeepsALaterHold},
	{"PRV18", "a branch approved before any decision of the revision leaves the revision open, since the version it approved", checkRevisionBranchRebasesWhatItApprovedBefore},
	{"PRV19", "a revision a branch's approval splits gives each part only the rework its own decisions made", checkRevisionSplitSharesNoRework},
	{"PRV20", "a decision recorded in the same millisecond as a branch's approval is one the approval did not show, and keeps the hold it put on work", checkRevisionBranchKeepsADecisionOfTheSameMillisecond},
	{"PRV21", "a decision recorded after a branch's approval is one it did not show, though the clock has stepped back behind the approval", checkRevisionBranchKeepsADecisionTheClockPlacesBefore},
	{"PRV22", "an approval closes the revision for every decision it showed, though the clock has stepped back behind them", checkRevisionApprovalClosesWhatTheClockPlacesAfter},
}

func checkRevisionReworkOfUnlistedWork(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if err := m.finish("load"); err != nil {
		return err
	}
	decided, err := m.decide(r.member(), "contract:"+m.contractOf("done"), questions.Reversible)
	if err != nil {
		return err
	}
	revision, err := m.demandOpen("a member decided about finished work")
	if err != nil {
		return err
	}
	if len(revision.Finished) != 1 || revision.Finished[0].WorkOrder != m.orders["done"] {
		return Violatedf("the revision lists %+v, want only the finished work the decision reached", revision.Finished)
	}
	ops := r.NewOps(m.s)
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, m.orders["load"], []string{decided.ID}, r.member()); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("rework of finished work the revision does not list was %v, want invalid", err)
	}
	if rework, err := m.reworkIDs(m.orders["load"]); err != nil || len(rework) != 0 {
		return Violatedf("a refused request left rework %v of the work: %v", rework, err)
	}
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, m.orders["done"], []string{decided.ID}, r.member()); err != nil {
		return Violatedf("rework of the work the revision lists was refused: %v", err)
	}
	return r.CheckQuestions(m.s)
}

// delegate is a member's delegate, an agent outside ropeshift that holds no authority over a plan.
func (r *Rig) delegate() ledger.Actor {
	return ledger.Actor{Kind: ledger.ActorDelegate, ID: "member-1/dlg_1"}
}

func checkRevisionOpensOnAssumption(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	assumed, err := m.record(r.member(), "plan:"+m.plan, questions.Reversible, questions.DecisionAssumed)
	if err != nil {
		return err
	}
	revision, err := m.demandOpen("a member assumed something inside the approved plan")
	if err != nil {
		return err
	}
	if !slices.Equal(revision.DecisionIDs, []string{assumed.ID}) {
		return Violatedf("the member's assumption %s opened %+v, want a revision that lists it", assumed.ID, revision)
	}
	return r.CheckQuestions(m.s)
}

// branchAsksForApproval has a member start the session that revises the plan, the session's lane end on a branch,
// and intake read it, which asks a member to approve it. It returns the session's work order and the request.
func (m *midPlan) branchAsksForApproval() (string, authority.Approval, error) {
	r := m.r
	ops := r.NewOps(m.s)
	session, err := ops.ReviseSession(r.Ctx, r.Scope, m.plan, control.ReviseSession{ID: r.ID("revise"), Repository: AcceptanceRepository}, r.member())
	if err != nil {
		return "", authority.Approval{}, err
	}
	if _, err := ops.Pass(r.Ctx, r.Scope, session.ID, r.member()); err != nil {
		return "", authority.Approval{}, err
	}
	if err := r.anotherAttempt(m.s, session.ID, LaneFinished(acceptedHead, LaneCheck(criterionOne, true))); err != nil {
		return "", authority.Approval{}, err
	}
	m.source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", midPlanID, "The second draft.")))
	var asked []string
	taken, err := control.NewIntake(m.s).TakeInPlans(r.Ctx, r.Scope, openFrom(m.source, &asked))
	if err != nil || len(taken) != 0 {
		return "", authority.Approval{}, Violatedf("intake took %v of a branch nobody accepted: %v", taken, err)
	}
	pending, err := r.theOpenPlanApproval(m.s)
	return session.ID, pending, err
}

// intakeTakesTheBranch has intake read the branch a member accepted, and record the version it holds.
func (m *midPlan) intakeTakesTheBranch(session string) error {
	var asked []string
	taken, err := control.NewIntake(m.s).TakeInPlans(m.r.Ctx, m.r.Scope, openFrom(m.source, &asked))
	if err != nil || !slices.Equal(taken, []string{session}) {
		return Violatedf("intake took %v, want the revising session %s: %v", taken, session, err)
	}
	return nil
}

func checkRevisionClosesOnBranchApproval(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	decided, err := m.decide(r.member(), "contract:"+m.contractOf("load"), questions.Reversible)
	if err != nil {
		return err
	}
	session, pending, err := m.branchAsksForApproval()
	if err != nil {
		return err
	}
	approved, err := r.approve(m.s, pending, true, theMember)
	if err != nil {
		return err
	}
	// The branch is accepted, and the ledger holds none of its version yet: nothing a member approved is a version to settle.
	if revision, err := m.demandOpen("a member approved a branch intake has not read"); err != nil || !slices.Equal(revision.DecisionIDs, []string{decided.ID}) {
		return Violatedf("approving a branch intake has not read left the revision %+v: %v", revision, err)
	}
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	if err := m.intakeTakesTheBranch(session); err != nil {
		return err
	}
	all, err := m.revisions()
	if err != nil || len(all) != 1 {
		return Violatedf("the plan has the revisions %+v after intake recorded the approved branch: %v", all, err)
	}
	revision := all[0]
	if revision.Open() || revision.Outcome != planning.RevisionApproved || revision.Approval != approved.ID || revision.ClosedBy != authority.Principal(theMember) ||
		!slices.Equal(revision.DecisionIDs, []string{decided.ID}) {
		return Violatedf("intake recording the approved branch left the revision %+v, want it closed as approved by %s, still listing %s", revision, approved.ID, decided.ID)
	}
	if load, err := m.order("load"); err != nil || load.State != workorders.Ready || load.StartHeld != nil {
		return Violatedf("the approved branch left the waiting work %s, held by %+v: %v", load.State, load.StartHeld, err)
	}
	return r.CheckQuestions(m.s)
}

// branchApprovedBeforeADecision has a member decide something about the plan, approve the branch of a revising
// session, and decide again about the work of one contract, a moment after the approval, which intake then records.
// The member's first decision stands in the revision the approval answers, and the second is one no approval showed.
func (m *midPlan) branchApprovedBeforeADecision(reversibility string) (shown, later questions.Decision, approved authority.Approval, err error) {
	r := m.r
	if shown, err = m.decide(r.member(), "contract:"+m.contractOf("web"), questions.Reversible); err != nil {
		return
	}
	var session string
	var pending authority.Approval
	if session, pending, err = m.branchAsksForApproval(); err != nil {
		return
	}
	if approved, err = r.approve(m.s, pending, true, theMember); err != nil {
		return
	}
	r.DB.ShiftClock(1_000)
	if later, err = m.decide(r.member(), "contract:"+m.contractOf("load"), reversibility); err != nil {
		return
	}
	err = m.intakeTakesTheBranch(session)
	return
}

func checkRevisionBranchSplitsAtItsApproval(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	shown, later, approved, err := m.branchApprovedBeforeADecision(questions.Reversible)
	if err != nil {
		return err
	}
	all, err := m.revisions()
	if err != nil || len(all) != 2 {
		return Violatedf("the plan has the revisions %+v, want the one the approval closed and the one for the later decision: %v", all, err)
	}
	settled, open := all[0], all[1]
	if settled.Open() || settled.Outcome != planning.RevisionApproved || settled.Approval != approved.ID || settled.ClosedBy != authority.Principal(theMember) ||
		!slices.Equal(settled.DecisionIDs, []string{shown.ID}) || settled.ApprovedVersion != 1 {
		return Violatedf("the first revision is %+v, want it closed as approved by %s, listing %s alone, so the approval's page shows it", settled, approved.ID, shown.ID)
	}
	if !open.Open() || open.ID == settled.ID || !slices.Equal(open.DecisionIDs, []string{later.ID}) || open.ApprovedVersion != 2 || open.OpenedAt < settled.ClosedAt {
		return Violatedf("the second revision is %+v, want a new one, open for %s alone, since version 2: the approval showed %s", open, later.ID, shown.ID)
	}
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	counts, err := m.revisionEvents()
	if err != nil {
		return err
	}
	if counts[planning.EventRevisionClosed] != 1 || counts[planning.EventRevisionOpened] != 2 {
		return Violatedf("the log holds %v, want one closing and two openings", counts)
	}
	if _, err := r.NewOps(m.s).ConfirmRevision(r.Ctx, r.Scope, m.plan, r.member()); err != nil {
		return err
	}
	if load, err := m.order("load"); err != nil || load.State != workorders.Ready {
		return Violatedf("Confirm left the work of the later decision %s: %v", load.State, err)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionBranchKeepsALaterHold(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if _, _, _, err := m.branchApprovedBeforeADecision(questions.OneWay); err != nil {
		return err
	}
	load, err := m.order("load")
	if err != nil {
		return err
	}
	if load.State != workorders.ContractReview || load.StartHeld == nil || load.StartHeld.Reason != workorders.HoldDecision {
		return Violatedf("work a one-way decision recorded after the approval binds is %s, held by %+v, want it held by the decision", load.State, load.StartHeld)
	}
	// A member's pass ends the hold, and the revision still keeps the work in review until it is settled.
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	if _, err := r.NewOps(m.s).ConfirmRevision(r.Ctx, r.Scope, m.plan, r.member()); err != nil {
		return err
	}
	if load, err = m.order("load"); err != nil || load.State != workorders.Ready || load.StartHeld != nil {
		return Violatedf("Confirm left the work %s, held by %+v: %v", load.State, load.StartHeld, err)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionBranchRebasesWhatItApprovedBefore(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	// A revising session needs a revision, and a member confirms that one before approving the session's branch.
	if _, err := m.decide(r.member(), "contract:"+m.contractOf("web"), questions.Reversible); err != nil {
		return err
	}
	session, pending, err := m.branchAsksForApproval()
	if err != nil {
		return err
	}
	if _, err := r.NewOps(m.s).ConfirmRevision(r.Ctx, r.Scope, m.plan, r.member()); err != nil {
		return err
	}
	if _, err := r.approve(m.s, pending, true, theMember); err != nil {
		return err
	}
	r.DB.ShiftClock(1_000)
	later, err := m.decide(r.member(), "contract:"+m.contractOf("load"), questions.Reversible)
	if err != nil {
		return err
	}
	if err := m.intakeTakesTheBranch(session); err != nil {
		return err
	}
	all, err := m.revisions()
	if err != nil || len(all) != 2 || all[0].Outcome != planning.RevisionConfirmed || !all[1].Open() || !slices.Equal(all[1].DecisionIDs, []string{later.ID}) || all[1].ApprovedVersion != 2 {
		return Violatedf("the plan has the revisions %+v, want the one Confirm closed and one for %s, still open and since version 2: the approval showed no decision: %v", all, later.ID, err)
	}
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	counts, err := m.revisionEvents()
	if err != nil {
		return err
	}
	if counts[planning.EventRevisionRebased] != 1 {
		return Violatedf("the log holds %v, want the revision rebased once", counts)
	}
	return r.CheckQuestions(m.s)
}

// checkRevisionSplitSharesNoRework has a decision reach a finished work order, a member make rework of it for that
// decision, and a branch's approval split the revision after a second decision reached the same work order. The
// revision the approval closes holds the rework, and the new one holds the second decision with none: nobody has
// asked for rework for it, so the card must still offer it.
func checkRevisionSplitSharesNoRework(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	done := m.orders["done"]
	shown, err := m.decide(r.member(), "contract:"+m.contractOf("done"), questions.Reversible)
	if err != nil {
		return err
	}
	rework, err := r.NewOps(m.s).DecisionRework(r.Ctx, r.Scope, done, []string{shown.ID}, r.member())
	if err != nil {
		return err
	}
	session, pending, err := m.branchAsksForApproval()
	if err != nil {
		return err
	}
	if _, err := r.approve(m.s, pending, true, theMember); err != nil {
		return err
	}
	r.DB.ShiftClock(1_000)
	later, err := m.decide(r.member(), "contract:"+m.contractOf("done"), questions.Reversible)
	if err != nil {
		return err
	}
	if err := m.intakeTakesTheBranch(session); err != nil {
		return err
	}
	all, err := m.revisions()
	if err != nil || len(all) != 2 {
		return Violatedf("the plan has the revisions %+v, want the one the approval closed and the one for the later decision: %v", all, err)
	}
	settled, open := all[0], all[1]
	if len(settled.Finished) != 1 || settled.Finished[0].WorkOrder != done || !slices.Equal(settled.Finished[0].Decisions, []string{shown.ID}) ||
		settled.Finished[0].Rework != rework || !slices.Equal(settled.Finished[0].ReworkFor, []string{shown.ID}) {
		return Violatedf("the revision the approval closed lists %+v, want the finished work with the rework %s made for %s", settled.Finished, rework, shown.ID)
	}
	if len(open.Finished) != 1 || open.Finished[0].WorkOrder != done || !slices.Equal(open.Finished[0].Decisions, []string{later.ID}) ||
		open.Finished[0].Rework != "" || len(open.Finished[0].ReworkFor) != 0 {
		return Violatedf("the new revision lists %+v, want the finished work for %s with no rework, because the rework %s was made for %s", open.Finished, later.ID, rework, shown.ID)
	}
	return r.CheckQuestions(m.s)
}

// freezeClock stops database time where it stands, so the writes that follow read one millisecond, and returns what
// starts it again.
func (r *Rig) freezeClock(s ports.Store) (func(), error) {
	var now int64
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		now, err = tx.Now()
		return err
	})
	if err != nil {
		return nil, err
	}
	r.DB.FreezeClock(now)
	return func() { r.DB.FreezeClock(0) }, nil
}

// showed reads the decisions an approval recorded as the ones it showed of the plan's revision, and false when it
// recorded none.
func (m *midPlan) showed(approval authority.Approval) ([]string, bool, error) {
	var row authority.Approval
	err := m.s.Read(m.r.Ctx, m.r.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Approvals().Approval(approval.ID)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	var detail planning.PlanDetail
	if err := json.Unmarshal([]byte(row.Detail), &detail); err != nil {
		return nil, false, Violatedf("the detail of %s is %q: %v", row.ID, row.Detail, err)
	}
	if detail.Shown == nil {
		return nil, false, nil
	}
	return detail.Shown.IDs, true, nil
}

// demandShowed says an approval recorded the decisions given as the ones it showed.
func (m *midPlan) demandShowed(approval authority.Approval, want ...string) error {
	showed, recorded, err := m.showed(approval)
	if err != nil {
		return err
	}
	if !recorded || !slices.Equal(showed, want) {
		return Violatedf("the approval %s recorded that it showed %v (recorded: %v), want %v", approval.ID, showed, recorded, want)
	}
	return nil
}

// demandSplitHoldsLater says an approval closed a revision that lists the decision it showed alone, that a new revision
// lists the later one alone, and that the work the later one-way decision binds is still held by it, until a member
// confirms the new revision.
func (m *midPlan) demandSplitHoldsLater(shown, later questions.Decision, approved authority.Approval) error {
	all, err := m.revisions()
	if err != nil || len(all) != 2 {
		return Violatedf("the plan has the revisions %+v, want the one the approval closed and one for the later decision %s: %v", all, later.ID, err)
	}
	settled, open := all[0], all[1]
	if settled.Open() || settled.Outcome != planning.RevisionApproved || settled.Approval != approved.ID || !slices.Equal(settled.DecisionIDs, []string{shown.ID}) {
		return Violatedf("the first revision is %+v, want it closed as approved by %s, listing %s alone", settled, approved.ID, shown.ID)
	}
	if !open.Open() || !slices.Equal(open.DecisionIDs, []string{later.ID}) {
		return Violatedf("the second revision is %+v, want it open for %s alone, which the approval did not show", open, later.ID)
	}
	load, err := m.order("load")
	if err != nil || load.State != workorders.ContractReview || load.StartHeld == nil || load.StartHeld.Reason != workorders.HoldDecision {
		return Violatedf("work the later one-way decision binds is %s, held by %+v, want contract review, held by the decision: %v", load.State, load.StartHeld, err)
	}
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	if _, err := m.r.NewOps(m.s).ConfirmRevision(m.r.Ctx, m.r.Scope, m.plan, m.r.member()); err != nil {
		return err
	}
	if load, err = m.order("load"); err != nil || load.State != workorders.Ready || load.StartHeld != nil {
		return Violatedf("Confirm left the work %s, held by %+v: %v", load.State, load.StartHeld, err)
	}
	return m.r.CheckQuestions(m.s)
}

func checkRevisionBranchKeepsADecisionOfTheSameMillisecond(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	shown, err := m.decide(r.member(), "contract:"+m.contractOf("web"), questions.Reversible)
	if err != nil {
		return err
	}
	session, pending, err := m.branchAsksForApproval()
	if err != nil {
		return err
	}
	thaw, err := r.freezeClock(m.s)
	if err != nil {
		return err
	}
	defer thaw()
	approved, err := r.approve(m.s, pending, true, theMember)
	if err != nil {
		return err
	}
	later, err := m.decide(r.member(), "contract:"+m.contractOf("load"), questions.OneWay)
	if err != nil {
		return err
	}
	thaw()
	if open, err := m.demandOpen("a member decided in the millisecond after approving a branch"); err != nil {
		return err
	} else if open.ListedAt[later.ID] != approved.DecidedAt {
		return Violatedf("the decision was listed at %d and the approval decided at %d, want one millisecond: the clock did not stop", open.ListedAt[later.ID], approved.DecidedAt)
	}
	if err := m.demandShowed(approved, shown.ID); err != nil {
		return err
	}
	if err := m.intakeTakesTheBranch(session); err != nil {
		return err
	}
	return m.demandSplitHoldsLater(shown, later, approved)
}

func checkRevisionBranchKeepsADecisionTheClockPlacesBefore(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	shown, err := m.decide(r.member(), "contract:"+m.contractOf("web"), questions.Reversible)
	if err != nil {
		return err
	}
	session, pending, err := m.branchAsksForApproval()
	if err != nil {
		return err
	}
	approved, err := r.approve(m.s, pending, true, theMember)
	if err != nil {
		return err
	}
	// The host's clock is set back a minute after the member approved, as a time service does, and the next decision
	// is recorded at a time before the approval.
	const step = 60_000
	r.DB.ShiftClock(-step)
	later, err := m.decide(r.member(), "contract:"+m.contractOf("load"), questions.OneWay)
	r.DB.ShiftClock(step)
	if err != nil {
		return err
	}
	if open, err := m.demandOpen("a member decided after the clock stepped back"); err != nil {
		return err
	} else if open.ListedAt[later.ID] >= approved.DecidedAt {
		return Violatedf("the decision was listed at %d and the approval decided at %d, want the decision to read earlier: the clock did not step back", open.ListedAt[later.ID], approved.DecidedAt)
	}
	if err := m.intakeTakesTheBranch(session); err != nil {
		return err
	}
	return m.demandSplitHoldsLater(shown, later, approved)
}

func checkRevisionApprovalClosesWhatTheClockPlacesAfter(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	decided, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay)
	if err != nil {
		return err
	}
	m.source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", midPlanID, "The second draft.")))
	if _, err := r.intake(m.s, m.source); err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(m.s)
	if err != nil {
		return err
	}
	// The clock is set back a minute before the member approves, so the decision reads as recorded after the approval,
	// though the member had it in front of them.
	const step = 60_000
	r.DB.ShiftClock(-step)
	approved, err := r.approve(m.s, pending, true, theMember)
	r.DB.ShiftClock(step)
	if err != nil {
		return err
	}
	all, err := m.revisions()
	if err != nil || len(all) != 1 {
		return Violatedf("the plan has the revisions %+v after the approval, want the one it closed: %v", all, err)
	}
	revision := all[0]
	if revision.ListedAt[decided.ID] <= approved.DecidedAt {
		return Violatedf("the decision was listed at %d and the approval decided at %d, want the decision to read later: the clock did not step back", revision.ListedAt[decided.ID], approved.DecidedAt)
	}
	if revision.Open() || revision.Outcome != planning.RevisionApproved || revision.Approval != approved.ID || !slices.Equal(revision.DecisionIDs, []string{decided.ID}) {
		return Violatedf("the approval left the revision %+v, want it closed as approved by %s, listing %s that the member had in front of them", revision, approved.ID, decided.ID)
	}
	if err := m.demandShowed(approved, decided.ID); err != nil {
		return err
	}
	if load, err := m.order("load"); err != nil || load.State != workorders.Ready || load.StartHeld != nil {
		return Violatedf("the approval left the waiting work %s, held by %+v: %v", load.State, load.StartHeld, err)
	}
	return r.CheckQuestions(m.s)
}

// revisions lists every revision of a plan, by when it opened.
func (m *midPlan) revisions() ([]planning.Revision, error) {
	var out []planning.Revision
	err := m.s.Read(m.r.Ctx, m.r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Planning().Revisions()
		for _, revision := range all {
			if revision.PlanID == m.plan {
				out = append(out, revision)
			}
		}
		return err
	})
	return out, err
}

// open reads the plan's open revision, and none when the plan has none.
func (m *midPlan) open() (*planning.Revision, error) {
	var revision *planning.Revision
	err := m.s.Read(m.r.Ctx, m.r.Scope, func(tx ports.ReadTx) error {
		found, err := tx.Planning().OpenRevision(m.plan)
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		revision = &found
		return err
	})
	return revision, err
}

// demandOpen reads the open revision, which a check that needs one refuses to go on without.
func (m *midPlan) demandOpen(after string) (planning.Revision, error) {
	revision, err := m.open()
	if err != nil {
		return planning.Revision{}, err
	}
	if revision == nil {
		return planning.Revision{}, Violatedf("%s, and the plan has no open revision", after)
	}
	return *revision, nil
}

// revisionEvents counts the events about a plan that record its revisions, by type.
func (m *midPlan) revisionEvents() (map[string]int, error) {
	events, err := m.r.Events(m.s, m.r.Scope)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, e := range events {
		if e.Subject == (ledger.Subject{Kind: planning.SubjectPlan, ID: m.plan}) && strings.HasPrefix(e.Type, "plan.revision_") {
			counts[e.Type]++
		}
	}
	return counts, nil
}

// passAfterConfirm settles the plan's revision as a member does and then passes a work order's review, which the
// revision kept in contract review until then.
func (m *midPlan) passAfterConfirm(id string) error {
	if _, err := m.r.NewOps(m.s).ConfirmRevision(m.r.Ctx, m.r.Scope, m.plan, m.r.member()); err != nil {
		return err
	}
	_, err := m.r.NewOps(m.s).Pass(m.r.Ctx, m.r.Scope, id, m.r.member())
	return err
}

// waitsOnRevision says a work order stands in contract review with nothing but the plan's revision to wait for.
func (m *midPlan) waitsOnRevision(contract string) error {
	order, err := m.order(contract)
	if err != nil {
		return err
	}
	if order.State != workorders.ContractReview {
		return Violatedf("work of the plan is %s while its revision is open, want contract review", order.State)
	}
	result, err := m.r.NewOps(m.s).Pass(m.r.Ctx, m.r.Scope, order.ID, m.r.member())
	if err != nil {
		return err
	}
	if result.State != workorders.ContractReview || !slices.ContainsFunc(result.Failures, func(f workorders.ReadyFailure) bool { return f.Check == workorders.CheckPlanRevision }) {
		return Violatedf("a member's pass left work of the plan %s with %+v while its revision is open, want contract review that names the revision", result.State, result.Failures)
	}
	return nil
}

func checkRevisionOpensAndExtends(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if revision, err := m.open(); err != nil || revision != nil {
		return Violatedf("a plan nobody decided anything inside has the revision %+v: %v", revision, err)
	}
	first, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	opened, err := m.demandOpen("a member decided inside the approved plan")
	if err != nil {
		return err
	}
	if opened.ApprovedVersion != 1 || !slices.Equal(opened.DecisionIDs, []string{first.ID}) || opened.ID == "" || opened.OpenedAt == 0 || !opened.Open() {
		return Violatedf("the revision is %+v, want version 1 approved and the decision %s", opened, first.ID)
	}
	second, err := m.decide(r.member(), "contract:"+m.contractOf("web"), questions.Reversible)
	if err != nil {
		return err
	}
	extended, err := m.demandOpen("a second decision was recorded")
	if err != nil {
		return err
	}
	if extended.ID != opened.ID || extended.OpenedAt != opened.OpenedAt || !slices.Equal(extended.DecisionIDs, []string{first.ID, second.ID}) {
		return Violatedf("the second decision made %+v, want the first revision to list %s and %s", extended, first.ID, second.ID)
	}
	if all, err := m.revisions(); err != nil || len(all) != 1 {
		return Violatedf("the plan has %d revisions after two decisions, want 1: %v", len(all), err)
	}
	counts, err := m.revisionEvents()
	if err != nil {
		return err
	}
	if counts[planning.EventRevisionOpened] != 1 || counts[planning.EventRevisionExtended] != 1 {
		return Violatedf("the log holds %v, want one opening and one extension", counts)
	}
	return r.CheckQuestions(m.s)
}

// contractOf is the ID of the contract a scenario work order runs under.
func (m *midPlan) contractOf(contract string) string {
	order, err := m.order(contract)
	if err != nil {
		return ""
	}
	return order.ContractID
}

func checkRevisionOnlyAMemberMarks(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if _, err := m.decide(m.holder, "plan:"+m.plan, questions.Reversible); err != nil {
		return err
	}
	if revision, err := m.open(); err != nil || revision != nil {
		return Violatedf("an orchestrator's decision about the plan opened %+v: %v", revision, err)
	}
	if err := r.HoldRepository(m.s, "nowhere"); err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "repo:nowhere", questions.Reversible); err != nil {
		return err
	}
	if revision, err := m.open(); err != nil || revision != nil {
		return Violatedf("a member's decision that binds no work opened %+v: %v", revision, err)
	}
	loose, err := r.NewWorkOrder(m.s)
	if err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "wo:"+loose.ID, questions.Reversible); err != nil {
		return err
	}
	if revision, err := m.open(); err != nil || revision != nil {
		return Violatedf("a member's decision about work no approved plan covers opened %+v: %v", revision, err)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionListsFinishedWork(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	first, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	second, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	revision, err := m.demandOpen("two reversible decisions bound the plan")
	if err != nil {
		return err
	}
	want := []planning.RevisionWork{{WorkOrder: m.orders["done"], Decisions: []string{first.ID, second.ID}}}
	if !slices.EqualFunc(revision.Finished, want, func(a, b planning.RevisionWork) bool {
		return a.WorkOrder == b.WorkOrder && slices.Equal(a.Decisions, b.Decisions) && a.Rework == b.Rework
	}) {
		return Violatedf("the revision lists the finished work %+v, want %+v", revision.Finished, want)
	}
	if rework, err := m.reworkIDs(m.orders["done"]); err != nil || len(rework) != 0 {
		return Violatedf("reversible decisions started rework %v: %v", rework, err)
	}
	third, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay)
	if err != nil {
		return err
	}
	if rework, err := m.reworkIDs(m.orders["done"]); err != nil || len(rework) != 1 {
		return Violatedf("a one-way decision started rework %v of the finished work: %v", rework, err)
	}
	if revision, err = m.demandOpen("a one-way decision was recorded"); err != nil {
		return err
	}
	if !slices.Equal(revision.DecisionIDs, []string{first.ID, second.ID, third.ID}) {
		return Violatedf("the revision lists the decisions %v, want all three", revision.DecisionIDs)
	}
	if len(revision.Finished) != 1 || slices.Contains(revision.Finished[0].Decisions, third.ID) {
		return Violatedf("a one-way decision reworks finished work, and the revision lists it as %+v", revision.Finished)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionHoldsWorkUntilConfirmed(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible); err != nil {
		return err
	}
	if _, err := r.NewOps(m.s).Promote(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	if _, err := r.NewOps(m.s).Promote(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	confirmed, err := r.NewOps(m.s).ConfirmRevision(r.Ctx, r.Scope, m.plan, r.member())
	if err != nil {
		return err
	}
	if confirmed.Open() || confirmed.Outcome != planning.RevisionConfirmed || confirmed.ClosedBy != authority.Principal(r.member()) || confirmed.ClosedAt == 0 {
		return Violatedf("Confirm returned %+v, want a revision closed as confirmed by the member", confirmed)
	}
	if revision, err := m.open(); err != nil || revision != nil {
		return Violatedf("the plan still has the open revision %+v after Confirm: %v", revision, err)
	}
	if load, err := m.order("load"); err != nil || load.State != workorders.Ready {
		return Violatedf("Confirm left the waiting work %s: %v", load.State, err)
	}
	if again, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible); err != nil || again.ID == "" {
		return err
	}
	if all, err := m.revisions(); err != nil || len(all) != 2 || all[0].Outcome != planning.RevisionConfirmed || !all[1].Open() {
		return Violatedf("a decision after Confirm left the revisions %+v, want the confirmed one and a new open one: %v", all, err)
	}
	counts, err := m.revisionEvents()
	if err != nil {
		return err
	}
	if counts[planning.EventRevisionClosed] != 1 || counts[planning.EventRevisionOpened] != 2 {
		return Violatedf("the log holds %v, want two openings and one closing", counts)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionConfirmReleasesHeldWork(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay); err != nil {
		return err
	}
	load, err := m.order("load")
	if err != nil || load.StartHeld == nil || load.StartHeld.Reason != workorders.HoldDecision {
		return Violatedf("work a person's one-way decision binds is held by %+v: %v", load.StartHeld, err)
	}
	if _, err := r.NewOps(m.s).ConfirmRevision(r.Ctx, r.Scope, m.plan, r.member()); err != nil {
		return err
	}
	if load, err = m.order("load"); err != nil || load.State != workorders.Ready || load.StartHeld != nil {
		return Violatedf("Confirm left work a one-way decision held %s, held by %+v: %v", load.State, load.StartHeld, err)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionConfirmIsAMembers(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	ops := r.NewOps(m.s)
	if _, err := ops.ConfirmRevision(r.Ctx, r.Scope, m.plan, r.member()); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("confirming a plan with no revision was %v, want not found", err)
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible); err != nil {
		return err
	}
	for name, actor := range map[string]ledger.Actor{
		"an orchestrator": m.holder,
		"a lane":          {Kind: ledger.ActorLane, ID: m.ids.lane},
		"the system":      {Kind: ledger.ActorSystem, ID: "tick"},
		"a delegate":      {Kind: ledger.ActorDelegate, ID: "delegate-1"},
	} {
		if _, err := ops.ConfirmRevision(r.Ctx, r.Scope, m.plan, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s confirming a revision was %v, want forbidden", name, err)
		}
	}
	if revision, err := m.open(); err != nil || revision == nil {
		return Violatedf("a refused Confirm closed the revision: %v", err)
	}
	return nil
}

func checkRevisionClosesOnApproval(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	decided, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay)
	if err != nil {
		return err
	}
	m.source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", midPlanID, "The second draft.")))
	if _, err := r.intake(m.s, m.source); err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(m.s)
	if err != nil || pending.PlanVersion != 2 || pending.State != authority.ApprovalPending {
		return Violatedf("the later version's request is %+v, want version 2 waiting for a member: %v", pending, err)
	}
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	if revision, err := m.open(); err != nil || revision == nil {
		return Violatedf("a new version that nobody approved closed the revision: %v", err)
	}
	approved, err := r.approve(m.s, pending, true, theMember)
	if err != nil {
		return err
	}
	closed, err := m.revisions()
	if err != nil || len(closed) != 1 {
		return Violatedf("the plan has the revisions %+v after the approval: %v", closed, err)
	}
	revision := closed[0]
	if revision.Open() || revision.Outcome != planning.RevisionApproved || revision.Approval != approved.ID || revision.ClosedBy != authority.Principal(theMember) ||
		!slices.Equal(revision.DecisionIDs, []string{decided.ID}) || revision.ApprovedVersion != 1 {
		return Violatedf("the approval of version 2 left the revision %+v, want it closed as approved by %s, still listing %s", revision, approved.ID, decided.ID)
	}
	if load, err := m.order("load"); err != nil || load.State != workorders.Ready || load.StartHeld != nil {
		return Violatedf("the approval left the waiting work %s, held by %+v: %v", load.State, load.StartHeld, err)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionOpensOnOverturn(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	judged, err := m.decide(m.holder, "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	if revision, err := m.open(); err != nil || revision != nil {
		return Violatedf("an orchestrator's decision opened %+v: %v", revision, err)
	}
	overturned, err := r.overturn(m.s, judged.ID)
	if err != nil {
		return err
	}
	revision, err := m.demandOpen("a member overturned a decision of the plan")
	if err != nil {
		return err
	}
	if !slices.Equal(revision.DecisionIDs, []string{overturned.New.ID}) || len(revision.Finished) != 0 {
		return Violatedf("the overturn opened %+v, want a revision that names %s and lists no finished work, because an overturn reworks it", revision, overturned.New.ID)
	}
	if reworked, err := m.reworkedFrom(overturned.Rework, m.orders["done"]); err != nil || !reworked {
		return Violatedf("the overturn reworked %v, and not the finished work: %v", overturned.Rework, err)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionStartsASession(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	ops := r.NewOps(m.s)
	session := control.ReviseSession{ID: r.ID("revise")}
	if _, err := ops.ReviseSession(r.Ctx, r.Scope, m.plan, session, r.member()); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("revising a plan with no revision was %v, want not found", err)
	}
	decided, err := m.decide(r.member(), "contract:"+m.contractOf("web"), questions.Reversible)
	if err != nil {
		return err
	}
	for name, actor := range map[string]ledger.Actor{"an orchestrator": m.holder, "a delegate": r.delegate()} {
		if _, err := ops.ReviseSession(r.Ctx, r.Scope, m.plan, session, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s revising a plan was %v, want forbidden", name, err)
		}
	}
	if _, err := r.WorkOrder(m.s, session.ID); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a refused request started the work order %s: %v", session.ID, err)
	}
	order, err := ops.ReviseSession(r.Ctx, r.Scope, m.plan, session, r.member())
	if err != nil {
		return err
	}
	if order.ID != session.ID || order.Kind != workorders.KindPlan || order.State != workorders.ContractReview {
		return Violatedf("revising started %+v, want a planning work order in contract review", order)
	}
	var contract planning.Contract
	if err := m.s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		contract, err = tx.Planning().Contract(order.ContractID, order.ContractVersion)
		return err
	}); err != nil {
		return err
	}
	for _, want := range []string{"Factory", decided.ID, decided.Statement, decided.Scope} {
		if !strings.Contains(contract.Statement, want) {
			return Violatedf("the brief of the session is %q, and it lacks %q", contract.Statement, want)
		}
	}
	again, err := ops.ReviseSession(r.Ctx, r.Scope, m.plan, session, r.member())
	if err != nil || again.ID != order.ID {
		return Violatedf("a retry of the same session started %+v: %v", again, err)
	}
	// An ID that names work which is not a planning session is not a session to return.
	taken := control.ReviseSession{ID: m.orders["web"]}
	if got, err := ops.ReviseSession(r.Ctx, r.Scope, m.plan, taken, r.member()); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("revising with the ID of the work order %s returned %+v with %v, want the ID refused", taken.ID, got, err)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionDecisionRework(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	ops := r.NewOps(m.s)
	first, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	second, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	done := m.orders["done"]
	for name, actor := range map[string]ledger.Actor{"an orchestrator": m.holder, "a delegate": r.delegate()} {
		if _, err := ops.DecisionRework(r.Ctx, r.Scope, done, []string{first.ID}, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s creating rework for a decision was %v, want forbidden", name, err)
		}
	}
	if rework, err := m.reworkIDs(done); err != nil || len(rework) != 0 {
		return Violatedf("a refused request left rework %v: %v", rework, err)
	}
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, m.orders["ids"], []string{first.ID}, r.member()); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("rework of work that has not finished was %v, want invalid", err)
	}
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, done, []string{"nobody"}, r.member()); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("rework for a decision that does not exist was %v, want not found", err)
	}
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, done, nil, r.member()); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("rework for no decision was %v, want invalid", err)
	}
	elsewhere, err := m.decide(r.member(), "contract:"+m.contractOf("web"), questions.Reversible)
	if err != nil {
		return err
	}
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, done, []string{elsewhere.ID}, r.member()); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("rework for a decision the revision does not list for the work was %v, want invalid", err)
	}
	id, err := ops.DecisionRework(r.Ctx, r.Scope, done, []string{first.ID, second.ID}, r.member())
	if err != nil {
		return err
	}
	if rework, err := m.reworkIDs(done); err != nil || !slices.Equal(rework, []string{id}) {
		return Violatedf("the finished work has rework %v, want only %s: %v", rework, id, err)
	}
	made, err := r.WorkOrder(m.s, id)
	if err != nil {
		return err
	}
	if made.Kind != workorders.KindRework || made.State != workorders.ContractReview || !strings.Contains(made.Title, first.ID) || !strings.Contains(made.Title, second.ID) {
		return Violatedf("the rework is %+v, want rework in contract review whose title names both decisions", made)
	}
	if revision, err := m.demandOpen("a member made rework"); err != nil {
		return err
	} else if len(revision.Finished) != 1 || revision.Finished[0].Rework != id {
		return Violatedf("the revision lists %+v, want the rework %s on the finished work", revision.Finished, id)
	}
	again, err := ops.DecisionRework(r.Ctx, r.Scope, done, []string{first.ID}, r.member())
	if err != nil || again != id {
		return Violatedf("a second request made rework %q, want the one that waits, %s: %v", again, id, err)
	}
	if rework, err := m.reworkIDs(done); err != nil || len(rework) != 1 {
		return Violatedf("a second request left rework %v, want one: %v", rework, err)
	}
	oneWay, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay)
	if err != nil {
		return err
	}
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, done, []string{oneWay.ID}, r.member()); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("rework for a one-way decision, which the revision does not list for the work, was %v, want invalid", err)
	}
	if _, err := ops.ConfirmRevision(r.Ctx, r.Scope, m.plan, r.member()); err != nil {
		return err
	}
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, done, []string{first.ID}, r.member()); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("rework for a decision once its revision closed was %v, want invalid", err)
	}
	return r.CheckQuestions(m.s)
}

// checkRevisionWaitsForBoundWorkOnly holds ADR-0030's reach of the wait: a revision holds the work its decisions
// bind, by their scopes, and not every work order of the plan. A decision about one contract leaves the work of
// another contract of the plan to pass its review while the revision is open, and a decision that binds it holds it.
func checkRevisionWaitsForBoundWorkOnly(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	ops := r.NewOps(m.s)
	if _, err := m.decide(r.member(), "contract:"+m.contractOf("web"), questions.Reversible); err != nil {
		return err
	}
	if _, err := m.demandOpen("a member decided about one contract of the plan"); err != nil {
		return err
	}
	load, err := m.order("load")
	if err != nil {
		return err
	}
	if load.State != workorders.Ready {
		return Violatedf("a decision about another contract moved work that it does not bind to %s", load.State)
	}
	// Unstarted work no decision of the revision binds goes back to review and passes it, whatever the plan's revision.
	if err := ops.Move(r.Ctx, r.Scope, load.ID, workorders.TriggerBackToReview, r.member()); err != nil {
		return err
	}
	result, err := ops.Pass(r.Ctx, r.Scope, load.ID, r.member())
	if err != nil {
		return err
	}
	if result.State != workorders.Ready {
		return Violatedf("work of the plan that no decision of its open revision binds is %s after its review, with %+v, want ready", result.State, result.Failures)
	}
	// A decision that binds it holds it, and Confirm releases it.
	if _, err := m.decide(r.member(), "contract:"+m.contractOf("load"), questions.Reversible); err != nil {
		return err
	}
	if err := m.waitsOnRevision("load"); err != nil {
		return err
	}
	if _, err := ops.ConfirmRevision(r.Ctx, r.Scope, m.plan, r.member()); err != nil {
		return err
	}
	if load, err = m.order("load"); err != nil || load.State != workorders.Ready {
		return Violatedf("Confirm left the work its decision bound %s: %v", load.State, err)
	}
	return r.CheckQuestions(m.s)
}

func checkRevisionOneForABatch(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	all, err := m.revisions()
	if err != nil || len(all) != 1 {
		return Violatedf("a batch of %d answers left %d revisions, want 1: %v", len(m.answers), len(all), err)
	}
	if len(all[0].DecisionIDs) != len(m.answers) {
		return Violatedf("the revision lists %d decisions, want the %d answers of the batch", len(all[0].DecisionIDs), len(m.answers))
	}
	for _, answer := range m.answers {
		if !all[0].Names(answer.ID) {
			return Violatedf("the revision does not list the answer %s", answer.ID)
		}
	}
	var listed int
	for _, work := range all[0].Finished {
		if work.WorkOrder == m.orders["done"] {
			listed++
		}
	}
	if listed > 1 {
		return Violatedf("the revision lists the finished work %d times", listed)
	}
	return nil
}

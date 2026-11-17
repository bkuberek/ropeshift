package contract

import (
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
	if _, err := ops.ReviseSession(r.Ctx, r.Scope, m.plan, session, m.holder); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator revising a plan was %v, want forbidden", err)
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
	if _, err := ops.DecisionRework(r.Ctx, r.Scope, done, []string{first.ID}, m.holder); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("an orchestrator creating rework for a decision was %v, want forbidden", err)
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

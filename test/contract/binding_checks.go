package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// BindingChecks are what a decision that binds work does to it, by where the work stands (ADR-0030), on the
// world of the mid-plan scenario: a plan with a lane running on three work orders, one finished, one ready,
// and a contract review.
var BindingChecks = []Check{
	{"BD1", "a person's one-way decision returns ready work to contract review and holds it until a member passes its review", checkBindingHoldsReadyWork},
	{"BD2", "a decision that can be taken back returns ready work to review and holds nothing", checkBindingReversibleHoldsNothing},
	{"BD3", "a decision binds only the work its scope names", checkBindingScopeIsExact},
	{"BD4", "a running lane is told and its pack renewed, and it parks when on_binding_decision says", checkBindingLaneFollowsPolicy},
	{"BD5", "on_one_way_decision parks a lane for a one-way decision alone", checkBindingOneWayParksAlone},
	{"BD6", "a one-way decision reworks finished work once, naming the decision", checkBindingOneWayReworks},
	{"BD7", "a reversible decision lists finished work and reworks none", checkBindingReversibleListsFinished},
	{"BD8", "a batch of decisions that bind a plan reworks each finished work order once", checkBindingBatchReworksOnce},
	{"BD9", "a decision tells the lane of a contract review and never moves or reworks the review", checkBindingReadersAreOnlyTold},
	{"BD10", "an overturn tells a lane that the decision had told", checkBindingOverturnTellsAgain},
	{"BD11", "the control plane records no scope outside the grammar, however a decision reaches it", checkBindingRefusesBadScopes},
	{"BD12", "a lane's wake on a decision names a scope in the grammar", checkBindingWakeNamesAScope},
	{"BD13", "a plan whose file names it with an id that is not plain is approved and its questions are answered", checkBindingUnplainPlanID},
	{"BD14", "a decision that reaches finished work and its finished rework reworks the rework alone", checkBindingReworksTheTipOfAChain},
}

// checkBindingReworksTheTipOfAChain holds that a chain of rework is reworked once, at its tip: a one-way decision
// starts a rework of the finished work, which finishes, and the next one-way decision reaches both and starts a rework
// of the rework, which covers the work it redid.
func checkBindingReworksTheTipOfAChain(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay); err != nil {
		return err
	}
	first, err := m.reworkIDs(m.orders["done"])
	if err != nil || len(first) != 1 {
		return Violatedf("the first decision started rework %v of the finished work: %v", first, err)
	}
	m.orders["redone"] = first[0]
	if _, err := r.NewOps(m.s).Pass(r.Ctx, r.Scope, first[0], r.member()); err != nil {
		return err
	}
	if err := m.finish("redone"); err != nil {
		return err
	}
	if redone, err := m.order("redone"); err != nil || !redone.State.Completed() {
		return Violatedf("the rework did not finish: %+v %v", redone, err)
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay); err != nil {
		return err
	}
	if again, err := m.reworkIDs(m.orders["done"]); err != nil || !slices.Equal(again, first) {
		return Violatedf("the finished work has rework %v after the second decision, want only %v: %v", again, first, err)
	}
	if chain, err := m.reworkIDs(first[0]); err != nil || len(chain) != 1 {
		return Violatedf("the finished rework has rework %v after the second decision, want one: %v", chain, err)
	}
	reached, err := m.reached()
	if err != nil {
		return err
	}
	if len(reached) != 2 || !slices.Equal(reached[1].WorkOrders, first) {
		return Violatedf("the second decision reached %+v, want the tip of the chain alone", reached)
	}
	return r.CheckQuestions(m.s)
}

// dottedPlanFile is a plan whose file names it with an id that is no plain ID, as an author may write one. The
// ledger gives the plan an ID of its own, which is what a scope names, so the file's id never reaches a scope.
const dottedPlanFile = `+++
kind = "plan"
id = "2026.q4"
title = "Dotted"

[[decision]]
id = "currency"
question = "Which currency do invoices use?"
answer = "Euro"
rationale = "The brand guide says so."
+++

How.
`

// checkBindingUnplainPlanID holds that the scopes the control plane builds itself never fail the grammar: a plan
// the author named "2026.q4" in its file is approved, its review's questions are answered, and the answer is
// scoped by the ID the ledger gave the plan.
func checkBindingUnplainPlanID(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(dottedPlanFile))
	source.Write("alpha.md", []byte(placedContract{ID: "alpha", Plan: "2026.q4"}.text()))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	plan, err := r.planID(s, "Dotted")
	if err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return Violatedf("a plan whose id is not plain was not approved: %v", err)
	}
	review, err := newStatusRigOn(r, s, func(store ports.Store) (string, error) {
		order, err := r.reviewOf(store, plan)
		return order.ID, err
	})
	if err != nil {
		return err
	}
	asked, err := review.askAs("Which currency do invoices use?", false)
	if err != nil {
		return err
	}
	answer, err := r.NewOps(s).Answer(r.Ctx, r.Scope, asked.QuestionID, questions.Answer{DecisionID: r.ID("d"), Statement: "Euro."}, r.member())
	if err != nil {
		return Violatedf("a question about a plan whose id is not plain was not answered: %v", err)
	}
	if want := "plan:" + plan; answer.Scope != want {
		return Violatedf("the answer is scoped %q, want %q", answer.Scope, want)
	}
	return r.CheckQuestions(s)
}

// checkBindingWakeNamesAScope holds the grammar at the other place a scope is written, a lane's wake on a
// decision, which a decision of that scope fires.
func checkBindingWakeNamesAScope(r *Rig) error {
	s := r.Store()
	for _, key := range []string{"payments", "payments/refunds", "team:payments"} {
		if _, err := r.Parking(s, fmt.Sprintf(`{%q:%q}`, parking.DecisionRecorded, key)); !errors.Is(err, questions.ErrBadScope) {
			return Violatedf("a wake on a decision of %q was stored or refused as %v", key, err)
		}
	}
	request, err := r.Parked(s, fmt.Sprintf(`{%q:%q}`, parking.DecisionRecorded, "repo:payments"))
	if err != nil {
		return Violatedf("a wake on a decision of a scope in the grammar was refused: %v", err)
	}
	if _, err := r.NewOps(s).RecordDecision(r.Ctx, r.Scope, questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Stripe.", Rationale: "It follows.", Citations: []string{"PAY-12"},
		Scope: "repo:payments", Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
	}, r.member()); err != nil {
		return err
	}
	return r.ExpectWoken(s, request)
}

// checkBindingRefusesBadScopes holds the grammar at the control plane, so a worker of an older build that sends a
// bare ID or free text cannot store one after the migration that rewrote the scopes stored before.
func checkBindingRefusesBadScopes(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	for _, scope := range []string{"billing", "payments/refunds", "team:billing", "plan:", "repo:a b"} {
		_, err := ops.RecordDecision(r.Ctx, r.Scope, questions.Decision{
			ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Charge in euros.", Rationale: "It follows.", Citations: []string{"PAY-12"},
			Scope: scope, Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
		}, r.member())
		if !errors.Is(err, questions.ErrBadScope) || !errors.Is(err, questions.ErrInvalid) {
			return Violatedf("a decision scoped %q was recorded or refused as %v", scope, err)
		}
	}
	id, holder, err := r.OnRung(s, "How many refund retries?", questions.RungDecide, nil)
	if err != nil {
		return err
	}
	draft := ruling(r.ID("d"), questions.DecisionDecided)
	draft.Scope = "refunds"
	if _, err := ops.Rule(r.Ctx, r.Scope, id, draft, holder); !errors.Is(err, questions.ErrBadScope) {
		return Violatedf("an orchestrator's ruling scoped %q was recorded or refused as %v", draft.Scope, err)
	}
	draft.Scope = "repo:refunds"
	if _, err := ops.Rule(r.Ctx, r.Scope, id, draft, holder); err != nil {
		return Violatedf("a ruling in the grammar was refused: %v", err)
	}
	return r.CheckQuestions(s)
}

// decide records a decision of the scenario's plan, with the other decisions it overlaps stated as no conflict.
func (m *midPlan) decide(by ledger.Actor, scope, reversibility string) (questions.Decision, error) {
	var overlapping []questions.Decision
	err := m.s.Read(m.r.Ctx, m.r.Scope, func(tx ports.ReadTx) error {
		var err error
		overlapping, err = control.DecisionsOverlapping(tx, scope)
		return err
	})
	if err != nil {
		return questions.Decision{}, err
	}
	var noConflict []string
	for _, decision := range overlapping {
		noConflict = append(noConflict, decision.ID)
	}
	return m.r.NewOps(m.s).RecordDecision(m.r.Ctx, m.r.Scope, questions.Decision{
		ID: m.r.ID("d"), Status: questions.DecisionDecided, Statement: "Binding decision " + m.r.ID("s"), Rationale: "The brand guide says so.",
		Citations: []string{"brand-guide"}, Scope: scope, Reversibility: reversibility, Categories: []string{questions.CategoryProduct}, NoConflict: noConflict,
	}, by)
}

// order reads one work order of the scenario by its contract.
func (m *midPlan) order(contract string) (workorders.WorkOrder, error) {
	return m.r.WorkOrder(m.s, m.orders[contract])
}

// reworkIDs lists the rework of one work order.
func (m *midPlan) reworkIDs(of string) ([]string, error) {
	rows, err := m.reworkOf(of)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

// reached reads the events that say what a decision bound.
func (m *midPlan) reached() ([]questions.Reached, error) {
	events, err := m.r.Events(m.s, m.r.Scope)
	if err != nil {
		return nil, err
	}
	var out []questions.Reached
	for _, e := range events {
		if e.Type != questions.EventDecisionReached {
			continue
		}
		var reached questions.Reached
		if err := json.Unmarshal(e.Data, &reached); err != nil {
			return nil, err
		}
		out = append(out, reached)
	}
	return out, nil
}

// publishBinding publishes a lane policy that states what a decision that binds a lane's work does, and what a
// one-way decision does when it is not the same.
func (s *statusRig) publishBinding(version int64, binding, oneWay string) error {
	body := "[lane_policy]\nversion = " + string(rune('0'+version)) + "\non_contract_version = \"notify\"\non_binding_decision = \"" + binding + "\"\n"
	if oneWay != "" {
		body += "on_one_way_decision = \"" + oneWay + "\"\n"
	}
	_, err := s.r.NewOps(s.store).PublishLanePolicy(s.r.Ctx, s.r.Scope, authority.LanePolicyDraft{
		Body: body, Policy: authority.LanePolicy{Version: version, OnContractVersion: authority.LaneNotify, OnBindingDecision: binding, OnOneWayDecision: oneWay},
	}, s.r.Actor)
	return err
}

func checkBindingHoldsReadyWork(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay); err != nil {
		return err
	}
	load, err := m.order("load")
	if err != nil {
		return err
	}
	if load.State != workorders.ContractReview || load.StartHeld == nil || load.StartHeld.Reason != workorders.HoldDecision {
		return Violatedf("ready work a person's one-way decision binds is %s, held by %+v", load.State, load.StartHeld)
	}
	// The tick passes covered reviews and runs the definition of ready, and the hold keeps the work in review.
	if _, err := r.NewOps(m.s).Promote(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	if load, err = m.order("load"); err != nil || load.State != workorders.ContractReview {
		return Violatedf("the tick returned held work to %s: %v", load.State, err)
	}
	result, err := r.NewOps(m.s).Pass(r.Ctx, r.Scope, load.ID, r.member())
	if err != nil {
		return err
	}
	if load, err = m.order("load"); err != nil {
		return err
	}
	if result.State != workorders.Ready || load.State != workorders.Ready || load.StartHeld != nil {
		return Violatedf("a member passing the review left the work %s, held by %+v: %+v", load.State, load.StartHeld, result)
	}
	running, err := m.order("ids")
	if err != nil || running.State != workorders.Running {
		return Violatedf("work with a lane on it is %s: %v", running.State, err)
	}
	return r.CheckQuestions(m.s)
}

func checkBindingReversibleHoldsNothing(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	// A decision a person made that can be taken back, and one an orchestrator recorded: neither holds the work.
	for name, by := range map[string]ledger.Actor{"a person's": r.member(), "an orchestrator's": m.holder} {
		if _, err := m.decide(by, "plan:"+m.plan, questions.Reversible); err != nil {
			return err
		}
		load, err := m.order("load")
		if err != nil {
			return err
		}
		if load.State != workorders.ContractReview || load.StartHeld != nil {
			return Violatedf("ready work %s reversible decision binds is %s, held by %+v", name, load.State, load.StartHeld)
		}
		if _, err := r.NewOps(m.s).Promote(r.Ctx, r.Scope, tickActor); err != nil {
			return err
		}
		if load, err = m.order("load"); err != nil || load.State != workorders.Ready {
			return Violatedf("the tick left the work %s, and nobody held it: %v", load.State, err)
		}
	}
	return nil
}

func checkBindingScopeIsExact(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	web, err := m.order("web")
	if err != nil {
		return err
	}
	d, err := m.decide(r.member(), "contract:"+web.ContractID, questions.Reversible)
	if err != nil {
		return err
	}
	if err := m.noticeNames(m.web, d.ID); err != nil {
		return Violatedf("the lane on the work of the contract: %v", err)
	}
	for _, lane := range []*statusRig{m.ids, m.schema} {
		if err := m.noticeNames(lane, d.ID); err == nil {
			return Violatedf("lane %s of other work was told of a decision about another contract", lane.lane)
		}
	}
	if load, err := m.order("load"); err != nil || load.State != workorders.Ready {
		return Violatedf("work of another contract is %s: %v", load.State, err)
	}
	return nil
}

func checkBindingLaneFollowsPolicy(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	first, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	if err := m.noticeNames(m.ids, first.ID); err != nil {
		return err
	}
	if wanted, err := m.ids.wanted(); err != nil || wanted != "" {
		return Violatedf("the built-in policy tells a lane, and desired state asks %q: %v", wanted, err)
	}
	pack, err := m.ids.newestPack()
	if err != nil {
		return err
	}
	if pack.Version != 2 || !packHolds(pack, first.ID) {
		return Violatedf("the lane's pack is version %d and holds %v, want version 2 with the decision", pack.Version, itemRefs(pack))
	}
	if err := m.ids.publishBinding(2, authority.LanePark, ""); err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible); err != nil {
		return err
	}
	if wanted, err := m.ids.wanted(); err != nil || wanted == "" {
		return Violatedf("on_binding_decision parks and desired state asks nothing: %v", err)
	}
	return r.CheckPacks(m.s)
}

func checkBindingOneWayParksAlone(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if err := m.ids.publishBinding(2, authority.LaneNotify, authority.LanePark); err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible); err != nil {
		return err
	}
	if wanted, err := m.ids.wanted(); err != nil || wanted != "" {
		return Violatedf("a reversible decision parked a lane under on_binding_decision notify: %q %v", wanted, err)
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay); err != nil {
		return err
	}
	if wanted, err := m.ids.wanted(); err != nil || wanted == "" {
		return Violatedf("on_one_way_decision parks, and a one-way decision asked nothing: %v", err)
	}
	return nil
}

func checkBindingOneWayReworks(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	d, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay)
	if err != nil {
		return err
	}
	rework, err := m.reworkOf(m.orders["done"])
	if err != nil {
		return err
	}
	if len(rework) != 1 || !strings.Contains(rework[0].Title, d.ID) || !strings.Contains(rework[0].Title, "recorded") {
		return Violatedf("a one-way decision that binds finished work started %+v, want one rework that names it", rework)
	}
	if rework[0].State != workorders.ContractReview {
		return Violatedf("the rework is %s, want contract review", rework[0].State)
	}
	return r.CheckQuestions(m.s)
}

func checkBindingReversibleListsFinished(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	d, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	if rework, err := m.reworkIDs(m.orders["done"]); err != nil || len(rework) != 0 {
		return Violatedf("a reversible decision started rework %v: %v", rework, err)
	}
	reached, err := m.reached()
	if err != nil {
		return err
	}
	if len(reached) != 1 || reached[0].DecisionID != d.ID || !slices.Equal(reached[0].WorkOrders, []string{m.orders["done"]}) || len(reached[0].Rework) != 0 {
		return Violatedf("the decision lists %+v, want the finished work order and no rework", reached)
	}
	return nil
}

func checkBindingBatchReworksOnce(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	var decided []string
	for range 3 {
		d, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay)
		if err != nil {
			return err
		}
		decided = append(decided, d.ID)
	}
	rework, err := m.reworkIDs(m.orders["done"])
	if err != nil {
		return err
	}
	if len(rework) != 1 {
		return Violatedf("three decisions that bind finished work started %d rework, want one", len(rework))
	}
	reached, err := m.reached()
	if err != nil {
		return err
	}
	var named []string
	for _, each := range reached {
		if slices.Equal(each.Rework, rework) {
			named = append(named, each.DecisionID)
		}
	}
	if !slices.Equal(slices.Sorted(slices.Values(named)), slices.Sorted(slices.Values(decided))) {
		return Violatedf("the rework %v is on record for decisions %v, want %v", rework, named, decided)
	}
	return nil
}

func checkBindingReadersAreOnlyTold(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	review, err := r.WorkOrder(m.s, m.review.grant.WorkOrder)
	if err != nil {
		return err
	}
	d, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay)
	if err != nil {
		return err
	}
	after, err := r.WorkOrder(m.s, review.ID)
	if err != nil {
		return err
	}
	if after.State != review.State || after.StartHeld != nil {
		return Violatedf("a decision moved the contract review from %s to %s, held by %+v", review.State, after.State, after.StartHeld)
	}
	if rework, err := m.reworkIDs(review.ID); err != nil || len(rework) != 0 {
		return Violatedf("a decision reworked the contract review: %v %v", rework, err)
	}
	notices, err := r.noticesTo(m.s, m.review.lane)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(notices, func(n messages.Message) bool { return strings.Contains(n.Body, d.ID) }) {
		return Violatedf("the lane of the contract review holds %d notices, and none names %s", len(notices), d.ID)
	}
	// Once the review ends its work order is finished work, which a one-way decision reworks for every other
	// work order, and never for a review.
	if err := m.review.reviewEnds(); err != nil {
		return err
	}
	if _, err := m.decide(r.member(), "plan:"+m.plan, questions.OneWay); err != nil {
		return err
	}
	if rework, err := m.reworkIDs(review.ID); err != nil || len(rework) != 0 {
		return Violatedf("a decision reworked the contract review that ended: %v %v", rework, err)
	}
	if rework, err := m.reworkIDs(m.orders["done"]); err != nil || len(rework) != 1 {
		return Violatedf("the finished work of the plan has rework %v, want one: %v", rework, err)
	}
	return nil
}

func checkBindingOverturnTellsAgain(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	d, err := m.decide(r.member(), "plan:"+m.plan, questions.Reversible)
	if err != nil {
		return err
	}
	if err := m.noticeNames(m.ids, d.ID); err != nil {
		return err
	}
	overturned, err := r.overturn(m.s, d.ID)
	if err != nil {
		return err
	}
	if err := m.noticeNames(m.ids, overturned.New.ID); err != nil {
		return Violatedf("a lane told that a decision binds its work was not told it ended: %v", err)
	}
	return nil
}

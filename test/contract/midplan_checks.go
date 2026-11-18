package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// MidPlanChecks are the mid-plan scenario of ADR-0030: a person partway through an approved plan answers
// one batch of decisions, some of them one-way, one of which contradicts a draft decision the plan approved,
// while lanes run on the plan's work. Each check states what the decisions owe in-flight work, and a check
// that asserts behavior the product does not have yet carries a knownGap marker with the ID of its gap.
var MidPlanChecks = []Check{
	{"MP1", "a plan review's questions and a lane's question reach a member as one batch", checkMidPlanOneBatch},
	{"MP2", "each item of the batch has options and a recommendation, and a one-way reason where it applies", checkMidPlanItems},
	{"MP3", "an item states the reversibility its answer will have", checkMidPlanItemReversibility},
	{"MP4", "answering the batch records one decision per question, one-way for the one-way ones", checkMidPlanDecisions},
	{"MP5", "every answer records a rationale and citations", checkMidPlanRationale},
	{"MP6", "an answer that contradicts a draft decision replaces it", checkMidPlanAnswerSupersedes},
	{"MP7", "a member's decision that supersedes a draft decision replaces it", checkMidPlanMemberSupersedes},
	{"MP8", "a later plan version does not decide a replaced draft again", checkMidPlanReplacedDraftStays},
	{"MP9", "an answer reaches the lane that asked and wakes it", checkMidPlanAnswerWakes},
	{"MP10", "a decision that binds the plan tells the running lanes of its work", checkMidPlanTellsLanes},
	{"MP11", "a decision that binds the plan returns unstarted work to review and reworks finished work", checkMidPlanWorkStates},
	{"MP12", "a member's decision inside an approved plan marks the plan as needing revision", checkMidPlanNeedsRevision},
	{"MP13", "overturning a decision reworks the finished work it bound and tells the lanes", checkMidPlanOverturn},
	{"MP14", "an item names the decision in force that its question contradicts", checkMidPlanItemNamesConflict},
	{"MP15", "overturning a review's decision reworks no review", checkMidPlanOverturnReworksNoReview},
	{"MP16", "a blocking question reaches a member when no orchestrator serves the project", checkMidPlanNoOrchestrator},
	{"MP17", "a batch answered in one request records all 18 answers, and a request with one refused item records none", checkMidPlanBatchOnce},
	{"MP18", "a lane's options and recommendation stay on its item when the orchestrator escalates it with none of its own", checkMidPlanLaneRecommendation},
}

const (
	midPlanID = "factory"
	// midPlanQuestions is how many questions the plan review asks, and the first midPlanBlocking of them block.
	midPlanQuestions = 17
	midPlanBlocking  = 4
	midPlanOneWay    = 3
	logoQuestion     = "Which logo set should the site use?"
	logoDraft        = "Which logo set?"
	// logoRecommendation and logoRationale are what L-web gives its question, beside logoOptions.
	logoRecommendation = "Wordmark"
	logoRationale      = "A wordmark reads at every size the site uses."
)

func logoOptions() []string { return []string{"Monogram (P17)", "Wordmark"} }

// midPlan is the world of the scenario: an approved plan with five contracts, lanes in flight on three of
// their work orders, one finished and one waiting, and a review lane, and then what the batch did.
type midPlan struct {
	r       *Rig
	s       ports.Store
	source  *fake.WorkSource
	plan    string
	p17     string
	orders  map[string]string
	ids     *statusRig
	schema  *statusRig
	web     *statusRig
	review  *statusRig
	holder  ledger.Actor
	asked   []string
	logo    string
	webWake string
	batch   string
	items   []questions.Item
	// answers maps each question to the decision that answered it.
	answers map[string]questions.Decision
}

// midPlanContracts are the contracts of the plan, each with its one work order `<id>-order`.
var midPlanContracts = []string{"ids", "schema", "web", "load", "done"}

const midPlanFile = `+++
kind = "plan"
id = "` + midPlanID + `"
title = "Factory"

[[decision]]
id = "p17"
question = "` + logoDraft + `"
answer = "Monogram"
rationale = "It is the mark the brand guide shows."
+++

How.
`

// midPlanWorld approves a plan and puts lanes on its work. Nobody has asked anything yet.
func midPlanWorld(r *Rig) (*midPlan, error) {
	s := r.Store()
	m := &midPlan{r: r, s: s, source: fake.NewWorkSource(), orders: map[string]string{}, holder: r.orchestrator("holder"), answers: map[string]questions.Decision{}}
	m.source.Write("plan.md", []byte(midPlanFile))
	m.source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", midPlanID, "The first draft.")))
	for _, id := range midPlanContracts {
		m.source.Write(id+".md", []byte(placedContract{ID: id, Plan: midPlanID}.text()))
	}
	if _, err := r.intake(s, m.source); err != nil {
		return nil, err
	}
	var err error
	if m.plan, err = r.planID(s, "Factory"); err != nil {
		return nil, err
	}
	m.p17 = derivedDecisionID(m.plan, 1, "p17")
	pending, err := r.theOpenPlanApproval(s)
	if err != nil {
		return nil, err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return nil, err
	}
	for _, id := range midPlanContracts {
		order, err := r.orderOfContract(s, id)
		if err != nil {
			return nil, err
		}
		if order.State != workorders.Ready {
			return nil, Violatedf("after approval %s is %s, want ready", order.ID, order.State)
		}
		m.orders[id] = order.ID
	}
	if m.ids, err = m.laneOn("ids"); err != nil {
		return nil, err
	}
	if m.schema, err = m.laneOn("schema"); err != nil {
		return nil, err
	}
	if m.web, err = m.laneOn("web"); err != nil {
		return nil, err
	}
	if err := m.finish("done"); err != nil {
		return nil, err
	}
	m.review, err = newStatusRigOn(r, s, func(store ports.Store) (string, error) {
		review, err := r.reviewOf(store, m.plan)
		return review.ID, err
	})
	return m, err
}

// laneOn runs a lane on the work order of a contract.
func (m *midPlan) laneOn(contract string) (*statusRig, error) {
	return newStatusRigOn(m.r, m.s, func(ports.Store) (string, error) { return m.orders[contract], nil })
}

// finish runs a work order's lane to its end and accepts the work.
func (m *midPlan) finish(contract string) error {
	r := m.r
	ops := r.NewOps(m.s)
	holder, err := r.Running(m.s, m.orders[contract], "finisher")
	if err != nil {
		return err
	}
	result := LaneFinished(acceptedHead, LaneCheck("c1", true))
	if err := ops.EndLane(r.Ctx, r.Scope, holder, result, r.worker("finisher")); err != nil {
		return err
	}
	_, err = ops.Accept(r.Ctx, r.Scope, m.orders[contract], r.Actor)
	return err
}

// midPlanAnswered runs the scenario to its end: the review's 17 questions and the site lane's question
// climb the ladder to a person, arrive as one batch, and are all answered with the recommendation.
func midPlanAnswered(r *Rig) (*midPlan, error) {
	m, err := midPlanDelivered(r)
	if err != nil {
		return nil, err
	}
	return m, m.answerAll()
}

// midPlanDelivered runs the scenario to the batch a member is shown, with nothing answered.
func midPlanDelivered(r *Rig) (*midPlan, error) {
	m, err := midPlanWorld(r)
	if err != nil {
		return nil, err
	}
	if err := m.raise(); err != nil {
		return nil, err
	}
	return m, m.deliver()
}

// raise has the review lane ask its questions, the review end, and every question climb to a person.
func (m *midPlan) raise() error {
	r := m.r
	for i := 1; i <= midPlanQuestions; i++ {
		asked, err := m.review.askAs(fmt.Sprintf("Decision %d of the plan review: which does the factory take?", i), i <= midPlanBlocking)
		if err != nil {
			return err
		}
		m.asked = append(m.asked, asked.QuestionID)
	}
	if err := m.review.reviewEnds(); err != nil {
		return err
	}
	for i, id := range m.asked {
		escalation := questions.Escalation{
			Reason: questions.ReasonScopeChange, Categories: []string{questions.CategoryProduct}, Options: []string{"A", "B"},
			Recommendation: "A", RecommendationRationale: "A keeps the plan's direction.",
		}
		switch {
		case i < midPlanOneWay:
			escalation.Reason, escalation.Categories = questions.ReasonOneWay, []string{questions.CategoryArchitecture}
		case i < midPlanBlocking:
			escalation.Categories = []string{questions.CategoryData}
		}
		if err := r.climbed(m.s, id, m.holder); err != nil {
			return err
		}
		if _, err := r.NewOps(m.s).Escalate(r.Ctx, r.Scope, id, escalation, m.holder); err != nil {
			return err
		}
	}
	asked, err := m.web.askWith(logoQuestion, true, func(a *questions.Ask) {
		a.Options, a.Recommendation, a.RecommendationRationale = logoOptions(), logoRecommendation, logoRationale
	})
	if err != nil {
		return err
	}
	m.logo = asked.QuestionID
	ops := r.NewOps(m.s)
	if _, err := ops.Take(r.Ctx, r.Scope, m.logo, TTLMs, m.holder); err != nil {
		return err
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		step := questions.Step{Rung: rung, Candidates: []string{"decision:" + m.p17}, Outcome: "no match"}
		if _, err := ops.Climb(r.Ctx, r.Scope, m.logo, step, m.holder); err != nil {
			return err
		}
	}
	// The orchestrator states no options and no recommendation: the lane's reach the person.
	if _, err := ops.Escalate(r.Ctx, r.Scope, m.logo, questions.Escalation{
		Reason: questions.ReasonRequirementConflict, Categories: []string{questions.CategoryDesign},
	}, m.holder); err != nil {
		return Violatedf("the logo question does not escalate with the options and recommendation its lane gave it: %v", err)
	}
	m.webWake = r.ID("wake")
	if err := accepted(m.web.parkingUpload(m.webWake, lanes.QuestionWake([]string{m.logo}))); err != nil {
		return err
	}
	return accepted(m.web.parkedUpload(m.webWake, true))
}

// deliver closes the batch window, delivers the batch, and reads what a member is shown.
func (m *midPlan) deliver() error {
	r := m.r
	r.DB.ShiftClock(BatchWindowMs + 1)
	if _, err := r.NewOps(m.s).DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	return m.s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		batches, err := tx.Questions().WaitingBatches()
		if err != nil {
			return err
		}
		if len(batches) != 1 {
			return Violatedf("%d waiting batches, want 1", len(batches))
		}
		rows, err := tx.Questions().BatchQuestions(batches[0].ID)
		if err != nil {
			return err
		}
		policy, err := control.DecisionPolicy(tx)
		if err != nil {
			return err
		}
		m.batch, m.items = batches[0].ID, questions.Present(batches[0], rows, "", policy).Items
		return nil
	})
}

// acceptances is one request that accepts every recommendation of the batch, as Accept all does. The logo
// answer contradicts the draft decision the plan approved, and its author says it replaces that decision.
func (m *midPlan) acceptances() []questions.BatchAnswer {
	var answers []questions.BatchAnswer
	for _, id := range append(slices.Clone(m.asked), m.logo) {
		answer := m.r.accepting(id)
		if id == m.logo {
			answer.Answer.Supersedes = m.p17
		}
		answers = append(answers, answer)
	}
	return answers
}

// answerAll answers the whole batch in one request.
func (m *midPlan) answerAll() error {
	answers := m.acceptances()
	decisions, err := m.r.NewOps(m.s).AnswerBatch(m.r.Ctx, m.r.Scope, m.batch, answers, m.r.Actor)
	if err != nil {
		return err
	}
	if len(decisions) != len(answers) {
		return Violatedf("%d decisions for %d answers", len(decisions), len(answers))
	}
	for i, answer := range answers {
		m.answers[answer.Question] = decisions[i]
	}
	return nil
}

// decisionFor reads what the ledger holds of a decision now.
func (m *midPlan) decisionFor(id string) (questions.Decision, error) {
	return m.r.decisionOf(m.s, id)
}

// webPark is the site lane's parked request, as the wake checks name one.
func (m *midPlan) webPark() ParkRequest {
	return ParkRequest{Holder: workorders.Holder{Lease: m.web.grant.Lease, Lane: m.web.lane, Generation: 1}, Wake: m.webWake}
}

// itemOf finds one question's item.
func (m *midPlan) itemOf(id string) (questions.Item, error) {
	for _, item := range m.items {
		if item.ID == id {
			return item, nil
		}
	}
	return questions.Item{}, Violatedf("question %s is not in the batch", id)
}

// fieldOf reads one field of an item as it reaches a member, by its name in the API, so a check can ask for
// a field the item does not carry yet.
func fieldOf(item questions.Item, name string) (any, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	return fields[name], nil
}

// noticeNames holds a lane to a decision notice that names a decision.
func (m *midPlan) noticeNames(s *statusRig, decision string) error {
	notices, err := m.r.noticesTo(m.s, s.lane)
	if err != nil {
		return err
	}
	for _, notice := range notices {
		if strings.Contains(notice.Body, decision) {
			return nil
		}
	}
	return Violatedf("lane %s holds %d decision notices, and none names %s", s.lane, len(notices), decision)
}

func checkMidPlanOneBatch(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	delivered, err := r.deliveries(m.s)
	if err != nil {
		return err
	}
	if len(delivered) != 1 {
		return Violatedf("%d batches were delivered, want 1", len(delivered))
	}
	want := append(slices.Clone(m.asked), m.logo)
	if got := slices.Sorted(slices.Values(delivered[0].Questions)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		return Violatedf("the batch holds %d questions, want the %d asked", len(got), len(want))
	}
	return nil
}

func checkMidPlanItems(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	if len(m.items) != midPlanQuestions+1 {
		return Violatedf("%d items, want %d", len(m.items), midPlanQuestions+1)
	}
	for _, item := range m.items {
		if len(item.Options) < 2 || item.Recommendation == "" {
			return Violatedf("item %s offers %v and recommends %q", item.ID, item.Options, item.Recommendation)
		}
	}
	for i, id := range m.asked[:midPlanOneWay] {
		item, err := m.itemOf(id)
		if err != nil {
			return err
		}
		if item.EscalationReason != questions.ReasonOneWay {
			return Violatedf("question %d reads %q, want one-way", i+1, item.EscalationReason)
		}
	}
	return nil
}

// checkMidPlanItemReversibility holds G6: the fourth question reaches a one-way decision through its
// category, and its item says so, as it does for a one-way reason. Each item states what its answer is.
func checkMidPlanItemReversibility(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	for i, id := range append(slices.Clone(m.asked), m.logo) {
		item, err := m.itemOf(id)
		if err != nil {
			return err
		}
		want := questions.Reversible
		if i < midPlanBlocking {
			want = questions.OneWay
		}
		if item.Reversibility != want {
			return Violatedf("the item of question %d states reversibility %q, want %q", i+1, item.Reversibility, want)
		}
		if recorded := m.answers[id].Reversibility == questions.OneWay; recorded != (item.Reversibility == questions.OneWay) {
			return Violatedf("the item of question %d states %q, and its answer is recorded %q", i+1, item.Reversibility, m.answers[id].Reversibility)
		}
	}
	return nil
}

func checkMidPlanDecisions(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	if len(m.answers) != midPlanQuestions+1 {
		return Violatedf("%d decisions, want %d", len(m.answers), midPlanQuestions+1)
	}
	for i, id := range m.asked {
		decision := m.answers[id]
		oneWay := i < midPlanBlocking
		if (decision.Reversibility == questions.OneWay) != oneWay {
			return Violatedf("question %d is recorded %q, one-way is %t", i+1, decision.Reversibility, oneWay)
		}
		if decision.Status != questions.DecisionDecided || decision.QuestionID != id {
			return Violatedf("question %d is answered by %+v", i+1, decision)
		}
	}
	return nil
}

// checkMidPlanRationale holds G7: accepting a recommendation records its rationale and the item's citations.
func checkMidPlanRationale(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	for id, decision := range m.answers {
		item, err := m.itemOf(id)
		if err != nil {
			return err
		}
		if decision.Rationale == "" || decision.Rationale != item.RecommendationRationale || len(decision.Citations) == 0 || !slices.Equal(decision.Citations, item.Citations) {
			return Violatedf("the decision for question %s records rationale %q and citations %v, and its item holds %q and %v", id, decision.Rationale, decision.Citations, item.RecommendationRationale, item.Citations)
		}
	}
	return nil
}

// checkMidPlanLaneRecommendation holds G8: L-web asked its question with options, a recommendation, and its
// rationale, and the orchestrator escalated it with none of its own, so its item keeps what the lane gave. The
// review's questions, which the orchestrator escalated with its own, keep the orchestrator's.
func checkMidPlanLaneRecommendation(r *Rig) error {
	m, err := midPlanDelivered(r)
	if err != nil {
		return err
	}
	item, err := m.itemOf(m.logo)
	if err != nil {
		return err
	}
	if !slices.Equal(item.Options, logoOptions()) || item.Recommendation != logoRecommendation || item.RecommendationRationale != logoRationale {
		return Violatedf("the logo item offers %v, and recommends %q because %q", item.Options, item.Recommendation, item.RecommendationRationale)
	}
	own, err := m.itemOf(m.asked[0])
	if err != nil {
		return err
	}
	if own.Recommendation != "A" || own.RecommendationRationale != "A keeps the plan's direction." {
		return Violatedf("a review item the orchestrator escalated with its own recommends %q because %q", own.Recommendation, own.RecommendationRationale)
	}
	return nil
}

// checkMidPlanBatchOnce holds G11: the batch is answered in one request. One refused item records no decision, not
// the 17 before it and not the contradicting logo answer, and the request with every item recorded all 18.
func checkMidPlanBatchOnce(r *Rig) error {
	m, err := midPlanDelivered(r)
	if err != nil {
		return err
	}
	answers := m.acceptances()
	last := len(answers) - 1
	answers[last] = questions.BatchAnswer{Question: m.logo, Answer: questions.Answer{DecisionID: r.ID("d"), Statement: "Wordmark", Supersedes: m.p17}}
	ops := r.NewOps(m.s)
	if _, err := ops.AnswerBatch(r.Ctx, r.Scope, m.batch, answers, r.Actor); !errors.Is(err, questions.ErrInvalid) || !strings.Contains(err.Error(), m.logo) {
		return Violatedf("a request with a refused item: %v", err)
	}
	for _, id := range append(slices.Clone(m.asked), m.logo) {
		row, err := r.readQuestion(m.s, id)
		if err != nil {
			return err
		}
		if row.Status != questions.StatusNeedsHuman || row.DecisionID != "" {
			return Violatedf("question %s is %s after a refused request", id, row.Status)
		}
	}
	draft, err := m.decisionFor(m.p17)
	if err != nil {
		return err
	}
	if draft.Status != questions.DecisionDecided {
		return Violatedf("the draft decision is %s after a refused request", draft.Status)
	}
	if err := m.answerAll(); err != nil {
		return err
	}
	if len(m.answers) != midPlanQuestions+1 {
		return Violatedf("%d decisions, want %d", len(m.answers), midPlanQuestions+1)
	}
	for _, id := range append(slices.Clone(m.asked), m.logo) {
		row, err := r.readQuestion(m.s, id)
		if err != nil {
			return err
		}
		if row.Status != questions.StatusAnswered || row.DecisionID != m.answers[id].ID {
			return Violatedf("question %s is %s with decision %q after the whole request", id, row.Status, row.DecisionID)
		}
	}
	return nil
}

// checkMidPlanAnswerSupersedes holds that the logo answer, which contradicts the draft decision the plan
// approved and names it, replaces it.
func checkMidPlanAnswerSupersedes(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	draft, err := m.decisionFor(m.p17)
	if err != nil {
		return err
	}
	if draft.Status != questions.DecisionReplaced {
		return Violatedf("after the answer %q, the draft decision is %s", m.answers[m.logo].Statement, draft.Status)
	}
	return m.replacedBy(m.p17, m.answers[m.logo].ID)
}

// replacedBy holds a decision to the one that ended it.
func (m *midPlan) replacedBy(old, by string) error {
	var replacing questions.Decision
	err := m.s.Read(m.r.Ctx, m.r.Scope, func(tx ports.ReadTx) error {
		var err error
		replacing, err = tx.Questions().ReplacedBy(old)
		return err
	})
	if err != nil {
		return Violatedf("nothing replaces %s: %v", old, err)
	}
	if replacing.ID != by {
		return Violatedf("%s is replaced by %s, want %s", old, replacing.ID, by)
	}
	return nil
}

// replaceDraft is what a member does today to replace a draft decision: record a decision that names it.
func (m *midPlan) replaceDraft() (questions.Decision, error) {
	return m.r.NewOps(m.s).RecordDecision(m.r.Ctx, m.r.Scope, questions.Decision{
		ID: m.r.ID("d"), Status: questions.DecisionDecided, Statement: "Q: " + logoDraft + " A: Wordmark",
		Rationale: "The site needs a mark that reads at small sizes.", Citations: []string{"brand-guide"},
		Scope: "plan:" + m.plan, Reversibility: questions.Reversible, Categories: []string{questions.CategoryDesign},
		Supersedes: m.p17,
	}, m.r.member())
}

func checkMidPlanMemberSupersedes(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	recorded, err := m.replaceDraft()
	if err != nil {
		return err
	}
	draft, err := m.decisionFor(m.p17)
	if err != nil {
		return err
	}
	if draft.Status != questions.DecisionReplaced {
		return Violatedf("a decision that names the draft left it %s", draft.Status)
	}
	return m.replacedBy(m.p17, recorded.ID)
}

// checkMidPlanReplacedDraftStays holds that a draft a member replaced is not decided again by the next version
// of the plan, whose text of it is unchanged.
func checkMidPlanReplacedDraftStays(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	if _, err := m.replaceDraft(); err != nil {
		return err
	}
	m.source.Write("spec.md", []byte(DocumentText("spec", "api", "The API", midPlanID, "The second draft.")))
	if _, err := r.intake(m.s, m.source); err != nil {
		return err
	}
	pending, err := r.theOpenPlanApproval(m.s)
	if err != nil || pending.PlanVersion != 2 {
		return Violatedf("the open request is %+v: %v", pending, err)
	}
	if _, err := r.approve(m.s, pending, true, theMember); err != nil {
		return err
	}
	inForce, err := r.planDecisions(m.s, m.plan)
	if err != nil {
		return err
	}
	for _, decision := range inForce {
		if decision.Status.InForce() && strings.Contains(decision.Statement, "Monogram") {
			return Violatedf("version 2 decided %q again as %s, after a member replaced it", decision.Statement, decision.ID)
		}
	}
	return nil
}

func checkMidPlanAnswerWakes(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	messagesToWeb, err := r.messagesTo(m.s, m.web.lane)
	if err != nil {
		return err
	}
	told := false
	for _, message := range messagesToWeb {
		told = told || (message.Kind == messages.KindAnswer && message.ReplyTo == m.logo && strings.Contains(message.Body, "Wordmark"))
	}
	if !told {
		return Violatedf("the site lane holds %d messages, and none is the answer to its question", len(messagesToWeb))
	}
	return r.ExpectWoken(m.s, m.webPark())
}

// checkMidPlanTellsLanes holds ADR-0030's scope: the review's questions are about the plan, so the scope of
// the decision that answers one is `plan:<id>`, which binds every work order under the plan, and the lanes
// that build under it are told.
func checkMidPlanTellsLanes(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	first := m.answers[m.asked[0]]
	if want := "plan:" + m.plan; first.Scope != want {
		return Violatedf("the decision that answers a question of the plan review has scope %q, want %q", first.Scope, want)
	}
	for _, lane := range []*statusRig{m.ids, m.schema} {
		if err := m.noticeNames(lane, first.ID); err != nil {
			return Violatedf("a lane that builds under the plan: %v", err)
		}
	}
	return nil
}

// checkMidPlanWorkStates holds ADR-0030's response per work state: four one-way decisions that bind the plan
// return its unstarted work to contract review and rework its finished work. The tick runs first. A blocking
// question already returns unstarted work to review while it waits, and the tick returns the work when the
// question closes, so the state before the tick is the question's doing and the state after it is what the
// decisions left: a person's decision holds the work in review until a member passes its review again.
func checkMidPlanWorkStates(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	if _, err := r.NewOps(m.s).Promote(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	var missing []string
	if load, err := r.WorkOrder(m.s, m.orders["load"]); err != nil {
		return err
	} else if load.State != workorders.ContractReview {
		missing = append(missing, fmt.Sprintf("work that has not started is %s", load.State))
	} else if load.StartHeld == nil || load.StartHeld.Reason != workorders.HoldDecision {
		// The plan's revision keeps the work in review as well, so a person's one-way decision is held to its own hold.
		missing = append(missing, fmt.Sprintf("work that has not started is held by %+v, and no decision holds it", load.StartHeld))
	}
	reworked, err := m.reworkOf(m.orders["done"])
	if err != nil {
		return err
	}
	if len(reworked) == 0 {
		missing = append(missing, "finished work has no rework")
	}
	if len(missing) != 0 {
		return Violatedf("after four one-way decisions that bind the plan: %s", strings.Join(missing, ", "))
	}
	return nil
}

// reworkOf lists the rework work orders of one work order.
func (m *midPlan) reworkOf(id string) ([]workorders.WorkOrder, error) {
	rows, err := m.r.readOrders(m.s)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rows, func(row workorders.WorkOrder) bool { return row.ReworkOf != id }), nil
}

// checkMidPlanNeedsRevision holds G5: the answers of a member inside an approved plan leave the plan needing
// revision, with one open revision that lists each of them and waits for a member to confirm the approved version
// or approve a later one.
func checkMidPlanNeedsRevision(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	revision, err := m.demandOpen(fmt.Sprintf("%d decisions of a member inside an approved plan", len(m.answers)))
	if err != nil {
		return err
	}
	if len(revision.DecisionIDs) != len(m.answers) || revision.ApprovedVersion != 1 {
		return Violatedf("the revision of version %d lists %d decisions, want the %d answers of version 1", revision.ApprovedVersion, len(revision.DecisionIDs), len(m.answers))
	}
	return nil
}

func checkMidPlanOverturn(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	// The site lane's own question: its answer binds the work order the lane runs on, which is not finished.
	logo, err := r.overturn(m.s, m.answers[m.logo].ID)
	if err != nil {
		return err
	}
	if len(logo.Rework) != 0 || !slices.Contains(logo.Noticed, m.web.lane) {
		return Violatedf("overturning the logo decision reworked %v and told %v, want no rework and the site lane", logo.Rework, logo.Noticed)
	}
	// A review's question, answered for the whole plan.
	overturned, err := r.overturn(m.s, m.answers[m.asked[midPlanBlocking]].ID)
	if err != nil {
		return err
	}
	switch reworked, err := m.reworkedFrom(overturned.Rework, m.orders["done"]); {
	case err != nil:
		return err
	case !reworked:
		return Violatedf("overturning a decision that binds the plan reworked %v, and not the finished work", overturned.Rework)
	case !slices.Contains(overturned.Noticed, m.ids.lane):
		return Violatedf("overturning a decision that binds the plan told %v, and not the lane that builds under it", overturned.Noticed)
	}
	return nil
}

// reworkedFrom says whether any of the work orders is a rework of one work order.
func (m *midPlan) reworkedFrom(ids []string, of string) (bool, error) {
	for _, id := range ids {
		row, err := m.r.WorkOrder(m.s, id)
		if err != nil {
			return false, err
		}
		if row.ReworkOf == of {
			return true, nil
		}
	}
	return false, nil
}

// checkMidPlanItemNamesConflict holds that the item of the logo question names the draft decision it contradicts
// among its overlaps, which the ladder's first rung found as a candidate and could not say contradicts it.
func checkMidPlanItemNamesConflict(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	item, err := m.itemOf(m.logo)
	if err != nil {
		return err
	}
	named, err := fieldOf(item, "overlaps")
	if err != nil {
		return err
	}
	if list, _ := named.([]any); !slices.Contains(list, any(m.p17)) {
		return Violatedf("the item of a question that contradicts decision %s names the overlaps %v", m.p17, named)
	}
	return nil
}

// checkMidPlanOverturnReworksNoReview holds the rule that a decision never reworks a work order that reads or
// judges: the review lane's decisions are attached to the review's own work order, which finished when the
// review ended, and overturning one once reworked it.
func checkMidPlanOverturnReworksNoReview(r *Rig) error {
	m, err := midPlanAnswered(r)
	if err != nil {
		return err
	}
	overturned, err := r.overturn(m.s, m.answers[m.asked[midPlanBlocking]].ID)
	if err != nil {
		return err
	}
	reworked, err := m.reworkedFrom(overturned.Rework, m.review.grant.WorkOrder)
	if err != nil {
		return err
	}
	if reworked {
		return Violatedf("overturning a decision of the review reworked the review's work order %s", m.review.grant.WorkOrder)
	}
	return nil
}

// checkMidPlanNoOrchestrator holds FR-QD-3: with no orchestrator serving the project, the core takes a lane's question
// to a person once its batch window passes, so a batch holds it.
func checkMidPlanNoOrchestrator(r *Rig) error {
	m, err := midPlanWorld(r)
	if err != nil {
		return err
	}
	asked, err := m.ids.askAs("Which ID scheme do the work orders take?", true)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	if _, err := r.NewOps(m.s).DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	delivered, err := r.deliveries(m.s)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(delivered, func(d questions.Delivery) bool { return slices.Contains(d.Questions, asked.QuestionID) }) {
		return Violatedf("a blocking question waited past the batch window with no orchestrator, and %d batches reached a member", len(delivered))
	}
	return nil
}

package control

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Rule records what an orchestrator decided on rung 3 or assumed on rung 4, and closes the
// question it holds, in one transaction. The policy in force decides what it may do, so a
// policy that changed while the question stood on a rung still applies (FR-ORCH-5).
func (q *Questions) Rule(ctx context.Context, scope ledger.WorkspaceID, id string, ruling questions.Ruling, actor ledger.Actor) (questions.Decision, error) {
	if !authority.Allowed(actor, authority.ActionRecordDecision) {
		return questions.Decision{}, workorders.ErrForbidden
	}
	record, err := questions.NewDecision(questions.Decision{
		ID: ruling.ID, Status: ruling.Status, Statement: ruling.Statement, Rationale: ruling.Rationale, Citations: ruling.Citations,
		Scope: ruling.Scope, Reversibility: ruling.Reversibility, Categories: ruling.Categories, DecidedBy: actor.ID,
		Supersedes: ruling.Supersedes, NoConflict: ruling.NoConflict, QuestionID: id,
	})
	if err != nil {
		return questions.Decision{}, err
	}
	by, err := readParty(ctx, q.store, scope, actor)
	if err != nil {
		return questions.Decision{}, err
	}
	err = q.store.Write(ctx, scope, "question.rule", func(tx ports.WriteTx) error {
		question, err := tx.Questions().Question(id)
		if err != nil {
			return err
		}
		if question.DecisionID == record.ID && question.Status == questions.StatusAnswered {
			record, err = tx.Questions().Decision(record.ID)
			return err
		}
		if question, err = held(tx, id, actor); err != nil {
			return err
		}
		policy, err := policyInForce(tx)
		if err != nil {
			return err
		}
		if err := checkRung(tx, question, record, policy); err != nil {
			return err
		}
		if record.Scope == "" {
			record.Scope = questions.ScopeOf(question.SubjectKind, question.SubjectID)
		} else if err := checkHeld(tx, record.Scope); err != nil {
			return err
		}
		for _, category := range question.Categories {
			if !slices.Contains(record.Categories, category) {
				record.Categories = append(record.Categories, category)
			}
		}
		if err := checkCategories(tx.Questions(), record.Categories); err != nil {
			return err
		}
		if err := admit(tx, &record, policy, by); err != nil {
			return err
		}
		if err := q.recordAdmitted(tx, record, boundAnswered, actor, ""); err != nil {
			return err
		}
		crashpoint.Hit("question.rule", "decided")
		step := questions.Step{
			Rung: question.Rung, Candidates: record.Citations, Outcome: string(record.Status), By: actor.ID, Decision: record.ID, Rationale: record.Rationale,
		}
		question.LadderTrace = append(question.LadderTrace, step)
		question.Status, question.DecisionID, question.ClaimedBy, question.ClaimUntil = questions.StatusAnswered, record.ID, "", 0
		return q.closeQuestion(tx, question, actor)
	})
	if err != nil {
		return questions.Decision{}, err
	}
	return record, nil
}

// checkRung holds a ruling to the rung its question stands on, and an assumption to the
// policy's limit. An assumption on a question about a contract counts toward no work order.
func checkRung(tx ports.WriteTx, question questions.Question, record questions.Decision, policy questions.Policy) error {
	if !policy.MayDecide(question) {
		return fmt.Errorf("%w: %v", questions.ErrOneWay, question.Categories)
	}
	rung := questions.RungDecide
	if record.Status == questions.DecisionAssumed {
		rung = questions.RungAssume
	}
	if question.Rung != rung {
		return fmt.Errorf("%w: a %s ruling on rung %d", questions.ErrWrongRung, record.Status, question.Rung)
	}
	if record.Status != questions.DecisionAssumed || question.SubjectKind != workorders.SubjectKind {
		return nil
	}
	open, err := tx.Questions().Assumptions(question.SubjectID)
	if err != nil {
		return err
	}
	if open >= policy.MaxAssumptionsPerWorkOrder {
		return fmt.Errorf("%w: %s holds %d", questions.ErrAssumptionLimit, question.SubjectID, open)
	}
	return nil
}

// admit stamps a decision and holds it to the policy, unless a person is deciding. An orchestrator decides a reversible choice outside
// every one-way category, and says how it stands to each decision it overlaps. Replacing a
// decision is deciding its question again, so that decision's categories count too, and an
// orchestrator replaces only what is recorded as reversible (FR-QD-8). A delegate's decision runs
// outside those bounds only when the delegate policy exempted it, and never in a one-way category.
func admit(tx ports.WriteTx, record *questions.Decision, policy questions.Policy, by party) error {
	// A scope is in the grammar whoever records it, so a worker of an older build cannot store one outside it
	// after the migration that rewrote the scopes stored before (ADR-0030).
	if _, err := questions.ParseScope(record.Scope); err != nil && !acceptsAnyScope() {
		return err
	}
	unattended, err := runsUnattended(tx.Policies(), tx.Workers(), by, tx.Now())
	if err != nil {
		return err
	}
	return bound(tx.Questions(), newScopeFacts(tx.Planning(), tx.WorkOrders()), record, policy, recorder{actor: by.Actor, unattended: unattended})
}

// recorder is who records a decision, and whether a member set its worker to unattended and accepted its one-way
// acknowledgement, which lets an orchestrator decide a one-way choice the policy leaves it.
type recorder struct {
	actor      ledger.Actor
	unattended bool
}

func bound(reader ports.QuestionReader, facts *scopeFacts, record *questions.Decision, policy questions.Policy, by recorder) error {
	actor := by.actor
	if err := stamp(reader, facts, record, policy); err != nil {
		return err
	}
	var old questions.Decision
	var err error
	if record.Supersedes != "" {
		if old, err = reader.Decision(record.Supersedes); err != nil {
			return err
		}
		if !old.Status.InForce() {
			return fmt.Errorf("%w: %s is %s", questions.ErrDecisionEnded, old.ID, old.Status)
		}
	}
	unbound := authority.Allowed(actor, authority.ActionDecideUnbound)
	if unbound && authority.Allowed(actor, authority.ActionDecideOneWay) {
		return nil
	}
	if !unbound {
		check := record.CheckBound
		if by.unattended {
			check = record.CheckBoundUnattended
		}
		if err := check(); err != nil {
			return err
		}
	}
	categories, err := categoriesOf(reader, *record, old)
	if err != nil {
		return err
	}
	// An unattended orchestrator's one-way decision that names no category counts as kept, so it reaches a member too.
	if !policy.Open(categories) || (record.Reversibility == questions.OneWay && (unbound || policy.KeepsOneWay(categories))) {
		return fmt.Errorf("%w: %v", questions.ErrOneWay, categories)
	}
	if record.Supersedes != "" && old.Reversibility != questions.Reversible {
		return fmt.Errorf("%w: %s is not recorded as reversible", questions.ErrOneWay, old.ID)
	}
	if unbound {
		return nil
	}
	if unstated := record.Unstated(record.Overlaps); len(unstated) != 0 {
		return &questions.OverlapError{Overlapping: unstated}
	}
	return nil
}

// categoriesOf are the categories a decision is held to: its own, the ones of the decision it replaces,
// and the ones of the question it answers.
func categoriesOf(reader ports.QuestionReader, record, old questions.Decision) ([]string, error) {
	categories := slices.Concat(record.Categories, old.Categories)
	if record.QuestionID != "" {
		question, err := reader.Question(record.QuestionID)
		if err != nil {
			return nil, err
		}
		categories = slices.Concat(categories, question.Categories)
	}
	return categories, nil
}

// stamp fills in what a decision records about the moment it was made.
func stamp(reader ports.QuestionReader, facts *scopeFacts, record *questions.Decision, policy questions.Policy) error {
	record.PolicyVersion = policy.Version
	overlapping, err := overlappingDecisions(reader, facts, record.Scope)
	if err != nil {
		return err
	}
	record.Overlaps = nil
	for _, row := range overlapping {
		record.Overlaps = append(record.Overlaps, row.ID)
	}
	return nil
}

// policyProbe is who a decision is held to when the question is whether an orchestrator could record it.
var policyProbe = ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "policy-probe"}

// DecisionStanding says where a decision would stand under the decision policy in force: whether an
// orchestrator could record it as it is, and whether it falls in a one-way category.
type DecisionStanding struct {
	WithinPolicy bool
	OneWay       bool
}

// readPolicy reads the decision policy in force, or the built-in one, with no write.
func readPolicy(tx ports.ReadTx) (questions.Policy, error) {
	version, err := tx.Policies().InForce(authority.PolicyDecision)
	if errors.Is(err, ports.ErrNotFound) {
		if version, err = builtInPolicy(builtInPolicyAuthor, 0); err != nil {
			return questions.Policy{}, err
		}
	} else if err != nil {
		return questions.Policy{}, err
	}
	return questions.ReadPolicy(version)
}

// DecisionPolicy reads the decision policy in force, for a read that shows what the policy decides, such as the
// reversibility an item's answer will have.
func DecisionPolicy(tx ports.ReadTx) (questions.Policy, error) { return readPolicy(tx) }

// Weigh reads where a decision would stand, with no write. A delegate's decision that an orchestrator
// could record is operational, and any other is consent (ADR-0024).
func (q *Questions) Weigh(ctx context.Context, scope ledger.WorkspaceID, draft questions.Decision) (DecisionStanding, error) {
	record, err := questions.NewDecision(draft)
	if err != nil {
		return DecisionStanding{}, err
	}
	var standing DecisionStanding
	err = q.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		policy, err := readPolicy(tx)
		if err != nil {
			return err
		}
		var old questions.Decision
		if record.Supersedes != "" {
			if old, err = tx.Questions().Decision(record.Supersedes); err != nil && !errors.Is(err, ports.ErrNotFound) {
				return err
			}
		}
		categories, err := categoriesOf(tx.Questions(), record, old)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		standing.OneWay = !policy.Open(categories) || record.Reversibility == questions.OneWay ||
			(record.Supersedes != "" && old.Reversibility != questions.Reversible)
		probe := record
		standing.WithinPolicy = !standing.OneWay && bound(tx.Questions(), newScopeFacts(tx.Planning(), tx.WorkOrders()), &probe, policy, recorder{actor: policyProbe}) == nil
		return nil
	})
	return standing, err
}

// AnswerStanding says whether answering a question decides in a one-way category, which no delegate
// policy exempts.
func (q *Questions) AnswerStanding(ctx context.Context, scope ledger.WorkspaceID, id string) (bool, error) {
	var oneWay bool
	err := q.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		question, err := tx.Questions().Question(id)
		if err != nil {
			return err
		}
		policy, err := readPolicy(tx)
		if err != nil {
			return err
		}
		oneWay = policy.AnswerReversibility(question) == questions.OneWay
		return nil
	})
	return oneWay, err
}

// recordAdmitted writes a decision, applies what it binds, and ends the one it replaces. Replacing a decision
// implies no rework of the work it bound: the new decision binds that work as any decision does. channel is where
// a member's word came from, which the decision's event keeps; empty records none.
func (q *Questions) recordAdmitted(tx ports.WriteTx, record questions.Decision, how string, actor ledger.Actor, channel string) error {
	if err := insertDecisionFrom(tx, record, actor, channel); err != nil {
		return err
	}
	if record.Supersedes == "" {
		return q.applyBinding(tx, record, how, actor)
	}
	old, err := tx.Questions().Decision(record.Supersedes)
	if err != nil {
		return err
	}
	// A decision that replaces one with the same content, as a member's Keep of an orchestrator's one-way decision does,
	// binds nothing the decision it replaces did not bind already, so the work it reaches stands as it was: no hold, no
	// park, no rework, and no plan revision (FR-QD-8).
	if !record.SameContent(old) {
		if err := q.applyBinding(tx, record, how, actor); err != nil {
			return err
		}
	}
	touched, err := reachOf(tx, old)
	if err != nil {
		return err
	}
	if _, err := endDecision(tx, old, questions.DecisionReplaced, actor); err != nil {
		return err
	}
	crashpoint.Hit("decision.record", "replaced")
	if _, err := q.noticeLanes(tx, old.ID, record.ID, touched, actor); err != nil {
		return err
	}
	if err := q.renewPacks(tx, touched.packs, actor); err != nil {
		return err
	}
	crashpoint.Hit("decision.record", "repacked")
	return nil
}

// endDecision is the one way a decision leaves force, so it ends once.
func endDecision(tx ports.WriteTx, decision questions.Decision, status questions.DecisionStatus, actor ledger.Actor) (questions.Decision, error) {
	ended, err := tx.Questions().EndDecision(decision.ID, status)
	if err != nil {
		return questions.Decision{}, err
	}
	if !ended {
		return questions.Decision{}, fmt.Errorf("%w: %s", questions.ErrDecisionEnded, decision.ID)
	}
	decision.Status = status
	event, err := questions.RecordEvent(questions.EventDecisionEnded, questions.SubjectDecision, decision.ID, decision, actor)
	if err != nil {
		return questions.Decision{}, err
	}
	_, err = tx.Append(event)
	return decision, err
}

// reach is the work and the lanes a decision touched, and the packs that hold it. The work orders in
// `orders` are the ones a decision acts on, and `told` are the contract reviews and reviews whose lanes read
// or judge work it binds: their lanes are told, and the work orders themselves are never moved or reworked.
type reach struct {
	orders []workorders.WorkOrder
	told   []workorders.WorkOrder
	lanes  map[string]int64
	// asked are the lanes attached to a question the decision answered, which read the answer itself.
	asked map[string]bool
	packs []string
}

// readsWhatIsBound says a work order that only reads or judges stands over work a decision binds: a contract
// review of the plan a plan scope names, or a review of work the decision reached, which `bound` holds by ID.
func readsWhatIsBound(order workorders.WorkOrder, scope questions.Scope, bound map[string]bool) bool {
	switch order.Kind {
	case workorders.KindContractReview:
		return scope.Kind == questions.ScopePlan && order.ReviewsPlan == scope.ID
	case workorders.KindReview:
		return bound[order.Reviews]
	}
	return false
}

// reachOf finds what rested on a decision: the subject of each question it answered, the
// work of each lane attached to one, work under a contract that cites it, work its scope binds
// (resolved through the ledger), and every lane whose pack holds it, which the item index finds. A decision
// acts on `change` and `rework` work alone: the lanes of a plan, a contract review, or a review it reached
// are told, and those work orders are in none of the lists.
func reachOf(tx ports.WriteTx, decision questions.Decision) (reach, error) {
	touched := reach{lanes: map[string]int64{}, asked: map[string]bool{}}
	subjects := map[string]bool{}
	asked, err := questionsOf(tx, decision)
	if err != nil {
		return reach{}, err
	}
	for _, question := range asked {
		subjects[question.SubjectID] = question.SubjectID != ""
		attached, err := tx.Questions().Attachments(question.ID)
		if err != nil {
			return reach{}, err
		}
		for _, attachment := range attached {
			touched.lanes[attachment.Lane] = attachment.Generation
			touched.asked[attachment.Lane] = true
			row, err := tx.Lanes().Row(attachment.Lane)
			if err == nil {
				subjects[row.WorkOrder] = true
			} else if !errors.Is(err, ports.ErrNotFound) {
				return reach{}, err
			}
		}
	}
	contracts, err := tx.Planning().Contracts()
	if err != nil {
		return reach{}, err
	}
	for _, contract := range contracts {
		if slices.Contains(contract.Decisions, decision.ID) {
			subjects[contract.ID] = true
		}
	}
	orders, err := tx.WorkOrders().List()
	if err != nil {
		return reach{}, err
	}
	facts := newScopeFacts(tx.Planning(), tx.WorkOrders())
	for _, order := range orders {
		if responseOf(order) == workorders.Untouched {
			continue
		}
		covered := subjects[order.ID] || subjects[order.ContractID]
		if !covered {
			if covered, err = facts.covers(decision.Scope, order); err != nil {
				return reach{}, err
			}
		}
		if covered {
			touched.orders = append(touched.orders, order)
		}
	}
	plan, _ := questions.ParseScope(decision.Scope)
	bound := map[string]bool{}
	for _, order := range touched.orders {
		bound[order.ID] = true
	}
	for _, order := range orders {
		if readsWhatIsBound(order, plan, bound) {
			touched.told = append(touched.told, order)
		}
	}
	if err := addPinnedLanes(tx, &touched, contextpacks.KindDecision, decision.ID); err != nil {
		return reach{}, err
	}
	return touched, addRunningLanes(tx, &touched)
}

// questionsOf reads the questions a decision answered and the one it names, which a member
// can record a decision about without closing.
func questionsOf(tx ports.WriteTx, decision questions.Decision) ([]questions.Question, error) {
	asked, err := tx.Questions().AnsweredBy(decision.ID)
	if err != nil || decision.QuestionID == "" {
		return asked, err
	}
	named := func(q questions.Question) bool { return q.ID == decision.QuestionID }
	if slices.ContainsFunc(asked, named) {
		return asked, nil
	}
	question, err := tx.Questions().Question(decision.QuestionID)
	if errors.Is(err, ports.ErrNotFound) {
		return asked, nil
	}
	if err != nil {
		return nil, err
	}
	return append(asked, question), nil
}

// addRunningLanes adds the live lanes on affected work, and drops any lane that ended.
func addRunningLanes(tx ports.WriteTx, touched *reach) error {
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return err
	}
	onWork := map[string]bool{}
	for _, order := range slices.Concat(touched.orders, touched.told) {
		onWork[order.ID] = true
	}
	for _, row := range rows {
		switch {
		case row.Status.ActualState == ledger.StateEnded:
			delete(touched.lanes, row.ID)
		case onWork[row.WorkOrder]:
			touched.lanes[row.ID] = row.Generation
		}
	}
	return nil
}

// noticeLanes leaves one notice per lane. A lane reads it from the ledger, so a notice
// missed in flight is still there for the next generation.
func (q *Questions) noticeLanes(tx ports.WriteTx, decision, replacedBy string, touched reach, actor ledger.Actor) ([]string, error) {
	lanes := slices.Sorted(maps.Keys(touched.lanes))
	replacement, err := tx.Questions().Decision(replacedBy)
	if err != nil {
		return nil, err
	}
	return lanes, q.tellDecision(tx, decision, replacedBy, messages.Clip(questions.NoticeText(decision, replacement)), touched, actor)
}

// tellDecision leaves one notice per lane, and repeats nothing a lane was told. A lane told that a decision
// binds its work is told again when the decision ends, because that is another fact.
func (q *Questions) tellDecision(tx ports.WriteTx, decision, replacedBy, body string, touched reach, actor ledger.Actor) error {
	for _, lane := range slices.Sorted(maps.Keys(touched.lanes)) {
		notice := questions.Notice{
			DecisionID: decision, Lane: lane, Generation: touched.lanes[lane], ReplacedBy: replacedBy, NoticedAt: tx.Now(),
		}
		added, err := tx.Questions().Notice(notice)
		if err != nil {
			return err
		}
		if !added {
			continue
		}
		event, err := questions.RecordEvent(questions.EventDecisionNoticed, questions.SubjectDecision, decision, notice, actor)
		if err != nil {
			return err
		}
		if _, err := tx.Append(event); err != nil {
			return err
		}
		draft := messages.Draft{
			ID: messages.DerivedID(string(messages.KindDecisionNotice), decision, lane, replacedBy), To: messages.Target{Lane: lane},
			Kind: messages.KindDecisionNotice, Body: body,
		}
		if err := q.tell(tx, draft, actor); err != nil {
			return err
		}
	}
	return nil
}

// Overturn is a member reversing a decision, in one transaction: the old decision becomes
// `overturned`, a new one supersedes it, each finished work order it bound gets rework, unstarted work
// returns to review, each lane is told and parks as the lane policy says, and the old decision's experience
// record gains its outcome. A kill leaves all of that or none of it. The client's ID lets a retry find the
// first attempt.
func (q *Questions) Overturn(ctx context.Context, scope ledger.WorkspaceID, id string, overturn questions.Overturn, actor ledger.Actor) (questions.Overturned, error) {
	if !authority.Allowed(actor, authority.ActionOverturn) {
		return questions.Overturned{}, workorders.ErrForbidden
	}
	if err := overturn.Check(); err != nil {
		return questions.Overturned{}, err
	}
	var result questions.Overturned
	err := q.store.Write(ctx, scope, "decision.overturn", func(tx ports.WriteTx) error {
		found, done, err := overturnFound(tx, id, overturn)
		if err != nil {
			return err
		}
		if done {
			result = found
			return nil
		}
		result, err = q.overturnIn(tx, id, overturn, actor)
		return err
	})
	if err != nil {
		return questions.Overturned{}, err
	}
	return result, nil
}

// overturnFound rebuilds what an earlier attempt with this new decision's ID did.
func overturnFound(tx ports.WriteTx, id string, overturn questions.Overturn) (questions.Overturned, bool, error) {
	next, err := tx.Questions().Decision(overturn.DecisionID)
	if errors.Is(err, ports.ErrNotFound) {
		return questions.Overturned{}, false, nil
	}
	if err != nil {
		return questions.Overturned{}, false, err
	}
	if next.Supersedes != id {
		return questions.Overturned{}, false, fmt.Errorf("%w: decision %s exists", questions.ErrInvalid, next.ID)
	}
	old, err := tx.Questions().Decision(id)
	if err != nil {
		return questions.Overturned{}, false, err
	}
	if old.Status != questions.DecisionOverturned {
		return questions.Overturned{}, false, fmt.Errorf("%w: decision %s replaced %s", questions.ErrInvalid, next.ID, id)
	}
	experience, err := tx.Measures().Experience(measures.SubjectDecision, id)
	if err != nil {
		return questions.Overturned{}, false, err
	}
	notices, err := tx.Questions().Notices(id)
	if err != nil {
		return questions.Overturned{}, false, err
	}
	result := questions.Overturned{Old: old, New: next}
	if experience.Outcome != nil {
		result.Rework = experience.Outcome.ReworkWorkOrders
	}
	for _, notice := range notices {
		if notice.ReplacedBy != "" {
			result.Noticed = append(result.Noticed, notice.Lane)
		}
	}
	return result, true, nil
}

func (q *Questions) overturnIn(tx ports.WriteTx, id string, overturn questions.Overturn, actor ledger.Actor) (questions.Overturned, error) {
	old, err := tx.Questions().Decision(id)
	if err != nil {
		return questions.Overturned{}, err
	}
	policy, err := policyInForce(tx)
	if err != nil {
		return questions.Overturned{}, err
	}
	// What rested on the decision is read first, while its questions still name it.
	touched, err := reachOf(tx, old)
	if err != nil {
		return questions.Overturned{}, err
	}
	result := questions.Overturned{}
	if result.Old, err = endDecision(tx, old, questions.DecisionOverturned, actor); err != nil {
		return questions.Overturned{}, err
	}
	crashpoint.Hit("decision.overturn", "overturned")
	if result.New, err = succeed(tx, result.Old, overturn, policy, actor); err != nil {
		return questions.Overturned{}, err
	}
	crashpoint.Hit("decision.overturn", "succeeded")
	if err := returnUnstarted(tx, touched.in(workorders.Unstarted), result.New.ID, result.New.Reversibility == questions.OneWay, actor); err != nil {
		return questions.Overturned{}, err
	}
	why := []because{{result.Old.ID, boundOverturned}}
	finished := chainTips(touched.in(workorders.Finished))
	if result.Rework, err = reworkFinished(tx, finished, why, actor); err != nil {
		return questions.Overturned{}, err
	}
	if err := recordReached(tx, result.Old.ID, finished, result.Rework, boundOverturned, actor); err != nil {
		return questions.Overturned{}, err
	}
	crashpoint.Hit("decision.overturn", "reworked")
	// An overturn reworked the finished work it reached, so it lists none.
	if !marksNoOverturn() {
		if err := reviseApprovedPlans(tx, result.New, touched, nil, true, actor); err != nil {
			return questions.Overturned{}, err
		}
	}
	crashpoint.Hit("decision.overturn", "revised")
	if result.Noticed, err = q.noticeLanes(tx, id, result.New.ID, touched, actor); err != nil {
		return questions.Overturned{}, err
	}
	if err := parkAsPolicySays(tx, touched, boundOverturned, result.New.Reversibility == questions.OneWay, result.New.ID, actor); err != nil {
		return questions.Overturned{}, err
	}
	crashpoint.Hit("decision.overturn", "noticed")
	packs, err := packsOfWork(tx, touched.in(workorders.Live))
	if err != nil {
		return questions.Overturned{}, err
	}
	if err := q.renewPacks(tx, slices.Compact(slices.Sorted(slices.Values(append(packs, touched.packs...)))), actor); err != nil {
		return questions.Overturned{}, err
	}
	crashpoint.Hit("decision.overturn", "repacked")
	outcome := measures.Outcome{Status: measures.OutcomeOverturned, ReworkWorkOrders: result.Rework}
	filled, err := fillOutcome(tx, measures.SubjectDecision, id, outcome, actor)
	if err != nil {
		return questions.Overturned{}, err
	}
	if !filled {
		return questions.Overturned{}, fmt.Errorf("%w: the outcome of %s is already filled", questions.ErrDecisionEnded, id)
	}
	crashpoint.Hit("decision.overturn", "measured")
	return result, nil
}

// succeed records what took an overturned decision's place. Its question points to the new
// decision, so the next asker reads the answer a person gave.
func succeed(tx ports.WriteTx, old questions.Decision, overturn questions.Overturn, policy questions.Policy, actor ledger.Actor) (questions.Decision, error) {
	record, err := questions.NewDecision(questions.Decision{
		ID: overturn.DecisionID, Status: questions.DecisionDecided, Statement: overturn.Statement, Rationale: overturn.Rationale,
		Citations: overturn.Citations, Scope: old.Scope, Reversibility: old.Reversibility, Categories: old.Categories,
		DecidedBy: actor.ID, Supersedes: old.ID, QuestionID: old.QuestionID,
	})
	if err != nil {
		return questions.Decision{}, err
	}
	if err := stamp(tx.Questions(), newScopeFacts(tx.Planning(), tx.WorkOrders()), &record, policy); err != nil {
		return questions.Decision{}, err
	}
	if err := insertDecision(tx, record, actor); err != nil {
		return questions.Decision{}, err
	}
	answered, err := tx.Questions().AnsweredBy(old.ID)
	if err != nil {
		return questions.Decision{}, err
	}
	for _, question := range answered {
		question.DecisionID = record.ID
		if err := saveQuestion(tx, question, actor); err != nil {
			return questions.Decision{}, err
		}
	}
	return record, nil
}

// insertRework starts the rework of a finished work order, with the reasons decisions gave for it in its title.
func insertRework(tx ports.WriteTx, id string, order workorders.WorkOrder, reason string, actor ledger.Actor) error {
	version, err := pinnedVersion(tx.Planning(), order)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("Rework %s: %s", order.Title, reason)
	record, err := workorders.NewRework(id, order, title, version)
	if err != nil {
		return err
	}
	if err := tx.WorkOrders().Insert(record); err != nil {
		return err
	}
	event, err := workorders.CreatedEvent(record, actor)
	if err != nil {
		return err
	}
	if _, err := tx.Append(event); err != nil {
		return err
	}
	if err := estimateMade(tx, record); err != nil {
		return err
	}
	for _, trigger := range []workorders.Trigger{workorders.TriggerAccepted, workorders.TriggerContractSubmitted} {
		if err := takeEdge(tx, id, trigger, actor); err != nil {
			return err
		}
	}
	return nil
}

// pinnedVersion is the contract version rework starts on: the newest, because the rework
// follows what the contract says now. Work with no contract pins none.
func pinnedVersion(reader ports.PlanningReader, order workorders.WorkOrder) (int64, error) {
	if order.ContractID == "" {
		return order.ContractVersion, nil
	}
	latest, err := reader.LatestContract(order.ContractID)
	if errors.Is(err, ports.ErrNotFound) {
		return order.ContractVersion, nil
	}
	if err != nil {
		return 0, err
	}
	return latest.Version, nil
}

// writeExperience opens a decision's experience record, with the policy version in force, the options
// its question offered, and the settings of the session that decided (FR-EXP-1, FR-EXP-2). A question
// about a work order gives the record that work's size.
func writeExperience(tx ports.WriteTx, record questions.Decision, actor ledger.Actor) error {
	var options []string
	settings, err := deciderSettings(tx, actor)
	if err != nil {
		return err
	}
	settings.WorkOrderSize = measures.SizeUnknown
	if record.QuestionID != "" {
		question, err := tx.Questions().Question(record.QuestionID)
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		options = question.Options
		if question.SubjectKind == workorders.SubjectKind {
			if order, err := tx.WorkOrders().Get(question.SubjectID); err == nil {
				settings.WorkOrderSize = measures.SizeOf(order.Size)
			}
		}
	}
	id, err := newID(tx.Now())
	if err != nil {
		return err
	}
	return insertExperience(tx, measures.Experience{
		ID: id, SubjectKind: measures.SubjectDecision, SubjectID: record.ID, PolicyVersion: record.PolicyVersion,
		Settings: settings, OptionsConsidered: options, Chosen: record.Statement,
	}, actor)
}

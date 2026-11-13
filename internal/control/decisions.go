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
			record.Scope = question.SubjectID
		}
		for _, category := range question.Categories {
			if !slices.Contains(record.Categories, category) {
				record.Categories = append(record.Categories, category)
			}
		}
		if err := checkCategories(tx.Questions(), record.Categories); err != nil {
			return err
		}
		if err := admit(tx, &record, policy, actor); err != nil {
			return err
		}
		if err := q.recordAdmitted(tx, record, actor); err != nil {
			return err
		}
		crashpoint.Hit("question.rule", "decided")
		step := questions.Step{Rung: question.Rung, Candidates: record.Citations, Outcome: string(record.Status)}
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
func admit(tx ports.WriteTx, record *questions.Decision, policy questions.Policy, actor ledger.Actor) error {
	return bound(tx.Questions(), record, policy, actor)
}

func bound(reader ports.QuestionReader, record *questions.Decision, policy questions.Policy, actor ledger.Actor) error {
	if err := stamp(reader, record, policy); err != nil {
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
		if err := record.CheckBound(); err != nil {
			return err
		}
	}
	categories, err := categoriesOf(reader, *record, old)
	if err != nil {
		return err
	}
	if !policy.Open(categories) || (unbound && record.Reversibility == questions.OneWay) {
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
func stamp(reader ports.QuestionReader, record *questions.Decision, policy questions.Policy) error {
	record.PolicyVersion = policy.Version
	overlapping, err := reader.Overlapping(record.Scope)
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
		standing.WithinPolicy = !standing.OneWay && bound(tx.Questions(), &probe, policy, policyProbe) == nil
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
		oneWay = question.EscalationReason == questions.ReasonOneWay || !policy.MayDecide(question)
		return nil
	})
	return oneWay, err
}

// recordAdmitted writes a decision and ends the one it replaces, with no rework implied.
func (q *Questions) recordAdmitted(tx ports.WriteTx, record questions.Decision, actor ledger.Actor) error {
	if err := insertDecision(tx, record, actor); err != nil {
		return err
	}
	if err := q.noticeOneWay(tx, record, actor); err != nil {
		return err
	}
	if record.Supersedes == "" {
		return nil
	}
	old, err := tx.Questions().Decision(record.Supersedes)
	if err != nil {
		return err
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

// reach is the work and the lanes a decision touched, and the packs that hold it.
type reach struct {
	orders []workorders.WorkOrder
	lanes  map[string]int64
	packs  []string
}

// reachOf finds what rested on a decision: the subject of each question it answered, the
// work of each lane attached to one, work under a contract that cites it, work inside its
// scope, and every lane whose pack holds it, which the item index finds.
func reachOf(tx ports.WriteTx, decision questions.Decision) (reach, error) {
	touched := reach{lanes: map[string]int64{}}
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
	for _, order := range orders {
		if rests(order, decision.Scope, subjects) {
			touched.orders = append(touched.orders, order)
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

func rests(order workorders.WorkOrder, scope string, subjects map[string]bool) bool {
	return subjects[order.ID] || subjects[order.ContractID] || questions.ScopesOverlap(scope, order.ID) ||
		questions.ScopesOverlap(scope, order.ContractID) || questions.ScopesOverlap(scope, order.RepositoryID)
}

// addRunningLanes adds the live lanes on affected work, and drops any lane that ended.
func addRunningLanes(tx ports.WriteTx, touched *reach) error {
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return err
	}
	onWork := map[string]bool{}
	for _, order := range touched.orders {
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

// tellDecision leaves one notice per lane, and repeats nothing a lane was told.
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
			ID: messages.DerivedID(string(messages.KindDecisionNotice), decision, lane), To: messages.Target{Lane: lane},
			Kind: messages.KindDecisionNotice, Body: body,
		}
		if err := q.tell(tx, draft, actor); err != nil {
			return err
		}
	}
	return nil
}

// noticeOneWay tells each lane a one-way decision now covers, and parks the ones its policy says
// to, because work that goes on past a choice nobody can take back may have to be thrown away.
func (q *Questions) noticeOneWay(tx ports.WriteTx, record questions.Decision, actor ledger.Actor) error {
	if record.Reversibility != questions.OneWay {
		return nil
	}
	touched, err := reachOf(tx, record)
	if err != nil {
		return err
	}
	crashpoint.Hit("decision.record", "noticed")
	if len(touched.lanes) == 0 {
		return nil
	}
	if err := q.tellDecision(tx, record.ID, "", messages.Clip(questions.OneWayNoticeText(record)), touched, actor); err != nil {
		return err
	}
	policy, err := lanePolicyInForce(tx)
	if err != nil || policy.OnOneWayDecision != authority.LanePark {
		return err
	}
	for _, lane := range slices.Sorted(maps.Keys(touched.lanes)) {
		if err := askToPark(tx, lane, touched.lanes[lane], "one-way decision "+record.ID, actor); err != nil {
			return err
		}
	}
	return nil
}

// Overturn is a member reversing a decision, in one transaction: the old decision becomes
// `overturned`, a new one supersedes it, each completed work order that rested on it gets
// rework, each lane is told, and the old decision's experience record gains its outcome. A
// kill leaves all of that or none of it. The client's ID lets a retry find the first attempt.
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
		result.Noticed = append(result.Noticed, notice.Lane)
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
	if result.Rework, err = createRework(tx, result.Old, touched, actor); err != nil {
		return questions.Overturned{}, err
	}
	crashpoint.Hit("decision.overturn", "reworked")
	if result.Noticed, err = q.noticeLanes(tx, id, result.New.ID, touched, actor); err != nil {
		return questions.Overturned{}, err
	}
	crashpoint.Hit("decision.overturn", "noticed")
	if err := q.renewPacks(tx, touched.packs, actor); err != nil {
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
	if err := stamp(tx.Questions(), &record, policy); err != nil {
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

// createRework starts one `rework` work order per completed one, in contract review, where a
// person reviews it as they would any work. Work still in progress learns from its notice.
func createRework(tx ports.WriteTx, old questions.Decision, touched reach, actor ledger.Actor) ([]string, error) {
	var created []string
	for _, order := range touched.orders {
		if !order.State.Completed() {
			continue
		}
		id, err := newID(tx.Now())
		if err != nil {
			return nil, err
		}
		if err := insertRework(tx, id, order, old, actor); err != nil {
			return nil, err
		}
		if err := calibrateLaterDefect(tx, order, fmt.Sprintf("decision %s was overturned", old.ID), actor); err != nil {
			return nil, err
		}
		created = append(created, id)
	}
	return created, nil
}

func insertRework(tx ports.WriteTx, id string, order workorders.WorkOrder, old questions.Decision, actor ledger.Actor) error {
	version, err := pinnedVersion(tx.Planning(), order)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("Rework %s: decision %s was overturned", order.Title, old.ID)
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

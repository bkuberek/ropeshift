package contract

import (
	"context"
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// UnjudgedChecks hold FR-QD-3: a question no live orchestrator took within its batch window reaches a person from the
// core, a member works it in an orchestrator's place, and an orchestrator that returns reads only what is still open.
var UnjudgedChecks = []Check{
	{"UJ1", "a question an orchestrator takes within its window stays the orchestrator's", checkUnjudgedTakenInWindow},
	{"UJ2", "a question no orchestrator takes reaches a person after its window, saying no orchestrator judged it", checkUnjudgedReachesAPerson},
	{"UJ3", "a question whose orchestrator stops mid-window reaches a person once its hold ends", checkUnjudgedOrchestratorStops},
	{"UJ4", "a member answers, assumes, or merges a question with no orchestrator, and only the one left reaches a batch", checkUnjudgedMemberWorks},
	{"UJ5", "an orchestrator that returns takes only the questions still open", checkUnjudgedOrchestratorReturns},
	{"UJ6", "only a member merges a question with no hold, never into itself or into a closed question, and a planning lane's question only into one a person answers", checkUnjudgedMergeRules},
	{"UJ7", "a member's assumption of a one-way question is refused, and the question waits", checkUnjudgedOneWayAssumption},
	{"UJ8", "a member claims, dispatches, and recovers work with no orchestrator", checkUnjudgedMemberRunsWork},
	{"UJ9", "a question whose text search fails still reaches a person, with no candidates, and the tick says so once", checkUnjudgedMatcherFails},
	{"UJ10", "an orchestrator that takes a question between the core's read and its write keeps it", checkUnjudgedCoreLosesTake},
}

const unjudgedText = "Which ID scheme do the work orders take?"

// deliveredHolds ticks once and reports whether a delivered batch holds a question.
func (r *Rig) deliveredHolds(s ports.Store, id string) (bool, error) {
	if _, err := r.NewOps(s).DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return false, err
	}
	delivered, err := r.deliveries(s)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(delivered, func(d questions.Delivery) bool { return slices.Contains(d.Questions, id) }), nil
}

func checkUnjudgedTakenInWindow(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	asked, err := r.ask(s, unjudgedText, "lane-a", blocking)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs - TTLMs/2)
	holder := r.orchestrator("holder")
	if _, err := ops.Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder); err != nil {
		return err
	}
	r.DB.ShiftClock(TTLMs/2 + 1)
	reached, err := r.deliveredHolds(s, asked.QuestionID)
	if err != nil {
		return err
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if reached || row.ClaimedBy != holder.ID || row.Status != questions.StatusTriage || row.NoOrchestrator() {
		return Violatedf("a question an orchestrator holds past its window reads %+v, delivered %t", row, reached)
	}
	if err := r.climbed(s, asked.QuestionID, holder); err != nil {
		return Violatedf("the orchestrator cannot climb the question it holds: %v", err)
	}
	return nil
}

func checkUnjudgedReachesAPerson(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	cited, err := ops.RecordDecision(r.Ctx, r.Scope, questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Work orders take sequential integer IDs.",
		Categories: []string{questions.CategoryProduct},
	}, r.Actor)
	if err != nil {
		return err
	}
	asked, err := r.ask(s, unjudgedText, "lane-a", nil)
	if err != nil {
		return err
	}
	if err := r.noDeliveryYet(s); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	reached, err := r.deliveredHolds(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if !reached {
		return Violatedf("a question no orchestrator took waited past its window, and no batch holds it")
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusNeedsHuman || row.EscalationReason != questions.ReasonScopeChange || !row.NoOrchestrator() {
		return Violatedf("the question reads %+v", row)
	}
	var item questions.Item
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		batch, err := tx.Questions().Batch(row.BatchID)
		if err != nil {
			return err
		}
		policy, err := control.DecisionPolicy(tx)
		if err != nil {
			return err
		}
		presented := questions.Present(batch, []questions.Question{row}, "", policy)
		if len(presented.Items) != 1 {
			return Violatedf("the batch presents %d items", len(presented.Items))
		}
		item = presented.Items[0]
		return nil
	})
	if err != nil {
		return err
	}
	if !item.NoOrchestrator || !slices.Contains(item.Citations, cited.ID) || len(item.LadderTrace) != 4 {
		return Violatedf("the item reads %+v, want it unjudged with %s among its candidates", item, cited.ID)
	}
	return nil
}

func checkUnjudgedOrchestratorStops(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	holder := r.orchestrator("holder")
	stopped, err := r.ask(s, unjudgedText, "lane-a", blocking)
	if err != nil {
		return err
	}
	if err := r.climbed(s, stopped.QuestionID, holder); err != nil {
		return err
	}
	if _, err := ops.LetGo(r.Ctx, r.Scope, holder); err != nil {
		return err
	}
	crashed, err := r.ask(s, "Which logo set should the site use?", "lane-b", blocking)
	if err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, crashed.QuestionID, TTLMs, r.orchestrator("crashed")); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	for _, id := range []string{stopped.QuestionID, crashed.QuestionID} {
		reached, err := r.deliveredHolds(s, id)
		if err != nil {
			return err
		}
		if !reached {
			return Violatedf("question %s, whose orchestrator stopped, reached no batch", id)
		}
	}
	row, err := r.readQuestion(s, stopped.QuestionID)
	if err != nil {
		return err
	}
	if len(row.LadderTrace) != 4 || row.LadderTrace[0].Outcome != "no match" || row.LadderTrace[2].Outcome != questions.OutcomeNoOrchestrator {
		return Violatedf("the trace of a question an orchestrator climbed before it stopped reads %+v", row.LadderTrace)
	}
	return nil
}

func checkUnjudgedMemberWorks(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	var ids []string
	for _, text := range []string{"What is the retry budget?", "Which region hosts the audit log?", "Which ID scheme do work orders use?", unjudgedText} {
		asked, err := r.ask(s, text, r.ID("lane"), blocking)
		if err != nil {
			return err
		}
		ids = append(ids, asked.QuestionID)
	}
	answered, assumed, merged, stays := ids[0], ids[1], ids[2], ids[3]
	if _, err := ops.Answer(r.Ctx, r.Scope, answered, questions.Answer{DecisionID: r.ID("d"), Statement: "Two retries."}, r.Actor); err != nil {
		return err
	}
	assumption, err := ops.Answer(r.Ctx, r.Scope, assumed, questions.Answer{DecisionID: r.ID("d"), Statement: "The home region.", Rationale: "It is where the data lives.", Assumed: true}, r.Actor)
	if err != nil {
		return err
	}
	if assumption.Status != questions.DecisionAssumed {
		return Violatedf("a member's assumption recorded %s", assumption.Status)
	}
	if err := ops.MergeQuestion(r.Ctx, r.Scope, merged, stays, r.Actor); err != nil {
		return err
	}
	for id, want := range map[string]questions.Status{answered: questions.StatusAnswered, assumed: questions.StatusAnswered, merged: questions.StatusDismissed} {
		row, err := r.readQuestion(s, id)
		if err != nil {
			return err
		}
		if row.Status != want {
			return Violatedf("question %s is %s, want %s", id, row.Status, want)
		}
		if into, ok := row.MergedInto(); id == merged && (!ok || into != stays) {
			return Violatedf("the merged question names %q as the one that stays", into)
		}
	}
	attached, err := r.attachments(s, stays)
	if err != nil {
		return err
	}
	if len(attached) != 2 {
		return Violatedf("the question that stays holds %d lanes, want its own and the merged one's", len(attached))
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	if _, err := ops.DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	delivered, err := r.deliveries(s)
	if err != nil {
		return err
	}
	if len(delivered) != 1 || !slices.Equal(delivered[0].Questions, []string{stays}) {
		return Violatedf("the batches delivered %+v, want only %s", delivered, stays)
	}
	return nil
}

func checkUnjudgedOrchestratorReturns(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	var ids []string
	for _, text := range []string{"What is the retry budget?", "Which region hosts the audit log?", "Which ID scheme do work orders use?", unjudgedText} {
		asked, err := r.ask(s, text, r.ID("lane"), nil)
		if err != nil {
			return err
		}
		ids = append(ids, asked.QuestionID)
	}
	escalated, answered, merged, open := ids[0], ids[1], ids[2], ids[3]
	r.DB.ShiftClock(BatchWindowMs / 2)
	late, err := r.ask(s, "Who signs in to the admin console?", "lane-late", nil)
	if err != nil {
		return err
	}
	if _, err := ops.Answer(r.Ctx, r.Scope, answered, questions.Answer{DecisionID: r.ID("d"), Statement: "Two retries."}, r.Actor); err != nil {
		return err
	}
	if err := ops.MergeQuestion(r.Ctx, r.Scope, merged, open, r.Actor); err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs/2 + 1)
	if _, err := ops.DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	returned := r.orchestrator("returned")
	for _, id := range []string{escalated, answered, merged, open} {
		if _, err := ops.Take(r.Ctx, r.Scope, id, TTLMs, returned); err == nil {
			return Violatedf("a returning orchestrator took question %s, which no longer waits for one", id)
		}
	}
	if _, err := ops.Take(r.Ctx, r.Scope, late.QuestionID, TTLMs, returned); err != nil {
		return Violatedf("a returning orchestrator cannot take the question still open: %v", err)
	}
	return nil
}

func checkUnjudgedMergeRules(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	one, err := r.ask(s, "What is the retry budget?", "lane-a", nil)
	if err != nil {
		return err
	}
	other, err := r.ask(s, unjudgedText, "lane-b", nil)
	if err != nil {
		return err
	}
	for _, actor := range []string{"orchestrator", "lane"} {
		caller := r.laneActor("lane-a")
		if actor == "orchestrator" {
			caller = r.orchestrator("holder")
		}
		if err := ops.MergeQuestion(r.Ctx, r.Scope, one.QuestionID, other.QuestionID, caller); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("an %s merged a question with no hold: %v", actor, err)
		}
	}
	if err := ops.MergeQuestion(r.Ctx, r.Scope, one.QuestionID, one.QuestionID, r.Actor); !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("a question merged into itself: %v", err)
	}
	if err := ops.Dismiss(r.Ctx, r.Scope, other.QuestionID, r.Actor); err != nil {
		return err
	}
	if err := ops.MergeQuestion(r.Ctx, r.Scope, one.QuestionID, other.QuestionID, r.Actor); !errors.Is(err, questions.ErrResolved) {
		return Violatedf("a question merged into a dismissed one: %v", err)
	}
	stays, err := r.ask(s, "How long does a lease last?", "lane-c", nil)
	if err != nil {
		return err
	}
	if err := ops.MergeQuestion(r.Ctx, r.Scope, other.QuestionID, stays.QuestionID, r.Actor); !errors.Is(err, questions.ErrResolved) {
		return Violatedf("a dismissed question merged into an open one: %v", err)
	}
	if attached, err := r.attachments(s, stays.QuestionID); err != nil || len(attached) != 1 {
		return Violatedf("a refused merge of a closed question left %d lanes on the one that stays: %v", len(attached), err)
	}
	return checkPlannersQuestionMerges(r, s)
}

// checkPlannersQuestionMerges holds the rule an ask holds for a planning lane's question: it goes to the person, so a
// merge never moves its lane onto a question an orchestrator could take, decide, or assume, open or in triage.
func checkPlannersQuestionMerges(r *Rig, s ports.Store) error {
	ops := r.NewOps(s)
	planner, err := r.ask(s, "Who signs in to the admin console?", "lane-planner", direct)
	if err != nil {
		return err
	}
	open, err := r.ask(s, "Which accounts reach the admin console?", "lane-a", nil)
	if err != nil {
		return err
	}
	triage, err := r.ask(s, "Who may sign in as an administrator?", "lane-b", nil)
	if err != nil {
		return err
	}
	if _, err := ops.Take(r.Ctx, r.Scope, triage.QuestionID, TTLMs, r.orchestrator("holder")); err != nil {
		return err
	}
	for _, target := range []string{open.QuestionID, triage.QuestionID} {
		if err := ops.MergeQuestion(r.Ctx, r.Scope, planner.QuestionID, target, r.Actor); !errors.Is(err, questions.ErrInvalid) {
			return Violatedf("a planning lane's question merged into %s, which an orchestrator could answer: %v", target, err)
		}
		attached, err := r.attachments(s, target)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(attached, func(a questions.Attachment) bool { return a.Lane == "lane-planner" }) {
			return Violatedf("a refused merge moved the planning lane onto %s", target)
		}
	}
	waiting, err := r.escalated(s, "Who administers the console?", nil)
	if err != nil {
		return err
	}
	if err := ops.MergeQuestion(r.Ctx, r.Scope, planner.QuestionID, waiting, r.Actor); err != nil {
		return Violatedf("a planning lane's question did not merge into one that waits for a person: %v", err)
	}
	return nil
}

func checkUnjudgedOneWayAssumption(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	asked, err := r.ask(s, "May audit data leave the region?", "lane-a", func(a *questions.Ask) { a.Categories = []string{questions.CategoryData} })
	if err != nil {
		return err
	}
	_, err = ops.Answer(r.Ctx, r.Scope, asked.QuestionID, questions.Answer{DecisionID: r.ID("d"), Statement: "It may.", Rationale: "Nobody objected.", Assumed: true}, r.Actor)
	if !errors.Is(err, questions.ErrOneWay) {
		return Violatedf("a member's assumption of a one-way question: %v", err)
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if !row.Status.Unresolved() {
		return Violatedf("a refused assumption left the question %s", row.Status)
	}
	return nil
}

func checkUnjudgedMemberRunsWork(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	grant, err := ops.Claim(r.Ctx, r.Scope, r.Request(id, "worker-1"), r.Actor)
	if err != nil {
		return Violatedf("a member's claim with no orchestrator: %v", err)
	}
	if _, err := ops.Dispatch(r.Ctx, r.Scope, grant.WorkOrder, r.ID("lane"), r.Actor); err != nil {
		return Violatedf("a member's dispatch with no orchestrator: %v", err)
	}
	r.DB.ShiftClock(TTLMs + 1)
	if _, err := ops.Sweep(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	if err := ops.Reassign(r.Ctx, r.Scope, grant.WorkOrder, "the worker is gone", r.Actor); err != nil {
		return Violatedf("a member's recovery with no orchestrator: %v", err)
	}
	order, err := r.WorkOrder(s, id)
	if err != nil {
		return err
	}
	if order.State != workorders.Ready {
		return Violatedf("recovered work is %s, want ready", order.State)
	}
	return nil
}

// failingMatcher is a text search that always fails.
type failingMatcher struct{}

var errSearchDown = errors.New("the search is down")

func (failingMatcher) Candidates(context.Context, ledger.WorkspaceID, ports.MatchRequest) ([]ports.Candidate, error) {
	return nil, errSearchDown
}

func checkUnjudgedMatcherFails(r *Rig) error {
	s := r.Store()
	core := control.NewQuestions(s, questions.BatchPolicy{WindowMs: BatchWindowMs, MaxWaitMs: BatchMaxWaitMs}).Matching(failingMatcher{})
	asked, err := r.ask(s, unjudgedText, "lane-a", blocking)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	if _, err := core.DeliverBatches(r.Ctx, r.Scope, tickActor); !errors.Is(err, errSearchDown) {
		return Violatedf("a tick whose search failed says %v", err)
	}
	row, err := r.readQuestion(s, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.Status != questions.StatusNeedsHuman || !row.NoOrchestrator() {
		return Violatedf("a question whose search failed reads %+v", row)
	}
	for _, step := range row.LadderTrace {
		if len(step.Candidates) != 0 {
			return Violatedf("a question whose search failed lists candidates on rung %d: %v", step.Rung, step.Candidates)
		}
	}
	if _, err := core.DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return Violatedf("the next tick says %v, though nothing waits on the search", err)
	}
	return nil
}

func checkUnjudgedCoreLosesTake(r *Rig) error {
	plain := r.Store()
	asked, err := r.ask(plain, unjudgedText, "lane-a", blocking)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	holder := r.orchestrator("holder")
	var took error
	raced := false
	stepped := r.StoreWith(func(op, name string) {
		if op == "question.unjudged" && name == StepBefore && !raced {
			raced = true
			_, took = r.NewOps(plain).Take(r.Ctx, r.Scope, asked.QuestionID, TTLMs, holder)
		}
	})
	if _, err := r.NewOps(stepped).DeliverBatches(r.Ctx, r.Scope, tickActor); err != nil {
		return err
	}
	if !raced || took != nil {
		return Violatedf("the orchestrator's take did not land between the core's read and its write: raced %t, %v", raced, took)
	}
	row, err := r.readQuestion(plain, asked.QuestionID)
	if err != nil {
		return err
	}
	if row.ClaimedBy != holder.ID || row.Status != questions.StatusTriage || row.NoOrchestrator() {
		return Violatedf("the core took over a question an orchestrator took first: %+v", row)
	}
	delivered, err := r.deliveries(plain)
	if err != nil {
		return err
	}
	if len(delivered) != 0 {
		return Violatedf("a question an orchestrator took reached a member: %+v", delivered)
	}
	return nil
}

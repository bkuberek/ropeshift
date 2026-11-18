package contract

import (
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// BatchAnswerChecks hold a batch answered once (ADR-0030): every answer of one request is recorded in one
// transaction or none is, the first refusal is named, accepting a recommendation records its rationale and the
// item's citations, and only a member answers.
var BatchAnswerChecks = []Check{
	{"B14", "a batch answered in one request records every answer or none, and names the first refusal", checkBatchAnswerAllOrNone},
	{"B15", "a batch answer is refused for each way it can be wrong, and records nothing", checkBatchAnswerRefusals},
	{"B16", "only a member answers a batch", checkBatchAnswerActors},
	{"B17", "accepting a recommendation records its rationale and the item's citations, and a retry finds the answers", checkBatchAnswerAccepted},
	{"B18", "an answer that expects a reversibility the policy no longer gives refuses the whole batch, naming it", checkBatchAnswerExpectsReversibility},
}

// expecting is the answer that takes a question's recommendation on the word that its answer has this reversibility,
// as the member read it.
func (r *Rig) expecting(question, reversibility string) questions.BatchAnswer {
	answer := r.accepting(question)
	answer.ExpectReversibility = reversibility
	return answer
}

// accepting is the answer that takes a question's recommendation.
func (r *Rig) accepting(question string) questions.BatchAnswer {
	return questions.BatchAnswer{Question: question, Accepted: true, Answer: questions.Answer{DecisionID: r.ID("d")}}
}

// replying is a member's own answer to a question, with its rationale.
func (r *Rig) replying(question, statement string) questions.BatchAnswer {
	return questions.BatchAnswer{Question: question, Answer: questions.Answer{DecisionID: r.ID("d"), Statement: statement, Rationale: "It follows."}}
}

// delivered escalates a question for each text, closes the window, and reads the batch that reached a member.
func (r *Rig) delivered(s ports.Store, texts ...string) (string, []string, error) {
	var ids []string
	for _, text := range texts {
		id, err := r.escalated(s, text, nil)
		if err != nil {
			return "", nil, err
		}
		ids = append(ids, id)
	}
	r.DB.ShiftClock(BatchWindowMs + 1)
	if err := r.deliveredOnce(s, ids...); err != nil {
		return "", nil, err
	}
	delivered, err := r.deliveries(s)
	if err != nil {
		return "", nil, err
	}
	return delivered[0].Batch.ID, ids, nil
}

// recordedDecisions counts the decisions the ledger holds.
func (r *Rig) recordedDecisions(s ports.Store) (int, error) {
	var count int
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.Questions().Decisions(ports.RecordFilter{})
		count = len(rows)
		return err
	})
	return count, err
}

// nothingRecorded holds that no question of a batch is answered and no decision exists beside the ones the
// caller expects.
func (r *Rig) nothingRecorded(s ports.Store, ids []string, decisions int) error {
	for _, id := range ids {
		row, err := r.readQuestion(s, id)
		if err != nil {
			return err
		}
		if row.Status != questions.StatusNeedsHuman || row.DecisionID != "" {
			return Violatedf("question %s is %s with decision %q after a refused request", id, row.Status, row.DecisionID)
		}
	}
	count, err := r.recordedDecisions(s)
	if err != nil {
		return err
	}
	if count != decisions {
		return Violatedf("%d decisions after a refused request, want %d", count, decisions)
	}
	return r.CheckQuestions(s)
}

func checkBatchAnswerAllOrNone(r *Rig) error {
	s := r.Store()
	batch, ids, err := r.delivered(s, "What is the retry budget?", "Which currency do refunds use?", "May audit data leave the region?")
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	refused := questions.BatchAnswer{Question: ids[2], Answer: questions.Answer{DecisionID: r.ID("d"), Statement: "Allow one replica."}}
	_, err = ops.AnswerBatch(r.Ctx, r.Scope, batch, []questions.BatchAnswer{r.accepting(ids[0]), r.replying(ids[1], "The original currency."), refused}, r.Actor)
	if !errors.Is(err, questions.ErrInvalid) {
		return Violatedf("a request with a refusal in it: %v", err)
	}
	if !strings.Contains(err.Error(), ids[2]) {
		return Violatedf("the refusal %q does not name question %s", err, ids[2])
	}
	if err := r.nothingRecorded(s, ids, 0); err != nil {
		return err
	}
	refused.Answer.Rationale = "A replica stays inside the region."
	decisions, err := ops.AnswerBatch(r.Ctx, r.Scope, batch, []questions.BatchAnswer{r.accepting(ids[0]), r.replying(ids[1], "The original currency."), refused}, r.Actor)
	if err != nil {
		return err
	}
	if len(decisions) != len(ids) {
		return Violatedf("%d decisions for %d answers", len(decisions), len(ids))
	}
	for i, id := range ids {
		row, err := r.readQuestion(s, id)
		if err != nil {
			return err
		}
		if row.Status != questions.StatusAnswered || row.DecisionID != decisions[i].ID || decisions[i].QuestionID != id || decisions[i].DecidedBy != r.Actor.ID {
			return Violatedf("question %s is %s with decision %q, answered by %+v", id, row.Status, row.DecisionID, decisions[i])
		}
	}
	if count, err := r.recordedDecisions(s); err != nil || count != len(ids) {
		return Violatedf("%d decisions recorded for %d answers: %v", count, len(ids), err)
	}
	return r.CheckQuestions(s)
}

func checkBatchAnswerRefusals(r *Rig) error {
	s := r.Store()
	batch, ids, err := r.delivered(s, "What is the retry budget?", "Which currency do refunds use?")
	if err != nil {
		return err
	}
	// The three questions below are asked after the first batch was delivered, so they wait in the next one. A
	// direct question went to the person with no recommendation.
	asked, err := r.ask(s, "Who signs in to the admin console?", planner, direct)
	if err != nil {
		return err
	}
	later, err := r.escalated(s, "How long are logs kept?", nil)
	if err != nil {
		return err
	}
	settled, err := r.escalated(s, "Which region hosts the replica?", nil)
	if err != nil {
		return err
	}
	if _, err := r.answer(s, settled, "The first region."); err != nil {
		return err
	}
	bare := asked.QuestionID
	row, err := r.readQuestion(s, bare)
	if err != nil {
		return err
	}
	cases := []struct {
		name    string
		batch   string
		answers []questions.BatchAnswer
		want    error
	}{
		{"no answers", batch, nil, questions.ErrInvalid},
		{"a batch nobody opened", "no-such-batch", []questions.BatchAnswer{r.accepting(ids[0])}, ports.ErrNotFound},
		{"a question nobody asked", batch, []questions.BatchAnswer{r.accepting("no-such-question")}, ports.ErrNotFound},
		{"a question of another batch", batch, []questions.BatchAnswer{r.accepting(later)}, questions.ErrInvalid},
		{"one question twice", batch, []questions.BatchAnswer{r.accepting(ids[0]), r.replying(ids[0], "Two.")}, questions.ErrInvalid},
		{"a question answered already", row.BatchID, []questions.BatchAnswer{r.accepting(settled)}, questions.ErrResolved},
		{"an answer with no rationale", batch, []questions.BatchAnswer{{Question: ids[0], Answer: questions.Answer{DecisionID: r.ID("d"), Statement: "Two."}}}, questions.ErrInvalid},
		{"an answer that states nothing", batch, []questions.BatchAnswer{{Question: ids[0], Answer: questions.Answer{DecisionID: r.ID("d"), Rationale: "It follows."}}}, questions.ErrInvalid},
		{"an accepted answer with words of its own", batch, []questions.BatchAnswer{{Question: ids[0], Accepted: true, Answer: questions.Answer{DecisionID: r.ID("d"), Statement: "Two."}}}, questions.ErrInvalid},
		{"an answer with no decision ID", batch, []questions.BatchAnswer{{Question: ids[0], Accepted: true}}, questions.ErrInvalid},
		{"a recommendation nobody made", row.BatchID, []questions.BatchAnswer{r.accepting(bare)}, questions.ErrInvalid},
	}
	ops := r.NewOps(s)
	for _, c := range cases {
		if _, err := ops.AnswerBatch(r.Ctx, r.Scope, c.batch, c.answers, r.Actor); !errors.Is(err, c.want) {
			return Violatedf("%s: %v, want %v", c.name, err, c.want)
		}
		if err := r.nothingRecorded(s, ids, 1); err != nil {
			return Violatedf("%s: %v", c.name, err)
		}
	}
	return nil
}

func checkBatchAnswerActors(r *Rig) error {
	s := r.Store()
	batch, ids, err := r.delivered(s, "What is the retry budget?", "Which currency do refunds use?")
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	callers := map[string]ledger.Actor{"lane": r.laneActor("lane-a"), "orchestrator": r.orchestrator("holder"), "system": tickActor}
	for _, kind := range []string{"lane", "orchestrator", "system"} {
		answers := []questions.BatchAnswer{r.accepting(ids[0]), r.accepting(ids[1])}
		if _, err := ops.AnswerBatch(r.Ctx, r.Scope, batch, answers, callers[kind]); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s answered a batch: %v", kind, err)
		}
		if err := r.nothingRecorded(s, ids, 0); err != nil {
			return Violatedf("after a %s: %v", kind, err)
		}
	}
	return nil
}

func checkBatchAnswerAccepted(r *Rig) error {
	s := r.Store()
	batch, ids, err := r.delivered(s, "What is the retry budget?", "Which currency do refunds use?")
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	answers := []questions.BatchAnswer{r.accepting(ids[0]), r.replying(ids[1], "The original currency.")}
	decisions, err := ops.AnswerBatch(r.Ctx, r.Scope, batch, answers, r.Actor)
	if err != nil {
		return err
	}
	accepted, own := decisions[0], decisions[1]
	if accepted.Statement != "Keep" || accepted.Rationale != "Keep follows the current design." || !slices.Equal(accepted.Citations, []string{"decision:d1"}) {
		return Violatedf("an accepted recommendation recorded %+v", accepted)
	}
	if own.Statement != "The original currency." || own.Rationale != "It follows." {
		return Violatedf("a member's own answer recorded %+v", own)
	}
	retry, err := ops.AnswerBatch(r.Ctx, r.Scope, batch, answers, r.Actor)
	if err != nil {
		return Violatedf("a retry: %v", err)
	}
	if len(retry) != len(decisions) || retry[0].ID != accepted.ID || retry[1].ID != own.ID {
		return Violatedf("a retry found %+v, want %+v", retry, decisions)
	}
	if count, err := r.recordedDecisions(s); err != nil || count != len(ids) {
		return Violatedf("%d decisions after a retry, want %d: %v", count, len(ids), err)
	}
	return r.CheckQuestions(s)
}

// checkBatchAnswerExpectsReversibility holds that an answer taken on the word of what a member read is refused when a
// policy published since has changed it: a category turned one-way, or one turned back. The whole request is refused,
// naming the item, and records nothing.
func checkBatchAnswerExpectsReversibility(r *Rig) error {
	s := r.Store()
	batch, ids, err := r.delivered(s, "What is the retry budget?", "Which currency do refunds use?")
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	// A member read both answers as reversible, and then a policy made the product category one-way.
	if _, err := r.publish(s, 2, 5, questions.CategoryProduct); err != nil {
		return err
	}
	answers := []questions.BatchAnswer{r.accepting(ids[0]), r.expecting(ids[1], questions.Reversible)}
	_, err = ops.AnswerBatch(r.Ctx, r.Scope, batch, answers, r.Actor)
	if !errors.Is(err, questions.ErrReversibilityChanged) || !strings.Contains(err.Error(), ids[1]) || !strings.Contains(err.Error(), "one-way now") {
		return Violatedf("an answer that expected a reversible answer under a policy that made it one-way: %v", err)
	}
	if err := r.nothingRecorded(s, ids, 0); err != nil {
		return err
	}
	// The other way: the policy turned back, so an answer read as one-way is refused.
	if _, err := r.publish(s, 3, 5); err != nil {
		return err
	}
	_, err = ops.AnswerBatch(r.Ctx, r.Scope, batch, []questions.BatchAnswer{r.expecting(ids[0], questions.OneWay)}, r.Actor)
	if !errors.Is(err, questions.ErrReversibilityChanged) || !strings.Contains(err.Error(), ids[0]) || !strings.Contains(err.Error(), "reversible now") {
		return Violatedf("an answer that expected a one-way answer under a policy that made it reversible: %v", err)
	}
	if err := r.nothingRecorded(s, ids, 0); err != nil {
		return err
	}
	// What the policy says now is recorded, and so is an answer that expects nothing.
	retry := []questions.BatchAnswer{r.expecting(ids[0], questions.Reversible), r.accepting(ids[1])}
	decisions, err := ops.AnswerBatch(r.Ctx, r.Scope, batch, retry, r.Actor)
	if err != nil || len(decisions) != 2 {
		return Violatedf("answers that expect what the policy says: %+v, %v", decisions, err)
	}
	return r.CheckQuestions(s)
}

package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// Questions holds the use cases of questions and decisions. The core stores, searches, and
// validates, and an agent judges (ADR-0007).
type Questions struct {
	store   ports.Store
	batches questions.BatchPolicy
	windows messages.Windows
	// packTokens is the budget of a pack a decision renews. Zero takes the default.
	packTokens int64
}

// NewQuestions returns the use cases over a Store, with how long questions gather for a
// person.
func NewQuestions(store ports.Store, batches questions.BatchPolicy) *Questions {
	return &Questions{store: store, batches: batches.Filled()}
}

// Telling sets how long what these use cases send a lane stays useful, per kind. A kind the
// windows leave out takes the default.
func (q *Questions) Telling(windows messages.Windows) *Questions {
	q.windows = windows
	return q
}

// Budgeted sets the budget of the packs these use cases renew.
func (q *Questions) Budgeted(tokens int64) *Questions {
	q.packTokens = tokens
	return q
}

// RecordQuestion keeps a question as it is, in `open`. The client generates its ID, so a
// retry returns what the first attempt wrote.
func (q *Questions) RecordQuestion(ctx context.Context, scope ledger.WorkspaceID, draft questions.Question, actor ledger.Actor) (questions.Question, error) {
	record, err := questions.New(draft)
	if err != nil {
		return questions.Question{}, err
	}
	err = q.store.Write(ctx, scope, "question.record", func(tx ports.WriteTx) error {
		existing, err := tx.Questions().Question(record.ID)
		if err == nil {
			record = existing
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		if err := checkCategories(tx.Questions(), record.Categories); err != nil {
			return err
		}
		if err := insertQuestion(tx, record, actor); err != nil {
			return err
		}
		return holdBack(tx, record, actor)
	})
	if err != nil {
		return questions.Question{}, err
	}
	return record, nil
}

func checkCategories(reader ports.QuestionReader, names []string) error {
	added, err := reader.Categories()
	if err != nil {
		return err
	}
	return questions.CheckCategories(names, added)
}

// insertQuestion writes a question, its index entry, and its event together.
func insertQuestion(tx ports.WriteTx, record questions.Question, actor ledger.Actor) error {
	if err := tx.Questions().Insert(record); err != nil {
		return err
	}
	if err := tx.Index(questions.KindQuestion, record.ID, record.Text); err != nil {
		return err
	}
	crashpoint.Hit("question.record", "indexed")
	return appendQuestion(tx, record, actor)
}

func appendQuestion(tx ports.WriteTx, record questions.Question, actor ledger.Actor) error {
	event, err := questions.RecordEvent(questions.EventQuestionChanged, questions.SubjectQuestion, record.ID, record, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// Dismiss closes a question with no answer. A member or an orchestrator decides that, and
// it stays on record.
func (q *Questions) Dismiss(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionDismissQuestion) {
		return workorders.ErrForbidden
	}
	return q.store.Write(ctx, scope, "question.dismiss", func(tx ports.WriteTx) error {
		record, err := tx.Questions().Question(id)
		if err != nil {
			return err
		}
		if !record.Status.Unresolved() {
			return fmt.Errorf("%w: question %s is %s", questions.ErrInvalid, id, record.Status)
		}
		record.Status = questions.StatusDismissed
		if err := tx.Questions().Save(record); err != nil {
			return err
		}
		return appendQuestion(tx, record, actor)
	})
}

// RecordDecision keeps a decision, under the policy in force. The client generates its ID,
// so a retry returns what the first attempt wrote. A member or an orchestrator decides, and
// a lane never does.
func (q *Questions) RecordDecision(ctx context.Context, scope ledger.WorkspaceID, draft questions.Decision, actor ledger.Actor) (questions.Decision, error) {
	if !authority.Allowed(actor, authority.ActionRecordDecision) {
		return questions.Decision{}, workorders.ErrForbidden
	}
	record, err := questions.NewDecision(draft)
	if err != nil {
		return questions.Decision{}, err
	}
	record.DecidedBy = actor.ID
	err = q.store.Write(ctx, scope, "decision.record", func(tx ports.WriteTx) error {
		existing, err := tx.Questions().Decision(record.ID)
		if err == nil {
			record = existing
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		policy, err := policyInForce(tx)
		if err != nil {
			return err
		}
		if err := checkCategories(tx.Questions(), record.Categories); err != nil {
			return err
		}
		if err := admit(tx, &record, policy, actor); err != nil {
			return err
		}
		return q.recordAdmitted(tx, record, actor)
	})
	if err != nil {
		return questions.Decision{}, err
	}
	return record, nil
}

func insertDecision(tx ports.WriteTx, record questions.Decision, actor ledger.Actor) error {
	if err := tx.Questions().InsertDecision(record); err != nil {
		return err
	}
	if err := tx.Index(questions.KindDecision, record.ID, record.Statement+" "+record.Rationale); err != nil {
		return err
	}
	crashpoint.Hit("decision.record", "indexed")
	if err := writeExperience(tx, record, actor); err != nil {
		return err
	}
	event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, record.ID, record, actor)
	if err != nil {
		return err
	}
	if _, err := tx.Append(event); err != nil {
		return err
	}
	_, err = fireWakes(tx, "decision:"+record.ID, actor, parking.DecisionRecorded)
	return err
}

// closeQuestion saves a question an answer closed, tells every lane attached to it, and fires what
// waits on that answer, in one transaction (FR-QD-10).
func (q *Questions) closeQuestion(tx ports.WriteTx, record questions.Question, actor ledger.Actor) error {
	if err := saveQuestion(tx, record, actor); err != nil {
		return err
	}
	if err := q.tellAnswer(tx, record, actor); err != nil {
		return err
	}
	_, err := fireWakes(tx, "answer:"+record.ID, actor, parking.QuestionAnswered)
	return err
}

// tellAnswer sends an answer to each lane that waits on it. A lane the ledger has no record of
// hears nothing, and a lane whose generation ended meets the rollover table, which is how a parked
// lane's next generation is told.
func (q *Questions) tellAnswer(tx ports.WriteTx, record questions.Question, actor ledger.Actor) error {
	if leavesAnswers() {
		return nil
	}
	decision, err := tx.Questions().Decision(record.DecisionID)
	if err != nil {
		return err
	}
	attached, err := tx.Questions().Attachments(record.ID)
	if err != nil {
		return err
	}
	body := messages.Clip(questions.AnswerText(record, decision))
	for _, attachment := range attached {
		draft := messages.Draft{
			ID: messages.DerivedID(string(messages.KindAnswer), record.ID, attachment.Lane), To: messages.Target{Lane: attachment.Lane},
			ReplyTo: record.ID, Kind: messages.KindAnswer, Body: body,
		}
		if err := q.tell(tx, draft, actor); err != nil {
			return err
		}
		crashpoint.Hit("question.answer", "told")
	}
	return nil
}

// tell sends what the ledger owes a lane, in the transaction that decided it. What closes a question
// or ends a decision does so once, so this sends once.
func (q *Questions) tell(tx ports.WriteTx, draft messages.Draft, actor ledger.Actor) error {
	if _, err := tx.Lanes().Row(draft.To.Lane); errors.Is(err, ports.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	_, err := sendIn(tx, draft, q.windows, actor)
	return err
}

// AddCategory extends a workspace's categories. Only a member can, and a repeat is done.
func (q *Questions) AddCategory(ctx context.Context, scope ledger.WorkspaceID, name string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionAddCategory) {
		return workorders.ErrForbidden
	}
	if err := questions.CheckCategoryName(name); err != nil {
		return err
	}
	return q.store.Write(ctx, scope, "category.add", func(tx ports.WriteTx) error {
		added, err := tx.Questions().AddCategory(name)
		if err != nil || !added {
			return err
		}
		event, err := questions.RecordEvent(questions.EventCategoryAdded, questions.SubjectCategory, name, struct {
			Name string `json:"name"`
		}{name}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

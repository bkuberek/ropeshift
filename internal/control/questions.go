package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// Questions holds the use cases of questions and decisions. The core stores, searches, and
// validates, and an agent judges (ADR-0007).
type Questions struct {
	store ports.Store
}

// NewQuestions returns the use cases over a Store.
func NewQuestions(store ports.Store) *Questions {
	return &Questions{store: store}
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
		return insertQuestion(tx, record, actor)
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
	if actor.Kind != ledger.ActorMember && actor.Kind != ledger.ActorOrchestrator {
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

// RecordDecision keeps a decision. The client generates its ID, so a retry returns what the
// first attempt wrote. A member or an orchestrator decides, and a lane never does.
func (q *Questions) RecordDecision(ctx context.Context, scope ledger.WorkspaceID, draft questions.Decision, actor ledger.Actor) (questions.Decision, error) {
	if actor.Kind != ledger.ActorMember && actor.Kind != ledger.ActorOrchestrator {
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
		if err := checkCategories(tx.Questions(), record.Categories); err != nil {
			return err
		}
		return insertDecision(tx, record, actor)
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
	event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, record.ID, record, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// AddCategory extends a workspace's categories. Only a member can, and a repeat is done.
func (q *Questions) AddCategory(ctx context.Context, scope ledger.WorkspaceID, name string, actor ledger.Actor) error {
	if actor.Kind != ledger.ActorMember {
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

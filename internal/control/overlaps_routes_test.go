package control_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// overlapsWorld is a ledger with one decision in force for each of two plans.
type overlapsWorld struct {
	store  ports.Store
	ops    *control.Questions
	member ledger.Actor
	lane   ledger.Actor
}

func anOverlapsWorld(t *testing.T) overlapsWorld {
	t.Helper()
	ctx := t.Context()
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: "w"}, member); err != nil {
		t.Fatal(err)
	}
	err = store.Write(ctx, "w", "test.decisions", func(tx ports.WriteTx) error {
		for id, scope := range map[string]string{"d-checkout": "plan:checkout", "d-billing": "plan:billing"} {
			record := questions.Decision{ID: id, Status: questions.DecisionDecided, Statement: "Statement of " + id, Rationale: "It follows.", Scope: scope}
			if err := tx.Questions().InsertDecision(record); err != nil {
				return err
			}
			event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, record.ID, record, member)
			if err != nil {
				return err
			}
			if _, err := tx.Append(event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return overlapsWorld{store: store, ops: control.NewQuestions(store, questions.BatchPolicy{}), member: member, lane: ledger.Actor{Kind: ledger.ActorLane, ID: "lane-a"}}
}

// planned asks a question from a planning lane about a plan, which goes to the person with no ladder.
func (w overlapsWorld) planned(t *testing.T, id, plan string) {
	t.Helper()
	if _, err := w.ops.Ask(t.Context(), "w", questions.Ask{
		ID: id, Text: "Which logo set does " + plan + " use?", Categories: []string{questions.CategoryProduct},
		Lane: w.lane.ID, Generation: 1, SubjectKind: planning.SubjectPlan, SubjectID: plan, Direct: true,
	}, w.lane); err != nil {
		t.Fatal(err)
	}
}

func (w overlapsWorld) read(t *testing.T, id string) questions.Question {
	t.Helper()
	var row questions.Question
	err := w.store.Read(t.Context(), "w", func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Questions().Question(id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// TestAPlanningLanesQuestionKeepsTheDecisionsItsScopeOverlaps holds that a question a planning lane puts straight to
// the person names the decisions in force its scope overlaps, as one escalated through the ladder does (FR-QD-5).
func TestAPlanningLanesQuestionKeepsTheDecisionsItsScopeOverlaps(t *testing.T) {
	w := anOverlapsWorld(t)
	w.planned(t, "q-direct", "checkout")
	row := w.read(t, "q-direct")
	if row.Status != questions.StatusNeedsHuman || !row.Direct() {
		t.Fatalf("the question reads %s, direct %v", row.Status, row.Direct())
	}
	if want := []string{"d-checkout"}; !slices.Equal(row.Overlaps, want) {
		t.Errorf("the planning lane's question keeps the overlaps %q, want %q", row.Overlaps, want)
	}
}

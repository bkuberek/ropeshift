package server

import (
	"fmt"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ledgerReads counts the reads that resolve which decisions overlap a scope.
type ledgerReads struct {
	decisions, orders, plans int
}

type countingTx struct {
	ports.ReadTx
	reads *ledgerReads
}

func (c countingTx) Questions() ports.QuestionReader {
	return countingQuestions{c.ReadTx.Questions(), c.reads}
}
func (c countingTx) WorkOrders() ports.WorkOrderReader {
	return countingOrders{c.ReadTx.WorkOrders(), c.reads}
}
func (c countingTx) Planning() ports.PlanningReader {
	return countingPlanning{c.ReadTx.Planning(), c.reads}
}

type countingQuestions struct {
	ports.QuestionReader
	reads *ledgerReads
}

func (c countingQuestions) Decisions(filter ports.RecordFilter) ([]questions.Decision, error) {
	c.reads.decisions++
	return c.QuestionReader.Decisions(filter)
}

type countingOrders struct {
	ports.WorkOrderReader
	reads *ledgerReads
}

func (c countingOrders) Get(id string) (workorders.WorkOrder, error) {
	c.reads.orders++
	return c.WorkOrderReader.Get(id)
}

func (c countingOrders) List() ([]workorders.WorkOrder, error) {
	c.reads.orders++
	return c.WorkOrderReader.List()
}

func (c countingOrders) ByContract(contract string) ([]workorders.WorkOrder, error) {
	c.reads.orders++
	return c.WorkOrderReader.ByContract(contract)
}

type countingPlanning struct {
	ports.PlanningReader
	reads *ledgerReads
}

func (c countingPlanning) Contract(id string, version int64) (planning.Contract, error) {
	c.reads.plans++
	return c.PlanningReader.Contract(id, version)
}

func (c countingPlanning) LatestContract(id string) (planning.Contract, error) {
	c.reads.plans++
	return c.PlanningReader.LatestContract(id)
}

// manyScopes holds a ledger with a decision in force for each of several plans and a question about each of as many
// work orders, and returns the questions as the store holds them.
func manyScopes(t *testing.T, count int) (ports.Store, []questions.Question) {
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
		for i := range count {
			record := questions.Decision{ID: fmt.Sprintf("d-%02d", i), Status: questions.DecisionDecided, Statement: "Settled.", Scope: fmt.Sprintf("plan:p%02d", i)}
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
	ops := control.NewQuestions(store, questions.BatchPolicy{})
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-a"}
	for i := range count {
		if _, err := ops.Ask(ctx, "w", questions.Ask{
			ID: fmt.Sprintf("q-%02d", i), Text: fmt.Sprintf("Which logo set does work %d use?", i), Categories: []string{questions.CategoryProduct},
			Lane: lane.ID, Generation: 1, SubjectKind: workorders.SubjectKind, SubjectID: fmt.Sprintf("w%02d", i),
		}, lane); err != nil {
			t.Fatal(err)
		}
	}
	var rows []questions.Question
	err = store.Read(ctx, "w", func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.Questions().Questions(ports.RecordFilter{})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, rows
}

// TestOverlapsOfAPageAreReadOnceForThePage holds that naming the decisions each question's scope overlaps reads the
// decisions in force once for the whole page, and each work order once, however many questions and decisions it holds.
func TestOverlapsOfAPageAreReadOnceForThePage(t *testing.T) {
	const count = 10
	store, rows := manyScopes(t, count)
	if len(rows) != count {
		t.Fatalf("the ledger holds %d questions, want %d", len(rows), count)
	}
	var reads ledgerReads
	err := store.Read(t.Context(), "w", func(tx ports.ReadTx) error {
		_, err := questionViews(countingTx{tx, &reads}, rows)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// The decisions in force take one page for each status, and each work order one read.
	if reads.decisions > 2 || reads.orders > count {
		t.Errorf("a page of %d questions and %d decisions read the decisions %d times and the work orders %d times, want at most 2 and %d",
			count, count, reads.decisions, reads.orders, count)
	}
}

// TestOverlapsOfBatchesAreReadOnceForAllOfThem holds the same for the items of delivered batches, which read once for
// all the batches however many items they hold.
func TestOverlapsOfBatchesAreReadOnceForAllOfThem(t *testing.T) {
	const count = 10
	store, rows := manyScopes(t, count)
	var presented []questions.Presented
	for _, row := range rows {
		presented = append(presented, questions.Presented{
			Batch: questions.Batch{ID: "b-" + row.ID},
			Items: []questions.Item{{ID: row.ID, Scope: questions.ScopeOf(row.SubjectKind, row.SubjectID)}},
		})
	}
	var reads ledgerReads
	var batches []api.QuestionBatch
	err := store.Read(t.Context(), "w", func(tx ports.ReadTx) error {
		var err error
		batches, err = questionBatches(countingTx{tx, &reads}, presented)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, batch := range batches {
		if len(batch.Items[0].Overlaps) != 0 {
			t.Errorf("item %s names the overlaps %q, and no decision covers its work order", batch.Items[0].ID, batch.Items[0].Overlaps)
		}
	}
	if reads.decisions < 1 || reads.decisions > 2 || reads.orders > count {
		t.Errorf("%d batches read the decisions %d times and the work orders %d times, want 1 or 2 and at most %d", count, reads.decisions, reads.orders, count)
	}
}

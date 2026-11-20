package control

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// leftFilesRig is a workspace with one change in repo-1, whose lanes report paths they left out.
type leftFilesRig struct {
	t     *testing.T
	store ports.Store
}

func newLeftFilesRig(t *testing.T) leftFilesRig {
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
	if _, err := NewRepositories(store).Detect(ctx, "w", DetectedRepository{ID: "repo-1", DefaultBranch: "main", Directory: "app"}, member); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWorkOrders(store).Record(ctx, "w", LedgerWork{
		Order:    workorders.WorkOrder{ID: "wo", Kind: workorders.KindChange, Title: "T", RepositoryID: "repo-1"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, member); err != nil {
		t.Fatal(err)
	}
	return leftFilesRig{t: t, store: store}
}

// report folds one lane guard report that left the paths out.
func (r leftFilesRig) report(id string, paths ...string) {
	r.t.Helper()
	err := r.store.Write(r.t.Context(), "w", "test.guard", func(tx ports.WriteTx) error {
		return askLeftFiles(tx, "worker-1", id, ledger.UploadEvent{WorkOrder: "wo"}, ledger.LaneGuard{LeftOut: paths})
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r leftFilesRig) asked() []questions.Question {
	r.t.Helper()
	var rows []questions.Question
	err := r.store.Read(r.t.Context(), "w", func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.Questions().Questions(ports.RecordFilter{Limit: ports.MaxRecords})
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return rows
}

// TestALeftOutPathIsDecidedOnce is FR-LANE-20's record per path: the orchestrator decides the question on rung 3
// under the built-in policy, and a later lane that leaves the same path out in the same repository asks nothing.
func TestALeftOutPathIsDecidedOnce(t *testing.T) {
	r := newLeftFilesRig(t)
	ctx := t.Context()
	r.report("event-1", ".atl/")
	r.report("event-2", ".atl/")
	asked := r.asked()
	if len(asked) != 1 || !slices.Equal(asked[0].Categories, []string{questions.CategoryFiles}) || asked[0].Blocking {
		t.Fatalf("questions %+v", asked)
	}
	ops := NewQuestions(r.store, questions.BatchPolicy{})
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-a"}
	id := asked[0].ID
	if _, err := ops.Take(ctx, "w", id, 60_000, holder); err != nil {
		t.Fatal(err)
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		if _, err := ops.Climb(ctx, "w", id, questions.Step{Rung: rung, Outcome: "no_match"}, holder); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ops.Rule(ctx, "w", id, questions.Ruling{
		ID: "d-atl", Status: questions.DecisionDecided, Statement: questions.LeftFileIgnore, Rationale: "A tool's cache.", Citations: []string{"the lane report that left it out"},
		Scope: "repo:repo-1", Reversibility: questions.Reversible,
	}, holder); err != nil {
		t.Fatalf("the orchestrator may not decide a left-out path: %v", err)
	}
	r.report("event-3", ".atl/", ".idea/")
	asked = r.asked()
	if len(asked) != 2 {
		t.Fatalf("questions %+v", asked)
	}
	for _, question := range asked {
		if question.ID != id && question.Status != questions.StatusOpen {
			t.Errorf("the new path's question %+v", question)
		}
	}
}

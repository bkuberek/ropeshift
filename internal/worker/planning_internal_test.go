package worker

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A worker finds a project's planning repository from the desired state its journal holds, with no lease: by the
// project's ID, and for a lane that names no project, the plane's only project. A journal with no desired state, a
// project the state does not name, and a project with none named give nothing (FR-PROF-6).
func TestAWorkerReadsAProjectsPlanningRepositoryFromItsJournal(t *testing.T) {
	t.Parallel()
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := New(nil, journal, fake.NewClock(time.UnixMilli(1_000_000)), Config{})
	if got := w.planningRepository(t.Context(), "p1"); got != "" {
		t.Errorf("a journal with no desired state names %q", got)
	}
	state := ledger.DesiredState{Rev: 1, Projects: []ledger.DesiredProject{{ID: "p1", Name: "shop", PlanningRepository: "plan"}, {ID: "p2", Name: "blog"}}}
	if err := journal.SaveDesired(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	for project, want := range map[string]string{"p1": "plan", "p2": "", "p3": "", "": ""} {
		if got := w.planningRepository(t.Context(), project); got != want {
			t.Errorf("project %q names %q, want %q", project, got, want)
		}
	}
	state.Projects = state.Projects[:1]
	if err := journal.SaveDesired(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if got := w.planningRepository(t.Context(), ""); got != "plan" {
		t.Errorf("the only project is named by an empty one: %q, want plan", got)
	}
}

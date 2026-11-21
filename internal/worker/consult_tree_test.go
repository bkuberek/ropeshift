package worker_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// FR-ORCH-9: the read-only worktree of the question's repository that a consultation reads is the consultation's own, so
// it is removed when the consultation ends, and so is the directory that held it, whatever the checkout it came from
// keeps.
func TestAConsultationRemovesItsRepositoryWorktreeWhenItEnds(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	_, err := control.NewWorkOrders(r.store).Record(t.Context(), scope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: "wo-retries", Kind: workorders.KindChange, Title: "Retries", RepositoryID: "retries"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It retries"}},
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	_, err = control.NewQuestions(r.store, questions.BatchPolicy{}).Ask(t.Context(), scope,
		questions.Ask{ID: "q-retries", Text: "Which retry budget for this work?", SubjectKind: workorders.SubjectKind, SubjectID: "wo-retries"}, member)
	if err != nil {
		t.Fatal(err)
	}
	trees := r.config.Provisioner.Worktrees.(*fake.Worktrees)
	existed := make(chan error, 1)
	r.nextSession([]fake.DriverStep{{Bound: true}, {Do: func(spec ports.SessionSpec) {
		if len(spec.ReadOnlyDirs) != 1 {
			existed <- errors.New("the session reads no worktree")
			return
		}
		_, err := trees.Survey(t.Context(), spec.ReadOnlyDirs[0])
		existed <- err
	}}, r.reportsConsultation(t, consultReport), completed(0.1)})
	r.consult(t, map[string]any{"question": "q-retries", "ask": consultAsk})
	r.awaitConsulted(t, "q-retries")
	if err := <-existed; err != nil {
		t.Fatalf("the worktree did not exist while the session ran, so this test shows nothing: %v", err)
	}
	tree := r.consultSpecs()[0].ReadOnlyDirs[0]
	if _, err := trees.Survey(t.Context(), tree); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the worktree %s remains after the consultation ended: %v", tree, err)
	}
	if _, err := os.Stat(filepath.Dir(tree)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the consultation's own directory %s remains after it ended: %v", filepath.Dir(tree), err)
	}
}

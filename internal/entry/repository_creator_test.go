package entry

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/worker"
)

// The daemon's creator of the repositories a plan approval names is the worker it runs, with that worker's template
// runtime: before the worker starts it refuses with why, and once it runs it answers with that worker's checks.
func TestTheDaemonCreatesRepositoriesOnTheWorkerItRuns(t *testing.T) {
	t.Parallel()
	local := &soloWorker{}
	// A key the worker refuses before it reads anything, so the check reaches no folder and no gh.
	job := ledger.RepositoryCreation{Key: "not a key", Name: "api", Path: "/work/tt/repos/api", ForgeName: "api"}
	if _, err := local.Check(t.Context(), job); !errors.Is(err, errWorkerNotStarted) {
		t.Errorf("a check before the worker started answers %v", err)
	}
	if _, err := local.Create(t.Context(), job); !errors.Is(err, errWorkerNotStarted) {
		t.Errorf("a creation before the worker started answers %v", err)
	}

	local.worker.Store(worker.New(nil, nil, clock.System{}, worker.Config{Name: "atlas", Templates: soloTemplates(t.TempDir(), fake.NewCheckouts())}))
	checked, err := local.Check(t.Context(), job)
	if err != nil || checked.Refusal == nil || checked.Refusal.Reason != ledger.TemplateRefusedInvalid {
		t.Errorf("the running worker's check answers %+v (%v), want its refusal of the key", checked, err)
	}
	report, err := local.Create(t.Context(), job)
	if err != nil || report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedInvalid {
		t.Errorf("the running worker's creation answers %+v (%v), want its refusal of the key", report, err)
	}
}

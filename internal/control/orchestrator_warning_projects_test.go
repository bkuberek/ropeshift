package control_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// failingRegistry opens each project as the registry does, except that one project's store refuses the writes of a notice
// while it is set to fail, and another project is gone while it is set to be missing.
type failingRegistry struct {
	ports.Projects
	failing, missing ledger.WorkspaceID
	fail             bool
}

func (f *failingRegistry) Open(ctx context.Context, p ledger.Project) (ports.Store, error) {
	if p.ID == f.missing {
		return nil, ports.ErrNotFound
	}
	store, err := f.Projects.Open(ctx, p)
	if err != nil || p.ID != f.failing {
		return store, err
	}
	return &failingStore{Store: store, fail: &f.fail}, nil
}

type failingStore struct {
	ports.Store
	fail *bool
}

func (s *failingStore) Write(ctx context.Context, scope ledger.WorkspaceID, op string, fn func(ports.WriteTx) error) error {
	if *s.fail && op == "spend.notice" {
		return errors.New("the store is unavailable")
	}
	return s.Store.Write(ctx, scope, op, fn)
}

// warningRig is the plane rig with an orchestrator, a warning at 5, and a plane whose registry fails as a test says.
type warningRig struct {
	*planeRig
	registry *failingRegistry
	plane    *control.Plane
	call     ledger.OrchestratorCall
	seq      int64
}

func newWarningRig(t *testing.T) *warningRig {
	t.Helper()
	r := newPlaneRig(t, ledger.ProtocolVersion)
	call := r.orchestrating()
	policy := authority.DefaultInstallationSpendPolicy()
	policy.Version, policy.OrchestratorDailyWarn = 2, 5
	if _, err := control.NewSpend(r.stores["pa"]).PublishInstallationSpendPolicy(t.Context(), "pa", authority.InstallationSpendPolicyDraft{Body: policy.Body(), Policy: policy}, projectMember); err != nil {
		t.Fatal(err)
	}
	registry := &failingRegistry{Projects: r.state}
	plane := control.NewPlane(registry, r.state.Installation, "pa", control.WorkerOptions{}, nil)
	return &warningRig{planeRig: r, registry: registry, plane: plane, call: call}
}

// spend sends the report of a generation's spend again when again is set, as a worker does after an upload failed.
func (w *warningRig) spend(usd float64, again bool) error {
	raw, err := json.Marshal(ledger.GenerationUsage{CostUSD: usd, KnownTurns: 2})
	if err != nil {
		return err
	}
	if !again {
		w.seq++
	}
	_, err = w.plane.Upload(w.t.Context(), ledger.UploadBatch{Auth: w.auth, Epoch: w.epoch, Events: []ledger.UploadEvent{{
		Epoch: w.epoch, Seq: w.seq, Type: ledger.UploadGenerationUsage, Lane: w.call.Lane, Generation: 1, Payload: raw, OccurredAt: 1,
	}}})
	return err
}

// warned counts the orchestrator's warnings a project's ledger holds.
func (w *warningRig) warned(project ledger.WorkspaceID) int {
	w.t.Helper()
	count := 0
	err := w.stores[project].Read(w.t.Context(), project, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectSpendNotice, ID: ledger.SpendNoticeSubject})
		if err != nil {
			return err
		}
		for _, event := range events {
			notice, err := ledger.ReadSpendNotice(event)
			if err != nil {
				return err
			}
			if notice.Kind == ledger.NoticeSpendSoftLimit && notice.Scope == ledger.NoticeScopeOrchestrator {
				count++
			}
		}
		return nil
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return count
}

// TestOneProjectThatFailsDoesNotKeepTheOthersFromTheWarning holds that every project is tried before the report that could
// not tell one is sent again, and that sending it again tells the project that was missed, once.
func TestOneProjectThatFailsDoesNotKeepTheOthersFromTheWarning(t *testing.T) {
	w := newWarningRig(t)
	w.registry.failing, w.registry.fail = "pa", true
	if err := w.spend(6, false); err == nil {
		t.Fatal("a report that could not tell a project of the warning was taken as told")
	}
	if a, b := w.warned("pa"), w.warned("pb"); a != 0 || b != 1 {
		t.Fatalf("a project whose store failed has %d warnings and the other has %d, want 0 and 1", a, b)
	}
	w.registry.fail = false
	if err := w.spend(6, true); err != nil {
		t.Fatal(err)
	}
	if a, b := w.warned("pa"), w.warned("pb"); a != 1 || b != 1 {
		t.Fatalf("after the report was sent again the projects have %d and %d warnings, want 1 each", a, b)
	}
}

// TestAProjectThatIsGoneIsSkippedForTheWarning holds that a project the registry no longer opens does not keep the report
// from the projects that remain.
func TestAProjectThatIsGoneIsSkippedForTheWarning(t *testing.T) {
	w := newWarningRig(t)
	w.registry.missing = "pb"
	if err := w.spend(6, false); err != nil {
		t.Fatal(err)
	}
	if a := w.warned("pa"); a != 1 {
		t.Fatalf("the project that remains has %d warnings, want 1", a)
	}
}

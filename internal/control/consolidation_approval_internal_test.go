package control

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// An approver who may not remove repositories cannot approve a consolidation that removes one, and is told why;
// the design repository stays in the project.
func TestAnApproverWhoMayNotRemoveRepositoriesIsToldWhy(t *testing.T) {
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
	if _, err := NewRepositories(store).Detect(ctx, "w", DetectedRepository{ID: "design-root", DefaultBranch: "main", Directory: "design"}, member); err != nil {
		t.Fatal(err)
	}
	detail, err := json.Marshal(planning.PlanDetail{Adoption: &planning.Adoption{
		Consolidation: &planning.Consolidation{Design: "design", Removes: &planning.RemovedRepository{ID: "design-root", Name: "design"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	approval := authority.Approval{ID: "approval-1", Kind: authority.KindPlan, WorkOrder: "adopt-1", Detail: string(detail)}
	delegate := ledger.Actor{Kind: ledger.ActorDelegate, ID: "member-1/credential-1"}
	err = store.Write(ctx, "w", "test.approve", func(tx ports.WriteTx) error { return removeConsolidatedDesign(tx, approval, delegate) })
	if !errors.Is(err, workorders.ErrForbidden) || !strings.Contains(err.Error(), "removes repository design") || !strings.Contains(err.Error(), "may remove repositories") {
		t.Errorf("an approver who may not remove repositories: %v", err)
	}
	err = store.Read(ctx, "w", func(tx ports.ReadTx) error {
		record, err := tx.Repositories().Repository("design-root")
		if err == nil && record.Removed() {
			t.Errorf("the design repository was removed: %+v", record)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

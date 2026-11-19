package control

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A rerun is asked of the worker for one head, and a request with no commit would ask it to rerun nothing the forge can
// name. A pull request that holds no commit hash for its head is refused the rerun, whoever asks, and nothing is counted or
// asked of the worker.
func TestARerunOfAPullRequestWithNoHeadIsRefusedAndAsksNothing(t *testing.T) {
	ctx := t.Context()
	store, err := fake.NewMigratedStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: "w"}, member); err != nil {
		t.Fatal(err)
	}
	for name, head := range map[string]string{"an empty head": "", "a head git would read as an option": "--upload-pack=x"} {
		err = store.Write(ctx, "w", "test.rerun", func(tx ports.WriteTx) error {
			pr := ledger.WorkPullRequest{
				PullRequest: ledger.PullRequest{Number: 7, State: ledger.PullRequestOpen, HeadCommit: head},
				WorkOrder:   "wo-1", Repository: "repo-1", Worker: "worker-1",
			}
			_, err := requestRerun(tx, rerunAsked{pr: pr, kind: ledger.CIItemInfrastructure, class: ciClassMember}, member)
			return err
		})
		if !errors.Is(err, workorders.ErrInvalid) {
			t.Errorf("a rerun of %s answered %v, want it refused as invalid", name, err)
		}
	}
	err = store.Read(ctx, "w", func(tx ports.ReadTx) error {
		waits, err := tx.CI().CIWaits()
		if len(waits) != 0 {
			t.Errorf("a refused rerun was counted: %+v", waits)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

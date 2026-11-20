package server_test

import (
	"errors"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// A member's rerun of a pull request that holds no commit hash for its head is refused, and the refusal undoes the rest of
// the rerun: the finding the rerun would have cleared is still raised, so the member still sees what CI asks of them.
func TestARefusedRerunLeavesTheFindingItWouldHaveCleared(t *testing.T) {
	const notACommit = "--upload-pack=x"
	d, _, _ := aCIWorldOf(t, ledger.CIRunInProgress, time.Minute)
	err := d.Store.Write(t.Context(), apitest.Scope, "test.pull_request", func(tx ports.WriteTx) error {
		pr, err := tx.Forge().PullRequest("wo-ci")
		if err != nil {
			return err
		}
		pr.HeadCommit = notACommit
		if err := tx.Forge().SavePullRequest(pr); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventPullRequestUpdated, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: ledger.SubjectPullRequest, ID: "wo-ci"},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	attention := control.NewCIAttention(d.Store)
	finding := ledger.CIFinding{WorkOrder: "wo-ci", Head: notACommit, Kind: ledger.CIItemInfrastructure, Check: "CI", Conclusion: "timed_out", URL: ciRunPage}
	if raised, err := attention.Raise(t.Context(), apitest.Scope, finding); err != nil || !raised {
		t.Fatalf("the finding raised %v: %v", raised, err)
	}

	_, err = attention.Rerun(t.Context(), apitest.Scope, "wo-ci", ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})

	if !errors.Is(err, workorders.ErrInvalid) {
		t.Fatalf("a rerun of a pull request with no commit for its head answered %v, want it refused as invalid", err)
	}
	var kept []string
	for _, item := range ciItems(t, d) {
		kept = append(kept, item.CI.Kind)
	}
	if len(kept) != 1 || kept[0] != ledger.CIItemInfrastructure {
		t.Errorf("after the refusal CI asks %v, want the infrastructure finding still raised", kept)
	}
}

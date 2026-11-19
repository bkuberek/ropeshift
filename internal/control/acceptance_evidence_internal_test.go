package control

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A branch's head and the merge that imported a design repository's history are commit IDs a worker reports. One that
// is no commit ID is left out of the evidence, and the branch it names stays.
func TestALaneEndKeepsTheBranchAndLeavesOutACommitThatIsNoID(t *testing.T) {
	commit := strings.Repeat("ab", 20)
	for name, tt := range map[string]struct {
		head, imported, wantDigest string
		wantImport                 bool
	}{
		"commit IDs":         {head: commit, imported: commit, wantDigest: commit, wantImport: true},
		"a head and a merge": {head: "--upload-pack=x", imported: "not-a-commit", wantDigest: "", wantImport: false},
	} {
		t.Run(name, func(t *testing.T) {
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
			result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}, Branch: "lane/lane-1", Head: tt.head, ImportMerge: tt.imported}
			err = store.Write(ctx, "w", "test.end", func(tx ports.WriteTx) error {
				if err := recordEvidence(tx, workorders.Holder{Lane: "lane-1", WorkOrder: "wo-1"}, 1, result); err != nil {
					return err
				}
				_, err := tx.Append(ledger.EventDraft{Type: ledger.EventLaneArchiveRequested, Actor: member, Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: "lane-1"}})
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			var artifacts []ledger.Artifact
			if err := store.Read(ctx, "w", func(tx ports.ReadTx) error {
				var err error
				artifacts, err = tx.Lanes().Artifacts("lane-1")
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var branch *ledger.Artifact
			imports := 0
			for i, artifact := range artifacts {
				switch artifact.Kind {
				case ledger.ArtifactBranch:
					branch = &artifacts[i]
				case ledger.ArtifactImport:
					imports++
				}
			}
			if branch == nil || branch.URI != "lane/lane-1" || branch.Digest != tt.wantDigest {
				t.Errorf("the branch artifact is %+v, want its digest %q", branch, tt.wantDigest)
			}
			if (imports == 1) != tt.wantImport {
				t.Errorf("%d import artifacts, want one: %v", imports, tt.wantImport)
			}
		})
	}
}

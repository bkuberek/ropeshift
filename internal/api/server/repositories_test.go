package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

const aRepository = "3f2a1b0c4d5e6f708192a3b4c5d6e7f8091a2b3c"

func detected(t *testing.T, d *apitest.Daemon, id, branch string) {
	t.Helper()
	_, err := control.NewRepositories(d.Store).Detect(t.Context(), apitest.Scope,
		control.DetectedRepository{ID: id, Remote: "https://example.invalid/acme/app.git", DefaultBranch: branch}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAMemberReadsAndChangesARepository(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	list, err := d.Client.Repositories(t.Context())
	if err != nil || len(list.Repositories) != 1 {
		t.Fatalf("list %+v: %v", list, err)
	}
	if got := list.Repositories[0]; got.ID != aRepository || got.Base != "main" || got.MergeMethod != ledger.MergeCommit || got.StartFrom != ledger.StartFromMerged || got.Integration {
		t.Errorf("the record reads %+v", got)
	}
	base, squash, on := "release/1.2", ledger.MergeSquash, true
	// A name that is a prefix of the identity is enough.
	changed, err := d.Client.SetRepository(t.Context(), aRepository[:8], api.RepositoryChange{Base: &base, MergeMethod: &squash, Integration: &on})
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID != aRepository || changed.Base != "release/1.2" || changed.BaseBranch != "release/1.2" || changed.MergeMethod != ledger.MergeSquash || !changed.Integration {
		t.Errorf("the change left %+v", changed)
	}
	shown, err := d.Client.Repository(t.Context(), aRepository)
	if err != nil || shown != changed {
		t.Errorf("show reads %+v: %v", shown, err)
	}
	// Empty follows the default branch again.
	empty := ""
	if back, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{Base: &empty}); err != nil || back.Base != "main" || back.BaseBranch != "" {
		t.Errorf("clearing the base left %+v: %v", back, err)
	}
}

func TestARepositoryRefusesWhatCannotHold(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	accepted, squash, bad := ledger.StartFromAccepted, ledger.MergeSquash, ledger.MergeMethod("fast-forward")
	if _, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{MergeMethod: &squash}); err != nil {
		t.Fatal(err)
	}
	_, err := d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{StartFrom: &accepted})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	_, err = d.Client.SetRepository(t.Context(), aRepository, api.RepositoryChange{MergeMethod: &bad})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	_, err = d.Client.SetRepository(t.Context(), "0000000", api.RepositoryChange{MergeMethod: &squash})
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	if _, err := d.Client.SetRepository(t.Context(), "3f2a1b0", api.RepositoryChange{}); err != nil {
		t.Errorf("an unambiguous prefix: %v", err)
	}
	detected(t, d, "3f2a1b0ffffffffffffffffffffffffffffffffffff", "main")
	_, err = d.Client.SetRepository(t.Context(), "3f2a1b0", api.RepositoryChange{})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}

// Reaching the socket proves nothing, because a lane runs as the same user. Only a member's session
// changes how much a repository may merge or where work starts.
func TestOnlyAMemberChangesARepository(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, aRepository, "main")
	credential, _ := joinWorker(t, d)
	on := true
	cases := []struct {
		name  string
		setup func(*api.Client) *api.Client
	}{
		{"a caller with nothing", func(c *api.Client) *api.Client { return c }},
		{"the worker's credential", func(c *api.Client) *api.Client { return c.WithCredential(credential) }},
		{"a lane's token where a session goes", withSession("a-lane-token")},
	}
	for _, tt := range cases {
		plain := api.NewUnixClient(d.Socket)
		t.Cleanup(plain.Close)
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.setup(plain).SetRepository(t.Context(), aRepository, api.RepositoryChange{Integration: &on})
			refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
		})
	}
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		record, err := tx.Repositories().Repository(aRepository)
		if err == nil && record.Integration {
			t.Errorf("a refused caller changed the record: %+v", record)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestARepositoryNameNeverReadsAsAnotherIdentity refuses the name that would let a call aimed at one
// repository's identity resolve to another repository.
func TestARepositoryNameNeverReadsAsAnotherIdentity(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	ctx := t.Context()
	for _, name := range []string{aRepository, aRepository[:7], aRepository[:20]} {
		_, err := d.Client.SetRepository(ctx, anotherRepository, api.RepositoryChange{Name: &name})
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
	short, own := aRepository[:6], anotherRepository[:12]
	if _, err := d.Client.SetRepository(ctx, anotherRepository, api.RepositoryChange{Name: &short}); err != nil {
		t.Errorf("a name shorter than a prefix that names a repository: %v", err)
	}
	if _, err := d.Client.SetRepository(ctx, anotherRepository, api.RepositoryChange{Name: &own}); err != nil {
		t.Errorf("a name that is a prefix of the repository's own identity: %v", err)
	}
}

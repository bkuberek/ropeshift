package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/test/apitest"
)

// FR-REPO-8: the first version of a plan a planning branch asks about, which the ledger holds no plan for until a
// member approves it, is read by its request's own ID, and that read previews what approving registers from the
// manifest and the work the request carries from the branch, as the plan's route does, and registers nothing.
func TestABranchsFirstVersionPreviewsItsManifestOnItsOwnRoute(t *testing.T) {
	t.Parallel()
	d := planningMachine(t, apitest.Options{})
	branch := askedByBranch(t, d, "", 1)
	var detail api.ApprovalDetail
	if err := d.Client.Do(t.Context(), http.MethodGet, d.Client.Route("/v1/approvals/"+branch.ID), nil, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != branch.ID {
		t.Fatalf("the route answers %+v, want the branch's request", detail.Approval)
	}
	wantPlannedPreview(t, detail.Registration)
	if _, ok := repositoriesOf(t, d.Client)["billing"]; ok {
		t.Fatal("reading the preview registered billing")
	}
}

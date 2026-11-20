package server_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Creating work from the browser is the API's own route, behind the web listener's session and token
// (FR-UI-7). These hold what the web UI relies on: a member's session creates it, the request the New work
// order form sends makes the record the command makes, and the API refuses the malformed requests the form
// checks for, in the words the page shows.

func TestAMemberCreatesWorkFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	b.send(t, http.MethodPost, "/v1/work-orders", api.CreateWorkOrder{
		ID: "wo-first", Title: "Export the invoices", Criteria: []planning.Criterion{{ID: "c1", Text: "An export downloads."}},
	}, http.StatusCreated)
	// Every field the command takes, in the shape the form sends it.
	raw := b.send(t, http.MethodPost, "/v1/work-orders", api.CreateWorkOrder{
		ID: "wo-full", Kind: workorders.KindChange, Title: "Add the export format", Statement: "The export offers CSV.",
		Criteria: []planning.Criterion{{ID: "c1", Text: "CSV downloads."}, {ID: "c2", Text: "Quotes are escaped."}},
		Priority: 3, Size: planning.SizeSmall, Requirements: []string{"FR-EXPORT-1"}, DependsOn: []string{"wo-first"},
		RepositoryID: "repo-1", Agent: "implementation",
	}, http.StatusCreated)
	var created workorders.WorkOrder
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "wo-full" || created.Kind != workorders.KindChange || created.Size != planning.SizeSmall || created.Priority != 3 ||
		created.RepositoryID != "repo-1" || created.Agent != "implementation" || !slices.Equal(created.Requirements, []string{"FR-EXPORT-1"}) {
		t.Fatalf("created %+v", created)
	}
	read, err := b.r.d.Client.WorkOrder(t.Context(), "wo-full")
	if err != nil || read.Title != "Add the export format" {
		t.Fatalf("the work order reads %+v: %v", read, err)
	}
	// A repeat under one ID is the record the first made, so a page may send it again after a lost reply.
	b.send(t, http.MethodPost, "/v1/work-orders", api.CreateWorkOrder{ID: "wo-full", Title: "Add the export format"}, http.StatusCreated)
	list, err := b.r.d.Client.WorkOrders(t.Context(), "")
	if err != nil || len(list) != 2 {
		t.Fatalf("the project holds %+v: %v", list, err)
	}
}

func TestTheLedgerRefusesWorkTheFormWouldNotSend(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	for name, request := range map[string]api.CreateWorkOrder{
		"no title":                  {ID: "wo-1"},
		"an ID with a space":        {ID: "wo 1", Title: "Export"},
		"an ID that is too long":    {ID: strings.Repeat("x", 65), Title: "Export"},
		"a size it does not know":   {ID: "wo-2", Title: "Export", Size: "huge"},
		"a requirement key":         {ID: "wo-3", Title: "Export", Requirements: []string{"FR EXPORT"}},
		"a repository with a space": {ID: "wo-4", Title: "Export", RepositoryID: "my repo"},
		"a kind of review":          {ID: "wo-5", Title: "Export", Kind: workorders.KindContractReview},
	} {
		t.Run(name, func(t *testing.T) {
			wantCode(t, b.send(t, http.MethodPost, "/v1/work-orders", request, http.StatusBadRequest), api.CodeInvalid)
		})
	}
	list, err := b.r.d.Client.WorkOrders(t.Context(), "")
	if err != nil || len(list) != 0 {
		t.Fatalf("a refused request left %+v: %v", list, err)
	}
}

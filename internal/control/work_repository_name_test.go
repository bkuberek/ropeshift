package control_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Work names its repository by ID, name, or a unique prefix of its ID, as source list shows them, and the work order
// keeps the record's ID, so every later read of the order finds the repository.
func TestWorkIsRecordedUnderTheIDOfTheRepositoryItNames(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	widgets := r.register("repo-widgets-0001", "widgets")
	r.register("repo-gadgets-0002", "gadgets")
	for _, tt := range []struct{ name, id, named string }{
		{"by its ID", "wo-by-id", widgets.ID}, {"by its name", "wo-by-name", "widgets"}, {"by a prefix of its ID", "wo-by-prefix", "repo-widg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id, named := tt.id, tt.named
			order, err := control.NewWorkOrders(r.store).Record(t.Context(), "pa", control.LedgerWork{
				Order:    workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "T", RepositoryID: named},
				Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
			}, projectMember)
			if err != nil {
				t.Fatal(err)
			}
			if order.RepositoryID != widgets.ID {
				t.Errorf("the order names repository %q, want %q", order.RepositoryID, widgets.ID)
			}
		})
	}
}

// A name a removed repository answers to is refused, and what no repository answers to stays as written, because work may
// be recorded ahead of its repository.
func TestWorkNamingARemovedRepositoryIsRefusedAndAnUnknownIDStays(t *testing.T) {
	r := newPracticesRig(t, ledger.ProtocolVersion)
	r.register("repo-widgets-0001", "widgets")
	gone := r.register("repo-gizmos-0002", "gizmos")
	if _, err := control.NewRepositories(r.store).Remove(t.Context(), "pa", gone.ID, nil, projectMember); err != nil {
		t.Fatal(err)
	}
	record := func(id, repository string) (workorders.WorkOrder, error) {
		return control.NewWorkOrders(r.store).Record(t.Context(), "pa", control.LedgerWork{
			Order:    workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "T", RepositoryID: repository},
			Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
		}, projectMember)
	}
	_, err := record("wo-removed", "gizmos")
	if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), `"gizmos" was removed`) {
		t.Errorf("a removed repository's name: %v", err)
	}
	order, err := record("wo-ahead", "repo-later")
	if err != nil || order.RepositoryID != "repo-later" {
		t.Errorf("an ID no repository holds yet: %+v, %v", order, err)
	}
}

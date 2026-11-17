package control

import (
	"maps"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// titleLedger holds records of each kind a scope names, and counts every read of them by the method that
// read. Any other read panics.
type titleLedger struct {
	plans        []planning.Plan
	contracts    []planning.Contract
	orders       []workorders.WorkOrder
	repositories []ledger.Repository
	reads        map[string]int
}

type titlePlanned struct {
	ports.PlanningReader
	l *titleLedger
}

func (p titlePlanned) Plan(id string) (planning.Plan, error) {
	p.l.reads["Plan"]++
	for _, plan := range p.l.plans {
		if plan.ID == id {
			return plan, nil
		}
	}
	return planning.Plan{}, ports.ErrNotFound
}

func (p titlePlanned) Plans() ([]planning.Plan, error) {
	p.l.reads["Plans"]++
	return p.l.plans, nil
}

func (p titlePlanned) LatestContract(id string) (planning.Contract, error) {
	p.l.reads["LatestContract"]++
	var newest planning.Contract
	for _, contract := range p.l.contracts {
		if contract.ID == id && contract.Version > newest.Version {
			newest = contract
		}
	}
	if newest.ID == "" {
		return planning.Contract{}, ports.ErrNotFound
	}
	return newest, nil
}

func (p titlePlanned) Contracts() ([]planning.Contract, error) {
	p.l.reads["Contracts"]++
	return p.l.contracts, nil
}

type titleOrders struct {
	ports.WorkOrderReader
	l *titleLedger
}

func (o titleOrders) Get(id string) (workorders.WorkOrder, error) {
	o.l.reads["Get"]++
	for _, order := range o.l.orders {
		if order.ID == id {
			return order, nil
		}
	}
	return workorders.WorkOrder{}, ports.ErrNotFound
}

func (o titleOrders) List() ([]workorders.WorkOrder, error) {
	o.l.reads["List"]++
	return o.l.orders, nil
}

type titleRepositories struct {
	ports.RepositoryReader
	l *titleLedger
}

func (r titleRepositories) Repository(id string) (ledger.Repository, error) {
	r.l.reads["Repository"]++
	for _, repository := range r.l.repositories {
		if repository.ID == id {
			return repository, nil
		}
	}
	return ledger.Repository{}, ports.ErrNotFound
}

func (r titleRepositories) Repositories() ([]ledger.Repository, error) {
	r.l.reads["Repositories"]++
	return r.l.repositories, nil
}

// newTitleLedger holds two plans, a contract with two versions and one with one, two work orders, and three
// repositories: one with a name, one whose name is its identity, and one with none.
func newTitleLedger() (*titleLedger, scopeRecords) {
	l := &titleLedger{
		plans: []planning.Plan{{ID: "p1", Title: "Checkout retries"}, {ID: "p2", Title: "Audit trail"}},
		contracts: []planning.Contract{
			{ID: "c1", Version: 2, Title: "Retry budget"}, {ID: "c1", Version: 1, Title: "Retry once"}, {ID: "c2", Version: 1, Title: "Audit log"},
		},
		orders:       []workorders.WorkOrder{{ID: "w1", Title: "Trim the log"}, {ID: "w2", Title: "Fix the flaky test"}},
		repositories: []ledger.Repository{{ID: "r1", Name: "checkout"}, {ID: "r2", Name: "r2"}, {ID: "r3"}},
		reads:        map[string]int{},
	}
	return l, scopeRecords{planned: titlePlanned{l: l}, orders: titleOrders{l: l}, repositories: titleRepositories{l: l}}
}

func TestAScopeIsTitledByTheRecordItNames(t *testing.T) {
	want := map[string]string{
		"plan:p1": "Checkout retries", "plan:p2": "Audit trail",
		"contract:c1": "Retry budget", "contract:c2": "Audit log",
		"wo:w1": "Trim the log", "wo:w2": "Fix the flaky test",
		"repo:r1": "checkout",
	}
	untitled := []string{"repo:r2", "repo:r3", "plan:gone", "wo:gone", "payments/retries", "plan:", ""}
	for _, alone := range []bool{false, true} {
		var lists [][]string
		if alone {
			// One scope at a time reads the one record it names.
			for scope := range want {
				lists = append(lists, []string{scope})
			}
			for _, scope := range untitled {
				lists = append(lists, []string{scope})
			}
		} else {
			lists = [][]string{append(append(slices.Sorted(maps.Keys(want)), untitled...), slices.Sorted(maps.Keys(want))...)}
		}
		got := map[string]string{}
		for _, scopes := range lists {
			_, records := newTitleLedger()
			titles, err := records.titles(scopes)
			if err != nil {
				t.Fatal(err)
			}
			maps.Copy(got, titles)
		}
		if !maps.Equal(got, want) {
			t.Errorf("one at a time %v: titles %v, want %v", alone, got, want)
		}
	}
}

// TestTitlesAreReadOnceForEachKindOfScope holds that a list of any length reads once for each kind of scope it
// holds: every record of a kind its several IDs name, or the one record a kind's only ID names.
func TestTitlesAreReadOnceForEachKindOfScope(t *testing.T) {
	var many []string
	for range 20 {
		many = append(many, "plan:p1", "plan:p2", "contract:c1", "contract:c2", "wo:w1", "wo:w2", "repo:r1", "repo:r3", "plan:gone")
	}
	l, records := newTitleLedger()
	titles, err := records.titles(many)
	if err != nil {
		t.Fatal(err)
	}
	if len(titles) != 7 {
		t.Errorf("titles %v", titles)
	}
	if want := map[string]int{"Plans": 1, "Contracts": 1, "List": 1, "Repositories": 1}; !maps.Equal(l.reads, want) {
		t.Errorf("%d scopes of four kinds read %v, want %v", len(many), l.reads, want)
	}

	l, records = newTitleLedger()
	if _, err := records.titles([]string{"plan:p1", "plan:p1", "wo:w2", "repo:r1", "contract:c1", "wo:w2"}); err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"Plan": 1, "LatestContract": 1, "Get": 1, "Repository": 1}; !maps.Equal(l.reads, want) {
		t.Errorf("one ID of each kind read %v, want %v", l.reads, want)
	}
}

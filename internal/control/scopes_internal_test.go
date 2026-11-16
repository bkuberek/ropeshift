package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// scopeLedger is the ledger scopes resolve through: two plans, three contracts, and their work, with one
// rework. Any other read panics.
type scopeLedger struct {
	contracts map[string]planning.Contract
	orders    map[string]workorders.WorkOrder
}

type scopePlanned struct {
	ports.PlanningReader
	ledger scopeLedger
}

func (s scopePlanned) LatestContract(id string) (planning.Contract, error) {
	if contract, ok := s.ledger.contracts[id]; ok {
		return contract, nil
	}
	return planning.Contract{}, ports.ErrNotFound
}

type scopeOrders struct {
	ports.WorkOrderReader
	ledger scopeLedger
}

func (s scopeOrders) Get(id string) (workorders.WorkOrder, error) {
	if order, ok := s.ledger.orders[id]; ok {
		return order, nil
	}
	return workorders.WorkOrder{}, ports.ErrNotFound
}

func (s scopeOrders) List() ([]workorders.WorkOrder, error) {
	var out []workorders.WorkOrder
	for _, order := range s.ledger.orders {
		out = append(out, order)
	}
	return out, nil
}

func (s scopeOrders) ByContract(contract string) ([]workorders.WorkOrder, error) {
	var out []workorders.WorkOrder
	for _, order := range s.ledger.orders {
		if order.ContractID == contract {
			out = append(out, order)
		}
	}
	return out, nil
}

func newScopeLedger() (scopeLedger, *scopeFacts) {
	l := scopeLedger{
		contracts: map[string]planning.Contract{
			"c1": {ID: "c1", PlanID: "p1"}, "c2": {ID: "c2", PlanID: "p1"}, "c3": {ID: "c3", PlanID: "p2"},
		},
		orders: map[string]workorders.WorkOrder{
			"w1":        {ID: "w1", ContractID: "c1", RepositoryID: "r1"},
			"w2":        {ID: "w2", ContractID: "c2", RepositoryID: "r2"},
			"w3":        {ID: "w3", ContractID: "c3", RepositoryID: "r1"},
			"w1-rework": {ID: "w1-rework", ContractID: "c1", RepositoryID: "r1", ReworkOf: "w1"},
			"planning":  {ID: "planning", Kind: workorders.KindPlan},
		},
	}
	return l, newScopeFacts(scopePlanned{ledger: l}, scopeOrders{ledger: l})
}

func TestAScopeBindsTheWorkItNames(t *testing.T) {
	l, facts := newScopeLedger()
	tests := []struct {
		scope string
		binds []string
	}{
		{"plan:p1", []string{"w1", "w2", "w1-rework"}},
		{"plan:p2", []string{"w3"}},
		{"contract:c1", []string{"w1", "w1-rework"}},
		{"contract:c3", []string{"w3"}},
		{"wo:w1", []string{"w1", "w1-rework"}},
		{"wo:w1-rework", []string{"w1-rework"}},
		{"repo:r1", []string{"w1", "w3", "w1-rework"}},
		{"repo:r2", []string{"w2"}},
		{"plan:nothing", nil},
		{"wo:w1/x", nil},
		{"p1", nil},
		{"payments/retries", nil},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			var bound []string
			for id, order := range l.orders {
				covered, err := facts.covers(tt.scope, order)
				if err != nil {
					t.Fatal(err)
				}
				if covered {
					bound = append(bound, id)
				}
			}
			if !sameSet(bound, tt.binds) {
				t.Errorf("%q binds %v, want %v", tt.scope, bound, tt.binds)
			}
		})
	}
}

// TestAScopeIsResolvedThroughTheLedgerAndNotFromItsText holds ADR-0030: an ID that is also the ID of a record
// of another kind binds only what its kind says, and the same text under two kinds is two scopes.
func TestAScopeIsResolvedThroughTheLedgerAndNotFromItsText(t *testing.T) {
	l, facts := newScopeLedger()
	l.orders["p1"] = workorders.WorkOrder{ID: "p1", ContractID: "c3", RepositoryID: "r2"}
	for scope, want := range map[string]bool{"wo:p1": true, "plan:p1": false, "contract:p1": false, "repo:p1": false} {
		covered, err := facts.covers(scope, l.orders["p1"])
		if err != nil || covered != want {
			t.Errorf("%q binds the work order p1: %v, %v, want %v", scope, covered, err, want)
		}
	}
}

func TestTwoScopesOverlap(t *testing.T) {
	_, facts := newScopeLedger()
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"equal", "plan:p1", "plan:p1", true},
		{"two plans", "plan:p1", "plan:p2", false},
		{"a plan and its contract", "plan:p1", "contract:c1", true},
		{"a contract and its plan", "contract:c2", "plan:p1", true},
		{"a plan and another plan's contract", "plan:p1", "contract:c3", false},
		{"a plan and a work order under it", "plan:p1", "wo:w2", true},
		{"a plan and a work order elsewhere", "plan:p1", "wo:w3", false},
		{"a plan and the rework under it", "wo:w1-rework", "plan:p1", true},
		{"a contract and its work order", "contract:c1", "wo:w1", true},
		{"a contract and another's work order", "contract:c1", "wo:w2", false},
		{"two contracts", "contract:c1", "contract:c2", false},
		{"a work order and its rework", "wo:w1", "wo:w1-rework", true},
		{"a rework and its work order", "wo:w1-rework", "wo:w1", true},
		{"two work orders", "wo:w1", "wo:w2", false},
		{"a repository and a work order in it", "repo:r1", "wo:w3", true},
		{"a work order and a repository", "wo:w2", "repo:r1", false},
		{"a repository and a contract with work in it", "contract:c1", "repo:r1", true},
		{"a repository and a contract with none", "contract:c2", "repo:r1", false},
		{"a repository and a plan with work in it", "repo:r2", "plan:p1", true},
		{"a repository and a plan with none", "repo:r2", "plan:p2", false},
		{"two repositories", "repo:r1", "repo:r2", false},
		{"a work order nothing holds", "wo:ghost", "plan:p1", false},
		{"an empty scope", "", "plan:p1", false},
		{"two empty scopes", "", "", false},
		{"two legacy scopes, the same", "payments", "payments", true},
		{"two legacy scopes, one inside the other", "payments/retries", "payments", true},
		{"two legacy scopes, apart", "payments", "billing", false},
		{"two legacy scopes, a shared prefix of text", "payments-eu", "payments", false},
		{"two legacy scopes, siblings", "checkout/retry", "checkout/refund", false},
		{"two legacy scopes, one inside the other from the other side", "checkout/retry", "checkout", true},
		{"a legacy scope and an empty one", "checkout", "", false},
		{"a legacy scope and a typed one", "p1", "plan:p1", false},
		{"a typed scope and a legacy one", "wo:w1", "w1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := facts.overlaps(tt.a, tt.b)
			if err != nil || got != tt.want {
				t.Errorf("overlaps(%q, %q) = %v, %v, want %v", tt.a, tt.b, got, err, tt.want)
			}
			back, err := facts.overlaps(tt.b, tt.a)
			if err != nil || back != got {
				t.Errorf("overlaps(%q, %q) = %v, %v, but the reverse is %v", tt.b, tt.a, back, err, got)
			}
		})
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

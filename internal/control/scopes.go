package control

import (
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// legacySeparator divides a scope written before the grammar into the parts a narrower scope extends.
const legacySeparator = "/"

// scopeFacts resolves a scope through the ledger: from a work order to its contract, and from a contract
// to its plan, never from the text of an ID (ADR-0030). It reads a plan of a contract once.
type scopeFacts struct {
	planned ports.PlanningReader
	orders  ports.WorkOrderReader
	plans   map[string]string
}

func newScopeFacts(planned ports.PlanningReader, orders ports.WorkOrderReader) *scopeFacts {
	return &scopeFacts{planned: planned, orders: orders, plans: map[string]string{}}
}

// planOf is the plan a contract belongs to, or none for a contract the ledger does not hold.
func (f *scopeFacts) planOf(contract string) (string, error) {
	if contract == "" {
		return "", nil
	}
	if plan, read := f.plans[contract]; read {
		return plan, nil
	}
	latest, err := f.planned.LatestContract(contract)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return "", err
	}
	f.plans[contract] = latest.PlanID
	return latest.PlanID, nil
}

// order reads a work order, and says whether the ledger holds it.
func (f *scopeFacts) order(id string) (workorders.WorkOrder, bool, error) {
	order, err := f.orders.Get(id)
	if errors.Is(err, ports.ErrNotFound) {
		return workorders.WorkOrder{}, false, nil
	}
	return order, err == nil, err
}

// binds says whether a scope covers a work order: a plan covers the work of its contracts, a contract the
// work that pins it, a work order itself and its rework, and a repository the work that runs in it. A scope
// that binds nothing, such as one written before the grammar, covers no work.
func (f *scopeFacts) binds(scope questions.Scope, order workorders.WorkOrder) (bool, error) {
	switch scope.Kind {
	case questions.ScopePlan:
		if bindsNoPlan() {
			return false, nil
		}
		plan, err := f.planOf(order.ContractID)
		return plan == scope.ID, err
	case questions.ScopeContract:
		return order.ContractID == scope.ID || bindsEveryContract(), nil
	case questions.ScopeWorkOrder:
		return order.ID == scope.ID || order.ReworkOf == scope.ID, nil
	case questions.ScopeRepository:
		return order.RepositoryID == scope.ID, nil
	}
	return false, nil
}

// overlaps says whether two scopes cover some of the same thing: the same scope, a plan and a contract of
// it, a plan or a contract and a work order under it, a work order and its rework, and a repository and a
// work order, a contract, or a plan with work in it. Two scopes written before the grammar keep the rule
// they had, equal or one inside the other by `/`, and one of them with a typed scope never overlaps. An
// empty scope covers nothing.
func (f *scopeFacts) overlaps(a, b string) (bool, error) {
	if a == "" || b == "" {
		return false, nil
	}
	first, errA := questions.ParseScope(a)
	second, errB := questions.ParseScope(b)
	switch {
	case errA != nil && errB != nil:
		return legacyOverlap(a, b), nil
	case errA != nil || errB != nil:
		return false, nil // a scope written before the grammar never meets a typed one
	case first == second:
		return true, nil
	}
	if rank(first.Kind) > rank(second.Kind) && !overlapsOneSide() {
		first, second = second, first
	}
	return f.within(first, second)
}

// rank orders the kinds of scope from the widest, so a pair is read once: a plan holds contracts, a contract
// holds work orders, and a repository is read last, against each.
func rank(kind questions.ScopeKind) int {
	return slices.Index([]questions.ScopeKind{questions.ScopePlan, questions.ScopeContract, questions.ScopeWorkOrder, questions.ScopeRepository}, kind)
}

// within says whether two different typed scopes overlap, the first of the kind of lower rank.
func (f *scopeFacts) within(a, b questions.Scope) (bool, error) {
	if b.Kind == questions.ScopeRepository {
		return f.runsIn(a, b.ID)
	}
	switch {
	case a.Kind == questions.ScopePlan && b.Kind == questions.ScopeContract:
		plan, err := f.planOf(b.ID)
		return plan == a.ID, err
	case a.Kind == questions.ScopePlan && b.Kind == questions.ScopeWorkOrder:
		order, held, err := f.order(b.ID)
		if err != nil || !held {
			return false, err
		}
		return f.binds(a, order)
	case a.Kind == questions.ScopeContract && b.Kind == questions.ScopeWorkOrder:
		order, held, err := f.order(b.ID)
		return held && order.ContractID == a.ID, err
	case a.Kind == questions.ScopeWorkOrder && b.Kind == questions.ScopeWorkOrder:
		return f.reworks(a.ID, b.ID)
	}
	return false, nil
}

// reworks says whether one work order is the rework of the other.
func (f *scopeFacts) reworks(a, b string) (bool, error) {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		order, held, err := f.order(pair[0])
		if err != nil {
			return false, err
		}
		if held && order.ReworkOf == pair[1] {
			return true, nil
		}
	}
	return false, nil
}

// runsIn says whether the work a scope covers runs in a repository.
func (f *scopeFacts) runsIn(scope questions.Scope, repository string) (bool, error) {
	var orders []workorders.WorkOrder
	switch scope.Kind {
	case questions.ScopeWorkOrder:
		order, held, err := f.order(scope.ID)
		if held {
			orders = []workorders.WorkOrder{order}
		}
		if err != nil {
			return false, err
		}
	case questions.ScopeContract:
		var err error
		if orders, err = f.orders.ByContract(scope.ID); err != nil {
			return false, err
		}
	case questions.ScopePlan:
		var err error
		if orders, err = f.orders.List(); err != nil {
			return false, err
		}
	}
	for _, order := range orders {
		if order.RepositoryID != repository {
			continue
		}
		if covered, err := f.binds(scope, order); err != nil || covered {
			return covered, err
		}
	}
	return false, nil
}

// covers says whether a decision's scope reaches a work order: by the grammar, or, for a scope written
// before it, by the rule that scope had, which compares it with the IDs of the work order, its contract, and
// its repository.
func (f *scopeFacts) covers(scope string, order workorders.WorkOrder) (bool, error) {
	if scope == "" {
		return false, nil
	}
	typed, err := questions.ParseScope(scope)
	if err != nil {
		return legacyOverlap(scope, order.ID) || legacyOverlap(scope, order.ContractID) || legacyOverlap(scope, order.RepositoryID), nil
	}
	return f.binds(typed, order)
}

// legacyOverlap is the rule of a scope written before the grammar: the same scope, or one inside the
// other, divided by `/`. An empty scope covers nothing.
func legacyOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+legacySeparator) || strings.HasPrefix(b, a+legacySeparator)
}

// decisionsInForce lists every decision that stands, by ID. It stops at no page, because a recording
// decision states how it stands to each overlapping one, and a pack selects from them.
func decisionsInForce(reader ports.QuestionReader) ([]questions.Decision, error) {
	var all []questions.Decision
	for _, status := range []questions.DecisionStatus{questions.DecisionAssumed, questions.DecisionDecided} {
		after := ""
		for {
			page, err := reader.Decisions(ports.RecordFilter{Status: string(status), Limit: ports.MaxRecords, After: after})
			if err != nil {
				return nil, err
			}
			all = append(all, page...)
			if len(page) < ports.MaxRecords || stopsAtOnePage() {
				break
			}
			after = page[len(page)-1].ID
		}
	}
	slices.SortFunc(all, func(a, b questions.Decision) int { return strings.Compare(a.ID, b.ID) })
	return all, nil
}

// overlappingDecisions lists the decisions in force whose scope overlaps one, by ID.
func overlappingDecisions(reader ports.QuestionReader, facts *scopeFacts, scope string) ([]questions.Decision, error) {
	if scope == "" {
		return nil, nil
	}
	standing, err := decisionsInForce(reader)
	if err != nil {
		return nil, err
	}
	var found []questions.Decision
	for _, decision := range standing {
		overlap, err := facts.overlaps(decision.Scope, scope)
		if err != nil {
			return nil, err
		}
		if overlap {
			found = append(found, decision)
		}
	}
	return found, nil
}

// DecisionsOverlapping lists the decisions in force whose scope overlaps one, by ID, resolved through the
// ledger the read holds.
func DecisionsOverlapping(tx ports.ReadTx, scope string) ([]questions.Decision, error) {
	return overlappingDecisions(tx.Questions(), newScopeFacts(tx.Planning(), tx.WorkOrders()), scope)
}

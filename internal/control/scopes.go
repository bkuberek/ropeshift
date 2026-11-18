package control

import (
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// legacySeparator divides a scope written before the grammar into the parts a narrower scope extends.
const legacySeparator = "/"

// scopeFacts resolves a scope through the ledger: from a work order to the contract version it is pinned to,
// and from a contract version to its plan, never from the text of an ID (ADR-0030). It reads the plan of a
// contract version once.
type scopeFacts struct {
	planned ports.PlanningReader
	orders  ports.WorkOrderReader
	plans   map[contractVersion]string
}

// contractVersion names one version of a contract, and version 0 its latest.
type contractVersion struct {
	id      string
	version int64
}

func newScopeFacts(planned ports.PlanningReader, orders ports.WorkOrderReader) *scopeFacts {
	return &scopeFacts{planned: planned, orders: orders, plans: map[contractVersion]string{}}
}

// planOf is the plan a version of a contract belongs to, or its latest version's for version 0, or none for a
// version the ledger does not hold. A contract can move to another plan, so the work pinned to an earlier
// version stays under the plan that version names.
func (f *scopeFacts) planOf(contract string, version int64) (string, error) {
	if contract == "" {
		return "", nil
	}
	key := contractVersion{contract, version}
	if plan, read := f.plans[key]; read {
		return plan, nil
	}
	var held planning.Contract
	var err error
	if version == 0 {
		held, err = f.planned.LatestContract(contract)
	} else {
		held, err = f.planned.Contract(contract, version)
	}
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return "", err
	}
	f.plans[key] = held.PlanID
	return held.PlanID, nil
}

// order reads a work order, and says whether the ledger holds it.
func (f *scopeFacts) order(id string) (workorders.WorkOrder, bool, error) {
	order, err := f.orders.Get(id)
	if errors.Is(err, ports.ErrNotFound) {
		return workorders.WorkOrder{}, false, nil
	}
	return order, err == nil, err
}

// binds says whether a scope covers a work order: a plan covers the work pinned to versions of contracts it
// holds, a contract the work that pins it, a work order itself and its rework, and a repository the work that runs in it. A scope
// written before the grammar is not one it reads: `covers` reads that scope by the rule it had.
func (f *scopeFacts) binds(scope questions.Scope, order workorders.WorkOrder) (bool, error) {
	switch scope.Kind {
	case questions.ScopePlan:
		if bindsNoPlan() {
			return false, nil
		}
		plan, err := f.planOf(order.ContractID, order.ContractVersion)
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
		plan, err := f.planOf(b.ID, 0)
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

// runsIn says whether the work a scope covers runs in a repository. A work order covers its rework, which is
// under the same contract, so the work of that contract holds both.
func (f *scopeFacts) runsIn(scope questions.Scope, repository string) (bool, error) {
	var orders []workorders.WorkOrder
	switch scope.Kind {
	case questions.ScopeWorkOrder:
		order, held, err := f.order(scope.ID)
		if err != nil || !held {
			return false, err
		}
		if order.ContractID == "" {
			orders, err = f.orders.List()
		} else {
			orders, err = f.orders.ByContract(order.ContractID)
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

// checkHeld refuses a scope a person or an orchestrator named when its ID names no record of its kind the ledger
// holds, because such a scope would bind nothing. A repository is named by the identity of its record, as work
// orders hold it, so a name the project does not hold names none. A scope outside the grammar is admit's to
// refuse. A scope the control plane builds from a question's subject is not held to this: the question named
// that record, and an answer to it is never refused for it (ScopeOf).
func checkHeld(tx ports.WriteTx, text string) error {
	scope, err := questions.ParseScope(text)
	if err != nil || scope.Zero() {
		return nil
	}
	_, err = scopeRecords{tx.Planning(), tx.WorkOrders(), tx.Repositories()}.titleOf(scope)
	if errors.Is(err, ports.ErrNotFound) {
		return scope.Unheld()
	}
	return err
}

// ScopeTitles names the record each scope names by its title, which a list or a read of decisions and
// questions shows beside the scope: a plan, a contract's newest version, and a work order by their titles, and
// a repository by its name. It reads once for each kind of scope the scopes hold, whatever their number: the
// one record a kind's only ID names, or the records of a kind several IDs name, by their IDs, and never every
// plan, contract, or work order. A scope outside the
// grammar, one whose record the ledger does not hold, and one whose title only repeats its ID have no title, so
// a display shows the ID alone.
func ScopeTitles(tx ports.ReadTx, scopes []string) (map[string]string, error) {
	return scopeRecords{tx.Planning(), tx.WorkOrders(), tx.Repositories()}.titles(scopes)
}

// scopeTitler names scopes for one reader by the title of the record each names, read once for each kind of scope
// (ScopeTitles). An orchestrator reads a title a delegate wrote as the delegate's text, as a wake states such a
// title, except inside a record a delegate made, whose whole text it reads as the delegate's (orchestratorText).
type scopeTitler struct {
	tx           ports.ReadTx
	orchestrator bool
}

// scopedRecord is a record a read found and the scope it names. Whole is the kind of a record an orchestrator
// reads as one text, which reads whole as a delegate's when a delegate made it. A reply that states a record field
// by field leaves it empty, so a title a delegate wrote is marked in it whoever made the record.
type scopedRecord struct {
	whole, id, scope string
}

// titles says the title each record's scope reads with, in order, and an empty title for a scope with none.
func (s scopeTitler) titles(found []scopedRecord) ([]string, error) {
	scopes := make([]string, len(found))
	for i, record := range found {
		scopes[i] = record.scope
	}
	titles, err := ScopeTitles(s.tx, scopes)
	if err != nil {
		return nil, err
	}
	byDelegate := map[string]bool{}
	out := make([]string, len(found))
	for i, record := range found {
		out[i] = titles[record.scope]
		if out[i] == "" || !s.orchestrator {
			continue
		}
		marked, read := byDelegate[record.scope]
		if !read {
			if marked, err = scopeTitleByDelegate(s.tx, record.scope); err != nil {
				return nil, err
			}
			byDelegate[record.scope] = marked
		}
		whole := false
		if marked && record.whole != "" {
			if whole, err = recordByDelegate(s.tx, record.whole, record.id); err != nil {
				return nil, err
			}
		}
		if marked && !whole {
			out[i] = lanes.DelegateText(out[i])
		}
	}
	return out, nil
}

// scopeTitleByDelegate says a delegate wrote the title of the record a scope names: a work order's title as a wake
// reads it (titleByDelegate), a contract or a plan a delegate made, or a repository's name a delegate gave when it
// registered the repository or changed its settings.
func scopeTitleByDelegate(tx ports.ReadTx, text string) (bool, error) {
	scope, err := questions.ParseScope(text)
	if err != nil {
		return false, nil
	}
	switch scope.Kind {
	case questions.ScopeWorkOrder:
		order, err := tx.WorkOrders().Get(scope.ID)
		if errors.Is(err, ports.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return titleByDelegate(tx, order)
	case questions.ScopeContract:
		return recordByDelegate(tx, questions.KindContract, scope.ID)
	case questions.ScopePlan:
		return madeByDelegate(tx, ledger.Subject{Kind: planning.SubjectPlan, ID: scope.ID}, planning.EventPlanRecorded)
	case questions.ScopeRepository:
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectRepository, ID: scope.ID})
		for _, event := range events {
			named := event.Type == ledger.EventRepositoryRegistered || event.Type == ledger.EventRepositoryChanged
			if named && (authority.PartyOf(event.Actor) == authority.PartyDelegate || authority.PartyOf(event.Proposer) == authority.PartyDelegate) {
				return true, nil
			}
		}
		return false, err
	}
	return false, nil
}

// scopeRecords reads the records a scope can name, by the same reads in a read and in a write.
type scopeRecords struct {
	planned      ports.PlanningReader
	orders       ports.WorkOrderReader
	repositories ports.RepositoryReader
}

// titleOf reads the one record a scope names, and says its title, or ErrNotFound when the ledger holds no
// record of that kind by that ID.
func (r scopeRecords) titleOf(scope questions.Scope) (string, error) {
	switch scope.Kind {
	case questions.ScopePlan:
		plan, err := r.planned.Plan(scope.ID)
		return plan.Title, err
	case questions.ScopeContract:
		contract, err := r.planned.LatestContract(scope.ID)
		return contract.Title, err
	case questions.ScopeWorkOrder:
		order, err := r.orders.Get(scope.ID)
		return order.Title, err
	case questions.ScopeRepository:
		repository, err := r.repositories.Repository(scope.ID)
		return repository.Name, err
	}
	return "", nil
}

// titlesOf reads the records of one kind some IDs name, by their IDs, and says each one's title by its ID. A
// contract is titled by its newest version. A project holds few repositories, so a read of them reads them all.
func (r scopeRecords) titlesOf(kind questions.ScopeKind, ids []string) (map[string]string, error) {
	titles := map[string]string{}
	switch kind {
	case questions.ScopePlan:
		plans, err := r.planned.PlansByIDs(ids)
		for _, plan := range plans {
			titles[plan.ID] = plan.Title
		}
		return titles, err
	case questions.ScopeContract:
		contracts, err := r.planned.LatestContracts(ids)
		for _, contract := range contracts {
			titles[contract.ID] = contract.Title
		}
		return titles, err
	case questions.ScopeWorkOrder:
		orders, err := r.orders.ByIDs(ids)
		for _, order := range orders {
			titles[order.ID] = order.Title
		}
		return titles, err
	case questions.ScopeRepository:
		repositories, err := r.repositories.Repositories()
		for _, repository := range repositories {
			titles[repository.ID] = repository.Name
		}
		return titles, err
	}
	return titles, nil
}

// titles names each typed scope by the title of the record it names, as ScopeTitles says.
func (r scopeRecords) titles(scopes []string) (map[string]string, error) {
	named := map[questions.ScopeKind][]string{}
	seen := map[questions.Scope]bool{}
	for _, text := range scopes {
		scope, err := questions.ParseScope(text)
		if err != nil || scope.Zero() || seen[scope] {
			continue
		}
		seen[scope] = true
		named[scope.Kind] = append(named[scope.Kind], scope.ID)
	}
	out := map[string]string{}
	for kind, ids := range named {
		held, err := r.heldTitles(kind, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if title := held[id]; title != "" && title != id {
				out[questions.Scope{Kind: kind, ID: id}.String()] = title
			}
		}
	}
	return out, nil
}

// heldTitles reads the titles of the records of one kind some IDs name, in one read: the one record an only
// ID names, or the records several IDs name, by their IDs.
func (r scopeRecords) heldTitles(kind questions.ScopeKind, ids []string) (map[string]string, error) {
	if len(ids) != 1 {
		return r.titlesOf(kind, ids)
	}
	title, err := r.titleOf(questions.Scope{Kind: kind, ID: ids[0]})
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	return map[string]string{ids[0]: title}, err
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

// overlapsOf lists, by ID, the decisions in force whose scope overlaps the scope an answer to the question takes,
// which are the decisions an answer to it may contradict.
func overlapsOf(tx ports.WriteTx, record questions.Question) ([]string, error) {
	found, err := overlappingDecisions(tx.Questions(), newScopeFacts(tx.Planning(), tx.WorkOrders()), questions.ScopeOf(record.SubjectKind, record.SubjectID))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, decision := range found {
		ids = append(ids, decision.ID)
	}
	return ids, nil
}

// DecisionsOverlapping lists the decisions in force whose scope overlaps one, by ID, resolved through the
// ledger the read holds.
func DecisionsOverlapping(tx ports.ReadTx, scope string) ([]questions.Decision, error) {
	return overlappingDecisions(tx.Questions(), newScopeFacts(tx.Planning(), tx.WorkOrders()), scope)
}

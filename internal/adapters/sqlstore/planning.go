package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

const (
	contractColumns    = `id, version, plan_id, title, digest, source, statement, criteria, requirements, decisions, status, milestone, shared, uses, work_orders`
	goalColumns        = `id, title, status, success_measures, statement, source`
	requirementColumns = `id, version, requirement_key, text, source, digest`
)

var (
	stmtInsertPlan = define("plan.insert",
		`INSERT INTO plans (workspace_id, id, goal_id, title, status, source)
VALUES (:workspace_id, :id, :goal_id, :title, :status, :source)`)

	stmtListPlans = define("plan.list",
		`SELECT id, goal_id, title, status, source FROM plans
 WHERE workspace_id = :workspace_id
 ORDER BY id`)

	stmtReadPlan = define("plan.read",
		`SELECT id, goal_id, title, status, source FROM plans
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtUpdatePlan = define("plan.update",
		`UPDATE plans SET goal_id = :goal_id, title = :title, status = :status, source = :source
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtSaveGoal = define("goal.save",
		`INSERT INTO goals (workspace_id, id, title, status, success_measures, statement, source)
VALUES (:workspace_id, :id, :title, :status, :success_measures, :statement, :source)
ON CONFLICT (workspace_id, id) DO UPDATE SET
  title = :title, status = :status, success_measures = :success_measures, statement = :statement, source = :source`)

	stmtReadGoal = define("goal.read",
		`SELECT `+goalColumns+` FROM goals WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListGoals = define("goal.list",
		`SELECT `+goalColumns+` FROM goals WHERE workspace_id = :workspace_id ORDER BY id`)

	stmtInsertRequirement = define("requirement.insert",
		`INSERT INTO requirements (workspace_id, id, version, requirement_key, text, source, digest)
VALUES (:workspace_id, :id, :version, :requirement_key, :text, :source, :digest)`)

	stmtLatestRequirement = define("requirement.latest",
		`SELECT `+requirementColumns+` FROM requirements
 WHERE workspace_id = :workspace_id AND requirement_key = :requirement_key
 ORDER BY version DESC
 LIMIT 1`)

	stmtListRequirements = define("requirement.list",
		`SELECT `+requirementColumns+` FROM requirements
 WHERE workspace_id = :workspace_id
 ORDER BY requirement_key, version`)

	stmtInsertContract = define("contract.insert",
		`INSERT INTO contracts
  (workspace_id, id, version, plan_id, title, digest, source, statement, criteria, requirements, decisions, status, milestone, shared, uses, work_orders)
VALUES
  (:workspace_id, :id, :version, :plan_id, :title, :digest, :source, :statement, :criteria, :requirements, :decisions, :status, :milestone, :shared, :uses, :work_orders)`)

	stmtReadContract = define("contract.read",
		`SELECT `+contractColumns+` FROM contracts
 WHERE workspace_id = :workspace_id AND id = :id AND version = :version`)

	stmtLatestContract = define("contract.latest",
		`SELECT `+contractColumns+` FROM contracts
 WHERE workspace_id = :workspace_id AND id = :id
 ORDER BY version DESC
 LIMIT 1`)

	stmtListContracts = define("contract.list",
		`SELECT `+contractColumns+` FROM contracts
 WHERE workspace_id = :workspace_id
 ORDER BY id, version`)

	stmtInsertLink = define("link.insert",
		`INSERT INTO external_links (workspace_id, record_kind, adapter, instance, native_id, record_id, display_key)
VALUES (:workspace_id, :record_kind, :adapter, :instance, :native_id, :record_id, :display_key)`)

	stmtReadLink = define("link.read",
		`SELECT record_kind, record_id, adapter, instance, native_id, display_key FROM external_links
 WHERE workspace_id = :workspace_id AND record_kind = :record_kind
   AND adapter = :adapter AND instance = :instance AND native_id = :native_id`)

	stmtListLinks = define("link.list",
		`SELECT record_kind, record_id, adapter, instance, native_id, display_key FROM external_links
 WHERE workspace_id = :workspace_id
 ORDER BY record_kind, adapter, instance, native_id`)

	stmtInsertDependency = define("dependency.insert",
		`INSERT INTO dependencies (workspace_id, work_order_id, depends_on, adapter, instance, kind)
VALUES (:workspace_id, :work_order_id, :depends_on, :adapter, :instance, :kind)`)

	stmtDeleteDependency = define("dependency.delete",
		`DELETE FROM dependencies
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND depends_on = :depends_on AND kind = :kind`)

	stmtDependenciesOf = define("dependency.of",
		`SELECT work_order_id, depends_on, adapter, instance, kind FROM dependencies
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY depends_on, kind`)

	stmtListDependencies = define("dependency.list",
		`SELECT work_order_id, depends_on, adapter, instance, kind FROM dependencies
 WHERE workspace_id = :workspace_id
 ORDER BY work_order_id, depends_on, kind`)

	stmtReadBinding = define("binding.read",
		`SELECT id, port, adapter, instance_url, cursor, health, detail, problems FROM source_bindings
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListBindings = define("binding.list",
		`SELECT id, port, adapter, instance_url, cursor, health, detail, problems FROM source_bindings
 WHERE workspace_id = :workspace_id
 ORDER BY id`)

	stmtSaveBinding = define("binding.save",
		`INSERT INTO source_bindings (workspace_id, id, port, adapter, instance_url, cursor, health, detail, problems)
VALUES (:workspace_id, :id, :port, :adapter, :instance_url, :cursor, :health, :detail, :problems)
ON CONFLICT (workspace_id, id) DO UPDATE SET
  cursor = :cursor, health = :health, detail = :detail, problems = :problems`)
)

// planningTx is the planning area of one transaction.
type planningTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t planningTx) scoped(p params) params {
	if p == nil {
		p = params{}
	}
	p["workspace_id"] = string(t.scope)
	return p
}

func (t planningTx) InsertPlan(plan planning.Plan) error {
	_, err := t.exec(stmtInsertPlan, t.scoped(params{
		"id": plan.ID, "goal_id": nullText(plan.GoalID), "title": plan.Title,
		"status": plan.Status, "source": nullText(plan.Source),
	}))
	return err
}

func scanPlan(row scanner) (planning.Plan, error) {
	var plan planning.Plan
	var goal, source sql.NullString
	if err := row.Scan(&plan.ID, &goal, &plan.Title, &plan.Status, &source); err != nil {
		return planning.Plan{}, err
	}
	plan.GoalID, plan.Source = goal.String, source.String
	return plan, nil
}

func (t planningTx) Plans() ([]planning.Plan, error) {
	rows, err := t.rows(stmtListPlans, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.Plan
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func (t planningTx) Plan(id string) (planning.Plan, error) {
	row, err := t.row(stmtReadPlan, t.scoped(params{"id": id}))
	if err != nil {
		return planning.Plan{}, err
	}
	plan, err := scanPlan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.Plan{}, fmt.Errorf("plan %s: %w", id, ports.ErrNotFound)
	}
	return plan, err
}

func (t planningTx) UpdatePlan(plan planning.Plan) error {
	_, err := t.exec(stmtUpdatePlan, t.scoped(params{
		"id": plan.ID, "goal_id": nullText(plan.GoalID), "title": plan.Title,
		"status": plan.Status, "source": nullText(plan.Source),
	}))
	return err
}

func (t planningTx) SaveGoal(goal planning.Goal) error {
	measures, err := json.Marshal(nonNil(goal.SuccessMeasures))
	if err != nil {
		return err
	}
	_, err = t.exec(stmtSaveGoal, t.scoped(params{
		"id": goal.ID, "title": goal.Title, "status": goal.Status, "success_measures": string(measures),
		"statement": goal.Statement, "source": nullText(goal.Source),
	}))
	return err
}

func scanGoal(row scanner) (planning.Goal, error) {
	var goal planning.Goal
	var measures string
	var source sql.NullString
	if err := row.Scan(&goal.ID, &goal.Title, &goal.Status, &measures, &goal.Statement, &source); err != nil {
		return planning.Goal{}, err
	}
	goal.Source = source.String
	if err := json.Unmarshal([]byte(measures), &goal.SuccessMeasures); err != nil {
		return planning.Goal{}, fmt.Errorf("goal %s: %w", goal.ID, err)
	}
	return goal, nil
}

func (t planningTx) Goal(id string) (planning.Goal, error) {
	row, err := t.row(stmtReadGoal, t.scoped(params{"id": id}))
	if err != nil {
		return planning.Goal{}, err
	}
	goal, err := scanGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.Goal{}, fmt.Errorf("goal %s: %w", id, ports.ErrNotFound)
	}
	return goal, err
}

func (t planningTx) Goals() ([]planning.Goal, error) {
	rows, err := t.rows(stmtListGoals, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.Goal
	for rows.Next() {
		goal, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, goal)
	}
	return out, rows.Err()
}

func (t planningTx) InsertRequirement(r planning.Requirement) error {
	_, err := t.exec(stmtInsertRequirement, t.scoped(params{
		"id": r.ID, "version": r.Version, "requirement_key": r.Key, "text": r.Text,
		"source": nullText(r.Source), "digest": r.Digest,
	}))
	return err
}

func scanRequirement(row scanner) (planning.Requirement, error) {
	var r planning.Requirement
	var source sql.NullString
	if err := row.Scan(&r.ID, &r.Version, &r.Key, &r.Text, &source, &r.Digest); err != nil {
		return planning.Requirement{}, err
	}
	r.Source = source.String
	return r, nil
}

func (t planningTx) Requirement(key string) (planning.Requirement, error) {
	row, err := t.row(stmtLatestRequirement, t.scoped(params{"requirement_key": key}))
	if err != nil {
		return planning.Requirement{}, err
	}
	r, err := scanRequirement(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.Requirement{}, fmt.Errorf("requirement %s: %w", key, ports.ErrNotFound)
	}
	return r, err
}

func (t planningTx) Requirements() ([]planning.Requirement, error) {
	rows, err := t.rows(stmtListRequirements, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.Requirement
	for rows.Next() {
		r, err := scanRequirement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t planningTx) InsertContract(c planning.Contract) error {
	criteria, err := json.Marshal(nonNil(c.Criteria))
	if err != nil {
		return err
	}
	requirements, err := json.Marshal(nonNil(c.Requirements))
	if err != nil {
		return err
	}
	decisions, err := json.Marshal(nonNil(c.Decisions))
	if err != nil {
		return err
	}
	uses, err := json.Marshal(nonNil(c.Uses))
	if err != nil {
		return err
	}
	orders, err := json.Marshal(nonNil(c.Orders))
	if err != nil {
		return err
	}
	shared := int64(0)
	if c.Shared {
		shared = 1
	}
	_, err = t.exec(stmtInsertContract, t.scoped(params{
		"id": c.ID, "version": c.Version, "plan_id": c.PlanID, "title": c.Title, "digest": c.Digest,
		"source": nullText(c.Source), "statement": c.Statement, "criteria": string(criteria),
		"requirements": string(requirements), "decisions": string(decisions), "status": c.Status,
		"milestone": c.Milestone, "shared": shared, "uses": string(uses), "work_orders": string(orders),
	}))
	if err != nil {
		return err
	}
	return t.insertPins(c)
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func scanContract(row scanner) (planning.Contract, error) {
	var c planning.Contract
	var source sql.NullString
	var criteria, requirements, decisions, uses, orders string
	var shared int64
	err := row.Scan(&c.ID, &c.Version, &c.PlanID, &c.Title, &c.Digest, &source, &c.Statement,
		&criteria, &requirements, &decisions, &c.Status, &c.Milestone, &shared, &uses, &orders)
	if err != nil {
		return planning.Contract{}, err
	}
	c.Source, c.Shared = source.String, shared != 0
	for _, field := range []struct {
		raw  string
		into any
	}{{criteria, &c.Criteria}, {requirements, &c.Requirements}, {decisions, &c.Decisions}, {uses, &c.Uses}, {orders, &c.Orders}} {
		if err := json.Unmarshal([]byte(field.raw), field.into); err != nil {
			return planning.Contract{}, fmt.Errorf("contract %s: %w", c.ID, err)
		}
	}
	return c, nil
}

func (t planningTx) oneContract(st *statement, p params) (planning.Contract, error) {
	row, err := t.row(st, t.scoped(p))
	if err != nil {
		return planning.Contract{}, err
	}
	c, err := scanContract(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.Contract{}, fmt.Errorf("contract %v: %w", p["id"], ports.ErrNotFound)
	}
	if err != nil {
		return planning.Contract{}, err
	}
	c.Documents, err = t.pinsOf(c.ID, c.Version)
	return c, err
}

func (t planningTx) Contract(id string, version int64) (planning.Contract, error) {
	return t.oneContract(stmtReadContract, params{"id": id, "version": version})
}

func (t planningTx) LatestContract(id string) (planning.Contract, error) {
	return t.oneContract(stmtLatestContract, params{"id": id})
}

func (t planningTx) Contracts() ([]planning.Contract, error) {
	rows, err := t.rows(stmtListContracts, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.Contract
	for rows.Next() {
		c, err := scanContract(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	pins, err := t.pinsByVersion()
	if err != nil {
		return nil, err
	}
	for i, c := range out {
		out[i].Documents = pins[planning.ContractKey(c.ID, c.Version)]
	}
	return out, nil
}

func (t planningTx) InsertLink(link ledger.ExternalLink) error {
	_, err := t.exec(stmtInsertLink, t.scoped(params{
		"record_kind": link.RecordKind, "adapter": link.Key.Adapter, "instance": link.Key.Instance,
		"native_id": link.Key.NativeID, "record_id": link.RecordID, "display_key": nullText(link.DisplayKey),
	}))
	return err
}

func scanLink(row scanner) (ledger.ExternalLink, error) {
	var link ledger.ExternalLink
	var display sql.NullString
	err := row.Scan(&link.RecordKind, &link.RecordID, &link.Key.Adapter, &link.Key.Instance, &link.Key.NativeID, &display)
	link.DisplayKey = display.String
	return link, err
}

func (t planningTx) Link(recordKind string, key ledger.ExternalKey) (ledger.ExternalLink, error) {
	row, err := t.row(stmtReadLink, t.scoped(params{
		"record_kind": recordKind, "adapter": key.Adapter, "instance": key.Instance, "native_id": key.NativeID,
	}))
	if err != nil {
		return ledger.ExternalLink{}, err
	}
	link, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.ExternalLink{}, fmt.Errorf("link %s: %w", key, ports.ErrNotFound)
	}
	return link, err
}

func (t planningTx) Links() ([]ledger.ExternalLink, error) {
	rows, err := t.rows(stmtListLinks, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.ExternalLink
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

func (t planningTx) InsertDependency(d planning.Dependency) error {
	_, err := t.exec(stmtInsertDependency, t.scoped(params{
		"work_order_id": d.WorkOrder, "depends_on": d.DependsOn, "adapter": nullText(d.Adapter),
		"instance": nullText(d.Instance), "kind": string(d.Kind),
	}))
	return err
}

func (t planningTx) DeleteDependency(d planning.Dependency) error {
	_, err := t.exec(stmtDeleteDependency, t.scoped(params{
		"work_order_id": d.WorkOrder, "depends_on": d.DependsOn, "kind": string(d.Kind),
	}))
	return err
}

func (t planningTx) dependencies(st *statement, p params) ([]planning.Dependency, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.Dependency
	for rows.Next() {
		var d planning.Dependency
		var adapter, instance sql.NullString
		var kind string
		if err := rows.Scan(&d.WorkOrder, &d.DependsOn, &adapter, &instance, &kind); err != nil {
			return nil, err
		}
		d.Adapter, d.Instance, d.Kind = adapter.String, instance.String, planning.DependencyKind(kind)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (t planningTx) Dependencies(workOrder string) ([]planning.Dependency, error) {
	return t.dependencies(stmtDependenciesOf, params{"work_order_id": workOrder})
}

func (t planningTx) AllDependencies() ([]planning.Dependency, error) {
	return t.dependencies(stmtListDependencies, nil)
}

func (t planningTx) Binding(id string) (planning.SourceBinding, error) {
	row, err := t.row(stmtReadBinding, t.scoped(params{"id": id}))
	if err != nil {
		return planning.SourceBinding{}, err
	}
	b, err := scanBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.SourceBinding{}, fmt.Errorf("binding %s: %w", id, ports.ErrNotFound)
	}
	return b, err
}

func (t planningTx) Bindings() ([]planning.SourceBinding, error) {
	rows, err := t.rows(stmtListBindings, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.SourceBinding
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanBinding(row scanner) (planning.SourceBinding, error) {
	var b planning.SourceBinding
	var cursor, detail sql.NullString
	var problems string
	if err := row.Scan(&b.ID, &b.Port, &b.Adapter, &b.Instance, &cursor, &b.Health, &detail, &problems); err != nil {
		return planning.SourceBinding{}, err
	}
	b.Cursor, b.Detail = cursor.String, detail.String
	if err := json.Unmarshal([]byte(problems), &b.Problems); err != nil {
		return planning.SourceBinding{}, fmt.Errorf("binding %s: %w", b.ID, err)
	}
	return b, nil
}

func (t planningTx) SaveBinding(b planning.SourceBinding) error {
	problems, err := json.Marshal(nonNil(b.Problems))
	if err != nil {
		return err
	}
	_, err = t.exec(stmtSaveBinding, t.scoped(params{
		"id": b.ID, "port": b.Port, "adapter": b.Adapter, "instance_url": b.Instance,
		"cursor": nullText(b.Cursor), "health": b.Health, "detail": nullText(b.Detail), "problems": string(problems),
	}))
	return err
}

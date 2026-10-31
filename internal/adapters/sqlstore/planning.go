package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
)

const contractColumns = `id, version, plan_id, title, digest, source, statement, criteria, requirements, decisions, status`

var (
	stmtInsertPlan = define("plan.insert",
		`INSERT INTO plans (workspace_id, id, goal_id, title, status, source)
VALUES (:workspace_id, :id, :goal_id, :title, :status, :source)`)

	stmtListPlans = define("plan.list",
		`SELECT id, goal_id, title, status, source FROM plans
 WHERE workspace_id = :workspace_id
 ORDER BY id`)

	stmtInsertContract = define("contract.insert",
		`INSERT INTO contracts
  (workspace_id, id, version, plan_id, title, digest, source, statement, criteria, requirements, decisions, status)
VALUES
  (:workspace_id, :id, :version, :plan_id, :title, :digest, :source, :statement, :criteria, :requirements, :decisions, :status)`)

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

	stmtDependenciesOf = define("dependency.of",
		`SELECT work_order_id, depends_on, adapter, instance, kind FROM dependencies
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY depends_on`)

	stmtListDependencies = define("dependency.list",
		`SELECT work_order_id, depends_on, adapter, instance, kind FROM dependencies
 WHERE workspace_id = :workspace_id
 ORDER BY work_order_id, depends_on`)

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

func (t planningTx) Plans() ([]planning.Plan, error) {
	rows, err := t.rows(stmtListPlans, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.Plan
	for rows.Next() {
		var plan planning.Plan
		var goal, source sql.NullString
		if err := rows.Scan(&plan.ID, &goal, &plan.Title, &plan.Status, &source); err != nil {
			return nil, err
		}
		plan.GoalID, plan.Source = goal.String, source.String
		out = append(out, plan)
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
	_, err = t.exec(stmtInsertContract, t.scoped(params{
		"id": c.ID, "version": c.Version, "plan_id": c.PlanID, "title": c.Title, "digest": c.Digest,
		"source": nullText(c.Source), "statement": c.Statement, "criteria": string(criteria),
		"requirements": string(requirements), "decisions": string(decisions), "status": c.Status,
	}))
	return err
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
	var criteria, requirements, decisions string
	err := row.Scan(&c.ID, &c.Version, &c.PlanID, &c.Title, &c.Digest, &source, &c.Statement,
		&criteria, &requirements, &decisions, &c.Status)
	if err != nil {
		return planning.Contract{}, err
	}
	c.Source = source.String
	for _, field := range []struct {
		raw  string
		into any
	}{{criteria, &c.Criteria}, {requirements, &c.Requirements}, {decisions, &c.Decisions}} {
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
	return out, rows.Err()
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

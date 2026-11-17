package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

const revisionColumns = `id, plan_id, approved_version, decision_ids, finished, opened_at, closed_at, closed_by, outcome, approval_id`

var (
	stmtSaveRevision = define("plan_revision.save",
		`INSERT INTO plan_revisions (workspace_id, id, plan_id, approved_version, decision_ids, finished, opened_at, closed_at, closed_by, outcome, approval_id)
VALUES (:workspace_id, :id, :plan_id, :approved_version, :decision_ids, :finished, :opened_at, :closed_at, :closed_by, :outcome, :approval_id)
ON CONFLICT (workspace_id, id) DO UPDATE SET
  decision_ids = :decision_ids, finished = :finished, closed_at = :closed_at, closed_by = :closed_by, outcome = :outcome, approval_id = :approval_id`)

	stmtOpenRevision = define("plan_revision.open",
		`SELECT `+revisionColumns+` FROM plan_revisions
 WHERE workspace_id = :workspace_id AND plan_id = :plan_id AND closed_at IS NULL`)

	stmtListRevisions = define("plan_revision.list",
		`SELECT `+revisionColumns+` FROM plan_revisions
 WHERE workspace_id = :workspace_id
 ORDER BY plan_id, opened_at, id`)
)

func (t planningTx) SaveRevision(revision planning.Revision) error {
	decisions, err := json.Marshal(nonNil(revision.DecisionIDs))
	if err != nil {
		return err
	}
	finished, err := json.Marshal(nonNil(revision.Finished))
	if err != nil {
		return err
	}
	_, err = t.exec(stmtSaveRevision, t.scoped(params{
		"id": revision.ID, "plan_id": revision.PlanID, "approved_version": revision.ApprovedVersion,
		"decision_ids": string(decisions), "finished": string(finished), "opened_at": revision.OpenedAt,
		"closed_at": nullInt(revision.ClosedAt), "closed_by": revision.ClosedBy, "outcome": string(revision.Outcome),
		"approval_id": revision.Approval,
	}))
	return err
}

func scanRevision(row scanner) (planning.Revision, error) {
	var revision planning.Revision
	var decisions, finished, outcome string
	var closedAt sql.NullInt64
	if err := row.Scan(&revision.ID, &revision.PlanID, &revision.ApprovedVersion, &decisions, &finished, &revision.OpenedAt, &closedAt, &revision.ClosedBy, &outcome, &revision.Approval); err != nil {
		return planning.Revision{}, err
	}
	revision.ClosedAt, revision.Outcome = closedAt.Int64, planning.RevisionOutcome(outcome)
	for _, field := range []struct {
		raw  string
		into any
	}{{decisions, &revision.DecisionIDs}, {finished, &revision.Finished}} {
		if err := json.Unmarshal([]byte(field.raw), field.into); err != nil {
			return planning.Revision{}, fmt.Errorf("plan %s revision %s: %w", revision.PlanID, revision.ID, err)
		}
	}
	return revision, nil
}

func (t planningTx) OpenRevision(plan string) (planning.Revision, error) {
	row, err := t.row(stmtOpenRevision, t.scoped(params{"plan_id": plan}))
	if err != nil {
		return planning.Revision{}, err
	}
	revision, err := scanRevision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return planning.Revision{}, fmt.Errorf("plan %s has no open revision: %w", plan, ports.ErrNotFound)
	}
	return revision, err
}

func (t planningTx) Revisions() ([]planning.Revision, error) {
	rows, err := t.rows(stmtListRevisions, t.scoped(nil))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []planning.Revision
	for rows.Next() {
		revision, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, revision)
	}
	return out, rows.Err()
}

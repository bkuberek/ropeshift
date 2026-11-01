package sqlstore

import (
	"database/sql"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
)

const evidenceColumns = `id, work_order_id, lane_id, fence, artifact_id, criterion_id, name, status, source, details_uri, created_at`

var (
	stmtEvidenceRecord = define("evidence.record",
		`INSERT INTO check_results (workspace_id, id, work_order_id, lane_id, fence, artifact_id, criterion_id, name, status, source, details_uri, created_at)
VALUES (:workspace_id, :id, :work_order_id, :lane_id, :fence, :artifact_id, :criterion_id, :name, :status, :source, :details_uri, :now)
ON CONFLICT (workspace_id, id) DO UPDATE SET status = :status, details_uri = :details_uri`)

	stmtEvidenceAttempt = define("evidence.attempt",
		`SELECT `+evidenceColumns+` FROM check_results
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND fence = :fence
 ORDER BY created_at, id`)

	stmtEvidenceChecks = define("evidence.checks",
		`SELECT `+evidenceColumns+` FROM check_results
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id
 ORDER BY created_at, id`)

	stmtEvidenceArtifacts = define("evidence.artifacts",
		`SELECT a.id, a.lane_id, a.kind, a.uri, a.digest, a.fence, a.created_at
  FROM artifacts a
  JOIN lanes l ON l.workspace_id = a.workspace_id AND l.id = a.lane_id
 WHERE a.workspace_id = :workspace_id AND l.work_order_id = :work_order_id
 ORDER BY a.created_at, a.id`)
)

// evidenceTx is the acceptance area of one transaction.
type evidenceTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t evidenceTx) Record(e workorders.Evidence) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtEvidenceRecord, params{
		"workspace_id": string(t.scope), "id": e.ID, "work_order_id": e.WorkOrder, "lane_id": e.Lane, "fence": e.Fence,
		"artifact_id": e.ArtifactID, "criterion_id": e.Criterion, "name": e.Name, "status": string(e.Status),
		"source": string(e.Source), "details_uri": e.DetailsURI, "now": now,
	})
	return err
}

func (t evidenceTx) Attempt(workOrder string, fence int64) ([]workorders.Evidence, error) {
	rows, err := t.rows(stmtEvidenceAttempt, params{"workspace_id": string(t.scope), "work_order_id": workOrder, "fence": fence})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvidence(rows)
}

func (t evidenceTx) Checks(workOrder string) ([]workorders.Evidence, error) {
	rows, err := t.rows(stmtEvidenceChecks, params{"workspace_id": string(t.scope), "work_order_id": workOrder})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvidence(rows)
}

func (t evidenceTx) Artifacts(workOrder string) ([]ledger.Artifact, error) {
	rows, err := t.rows(stmtEvidenceArtifacts, params{"workspace_id": string(t.scope), "work_order_id": workOrder})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifacts(rows)
}

func scanEvidence(rows *sql.Rows) ([]workorders.Evidence, error) {
	var out []workorders.Evidence
	for rows.Next() {
		var e workorders.Evidence
		var status, source string
		if err := rows.Scan(&e.ID, &e.WorkOrder, &e.Lane, &e.Fence, &e.ArtifactID, &e.Criterion, &e.Name, &status, &source, &e.DetailsURI, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Status, e.Source = workorders.CheckStatus(status), workorders.EvidenceSource(source)
		out = append(out, e)
	}
	return out, rows.Err()
}

package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const templateWriteColumns = `id, worker_id, requested_by, requested_at, expires_at, shape, path, name, project, repositories, first_commit, remote, state, report, reported_at`

var (
	stmtInsertTemplateWrite = define("template_write.insert",
		`INSERT INTO template_writes (workspace_id, id, worker_id, requested_by, requested_at, expires_at, shape, path, name, project, repositories, first_commit, remote, state)
VALUES (:workspace_id, :id, :worker_id, :requested_by, :requested_at, :expires_at, :shape, :path, :name, :project, :repositories, :first_commit, :remote, :state)`)

	stmtFindTemplateWrite = define("template_write.find",
		`SELECT `+templateWriteColumns+` FROM template_writes WHERE workspace_id = :workspace_id AND id = :id`)

	stmtOfferedTemplateWrites = define("template_write.offered",
		`SELECT `+templateWriteColumns+` FROM template_writes
 WHERE workspace_id = :workspace_id AND worker_id = :worker_id AND state = :state
 ORDER BY requested_at, id`)

	// stmtSettleTemplateWrite changes a request only while it is offered, so the first report is the one kept.
	stmtSettleTemplateWrite = define("template_write.settle",
		`UPDATE template_writes SET state = :state, report = :report, reported_at = :reported_at
 WHERE workspace_id = :workspace_id AND id = :id AND state = :offered`)
)

// templateWriteTx is the template write area of one transaction. The requests live where the records every
// project shares do: the installation's database, or the project's own on a store with none.
type templateWriteTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t templateWriteTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanTemplateWrite(row scanner) (ledger.TemplateWriteRecord, error) {
	var r ledger.TemplateWriteRecord
	var repositories, remote, state, report string
	var firstCommit int64
	err := row.Scan(&r.ID, &r.Worker, &r.RequestedBy, &r.RequestedAt, &r.ExpiresAt, &r.Request.Shape, &r.Request.Path, &r.Request.Name, &r.Request.Project,
		&repositories, &firstCommit, &remote, &state, &report, &r.ReportedAt)
	if err != nil {
		return ledger.TemplateWriteRecord{}, err
	}
	if remote != "" {
		r.Request.Remote = &ledger.ProjectRemote{}
		if err := json.Unmarshal([]byte(remote), r.Request.Remote); err != nil {
			return ledger.TemplateWriteRecord{}, fmt.Errorf("template write %s: %w", r.ID, err)
		}
	}
	r.State = ledger.TemplateWriteState(state)
	r.Request.FirstCommit = firstCommit != 0
	if err := json.Unmarshal([]byte(repositories), &r.Request.Repositories); err != nil {
		return ledger.TemplateWriteRecord{}, fmt.Errorf("template write %s: %w", r.ID, err)
	}
	if report != "" {
		r.Report = &ledger.TemplateWriteReport{}
		if err := json.Unmarshal([]byte(report), r.Report); err != nil {
			return ledger.TemplateWriteRecord{}, fmt.Errorf("template write %s: %w", r.ID, err)
		}
	}
	return r, nil
}

func (t templateWriteTx) Find(id string) (ledger.TemplateWriteRecord, error) {
	row, err := t.row(stmtFindTemplateWrite, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.TemplateWriteRecord{}, err
	}
	record, err := scanTemplateWrite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.TemplateWriteRecord{}, fmt.Errorf("template write %s: %w", id, ports.ErrNotFound)
	}
	return record, err
}

func (t templateWriteTx) Offered(worker string) ([]ledger.TemplateWriteRecord, error) {
	rows, err := t.rows(stmtOfferedTemplateWrites, t.scoped(params{"worker_id": worker, "state": string(ledger.TemplateWriteOffered)}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.TemplateWriteRecord
	for rows.Next() {
		record, err := scanTemplateWrite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (t templateWriteTx) Insert(r ledger.TemplateWriteRecord) error {
	repositories, err := json.Marshal(append([]ledger.TemplateWriteRepository{}, r.Request.Repositories...))
	if err != nil {
		return err
	}
	var firstCommit int64
	if r.Request.FirstCommit {
		firstCommit = 1
	}
	var remote []byte
	if r.Request.Remote != nil {
		if remote, err = json.Marshal(r.Request.Remote); err != nil {
			return err
		}
	}
	_, err = t.exec(stmtInsertTemplateWrite, t.scoped(params{
		"id": r.ID, "worker_id": r.Worker, "requested_by": r.RequestedBy, "requested_at": r.RequestedAt, "expires_at": r.ExpiresAt,
		"shape": r.Request.Shape, "path": r.Request.Path, "name": r.Request.Name, "project": r.Request.Project, "repositories": string(repositories),
		"first_commit": firstCommit, "remote": string(remote), "state": string(ledger.TemplateWriteOffered),
	}))
	return err
}

func (t templateWriteTx) Settle(id string, state ledger.TemplateWriteState, report ledger.TemplateWriteReport, reportedAt int64) (bool, error) {
	body, err := json.Marshal(report)
	if err != nil {
		return false, err
	}
	n, err := t.exec(stmtSettleTemplateWrite, t.scoped(params{
		"id": id, "state": string(state), "report": string(body), "reported_at": reportedAt, "offered": string(ledger.TemplateWriteOffered),
	}))
	return n > 0, err
}

func (t installationReadTx) TemplateWrites() ports.TemplateWriteReader {
	return templateWriteTx{session: t.session, scope: ledger.InstallationScope}
}

func (t *installationTx) TemplateWrites() ports.TemplateWriteWriter {
	return templateWriteTx{session: t.session, scope: ledger.InstallationScope}
}

func (r ownSharedRead) TemplateWrites() ports.TemplateWriteReader {
	return templateWriteTx{session: r.t.session, scope: r.t.scope}
}

func (w ownSharedWrite) TemplateWrites() ports.TemplateWriteWriter {
	return templateWriteTx{session: w.t.session, scope: w.t.scope}
}

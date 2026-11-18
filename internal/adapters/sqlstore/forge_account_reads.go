package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const forgeAccountReadColumns = `id, worker_id, requested_by, requested_at, expires_at, forge, state, report, reported_at`

var (
	stmtInsertForgeAccountRead = define("forge_account_read.insert",
		`INSERT INTO forge_account_reads (workspace_id, id, worker_id, requested_by, requested_at, expires_at, forge, state)
VALUES (:workspace_id, :id, :worker_id, :requested_by, :requested_at, :expires_at, :forge, :state)`)

	stmtFindForgeAccountRead = define("forge_account_read.find",
		`SELECT `+forgeAccountReadColumns+` FROM forge_account_reads WHERE workspace_id = :workspace_id AND id = :id`)

	stmtOfferedForgeAccountReads = define("forge_account_read.offered",
		`SELECT `+forgeAccountReadColumns+` FROM forge_account_reads
 WHERE workspace_id = :workspace_id AND worker_id = :worker_id AND state = :state
 ORDER BY requested_at, id`)

	// stmtSettleForgeAccountRead changes a request only while it is offered, so the first report is the one kept.
	stmtSettleForgeAccountRead = define("forge_account_read.settle",
		`UPDATE forge_account_reads SET state = :state, report = :report, reported_at = :reported_at
 WHERE workspace_id = :workspace_id AND id = :id AND state = :offered`)
)

// forgeAccountReadTx is the forge account read area of one transaction, kept where the template writes are.
type forgeAccountReadTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t forgeAccountReadTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanForgeAccountRead(row scanner) (ledger.ForgeAccountReadRecord, error) {
	var r ledger.ForgeAccountReadRecord
	var state, report string
	err := row.Scan(&r.ID, &r.Worker, &r.RequestedBy, &r.RequestedAt, &r.ExpiresAt, &r.Forge, &state, &report, &r.ReportedAt)
	if err != nil {
		return ledger.ForgeAccountReadRecord{}, err
	}
	r.State = ledger.ForgeAccountReadState(state)
	if report != "" {
		r.Report = &ledger.ForgeAccountReport{}
		if err := json.Unmarshal([]byte(report), r.Report); err != nil {
			return ledger.ForgeAccountReadRecord{}, fmt.Errorf("forge account read %s: %w", r.ID, err)
		}
	}
	return r, nil
}

func (t forgeAccountReadTx) Find(id string) (ledger.ForgeAccountReadRecord, error) {
	row, err := t.row(stmtFindForgeAccountRead, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.ForgeAccountReadRecord{}, err
	}
	record, err := scanForgeAccountRead(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.ForgeAccountReadRecord{}, fmt.Errorf("forge account read %s: %w", id, ports.ErrNotFound)
	}
	return record, err
}

func (t forgeAccountReadTx) Offered(worker string) ([]ledger.ForgeAccountReadRecord, error) {
	rows, err := t.rows(stmtOfferedForgeAccountReads, t.scoped(params{"worker_id": worker, "state": string(ledger.ForgeAccountReadOffered)}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.ForgeAccountReadRecord
	for rows.Next() {
		record, err := scanForgeAccountRead(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (t forgeAccountReadTx) Insert(r ledger.ForgeAccountReadRecord) error {
	_, err := t.exec(stmtInsertForgeAccountRead, t.scoped(params{
		"id": r.ID, "worker_id": r.Worker, "requested_by": r.RequestedBy, "requested_at": r.RequestedAt, "expires_at": r.ExpiresAt,
		"forge": r.Forge, "state": string(ledger.ForgeAccountReadOffered),
	}))
	return err
}

func (t forgeAccountReadTx) Settle(id string, report ledger.ForgeAccountReport, reportedAt int64) (bool, error) {
	body, err := json.Marshal(report)
	if err != nil {
		return false, err
	}
	n, err := t.exec(stmtSettleForgeAccountRead, t.scoped(params{
		"id": id, "state": string(ledger.ForgeAccountReadReported), "report": string(body), "reported_at": reportedAt,
		"offered": string(ledger.ForgeAccountReadOffered),
	}))
	return n > 0, err
}

func (t installationReadTx) ForgeAccountReads() ports.ForgeAccountReadReader {
	return forgeAccountReadTx{session: t.session, scope: ledger.InstallationScope}
}

func (t *installationTx) ForgeAccountReads() ports.ForgeAccountReadWriter {
	return forgeAccountReadTx{session: t.session, scope: ledger.InstallationScope}
}

func (r ownSharedRead) ForgeAccountReads() ports.ForgeAccountReadReader {
	return forgeAccountReadTx{session: r.t.session, scope: r.t.scope}
}

func (w ownSharedWrite) ForgeAccountReads() ports.ForgeAccountReadWriter {
	return forgeAccountReadTx{session: w.t.session, scope: w.t.scope}
}

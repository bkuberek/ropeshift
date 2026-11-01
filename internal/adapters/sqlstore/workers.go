package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

const workerColumns = `id, name, epoch, protocol, capabilities, proof_digest, last_handshake_at`

var (
	stmtFindWorker = define("worker.find",
		`SELECT `+workerColumns+` FROM workers WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListWorkers = define("worker.list",
		`SELECT `+workerColumns+` FROM workers WHERE workspace_id = :workspace_id ORDER BY id`)

	stmtInsertWorker = define("worker.insert",
		`INSERT INTO workers (workspace_id, id, name, epoch, protocol, capabilities, proof_digest)
VALUES (:workspace_id, :id, :name, 0, 0, '{}', :proof_digest)`)

	stmtReplaceProof = define("worker.replace_proof",
		`UPDATE workers SET proof_digest = :proof_digest WHERE workspace_id = :workspace_id AND id = :id`)

	stmtOpenEpoch = define("worker.open_epoch",
		`UPDATE workers
   SET epoch = epoch + 1,
       protocol = :protocol,
       capabilities = :capabilities,
       last_handshake_at = :now
 WHERE workspace_id = :workspace_id AND id = :id
RETURNING epoch`)

	stmtInsertJoinGrant = define("join_grant.insert",
		`INSERT INTO join_grants (workspace_id, id, digest, created_by)
VALUES (:workspace_id, :id, :digest, :created_by)`)

	stmtReadJoinGrant = define("join_grant.read",
		`SELECT id, digest, created_by, used_by FROM join_grants
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtSpendJoinGrant = define("join_grant.spend",
		`UPDATE join_grants SET used_by = :worker
 WHERE workspace_id = :workspace_id AND id = :id AND used_by IS NULL`)
)

// workerTx is the worker area of one transaction.
type workerTx struct {
	*session
	scope ledger.WorkspaceID
	now   int64
}

func (t workerTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanWorker(row scanner) (ledger.WorkerRecord, error) {
	var w ledger.WorkerRecord
	var manifest string
	var handshake sql.NullInt64
	err := row.Scan(&w.ID, &w.Name, &w.Epoch, &w.Protocol, &manifest, &w.ProofDigest, &handshake)
	if err != nil {
		return ledger.WorkerRecord{}, err
	}
	w.LastHandshakeAt = handshake.Int64
	if err := json.Unmarshal([]byte(manifest), &w.Manifest); err != nil {
		return ledger.WorkerRecord{}, fmt.Errorf("worker %s: %w", w.ID, err)
	}
	return w, nil
}

func (t workerTx) Find(id string) (ledger.WorkerRecord, error) {
	row, err := t.row(stmtFindWorker, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.WorkerRecord{}, err
	}
	w, err := scanWorker(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.WorkerRecord{}, fmt.Errorf("worker %s: %w", id, ports.ErrNotFound)
	}
	return w, err
}

func (t workerTx) List() ([]ledger.Worker, error) {
	rows, err := t.rows(stmtListWorkers, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w.Worker)
	}
	return out, rows.Err()
}

func (t workerTx) Insert(w ledger.WorkerRecord) error {
	_, err := t.exec(stmtInsertWorker, t.scoped(params{"id": w.ID, "name": w.Name, "proof_digest": w.ProofDigest}))
	return err
}

func (t workerTx) ReplaceProof(id, digest string) error {
	_, err := t.exec(stmtReplaceProof, t.scoped(params{"id": id, "proof_digest": digest}))
	return err
}

func (t workerTx) OpenEpoch(w ledger.Worker) (int64, error) {
	raw, err := json.Marshal(w.Manifest)
	if err != nil {
		return 0, err
	}
	row, err := t.row(stmtOpenEpoch, t.scoped(params{
		"id": w.ID, "protocol": int64(w.Protocol), "capabilities": string(raw), "now": t.now,
	}))
	if err != nil {
		return 0, err
	}
	var epoch int64
	if err := row.Scan(&epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("worker %s: %w", w.ID, ports.ErrNotFound)
		}
		return 0, err
	}
	t.changed = true
	return epoch, nil
}

func (t workerTx) InsertJoinGrant(g ledger.JoinGrant) error {
	_, err := t.exec(stmtInsertJoinGrant, t.scoped(params{"id": g.ID, "digest": g.Digest, "created_by": g.CreatedBy}))
	return err
}

func (t workerTx) JoinGrant(id string) (ledger.JoinGrant, error) {
	row, err := t.row(stmtReadJoinGrant, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.JoinGrant{}, err
	}
	var g ledger.JoinGrant
	var used sql.NullString
	err = row.Scan(&g.ID, &g.Digest, &g.CreatedBy, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.JoinGrant{}, fmt.Errorf("join grant %s: %w", id, ports.ErrNotFound)
	}
	g.UsedBy = used.String
	return g, err
}

func (t workerTx) SpendJoinGrant(id, worker string) error {
	_, err := t.exec(stmtSpendJoinGrant, t.scoped(params{"id": id, "worker": worker}))
	return err
}

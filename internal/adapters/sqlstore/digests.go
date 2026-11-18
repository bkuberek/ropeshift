package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// A digest is a record of its owner and generation, so the insert keeps the first. A lane's and an assistant's
// sit in a project's ledger and an orchestrator's in the installation, each in a table of this name.
var (
	stmtInsertDigest = define("digest.insert",
		`INSERT INTO generation_digests (workspace_id, owner_kind, owner_id, generation, body, recorded_at)
VALUES (:workspace_id, :owner_kind, :owner_id, :generation, :body, :now)
ON CONFLICT (workspace_id, owner_kind, owner_id, generation) DO NOTHING`)

	stmtFindDigest = define("digest.find",
		`SELECT owner_kind, owner_id, generation, body, recorded_at
  FROM generation_digests
 WHERE workspace_id = :workspace_id AND owner_kind = :owner_kind AND owner_id = :owner_id AND generation = :generation`)
)

func insertDigest(se *session, scope ledger.WorkspaceID, digest ledger.GenerationDigest, now int64) error {
	_, err := se.exec(stmtInsertDigest, params{
		"workspace_id": string(scope), "owner_kind": digest.OwnerKind, "owner_id": digest.OwnerID,
		"generation": digest.Generation, "body": string(digest.Body), "now": now,
	})
	return err
}

func findDigest(se *session, scope ledger.WorkspaceID, kind, owner string, generation int64) (ledger.GenerationDigest, error) {
	row, err := se.row(stmtFindDigest, params{
		"workspace_id": string(scope), "owner_kind": kind, "owner_id": owner, "generation": generation,
	})
	if err != nil {
		return ledger.GenerationDigest{}, err
	}
	var out ledger.GenerationDigest
	var body string
	err = row.Scan(&out.OwnerKind, &out.OwnerID, &out.Generation, &body, &out.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.GenerationDigest{}, fmt.Errorf("digest %s %s/%d: %w", kind, owner, generation, ports.ErrNotFound)
	}
	out.Body = json.RawMessage(body)
	return out, err
}

func (t laneTx) SaveDigest(digest ledger.GenerationDigest) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	return insertDigest(t.session, t.scope, digest, now)
}

func (t laneTx) Digest(kind, owner string, generation int64) (ledger.GenerationDigest, error) {
	return findDigest(t.session, t.scope, kind, owner, generation)
}

func (t workerTx) SaveOrchestratorDigest(digest ledger.GenerationDigest) error {
	return insertDigest(t.session, t.scope, digest, t.now)
}

func (t workerTx) OrchestratorDigest(orchestrator string, generation int64) (ledger.GenerationDigest, error) {
	return findDigest(t.session, t.scope, ledger.DigestOwnerOrchestrator, orchestrator, generation)
}

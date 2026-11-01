package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

var (
	stmtStartGeneration = define("generation.start",
		`INSERT INTO generations (workspace_id, lane_id, generation, start_reason, handoff_in, started_at)
VALUES (:workspace_id, :lane_id, :generation, :start_reason, :handoff_in, :now)`)

	stmtBindSession = define("generation.bind",
		`UPDATE generations SET session_id = :session_id
 WHERE workspace_id = :workspace_id AND lane_id = :lane_id AND generation = :generation`)

	stmtEndGeneration = define("generation.end",
		`UPDATE generations SET end_reason = :end_reason, ended_at = :now
 WHERE workspace_id = :workspace_id AND lane_id = :lane_id AND generation = :generation AND ended_at IS NULL`)

	stmtHandoffOut = define("generation.handoff_out",
		`UPDATE generations SET handoff_out = :handoff_out
 WHERE workspace_id = :workspace_id AND lane_id = :lane_id AND generation = :generation`)

	stmtListGenerations = define("generation.list",
		`SELECT lane_id, generation, start_reason, end_reason, session_id, handoff_in, handoff_out, started_at, ended_at
  FROM generations
 WHERE workspace_id = :workspace_id AND lane_id = :lane_id
 ORDER BY generation`)

	stmtInsertHandoff = define("handoff.insert",
		`INSERT INTO handoffs (workspace_id, lane_id, generation, artifact_id, digest, degraded, body, created_at)
VALUES (:workspace_id, :lane_id, :generation, :artifact_id, :digest, :degraded, :body, :now)
ON CONFLICT (workspace_id, lane_id, generation) DO NOTHING`)

	// stmtInsertCheckpoint keeps one row per generation, because a lane parks at most once in
	// each, and a repeat of the same commit is the same checkpoint.
	stmtInsertCheckpoint = define("checkpoint.insert",
		`INSERT INTO checkpoints (workspace_id, lane_id, generation, branch, head_sha, snapshot_ref, pushed,
  handoff_artifact_id, pending_instruction, created_at)
VALUES (:workspace_id, :lane_id, :generation, :branch, :head_sha, :snapshot_ref, :pushed,
  :handoff_artifact_id, :pending_instruction, :now)
ON CONFLICT (workspace_id, lane_id, generation) DO NOTHING`)

	stmtFindCheckpoint = define("checkpoint.find",
		`SELECT lane_id, generation, branch, head_sha, snapshot_ref, pushed, handoff_artifact_id, pending_instruction, created_at
  FROM checkpoints
 WHERE workspace_id = :workspace_id AND lane_id = :lane_id AND generation = :generation`)

	stmtFindHandoff = define("handoff.find",
		`SELECT artifact_id, lane_id, generation, digest, degraded, body, created_at
  FROM handoffs
 WHERE workspace_id = :workspace_id AND lane_id = :lane_id AND generation = :generation`)
)

func (t laneTx) generationParams(lane string, number int64) params {
	return params{"workspace_id": string(t.scope), "lane_id": lane, "generation": number}
}

func (t laneTx) StartGeneration(generation ledger.Generation) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	p := t.generationParams(generation.Lane, generation.Number)
	p["start_reason"], p["handoff_in"], p["now"] = string(generation.StartReason), generation.HandoffIn, now
	_, err = t.exec(stmtStartGeneration, p)
	return err
}

func (t laneTx) BindSession(lane string, number int64, session string) error {
	p := t.generationParams(lane, number)
	p["session_id"] = session
	_, err := t.exec(stmtBindSession, p)
	return err
}

func (t laneTx) EndGeneration(lane string, number int64, reason string) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	p := t.generationParams(lane, number)
	p["end_reason"], p["now"] = reason, now
	_, err = t.exec(stmtEndGeneration, p)
	return err
}

func (t laneTx) Generations(lane string) ([]ledger.Generation, error) {
	rows, err := t.rows(stmtListGenerations, params{"workspace_id": string(t.scope), "lane_id": lane})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.Generation
	for rows.Next() {
		var g ledger.Generation
		var reason string
		var ended sql.NullInt64
		if err := rows.Scan(&g.Lane, &g.Number, &reason, &g.EndReason, &g.SessionID, &g.HandoffIn, &g.HandoffOut, &g.StartedAt, &ended); err != nil {
			return nil, err
		}
		g.StartReason, g.EndedAt = ledger.StartReason(reason), ended.Int64
		out = append(out, g)
	}
	return out, rows.Err()
}

func (t laneTx) SaveHandoff(record ledger.HandoffRecord) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	degraded := int64(0)
	if record.Degraded {
		degraded = 1
	}
	p := t.generationParams(record.Lane, record.Generation)
	p["artifact_id"], p["digest"], p["degraded"], p["body"], p["now"] = record.ArtifactID, record.Digest, degraded, string(record.Body), now
	if _, err := t.exec(stmtInsertHandoff, p); err != nil {
		return err
	}
	out := t.generationParams(record.Lane, record.Generation)
	out["handoff_out"] = record.ArtifactID
	_, err = t.exec(stmtHandoffOut, out)
	return err
}

func (t laneTx) Handoff(lane string, generation int64) (ledger.HandoffRecord, error) {
	row, err := t.row(stmtFindHandoff, t.generationParams(lane, generation))
	if err != nil {
		return ledger.HandoffRecord{}, err
	}
	var out ledger.HandoffRecord
	var degraded int64
	var body string
	err = row.Scan(&out.ArtifactID, &out.Lane, &out.Generation, &out.Digest, &degraded, &body, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.HandoffRecord{}, fmt.Errorf("handoff %s/%d: %w", lane, generation, ports.ErrNotFound)
	}
	out.Degraded, out.Body = degraded != 0, []byte(body)
	return out, err
}

func (t laneTx) SaveCheckpoint(checkpoint ledger.Checkpoint) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	pushed := int64(0)
	if checkpoint.Pushed {
		pushed = 1
	}
	p := t.generationParams(checkpoint.Lane, checkpoint.Generation)
	p["branch"], p["head_sha"], p["snapshot_ref"], p["pushed"] = checkpoint.Branch, checkpoint.Head, checkpoint.SnapshotRef, pushed
	p["handoff_artifact_id"], p["pending_instruction"], p["now"] = checkpoint.HandoffArtifact, checkpoint.PendingInstruction, now
	_, err = t.exec(stmtInsertCheckpoint, p)
	return err
}

func (t laneTx) Checkpoint(lane string, generation int64) (ledger.Checkpoint, error) {
	row, err := t.row(stmtFindCheckpoint, t.generationParams(lane, generation))
	if err != nil {
		return ledger.Checkpoint{}, err
	}
	var out ledger.Checkpoint
	var pushed int64
	err = row.Scan(&out.Lane, &out.Generation, &out.Branch, &out.Head, &out.SnapshotRef, &pushed,
		&out.HandoffArtifact, &out.PendingInstruction, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Checkpoint{}, fmt.Errorf("checkpoint %s/%d: %w", lane, generation, ports.ErrNotFound)
	}
	out.Pushed = pushed != 0
	return out, err
}

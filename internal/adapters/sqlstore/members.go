package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

var (
	stmtSaveMember = define("member.save",
		`INSERT INTO members (workspace_id, id, identity_subject, display_name, status, proof_digest)
VALUES (:workspace_id, :id, :identity_subject, :display_name, :status, :proof_digest)
ON CONFLICT (workspace_id, id) DO UPDATE SET proof_digest = :proof_digest`)

	stmtReadMember = define("member.read",
		`SELECT id, identity_subject, display_name, status, proof_digest
  FROM members WHERE workspace_id = :workspace_id AND id = :id`)

	stmtCountMembers = define("member.count",
		`SELECT count(*) FROM members WHERE workspace_id = :workspace_id`)

	stmtInsertSession = define("member_session.insert",
		`INSERT INTO member_sessions (workspace_id, digest, member_id, created_at, expires_at)
VALUES (:workspace_id, :digest, :member_id, :created_at, :expires_at)`)

	stmtReadSession = define("member_session.read",
		`SELECT digest, member_id, created_at, expires_at
  FROM member_sessions WHERE workspace_id = :workspace_id AND digest = :digest`)

	stmtListSessions = define("member_session.list",
		`SELECT digest, member_id, created_at, expires_at
  FROM member_sessions WHERE workspace_id = :workspace_id ORDER BY created_at, digest`)

	stmtDropSessions = define("member_session.drop",
		`DELETE FROM member_sessions WHERE workspace_id = :workspace_id AND expires_at <= :before`)
)

// memberTx is the member area of one transaction.
type memberTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t memberTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func (t memberTx) Member(id string) (authority.MemberRecord, error) {
	row, err := t.row(stmtReadMember, t.scoped(params{"id": id}))
	if err != nil {
		return authority.MemberRecord{}, err
	}
	var record authority.MemberRecord
	err = row.Scan(&record.ID, &record.IdentitySubject, &record.DisplayName, &record.Status, &record.ProofDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return authority.MemberRecord{}, fmt.Errorf("member %s: %w", id, ports.ErrNotFound)
	}
	return record, err
}

func (t memberTx) CountMembers() (int64, error) {
	row, err := t.row(stmtCountMembers, t.scoped(params{}))
	if err != nil {
		return 0, err
	}
	var count int64
	return count, row.Scan(&count)
}

func (t memberTx) Session(digest string) (authority.Session, error) {
	row, err := t.row(stmtReadSession, t.scoped(params{"digest": digest}))
	if err != nil {
		return authority.Session{}, err
	}
	var record authority.Session
	err = row.Scan(&record.Digest, &record.Member, &record.CreatedAt, &record.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return authority.Session{}, fmt.Errorf("member session: %w", ports.ErrNotFound)
	}
	return record, err
}

func (t memberTx) Sessions() ([]authority.Session, error) {
	rows, err := t.rows(stmtListSessions, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []authority.Session
	for rows.Next() {
		var record authority.Session
		if err := rows.Scan(&record.Digest, &record.Member, &record.CreatedAt, &record.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (t memberTx) SaveMember(record authority.MemberRecord) error {
	_, err := t.exec(stmtSaveMember, t.scoped(params{
		"id": record.ID, "identity_subject": record.IdentitySubject, "display_name": record.DisplayName,
		"status": record.Status, "proof_digest": record.ProofDigest,
	}))
	return err
}

func (t memberTx) InsertSession(record authority.Session) error {
	_, err := t.exec(stmtInsertSession, t.scoped(params{
		"digest": record.Digest, "member_id": record.Member, "created_at": record.CreatedAt, "expires_at": record.ExpiresAt,
	}))
	return err
}

func (t memberTx) DropSessions(before int64) error {
	_, err := t.exec(stmtDropSessions, t.scoped(params{"before": before}))
	return err
}

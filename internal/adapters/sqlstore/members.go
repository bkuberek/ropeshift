package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
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

	stmtDropSession = define("member_session.drop_one",
		`DELETE FROM member_sessions WHERE workspace_id = :workspace_id AND digest = :digest`)

	stmtDropMemberSessions = define("member_session.drop_member",
		`DELETE FROM member_sessions WHERE workspace_id = :workspace_id AND member_id = :member_id`)

	stmtInsertDelegate = define("delegate.insert",
		`INSERT INTO delegate_credentials
  (workspace_id, id, digest, member_id, project_id, label, kind, created_at, expires_at, last_used_at, revoked_at, conversation_id, generation)
VALUES (:workspace_id, :id, :digest, :member_id, :project_id, :label, :kind, :created_at, :expires_at, :last_used_at, :revoked_at,
  :conversation_id, :generation)`)

	stmtReadDelegate = define("delegate.read",
		`SELECT `+delegateColumns+` FROM delegate_credentials WHERE workspace_id = :workspace_id AND id = :id`)

	stmtReadDelegateByDigest = define("delegate.read_digest",
		`SELECT `+delegateColumns+` FROM delegate_credentials WHERE workspace_id = :workspace_id AND digest = :digest`)

	stmtListDelegates = define("delegate.list",
		`SELECT `+delegateColumns+` FROM delegate_credentials WHERE workspace_id = :workspace_id AND member_id = :member_id
 ORDER BY created_at, id`)

	stmtConversationDelegates = define("delegate.of_conversation",
		`SELECT `+delegateColumns+` FROM delegate_credentials WHERE workspace_id = :workspace_id AND conversation_id = :conversation_id
 ORDER BY created_at, id`)

	stmtTouchDelegate = define("delegate.touch",
		`UPDATE delegate_credentials SET last_used_at = :at WHERE workspace_id = :workspace_id AND id = :id`)

	stmtRevokeDelegate = define("delegate.revoke",
		`UPDATE delegate_credentials SET revoked_at = :at WHERE workspace_id = :workspace_id AND id = :id AND revoked_at IS NULL`)

	stmtDropDelegate = define("delegate.drop",
		`DELETE FROM delegate_credentials WHERE workspace_id = :workspace_id AND id = :id`)
)

const delegateColumns = `id, digest, member_id, project_id, label, kind, created_at, expires_at, last_used_at, revoked_at,
       conversation_id, generation`

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

func (t memberTx) DropSession(digest string) error {
	_, err := t.exec(stmtDropSession, t.scoped(params{"digest": digest}))
	return err
}

func (t memberTx) DropMemberSessions(member string) (int64, error) {
	return t.exec(stmtDropMemberSessions, t.scoped(params{"member_id": member}))
}

func scanDelegate(row scanner) (authority.DelegateCredential, error) {
	var d authority.DelegateCredential
	var project string
	var lastUsed, revoked, generation sql.NullInt64
	var conversation sql.NullString
	err := row.Scan(&d.ID, &d.Digest, &d.Member, &project, &d.Label, &d.Kind, &d.CreatedAt, &d.ExpiresAt, &lastUsed, &revoked, &conversation, &generation)
	d.Project, d.LastUsedAt, d.RevokedAt = ledger.WorkspaceID(project), lastUsed.Int64, revoked.Int64
	d.Conversation, d.Generation = conversation.String, generation.Int64
	return d, err
}

func (t memberTx) readDelegate(st *statement, p params) (authority.DelegateCredential, error) {
	row, err := t.row(st, t.scoped(p))
	if err != nil {
		return authority.DelegateCredential{}, err
	}
	d, err := scanDelegate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return authority.DelegateCredential{}, fmt.Errorf("delegate credential: %w", ports.ErrNotFound)
	}
	return d, err
}

func (t memberTx) Delegate(id string) (authority.DelegateCredential, error) {
	return t.readDelegate(stmtReadDelegate, params{"id": id})
}

func (t memberTx) DelegateByDigest(digest string) (authority.DelegateCredential, error) {
	return t.readDelegate(stmtReadDelegateByDigest, params{"digest": digest})
}

func (t memberTx) Delegates(member string) ([]authority.DelegateCredential, error) {
	return t.delegates(stmtListDelegates, params{"member_id": member})
}

func (t memberTx) ConversationDelegates(conversation string) ([]authority.DelegateCredential, error) {
	return t.delegates(stmtConversationDelegates, params{"conversation_id": conversation})
}

func (t memberTx) delegates(st *statement, p params) ([]authority.DelegateCredential, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []authority.DelegateCredential
	for rows.Next() {
		d, err := scanDelegate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (t memberTx) InsertDelegate(d authority.DelegateCredential) error {
	_, err := t.exec(stmtInsertDelegate, t.scoped(params{
		"id": d.ID, "digest": d.Digest, "member_id": d.Member, "project_id": string(d.Project), "label": d.Label, "kind": d.Kind,
		"created_at": d.CreatedAt, "expires_at": d.ExpiresAt, "last_used_at": nullInt(d.LastUsedAt), "revoked_at": nullInt(d.RevokedAt),
		"conversation_id": nullText(d.Conversation), "generation": nullGeneration(d),
	}))
	return err
}

func (t memberTx) TouchDelegate(id string, at int64) error {
	_, err := t.exec(stmtTouchDelegate, t.scoped(params{"id": id, "at": at}))
	return err
}

func (t memberTx) RevokeDelegate(id string, at int64) (bool, error) {
	n, err := t.exec(stmtRevokeDelegate, t.scoped(params{"id": id, "at": at}))
	return n > 0, err
}

func (t memberTx) DropDelegate(id string) error {
	_, err := t.exec(stmtDropDelegate, t.scoped(params{"id": id}))
	return err
}

// nullGeneration is an assistant credential's generation, and nothing for an external one.
func nullGeneration(d authority.DelegateCredential) sql.NullInt64 {
	return sql.NullInt64{Int64: d.Generation, Valid: d.Kind == authority.DelegateAssistant}
}

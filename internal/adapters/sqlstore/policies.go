package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
)

const (
	policyColumns     = `id, kind, version, digest, body, settings, authored_by, created_at`
	experienceColumns = `id, subject_kind, subject_id, policy_version, settings, options_considered, chosen,
       outcome, outcome_observed_at`
)

var (
	stmtInsertPolicy = define("policy.insert",
		`INSERT INTO policy_versions (workspace_id, id, kind, version, digest, body, settings, authored_by, created_at)
VALUES (:workspace_id, :id, :kind, :version, :digest, :body, :settings, :authored_by, :created_at)
ON CONFLICT (workspace_id, kind, version) DO NOTHING`)

	stmtPolicyInForce = define("policy.in_force",
		`SELECT `+policyColumns+` FROM policy_versions
 WHERE workspace_id = :workspace_id AND kind = :kind
 ORDER BY version DESC
 LIMIT 1`)

	stmtPolicyVersion = define("policy.version",
		`SELECT `+policyColumns+` FROM policy_versions
 WHERE workspace_id = :workspace_id AND kind = :kind AND version = :version`)

	stmtPolicyVersions = define("policy.versions",
		`SELECT `+policyColumns+` FROM policy_versions
 WHERE workspace_id = :workspace_id AND kind = :kind
 ORDER BY version DESC`)

	stmtInsertExperience = define("experience.insert",
		`INSERT INTO experience_records
  (workspace_id, id, subject_kind, subject_id, policy_version, settings, options_considered, chosen)
VALUES
  (:workspace_id, :id, :subject_kind, :subject_id, :policy_version, :settings, :options_considered, :chosen)`)

	stmtReadExperience = define("experience.read",
		`SELECT `+experienceColumns+` FROM experience_records
 WHERE workspace_id = :workspace_id AND subject_kind = :subject_kind AND subject_id = :subject_id`)

	stmtListExperience = define("experience.list",
		`SELECT `+experienceColumns+` FROM experience_records WHERE workspace_id = :workspace_id ORDER BY id`)

	stmtFillOutcome = define("experience.outcome",
		`UPDATE experience_records
   SET outcome = :outcome, outcome_observed_at = :observed_at
 WHERE workspace_id = :workspace_id AND subject_kind = :subject_kind AND subject_id = :subject_id
   AND outcome IS NULL`)
)

// policyTx is the policy area of one transaction.
type policyTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t policyTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanPolicy(row scanner) (authority.PolicyVersion, error) {
	var p authority.PolicyVersion
	var kind string
	err := row.Scan(&p.ID, &kind, &p.Version, &p.Digest, &p.Body, &p.Settings, &p.AuthoredBy, &p.CreatedAt)
	p.Kind = authority.PolicyKind(kind)
	return p, err
}

func (t policyTx) one(st *statement, p params, what string) (authority.PolicyVersion, error) {
	row, err := t.row(st, t.scoped(p))
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	version, err := scanPolicy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return authority.PolicyVersion{}, fmt.Errorf("%s: %w", what, ports.ErrNotFound)
	}
	return version, err
}

func (t policyTx) InForce(kind authority.PolicyKind) (authority.PolicyVersion, error) {
	return t.one(stmtPolicyInForce, params{"kind": string(kind)}, string(kind)+" policy")
}

func (t policyTx) Version(kind authority.PolicyKind, version int64) (authority.PolicyVersion, error) {
	return t.one(stmtPolicyVersion, params{"kind": string(kind), "version": version}, fmt.Sprintf("%s policy version %d", kind, version))
}

func (t policyTx) Versions(kind authority.PolicyKind) ([]authority.PolicyVersion, error) {
	rows, err := t.rows(stmtPolicyVersions, t.scoped(params{"kind": string(kind)}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []authority.PolicyVersion
	for rows.Next() {
		version, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, version)
	}
	return out, rows.Err()
}

func (t policyTx) Insert(p authority.PolicyVersion) (bool, error) {
	n, err := t.exec(stmtInsertPolicy, t.scoped(params{
		"id": p.ID, "kind": string(p.Kind), "version": p.Version, "digest": p.Digest, "body": p.Body,
		"settings": p.Settings, "authored_by": p.AuthoredBy, "created_at": p.CreatedAt,
	}))
	return n > 0, err
}

// measureTx is the experience area of one transaction.
type measureTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t measureTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanExperience(row scanner) (measures.Experience, error) {
	var e measures.Experience
	var settings, options string
	var outcome sql.NullString
	var observedAt sql.NullInt64
	err := row.Scan(&e.ID, &e.SubjectKind, &e.SubjectID, &e.PolicyVersion, &settings, &options, &e.Chosen, &outcome, &observedAt)
	if err != nil {
		return measures.Experience{}, err
	}
	if err := json.Unmarshal([]byte(settings), &e.Settings); err != nil {
		return measures.Experience{}, fmt.Errorf("experience %s: %w", e.ID, err)
	}
	if err := json.Unmarshal([]byte(options), &e.OptionsConsidered); err != nil {
		return measures.Experience{}, fmt.Errorf("experience %s: %w", e.ID, err)
	}
	e.OutcomeObservedAt = observedAt.Int64
	if !outcome.Valid {
		return e, nil
	}
	e.Outcome = &measures.Outcome{}
	if err := json.Unmarshal([]byte(outcome.String), e.Outcome); err != nil {
		return measures.Experience{}, fmt.Errorf("experience %s: %w", e.ID, err)
	}
	return e, nil
}

func (t measureTx) Experience(subjectKind, subjectID string) (measures.Experience, error) {
	row, err := t.row(stmtReadExperience, t.scoped(params{"subject_kind": subjectKind, "subject_id": subjectID}))
	if err != nil {
		return measures.Experience{}, err
	}
	e, err := scanExperience(row)
	if errors.Is(err, sql.ErrNoRows) {
		return measures.Experience{}, fmt.Errorf("experience of %s %s: %w", subjectKind, subjectID, ports.ErrNotFound)
	}
	return e, err
}

func (t measureTx) Experiences() ([]measures.Experience, error) {
	rows, err := t.rows(stmtListExperience, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []measures.Experience
	for rows.Next() {
		e, err := scanExperience(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (t measureTx) InsertExperience(e measures.Experience) error {
	options, err := json.Marshal(nonNil(e.OptionsConsidered))
	if err != nil {
		return err
	}
	settings, err := json.Marshal(e.Settings)
	if err != nil {
		return err
	}
	_, err = t.exec(stmtInsertExperience, t.scoped(params{
		"id": e.ID, "subject_kind": e.SubjectKind, "subject_id": e.SubjectID, "policy_version": e.PolicyVersion,
		"settings": string(settings), "options_considered": string(options), "chosen": e.Chosen,
	}))
	return err
}

func (t measureTx) FillOutcome(subjectKind, subjectID string, outcome measures.Outcome, at int64) (bool, error) {
	data, err := json.Marshal(outcome)
	if err != nil {
		return false, err
	}
	n, err := t.exec(stmtFillOutcome, t.scoped(params{
		"subject_kind": subjectKind, "subject_id": subjectID, "outcome": string(data), "observed_at": at,
	}))
	return n > 0, err
}

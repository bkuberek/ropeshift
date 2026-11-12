package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
)

const rosterColumns = `id, scope, version, digest, body, summary, authored_by, prepared_by, created_at`

var (
	stmtInsertRoster = define("roster.insert",
		`INSERT INTO roster_versions (workspace_id, scope, version, id, digest, body, summary, authored_by, prepared_by, created_at)
VALUES (:workspace_id, :scope, :version, :id, :digest, :body, :summary, :authored_by, :prepared_by, :created_at)
ON CONFLICT (workspace_id, scope, version) DO NOTHING`)

	stmtRosterLatest = define("roster.latest",
		`SELECT `+rosterColumns+` FROM roster_versions
 WHERE workspace_id = :workspace_id AND scope = :scope
 ORDER BY version DESC
 LIMIT 1`)

	stmtRosterVersion = define("roster.version",
		`SELECT `+rosterColumns+` FROM roster_versions
 WHERE workspace_id = :workspace_id AND scope = :scope AND version = :version`)

	stmtRosterVersions = define("roster.versions",
		`SELECT `+rosterColumns+` FROM roster_versions
 WHERE workspace_id = :workspace_id AND scope = :scope
 ORDER BY version DESC`)
)

// rosterTx is the roster area of one transaction. The installation's layer lives where the records
// every project shares do: the installation's database, or the project's own on a store with none.
type rosterTx struct {
	*session
	scope             ledger.WorkspaceID
	installation      *session
	installationScope ledger.WorkspaceID
	// writes is the one scope this side may write.
	writes string
}

// on returns where a scope's versions live, with the workspace bound.
func (t rosterTx) on(scope string, p params) (*session, params) {
	p["scope"] = scope
	if scope == roster.ScopeInstallation && t.installation != nil {
		p["workspace_id"] = string(t.installationScope)
		return t.installation, p
	}
	p["workspace_id"] = string(t.scope)
	return t.session, p
}

func scanRoster(row scanner) (roster.Version, error) {
	var v roster.Version
	var body string
	if err := row.Scan(&v.ID, &v.Scope, &v.Version, &v.Digest, &body, &v.Summary, &v.AuthoredBy, &v.PreparedBy, &v.CreatedAt); err != nil {
		return roster.Version{}, err
	}
	layer, err := roster.Decode(body)
	v.Body = layer
	return v, err
}

func (t rosterTx) one(st *statement, scope string, p params, what string) (roster.Version, error) {
	se, p := t.on(scope, p)
	row, err := se.row(st, p)
	if err != nil {
		return roster.Version{}, err
	}
	version, err := scanRoster(row)
	if errors.Is(err, sql.ErrNoRows) {
		return roster.Version{}, fmt.Errorf("%s: %w", what, ports.ErrNotFound)
	}
	return version, err
}

func (t rosterTx) Latest(scope string) (roster.Version, error) {
	return t.one(stmtRosterLatest, scope, params{}, "the "+scope+" roster")
}

func (t rosterTx) Version(scope string, version int64) (roster.Version, error) {
	return t.one(stmtRosterVersion, scope, params{"version": version}, fmt.Sprintf("%s roster version %d", scope, version))
}

func (t rosterTx) Versions(scope string) ([]roster.Version, error) {
	se, p := t.on(scope, params{})
	rows, err := se.rows(stmtRosterVersions, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []roster.Version
	for rows.Next() {
		version, err := scanRoster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, version)
	}
	return out, rows.Err()
}

func (t rosterTx) Insert(v roster.Version) (bool, error) {
	if v.Scope != t.writes {
		return false, fmt.Errorf("%w: the %s roster is not this side's to write", ports.ErrNotShared, v.Scope)
	}
	body, digest, err := roster.Encode(v.Body)
	if err != nil {
		return false, err
	}
	se, bound := t.on(v.Scope, params{
		"id": v.ID, "version": v.Version, "digest": digest, "body": body, "summary": v.Summary,
		"authored_by": v.AuthoredBy, "prepared_by": v.PreparedBy, "created_at": v.CreatedAt,
	})
	n, err := se.exec(stmtInsertRoster, bound)
	return n > 0, err
}

func (t *readTx) Roster() ports.RosterReader {
	se, scope := t.side.session(t.session, t.scope)
	return rosterTx{session: t.session, scope: t.scope, installation: se, installationScope: scope}
}

func (t *writeTx) Roster() ports.RosterWriter {
	se, scope := t.side.session(t.session, t.scope)
	return rosterTx{session: t.session, scope: t.scope, installation: se, installationScope: scope, writes: roster.ScopeProject}
}

func (t installationReadTx) Roster() ports.RosterReader {
	return rosterTx{session: t.session, scope: ledger.InstallationScope}
}

func (t *installationTx) Roster() ports.RosterWriter {
	return rosterTx{session: t.session, scope: ledger.InstallationScope, writes: roster.ScopeInstallation}
}

func (r ownSharedRead) Roster() ports.RosterReader {
	return rosterTx{session: r.t.session, scope: r.t.scope}
}

func (w ownSharedWrite) Roster() ports.RosterWriter {
	return rosterTx{session: w.t.session, scope: w.t.scope, writes: roster.ScopeInstallation}
}

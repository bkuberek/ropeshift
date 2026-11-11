package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const repositoryColumns = `id, remote, default_branch, base_branch, forge_binding, merge_method, review_target, start_from, merge_authority`

var (
	stmtInsertRepository = define("repository.insert",
		`INSERT INTO repositories
  (workspace_id, id, remote, default_branch, base_branch, forge_binding, merge_method, review_target, start_from, merge_authority)
VALUES
  (:workspace_id, :id, :remote, :default_branch, :base_branch, :forge_binding, :merge_method, :review_target, :start_from, :merge_authority)
ON CONFLICT DO NOTHING`)

	stmtRepository = define("repository.get",
		`SELECT `+repositoryColumns+` FROM repositories WHERE workspace_id = :workspace_id AND id = :id`)

	stmtRepositories = define("repository.all",
		`SELECT `+repositoryColumns+` FROM repositories WHERE workspace_id = :workspace_id ORDER BY id`)

	// stmtDetectedRepository moves only when git said something new, so a read that finds the same
	// facts changes no row and appends no event.
	stmtDetectedRepository = define("repository.detected",
		`UPDATE repositories SET remote = :remote, default_branch = :default_branch
 WHERE workspace_id = :workspace_id AND id = :id AND (remote <> :remote OR default_branch <> :default_branch)`)

	// stmtRepositoryHolder reads which project holds an identity in the installation's repository index,
	// by the project's name.
	stmtRepositoryHolder = defineInstallation("repository_index.holder",
		`SELECT i.project_id, COALESCE(p.name, i.project_id)
  FROM repository_index i LEFT JOIN projects p ON p.id = i.project_id
 WHERE i.identity = :identity`)

	stmtReleaseRepository = defineInstallation("repository_index.release",
		`DELETE FROM repository_index WHERE identity = :identity AND project_id = :project_id AND repository_id = :repository_id`)

	stmtRepositorySettings = define("repository.settings",
		`UPDATE repositories
   SET base_branch = :base_branch, forge_binding = :forge_binding, merge_method = :merge_method,
       review_target = :review_target, start_from = :start_from, merge_authority = :merge_authority
 WHERE workspace_id = :workspace_id AND id = :id`)
)

// repositoryTx is the repository area of one transaction.
type repositoryTx struct {
	*session
	scope ledger.WorkspaceID
	// index is where a write keeps the installation's repository index, or nil for a store with no
	// installation.
	index *session
}

func (t repositoryTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanRepository(row scanner) (ledger.Repository, error) {
	var r ledger.Repository
	var method, target, start, authority string
	err := row.Scan(&r.ID, &r.Remote, &r.DefaultBranch, &r.BaseBranch, &r.ForgeBinding, &method, &target, &start, &authority)
	r.MergeMethod, r.ReviewTarget, r.StartFrom = ledger.MergeMethod(method), ledger.ReviewTarget(target), ledger.StartFrom(start)
	r.MergeAuthority = ledger.MergeAuthority(authority)
	return r, err
}

func (t repositoryTx) Repository(id string) (ledger.Repository, error) {
	row, err := t.row(stmtRepository, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.Repository{}, err
	}
	record, err := scanRepository(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Repository{}, fmt.Errorf("repository %s: %w", id, ports.ErrNotFound)
	}
	return record, err
}

func (t repositoryTx) Repositories() ([]ledger.Repository, error) {
	rows, err := t.rows(stmtRepositories, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.Repository
	for rows.Next() {
		record, err := scanRepository(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func repositoryParams(r ledger.Repository) params {
	return params{
		"id": r.ID, "remote": r.Remote, "default_branch": r.DefaultBranch, "base_branch": r.BaseBranch,
		"forge_binding": r.ForgeBinding, "merge_method": string(r.MergeMethod), "review_target": string(r.Target()), "start_from": string(r.StartFrom),
		"merge_authority": string(r.Level()),
	}
}

// Insert adds a record, and a new one holds its identity in the installation's repository index in the
// same transaction, or is refused when another project holds it (ADR-0021).
func (t repositoryTx) Insert(r ledger.Repository) (bool, error) {
	n, err := t.exec(stmtInsertRepository, t.scoped(repositoryParams(r)))
	if err != nil || n == 0 {
		return false, err
	}
	return true, t.hold(ledger.RepositoryIdentity(r.Remote, r.ID), r.ID)
}

// Detected replaces what git said of a repository. A remote that moves takes the record's place in the
// index to the new identity, which another project must not hold.
func (t repositoryTx) Detected(id, remote, defaultBranch string) (bool, error) {
	var before ledger.Repository
	if t.index != nil {
		var err error
		if before, err = t.Repository(id); err != nil {
			return false, err
		}
	}
	n, err := t.exec(stmtDetectedRepository, t.scoped(params{"id": id, "remote": remote, "default_branch": defaultBranch}))
	if err != nil || n == 0 || t.index == nil {
		return n > 0, err
	}
	was, now := ledger.RepositoryIdentity(before.Remote, id), ledger.RepositoryIdentity(remote, id)
	if was == now {
		return true, nil
	}
	if _, err := t.index.exec(stmtReleaseRepository, params{"identity": was, "project_id": string(t.scope), "repository_id": id}); err != nil {
		return false, err
	}
	return true, t.hold(now, id)
}

// hold enters an identity in the repository index for this project, unless another project holds it,
// which the refusal names. A second clone of a repository the project holds is the same repository.
func (t repositoryTx) hold(identity, id string) error {
	if t.index == nil {
		return nil
	}
	if err := t.heldElsewhere(identity); err != nil {
		return err
	}
	n, err := t.index.exec(stmtIndexRepository, params{"identity": identity, "project_id": string(t.scope), "repository_id": id})
	if err != nil || n > 0 {
		return err
	}
	return t.heldElsewhere(identity)
}

// heldElsewhere refuses an identity another project holds.
func (t repositoryTx) heldElsewhere(identity string) error {
	row, err := t.index.row(stmtRepositoryHolder, params{"identity": identity})
	if err != nil {
		return err
	}
	var project, name string
	switch err := row.Scan(&project, &name); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	case ledger.WorkspaceID(project) != t.scope:
		return fmt.Errorf("%w: project %s holds %s", ledger.ErrRepositoryHeld, name, identity)
	}
	return nil
}

func (t repositoryTx) Settings(r ledger.Repository) error {
	_, err := t.exec(stmtRepositorySettings, t.scoped(repositoryParams(r)))
	return err
}

func (t *readTx) Repositories() ports.RepositoryReader {
	return repositoryTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Repositories() ports.RepositoryWriter {
	tx := repositoryTx{session: t.session, scope: t.scope}
	if t.index != nil {
		tx.index, _ = t.index.session(t.session, t.scope)
	}
	return tx
}

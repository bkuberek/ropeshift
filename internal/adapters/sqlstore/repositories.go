package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const repositoryColumns = `id, remote, default_branch, base_branch, forge_binding, merge_method, integration, start_from`

var (
	stmtInsertRepository = define("repository.insert",
		`INSERT INTO repositories
  (workspace_id, id, remote, default_branch, base_branch, forge_binding, merge_method, integration, start_from)
VALUES
  (:workspace_id, :id, :remote, :default_branch, :base_branch, :forge_binding, :merge_method, :integration, :start_from)
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

	stmtRepositorySettings = define("repository.settings",
		`UPDATE repositories
   SET base_branch = :base_branch, forge_binding = :forge_binding, merge_method = :merge_method,
       integration = :integration, start_from = :start_from
 WHERE workspace_id = :workspace_id AND id = :id`)
)

// repositoryTx is the repository area of one transaction.
type repositoryTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t repositoryTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanRepository(row scanner) (ledger.Repository, error) {
	var r ledger.Repository
	var method, start string
	var integration int64
	err := row.Scan(&r.ID, &r.Remote, &r.DefaultBranch, &r.BaseBranch, &r.ForgeBinding, &method, &integration, &start)
	r.MergeMethod, r.StartFrom, r.Integration = ledger.MergeMethod(method), ledger.StartFrom(start), integration != 0
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
	integration := int64(0)
	if r.Integration {
		integration = 1
	}
	return params{
		"id": r.ID, "remote": r.Remote, "default_branch": r.DefaultBranch, "base_branch": r.BaseBranch,
		"forge_binding": r.ForgeBinding, "merge_method": string(r.MergeMethod), "integration": integration, "start_from": string(r.StartFrom),
	}
}

func (t repositoryTx) Insert(r ledger.Repository) (bool, error) {
	n, err := t.exec(stmtInsertRepository, t.scoped(repositoryParams(r)))
	return n > 0, err
}

func (t repositoryTx) Detected(id, remote, defaultBranch string) (bool, error) {
	n, err := t.exec(stmtDetectedRepository, t.scoped(params{"id": id, "remote": remote, "default_branch": defaultBranch}))
	return n > 0, err
}

func (t repositoryTx) Settings(r ledger.Repository) error {
	_, err := t.exec(stmtRepositorySettings, t.scoped(repositoryParams(r)))
	return err
}

func (t *readTx) Repositories() ports.RepositoryReader {
	return repositoryTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Repositories() ports.RepositoryWriter {
	return repositoryTx{session: t.session, scope: t.scope}
}

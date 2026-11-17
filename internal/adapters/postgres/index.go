package postgres

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"ropeshift.com/ropeshift/internal/ledger"
)

// revisionWait bounds how long the repair of the repository index waits for a project's revision, which a
// write holds until its transaction ends. A project whose revision stays busy is left for the next Open.
const revisionWait = 5 * time.Second

// querier is what a pool and a transaction both run a query on.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// reconcileIndex releases the entries of the repository index that no record backs. A project write enters
// its repository in the index in a transaction that commits just before the project's, so a crash between
// the two, or a commit of the project's that fails, leaves an entry whose record never came. It blocks
// every other project from registering the repository until it goes, and only a retry in the same project
// completes it. SQLite rebuilds the index from the records at every start; this repairs the same entries on
// Postgres.
//
// It does not rebuild the index from the records, because another process may be in the middle of a write:
// a rebuild would release the entry of a write whose record is about to commit. The caller holds the
// migration lock, so no project appears or goes while it runs. The entries of a project the registry does not
// hold are released. For every other project, the entries whose record its schema does not hold are found
// first without a lock, which is all a start does when the index is sound. Each of those is checked again
// once the project's revision is taken, which a write holds from its first statement to its commit, so a
// write in the middle of its commit is finished by then and its record is found.
//
// The repair is not needed to open the state. A project it cannot check stays as it was, with a warning,
// and the next Open tries again. It fails only when the registry or the index cannot be read.
func (s *State) reconcileIndex(ctx context.Context) error {
	projects, err := s.Installation.Projects(ctx)
	if err != nil {
		return fmt.Errorf("postgres: repository index: %w", err)
	}
	index, err := s.Installation.RepositoryIndex(ctx)
	if err != nil {
		return fmt.Errorf("postgres: repository index: %w", err)
	}
	held := make(map[ledger.WorkspaceID][]ledger.IndexedRepository)
	for _, entry := range index {
		held[entry.Project] = append(held[entry.Project], entry)
	}
	for _, p := range projects {
		entries := held[p.ID]
		delete(held, p.ID)
		if len(entries) == 0 {
			continue
		}
		if err := s.reconcileProject(ctx, p, entries); err != nil {
			if err := s.skipped(ctx, "repository index of project "+p.Name, err); err != nil {
				return err
			}
		}
	}
	var orphans []ledger.IndexedRepository
	for _, entry := range index {
		if _, ok := held[entry.Project]; ok {
			orphans = append(orphans, entry)
		}
	}
	if len(orphans) == 0 {
		return nil
	}
	if err := s.Installation.ReleaseRepositories(ctx, orphans); err != nil {
		return s.skipped(ctx, "repository index entries of projects the registry does not hold", err)
	}
	return nil
}

// reconcileProject releases the entries of one project that its records do not back.
func (s *State) reconcileProject(ctx context.Context, p ledger.Project, entries []ledger.IndexedRepository) error {
	schema, err := schemaOf(p)
	if err != nil {
		return err
	}
	suspects, err := withoutRecord(ctx, s.pool, schema, p.ID, entries)
	if err != nil || len(suspects) == 0 {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL lock_timeout = %d`, revisionWait.Milliseconds())); err != nil {
		return err
	}
	// The row a write takes its revision from. Waiting for it waits for every write of the project that
	// began before this one, and keeps a later one out until the entries are released.
	var workspace string
	if err := tx.QueryRow(ctx, `SELECT id FROM `+quote(schema)+`.workspaces WHERE id = $1 FOR UPDATE`, string(p.ID)).Scan(&workspace); err != nil {
		return fmt.Errorf("take the revision: %w", err)
	}
	stale, err := withoutRecord(ctx, tx, schema, p.ID, suspects)
	if err != nil {
		return err
	}
	if len(stale) > 0 {
		if err := s.Installation.ReleaseRepositories(ctx, stale); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// withoutRecord returns the entries whose repository the project's schema holds no record of.
func withoutRecord(ctx context.Context, q querier, schema string, scope ledger.WorkspaceID, entries []ledger.IndexedRepository) ([]ledger.IndexedRepository, error) {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.Repository
	}
	rows, err := q.Query(ctx, `SELECT id FROM `+quote(schema)+`.repositories WHERE workspace_id = $1 AND id = ANY($2)`, string(scope), ids)
	if err != nil {
		return nil, err
	}
	recorded, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(entries), func(entry ledger.IndexedRepository) bool {
		return slices.Contains(recorded, entry.Repository)
	}), nil
}

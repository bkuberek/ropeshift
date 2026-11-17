package postgres

import (
	"context"
	"fmt"
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

// repositoryRecord is what a project's repository record gives the repository index: its ID, its remote,
// and whether it is removed from the project.
type repositoryRecord struct {
	id, remote, state string
}

// recordedRepositories reads a project's repository records, by ID, through a pool or a transaction.
func recordedRepositories(ctx context.Context, q querier, schema string, scope ledger.WorkspaceID) ([]repositoryRecord, error) {
	rows, err := q.Query(ctx, `SELECT id, remote, state FROM `+quote(schema)+`.repositories WHERE workspace_id = $1 ORDER BY id`, string(scope))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (repositoryRecord, error) {
		var r repositoryRecord
		err := row.Scan(&r.id, &r.remote, &r.state)
		return r, err
	})
}

// indexedIdentities gives the identity each record of a project that is not removed holds, by record ID,
// and the entries a rebuild of the project's part of the index holds: one for each identity, the first
// record by ID taking it, because records of one repository share one entry.
func indexedIdentities(records []repositoryRecord, scope ledger.WorkspaceID) (identityOf map[string]string, entries []ledger.IndexedRepository) {
	identityOf = make(map[string]string, len(records))
	taken := make(map[string]bool, len(records))
	for _, r := range records {
		if r.state == string(ledger.RepositoryRemoved) {
			continue
		}
		identity := ledger.RepositoryIdentity(r.remote, r.id)
		identityOf[r.id] = identity
		if !taken[identity] {
			taken[identity] = true
			entries = append(entries, ledger.IndexedRepository{Identity: identity, Project: scope, Repository: r.id})
		}
	}
	return identityOf, entries
}

// indexAgrees reports whether a project's entries in the index agree with its records: each entry names a
// record that is not removed, at the identity it holds, and between them the entries hold the identity of
// every such record. An entry may name another record of the same repository than the first, which is as
// good.
func indexAgrees(entries []ledger.IndexedRepository, records []repositoryRecord, scope ledger.WorkspaceID) bool {
	identityOf, wanted := indexedIdentities(records, scope)
	if len(entries) != len(wanted) {
		return false
	}
	// The index holds an identity once, so entries that each name a record's identity and are as many as
	// the identities the records hold are those identities.
	for _, entry := range entries {
		if identity, ok := identityOf[entry.Repository]; !ok || identity != entry.Identity {
			return false
		}
	}
	return true
}

// reconcileIndex brings the repository index into agreement with the records. A project write enters and
// releases identities in the index in a transaction that commits just before the project's, so a crash
// between the two, or a commit of the project's that fails, leaves the index disagreeing with the records:
// an entry whose record never came, or whose record is still removed, or the identity of a remote move that
// did not happen, with the record's own identity free for another project. SQLite rebuilds the index from
// the records at every start; this repairs the same on Postgres.
//
// It does not rebuild the whole index at once, because another process may be in the middle of a write: a
// rebuild would release the entry of a write whose record is about to commit. The caller holds the migration
// lock, so no project appears or goes while it runs. The entries of a project the registry does not hold are
// released. Every other project is checked without a lock, which is all a start does when the index is
// sound, and a project whose entries disagree with its records is rebuilt by rebuildProjectIndex, which
// checks again once the project's revision is taken.
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

// reconcileProject rebuilds a project's part of the index when its entries disagree with its records.
func (s *State) reconcileProject(ctx context.Context, p ledger.Project, entries []ledger.IndexedRepository) error {
	schema, err := schemaOf(p)
	if err != nil {
		return err
	}
	records, err := recordedRepositories(ctx, s.pool, schema, p.ID)
	if err != nil || indexAgrees(entries, records, p.ID) {
		return err
	}
	return s.rebuildProjectIndex(ctx, p)
}

// repairProjectIndex rebuilds a project's part of the repository index from its committed records, for a
// caller that does not hold the migration lock, such as one that saw a write of the project fail after the
// index had committed its part. It takes the lock, as Open does.
func (s *State) repairProjectIndex(ctx context.Context, p ledger.Project) error {
	return s.locked(ctx, func(ctx context.Context) error { return s.rebuildProjectIndex(ctx, p) })
}

// rebuildProjectIndex recomputes a project's entries in the repository index from the repository records
// it has committed, when they disagree, and leaves every other project's entries alone. The caller holds the
// migration lock.
//
// It takes the project's revision first, the row a write takes its revision from and holds until its
// transaction ends, so a write of the project that has committed its part of the index and not yet its
// records is finished by the time the records are read, and a later write waits until the entries are
// replaced. The records are read and compared again under that revision, because the first look was
// without it. A record whose identity another project holds in the index gets no entry, as in SQLite's
// rebuild, and is reported with a warning.
func (s *State) rebuildProjectIndex(ctx context.Context, p ledger.Project) error {
	schema, err := schemaOf(p)
	if err != nil {
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
	var workspace string
	if err := tx.QueryRow(ctx, `SELECT id FROM `+quote(schema)+`.workspaces WHERE id = $1 FOR UPDATE`, string(p.ID)).Scan(&workspace); err != nil {
		return fmt.Errorf("take the revision: %w", err)
	}
	records, err := recordedRepositories(ctx, tx, schema, p.ID)
	if err != nil {
		return err
	}
	index, err := s.Installation.RepositoryIndex(ctx)
	if err != nil {
		return err
	}
	var entries []ledger.IndexedRepository
	for _, entry := range index {
		if entry.Project == p.ID {
			entries = append(entries, entry)
		}
	}
	if !indexAgrees(entries, records, p.ID) {
		_, wanted := indexedIdentities(records, p.ID)
		conflicts, err := s.Installation.ReplaceProjectRepositories(ctx, p.ID, wanted)
		if err != nil {
			return err
		}
		for _, c := range conflicts {
			s.warn("postgres: repository index: project %s keeps a record of %s, which another project holds in the index", p.Name, c.Identity)
		}
	}
	return tx.Commit(ctx)
}

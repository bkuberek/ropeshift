package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// sweep drops the schemas that carry a project's prefix and that no registered project names: what a Create
// left that stopped after it made the schema, such as one that gave itself an ID and so can never be
// retried, and what a Remove left that stopped after the registry forgot the project. The caller holds the
// migration lock, which every step that makes or drops a project's schema holds as well, so a schema no
// project names is not one a Create is about to register.
//
// The prefix belongs to ropeshift in the database it uses: a schema of that form that a person made is
// dropped too.
func (s *State) sweep(ctx context.Context) error {
	projects, err := s.Installation.Projects(ctx)
	if err != nil {
		return fmt.Errorf("postgres: sweep: %w", err)
	}
	named := make(map[string]bool, len(projects))
	for _, p := range projects {
		named[p.Database] = true
	}
	rows, err := s.pool.Query(ctx, `SELECT nspname FROM pg_namespace WHERE left(nspname, $1) = $2`, len(schemaPrefix), schemaPrefix)
	if err != nil {
		return fmt.Errorf("postgres: sweep: %w", err)
	}
	// The rows are read to the end before a schema drops, because the drop needs a connection too.
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("postgres: sweep: %w", err)
	}
	for _, name := range found {
		if named[name] || !projectSchema(name) {
			continue
		}
		if _, err := s.pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+quote(name)+` CASCADE`); err != nil {
			return fmt.Errorf("postgres: sweep: drop schema %s: %w", name, err)
		}
	}
	return nil
}

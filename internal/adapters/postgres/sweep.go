package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ulidAlphabet is the alphabet ledger.EncodeULID writes, which is the alphabet of a generated project ID.
const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ulidLength is the length of a generated project ID.
const ulidLength = 26

// generatedSchema reports whether a schema's name is the one a generated project ID gives: the prefix and
// a ULID, in lowercase as schemaName writes it, or in capitals as an earlier build kept it. A ULID's first
// character holds the top three bits of its timestamp, so it is never above 7. A name a person chose, such
// as a copy of a project's schema, does not have this shape.
func generatedSchema(name string) bool {
	id, ok := strings.CutPrefix(name, schemaPrefix)
	if !ok || len(id) != ulidLength || id != strings.ToUpper(id) && id != strings.ToLower(id) {
		return false
	}
	id = strings.ToUpper(id)
	return id[0] <= '7' && !strings.ContainsFunc(id, func(c rune) bool { return !strings.ContainsRune(ulidAlphabet, c) })
}

// sweep drops the schemas that carry the name of a generated project ID and that no registered project
// names: what a Create left that stopped after it made the schema, such as one that gave itself an ID and so
// can never be retried, and what a Remove left that stopped after the registry forgot the project. The
// caller holds the migration lock, which every step that makes or drops a project's schema holds as well,
// so a schema no project names is not one a Create is about to register.
//
// A schema whose name only starts with the prefix is a person's, such as a copy of a project's schema, and
// stays. A schema that cannot be listed or dropped stays too, with a warning, and the next Open tries
// again: the sweep fails only when the registry cannot be read.
func (s *State) sweep(ctx context.Context) error {
	projects, err := s.Installation.Projects(ctx)
	if err != nil {
		return fmt.Errorf("postgres: sweep: %w", err)
	}
	named := make(map[string]bool, len(projects))
	for _, p := range projects {
		named[p.Database] = true
	}
	found, err := s.prefixed(ctx)
	if err != nil {
		return s.skipped(ctx, "list the schemas", err)
	}
	for _, name := range found {
		if named[name] || !generatedSchema(name) {
			continue
		}
		if _, err := s.pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+quote(name)+` CASCADE`); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			s.warn("postgres: sweep: drop schema %s: %v", name, err)
		}
	}
	return nil
}

// prefixed lists the schemas whose names start with a project's prefix.
func (s *State) prefixed(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT nspname FROM pg_namespace WHERE left(nspname, $1) = $2`, len(schemaPrefix), schemaPrefix)
	if err != nil {
		return nil, err
	}
	// The rows are read to the end before a schema drops, because the drop needs a connection too.
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// skipped leaves a step of the sweep for the next Open, with a warning. A caller whose own context ended
// gets that error, because it is not the database's.
func (s *State) skipped(ctx context.Context, what string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	s.warn("postgres: sweep: %s: %v", what, err)
	return nil
}

// warn tells a person what the state goes on past, when Options.Store has a Warn.
func (s *State) warn(format string, args ...any) {
	if s.opts.Warn != nil {
		s.opts.Warn(fmt.Sprintf(format, args...))
	}
}

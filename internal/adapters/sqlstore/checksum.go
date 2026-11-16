package sqlstore

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
)

// A database records the checksum of each migration it applies. A start refuses a database whose
// applied migrations differ from the ones this build ships, because the schema such a database holds is
// not the one the statements expect.
var (
	// ErrMigrationChanged is a migration whose text changed after a database applied it.
	ErrMigrationChanged = errors.New("an applied migration changed")
	// ErrMigrationNotShipped is a migration a database applied that this build does not ship.
	ErrMigrationNotShipped = errors.New("an applied migration is not shipped")
)

// Checksum is the SHA-256 of a migration's text exactly as it ships, in lowercase hex.
func Checksum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// MigrationError names an applied migration a start refuses. It wraps ErrMigrationChanged or
// ErrMigrationNotShipped.
type MigrationError struct {
	// Set names the migrations: ledger, installation, journal, or telemetry.
	Set     string
	Version int64
	// File is the migration's file name.
	File string
	// Recorded is the checksum the database recorded, empty when it recorded none. Shipped is the
	// checksum of the file this build ships, empty when it ships none.
	Recorded string
	Shipped  string
	Err      error
}

func (e *MigrationError) Error() string {
	recorded := cmp.Or(e.Recorded, "none")
	if errors.Is(e.Err, ErrMigrationNotShipped) {
		return fmt.Sprintf("%s migration %d (%s) was applied, and this build does not ship it: recorded sha256 %s",
			e.Set, e.Version, e.File, recorded)
	}
	return fmt.Sprintf("%s migration %d (%s) changed after it was applied: recorded sha256 %s, shipped sha256 %s",
		e.Set, e.Version, e.File, recorded, e.Shipped)
}

func (e *MigrationError) Unwrap() error { return e.Err }

// statementReadColumns names the dialect's statement that lists a table's columns.
const statementReadColumns = "migration.columns"

// hasChecksum reads whether schema_migrations, in the store's schema, has its checksum column.
func (s *Store) hasChecksum(ctx context.Context, tx *sql.Tx) (bool, error) {
	se := s.session(ctx, tx, ModeMigrate, statementReadColumns)
	rows, err := se.rows(&statement{name: statementReadColumns, text: s.dialect.ColumnsSQL()}, params{"table": "schema_migrations"})
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return false, fmt.Errorf("%s: %w", statementReadColumns, err)
		}
		columns = append(columns, column)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, fmt.Errorf("%s: %w", statementReadColumns, err)
	}
	return slices.Contains(columns, "checksum"), nil
}

// appliedMigration is one row of schema_migrations.
type appliedMigration struct {
	version  int64
	name     string
	checksum sql.NullString
}

// checkApplied compares each migration the database applied with the one this build ships under its
// version, in one transaction. It records the checksum of one an earlier build applied without one. It
// refuses, naming each, one whose text changed and one this build no longer ships, and then records
// nothing.
func (s *Store) checkApplied(ctx context.Context, set string, shipped []Migration) error {
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	se := s.session(ctx, tx, ModeMigrate, "migration.check")
	applied, err := listApplied(se)
	if err != nil {
		return err
	}
	byVersion := make(map[int64]Migration, len(shipped))
	for _, m := range shipped {
		byVersion[m.Version] = m
	}
	var refused []error
	for _, a := range applied {
		m, ok := byVersion[a.version]
		if !ok {
			refused = append(refused, &MigrationError{
				Set: set, Version: a.version, File: a.name + ".sql", Recorded: a.checksum.String, Err: ErrMigrationNotShipped,
			})
			continue
		}
		sum := Checksum(m.SQL)
		switch {
		case !a.checksum.Valid:
			if _, err := se.exec(stmtBackfillChecksum, params{"version": a.version, "checksum": sum}); err != nil {
				return err
			}
		case a.checksum.String != sum:
			refused = append(refused, &MigrationError{
				Set: set, Version: a.version, File: m.Name + ".sql", Recorded: a.checksum.String, Shipped: sum, Err: ErrMigrationChanged,
			})
		}
	}
	if len(refused) > 0 {
		return errors.Join(refused...)
	}
	return tx.Commit()
}

// listApplied reads every row of schema_migrations, in version order.
func listApplied(se *session) ([]appliedMigration, error) {
	rows, err := se.rows(stmtListMigrations, params{})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []appliedMigration
	for rows.Next() {
		var a appliedMigration
		if err := rows.Scan(&a.version, &a.name, &a.checksum); err != nil {
			return nil, fmt.Errorf("%s: %w", stmtListMigrations.name, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

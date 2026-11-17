package sqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/version"
)

// A database records the checksum of each migration it applies. A start refuses a database whose
// applied migration changed, because the schema such a database holds is not the one the statements
// expect. A migration the database applied that this build does not ship, as a later build leaves, is
// only warned of, so an earlier build starts again after an upgrade.

// ErrMigrationChanged is a migration whose text changed after a database applied it.
var ErrMigrationChanged = errors.New("an applied migration changed")

// refusalPage is the user doc that says what a person does about a refusal.
const refusalPage = "docs/troubleshooting.md#database-refuses-to-start"

// refusalNextStep is what a person does about a refusal, and the link to the doc that says more, which names
// the tag of the release that refuses.
func refusalNextStep() string {
	return "to start, run the build that migrated this database, or restore the state directory from a backup; " +
		refusalPage + " says how: " + version.DocsURL(refusalPage)
}

// Checksum is the SHA-256 of a migration's text exactly as it ships, in lowercase hex.
func Checksum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// MigrationError names an applied migration whose text changed, which a start refuses. It wraps
// ErrMigrationChanged.
type MigrationError struct {
	// Set names the migrations: ledger, installation, journal, or telemetry.
	Set     string
	Version int64
	// File is the migration's file name.
	File string
	// Recorded is the checksum the database recorded, and Shipped the checksum of the file this build
	// ships.
	Recorded string
	Shipped  string
}

func (e *MigrationError) Error() string {
	return fmt.Sprintf("%s migration %d (%s) changed after it was applied: recorded sha256 %s, shipped sha256 %s",
		e.Set, e.Version, e.File, e.Recorded, e.Shipped)
}

func (e *MigrationError) Unwrap() error { return ErrMigrationChanged }

// Refusal is a start's refusal of a database: each applied migration that changed, and then once what a
// person does next.
func Refusal(changed []*MigrationError) error {
	errs := make([]error, len(changed))
	for i, e := range changed {
		errs[i] = e
	}
	return fmt.Errorf("%w\n%s", errors.Join(errs...), refusalNextStep())
}

// Unshipped is a migration a database applied that this build does not ship.
type Unshipped struct {
	Version int64
	// File is the migration's file name.
	File string
}

// UnshippedWarning is the one warning a start gives for the migrations of a set the database applied
// and this build does not ship. The start goes on with them in place.
func UnshippedWarning(set string, unshipped []Unshipped) string {
	named := make([]string, len(unshipped))
	for i, u := range unshipped {
		named[i] = fmt.Sprintf("%d (%s)", u.Version, u.File)
	}
	return fmt.Sprintf("the %s database holds migrations this build does not ship: %s; a later build migrated it, and this build starts on the schema it left",
		set, strings.Join(named, ", "))
}

// WarnFor wraps warn so each warning it receives names the database it is about, as "database: warning".
// A start that opens several ledgers gives each one's warning the same text, and the name tells them
// apart. A nil warn stays nil.
func WarnFor(warn func(string), database string) func(string) {
	if warn == nil {
		return nil
	}
	return func(warning string) { warn(database + ": " + warning) }
}

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
// refuses, naming each, one whose text changed, and then records nothing. It warns once of those this
// build does not ship, and goes on.
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
	var changed []*MigrationError
	var unshipped []Unshipped
	for _, a := range applied {
		m, ok := byVersion[a.version]
		if !ok {
			unshipped = append(unshipped, Unshipped{Version: a.version, File: a.name + ".sql"})
			continue
		}
		sum := Checksum(m.SQL)
		switch {
		case !a.checksum.Valid:
			if _, err := se.exec(stmtBackfillChecksum, params{"version": a.version, "checksum": sum}); err != nil {
				return err
			}
		case a.checksum.String != sum:
			changed = append(changed, &MigrationError{
				Set: set, Version: a.version, File: m.Name + ".sql", Recorded: a.checksum.String, Shipped: sum,
			})
		}
	}
	if len(changed) > 0 {
		return Refusal(changed)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(unshipped) > 0 && s.warn != nil {
		s.warn(UnshippedWarning(set, unshipped))
	}
	return nil
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

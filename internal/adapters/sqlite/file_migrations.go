package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
)

// fileMigrations are the schema files of a database the worker keeps for itself: the journal or the
// telemetry store. Each records the files it applied by name in a table of its own, because it is local
// and shares no versions with a ledger.
type fileMigrations struct {
	// set names the files in an error.
	set     string
	table   string
	files   fs.FS
	pattern string
}

var (
	journalMigrations = fileMigrations{
		set: "journal", table: "journal_migrations", files: journalFiles, pattern: "journal_migrations/*.sql",
	}
	telemetryMigrations = fileMigrations{
		set: "telemetry", table: "telemetry_migrations", files: telemetryFiles, pattern: "telemetry_migrations/*.sql",
	}
)

// migrate applies each file once, in name order, after it checks the files the database applied before
// against the ones this build ships, as the Store's migrations do. warn receives the warning for files
// this build does not ship, or is nil.
func (m fileMigrations) migrate(ctx context.Context, db *sql.DB, warn func(string)) error {
	if err := m.createTable(ctx, db); err != nil {
		return fmt.Errorf("%s migrations: %w", m.set, err)
	}
	names, err := fs.Glob(m.files, m.pattern)
	if err != nil {
		return err
	}
	slices.Sort(names)
	shipped := make(map[string]string, len(names))
	for _, name := range names {
		text, err := fs.ReadFile(m.files, name)
		if err != nil {
			return err
		}
		shipped[name] = string(text)
	}
	if err := m.checkApplied(ctx, db, shipped, warn); err != nil {
		return err
	}
	for _, name := range names {
		if err := m.apply(ctx, db, name, shipped[name]); err != nil {
			return fmt.Errorf("%s migration %s: %w", m.set, name, err)
		}
	}
	return nil
}

// createTable makes the table of applied files, and gives a table an earlier build made its checksum
// column. These databases are SQLite files only, whose transactions take the write lock as they begin,
// so two starts at once cannot both find the column missing.
func (m fileMigrations) createTable(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+m.table+` (name TEXT PRIMARY KEY, checksum TEXT)`); err != nil {
		return err
	}
	var has int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?1) WHERE name = 'checksum'`, m.table).Scan(&has); err != nil {
		return err
	}
	if has == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE `+m.table+` ADD COLUMN checksum TEXT`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// checkApplied compares each file the database applied with the one this build ships under its name, in
// one transaction. It records the checksum of one an earlier build applied without one. It refuses,
// naming each, one whose text changed, and then records nothing. It warns once of those this build does
// not ship, and goes on.
func (m fileMigrations) checkApplied(ctx context.Context, db *sql.DB, shipped map[string]string, warn func(string)) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	type applied struct {
		name     string
		checksum sql.NullString
	}
	rows, err := tx.QueryContext(ctx, `SELECT name, checksum FROM `+m.table+` ORDER BY name`)
	if err != nil {
		return err
	}
	var recorded []applied
	for rows.Next() {
		var a applied
		if err := rows.Scan(&a.name, &a.checksum); err != nil {
			_ = rows.Close()
			return err
		}
		recorded = append(recorded, a)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var changed []*sqlstore.MigrationError
	var unshipped []sqlstore.Unshipped
	for _, a := range recorded {
		// A name with no version is still named, under version 0.
		version, _ := migrationVersion(a.name)
		text, ok := shipped[a.name]
		if !ok {
			unshipped = append(unshipped, sqlstore.Unshipped{Version: version, File: path.Base(a.name)})
			continue
		}
		sum := sqlstore.Checksum(text)
		switch {
		case !a.checksum.Valid:
			if _, err := tx.ExecContext(ctx, `UPDATE `+m.table+` SET checksum = ?1 WHERE name = ?2`, sum, a.name); err != nil {
				return err
			}
		case a.checksum.String != sum:
			changed = append(changed, &sqlstore.MigrationError{
				Set: m.set, Version: version, File: path.Base(a.name), Recorded: a.checksum.String, Shipped: sum,
			})
		}
	}
	if len(changed) > 0 {
		return sqlstore.Refusal(changed)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(unshipped) > 0 && warn != nil {
		warn(sqlstore.UnshippedWarning(m.set, unshipped))
	}
	return nil
}

// apply runs one file and records it with its checksum, in one transaction, unless the database applied
// it before.
func (m fileMigrations) apply(ctx context.Context, db *sql.DB, name, text string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var applied int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+m.table+` WHERE name = ?1`, name).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, text); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+m.table+` (name, checksum) VALUES (?1, ?2)`, name, sqlstore.Checksum(text)); err != nil {
		return err
	}
	return tx.Commit()
}

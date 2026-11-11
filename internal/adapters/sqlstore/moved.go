package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
)

// sharedTable is one table of records every project shares, which a single ledger held before the
// installation database did (ADR-0021).
type sharedTable struct {
	name string
	// key is the primary key, besides workspace_id.
	key []string
	// columns are every column besides workspace_id.
	columns []string
	// where narrows the rows to the shared ones, in a table whose other rows stay in the project.
	where string
	// read selects a scope's rows, insert copies one under another scope and keeps a row already there,
	// has counts a row by its key, and drop deletes it.
	read, insert, has, drop *statement
}

func newSharedTable(name, where string, key []string, columns ...string) *sharedTable {
	t := &sharedTable{name: name, key: key, columns: columns, where: where}
	narrow := ""
	if where != "" {
		narrow = " AND " + where
	}
	byKey := make([]string, len(key))
	for i, column := range key {
		byKey[i] = column + " = :" + column
	}
	matches := "workspace_id = :workspace_id AND " + strings.Join(byKey, " AND ")
	t.read = defineInstallation("shared."+name+".read",
		`SELECT `+strings.Join(columns, ", ")+` FROM `+name+` WHERE workspace_id = :workspace_id`+narrow+` ORDER BY `+strings.Join(key, ", "))
	t.insert = defineInstallation("shared."+name+".insert",
		`INSERT INTO `+name+` (workspace_id, `+strings.Join(columns, ", ")+`)
VALUES (:workspace_id, :`+strings.Join(columns, ", :")+`)
ON CONFLICT DO NOTHING`)
	t.has = defineInstallation("shared."+name+".has", `SELECT count(*) FROM `+name+` WHERE `+matches)
	t.drop = defineInstallation("shared."+name+".drop", `DELETE FROM `+name+` WHERE `+matches)
	return t
}

// sharedTables are the records the installation holds for every project: members and their sessions,
// workers with their join grants, autonomy, and run windows, orchestrators, and the autonomy policy.
var sharedTables = []*sharedTable{
	newSharedTable("members", "", []string{"id"}, "id", "identity_subject", "display_name", "status", "proof_digest"),
	newSharedTable("member_sessions", "", []string{"digest"}, "digest", "member_id", "created_at", "expires_at"),
	newSharedTable("join_grants", "", []string{"id"}, "id", "digest", "created_by", "used_by"),
	newSharedTable("workers", "", []string{"id"}, "id", "name", "epoch", "protocol", "capabilities", "proof_digest", "last_handshake_at"),
	newSharedTable("worker_autonomy", "", []string{"worker_id"}, "worker_id", "level", "acknowledged_by", "acknowledged_kind", "acknowledged_digest", "acknowledged_at"),
	newSharedTable("run_windows", "", []string{"id"}, "id", "worker_id", "opened_by", "opened_kind", "opened_at", "closes_at", "closed_by", "closed_at"),
	newSharedTable("orchestrators", "", []string{"id"}, "id", "worker_id", "desired_state", "run", "resets", "actual_state", "generation", "conditions", "reported_at", "settings"),
	newSharedTable("policy_versions", "kind = 'autonomy'", []string{"kind", "version"}, "kind", "version", "id", "digest", "body", "settings", "authored_by", "created_at"),
}

// readShared reads a scope's rows of a shared table, each as the parameters that name its columns.
func readShared(se *session, table *sharedTable, scope ledger.WorkspaceID) ([]params, error) {
	rows, err := se.rows(table.read, params{"workspace_id": string(scope)})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []params
	for rows.Next() {
		values := make([]any, len(table.columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, fmt.Errorf("%s: %w", table.read.name, err)
		}
		row := params{}
		for i, column := range table.columns {
			row[column] = values[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// recordsMoved is the data of EventRecordsMoved: the project the records left, and how many rows of
// each table the installation took.
type recordsMoved struct {
	Project ledger.WorkspaceID `json:"project"`
	Records map[string]int64   `json:"records"`
}

// CopyRecords copies the shared records a project's ledger holds under its scope into the installation,
// under the installation's own, in one transaction that records what it took. A row the installation
// already holds stays as it is, so a repeated copy changes nothing and logs nothing. The ledger keeps
// its rows until DropMovedRecords. It is the second step of an upgrade from one ledger, which runs before
// the daemon serves anything, so the rows it read stand when it writes them. It reads them first, so it
// never holds the installation's lock while it waits for the ledger's.
func (in *Installation) CopyRecords(ctx context.Context, from *Store, scope ledger.WorkspaceID, actor ledger.Actor) (map[string]int64, error) {
	held, err := from.sharedRows(ctx, scope)
	if err != nil {
		return nil, err
	}
	moved := map[string]int64{}
	err = in.write(ctx, "installation.copy", func(t *installationTx) error {
		total := int64(0)
		for _, table := range sharedTables {
			for _, row := range held[table.name] {
				row["workspace_id"] = string(ledger.InstallationScope)
				n, err := t.exec(table.insert, row)
				if err != nil {
					return err
				}
				moved[table.name] += n
				total += n
			}
		}
		if total == 0 {
			return nil
		}
		data, err := json.Marshal(recordsMoved{Project: scope, Records: maps.Clone(moved)})
		if err != nil {
			return err
		}
		if err := t.append(ledger.EventDraft{
			Type: ledger.EventRecordsMoved, Actor: actor, Data: data,
			Subject: ledger.Subject{Kind: ledger.SubjectProject, ID: string(scope)},
		}); err != nil {
			return err
		}
		crashpoint.Hit("installation.upgrade", "copying")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return moved, nil
}

// sharedRows reads every shared record a ledger holds under a scope, in one view, by table.
func (s *Store) sharedRows(ctx context.Context, scope ledger.WorkspaceID) (map[string][]params, error) {
	tx, err := s.begin(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	se := s.session(ctx, tx, ModeRead, "installation.copy")
	out := map[string][]params{}
	for _, table := range sharedTables {
		rows, err := readShared(se, table, scope)
		if err != nil {
			return nil, err
		}
		out[table.name] = rows
	}
	return out, nil
}

// DropMovedRecords deletes the shared records a project's ledger holds under its scope, in one
// transaction of the ledger, once the installation holds each of them. A row the installation lacks
// stops it before anything is deleted, so it can never lose a record a copy did not take. It is a
// migration of the ledger, like a schema change, so it takes no revision and appends no event: the
// installation's log recorded the copy. It is the third step of an upgrade from one ledger.
func (in *Installation) DropMovedRecords(ctx context.Context, from *Store, scope ledger.WorkspaceID) error {
	tx, err := from.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ledgerSide := from.session(ctx, tx, ModeMigrate, "installation.drop")
	err = in.read(ctx, "installation.drop", func(installationSide *session) error {
		for _, table := range sharedTables {
			rows, err := readShared(ledgerSide, table, scope)
			if err != nil {
				return err
			}
			for _, row := range rows {
				key := params{"workspace_id": string(ledger.InstallationScope)}
				for _, column := range table.key {
					key[column] = row[column]
				}
				if err := hasCopy(installationSide, table, key); err != nil {
					return err
				}
				key["workspace_id"] = string(scope)
				if _, err := ledgerSide.exec(table.drop, key); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	crashpoint.Hit("installation.upgrade", "deleting")
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("installation.drop: commit: %w", err)
	}
	return nil
}

// hasCopy refuses a row the installation does not hold.
func hasCopy(se *session, table *sharedTable, key params) error {
	row, err := se.row(table.has, key)
	if err != nil {
		return err
	}
	var n int64
	if err := row.Scan(&n); err != nil {
		return fmt.Errorf("%s: %w", table.has.name, err)
	}
	if n == 0 {
		return fmt.Errorf("%s row %v is not in the installation yet, so the ledger keeps it", table.name, key)
	}
	return nil
}

// stmtListWorkspaces names every workspace a ledger holds.
var stmtListWorkspaces = define("workspace.list", `SELECT id FROM workspaces ORDER BY id`)

// Workspaces lists every workspace this database holds. An upgrade reads it to learn the workspace a
// ledger's first project keeps.
func (s *Store) Workspaces(ctx context.Context) ([]ledger.WorkspaceID, error) {
	tx, err := s.begin(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := s.session(ctx, tx, ModeRead, "workspace.list").rows(stmtListWorkspaces, params{})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.WorkspaceID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, ledger.WorkspaceID(id))
	}
	return out, rows.Err()
}

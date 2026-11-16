package contract

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// ScopeMigrationVersion is the schema change that rewrites the scope of a stored decision into the grammar.
const ScopeMigrationVersion = 130

// ScopeRewriteCheck holds the migration that gives each stored decision scope its prefix (ADR-0030). It
// builds the few columns the migration reads in a database it owns, stores scopes that equal exactly one
// record, two kinds of record, no record, and a scope already in the grammar, runs the migration's own SQL,
// and reads the scopes back. The database is SQLite or Postgres: the same text runs on both.
func ScopeRewriteCheck(ctx context.Context, db *sql.DB, migration string) error {
	for _, text := range []string{
		`CREATE TABLE plans (workspace_id TEXT NOT NULL, id TEXT NOT NULL)`,
		`CREATE TABLE contracts (workspace_id TEXT NOT NULL, id TEXT NOT NULL, version BIGINT NOT NULL)`,
		`CREATE TABLE work_orders (workspace_id TEXT NOT NULL, id TEXT NOT NULL)`,
		`CREATE TABLE repositories (workspace_id TEXT NOT NULL, id TEXT NOT NULL)`,
		`CREATE TABLE decisions (workspace_id TEXT NOT NULL, id TEXT NOT NULL, scope TEXT)`,
		`INSERT INTO plans VALUES ('w', 'only-plan'), ('w', 'plan-and-contract'), ('w', 'plan-and-order'), ('x', 'other-workspace-plan')`,
		`INSERT INTO contracts VALUES ('w', 'only-contract', 1), ('w', 'only-contract', 2), ('w', 'plan-and-contract', 1)`,
		`INSERT INTO work_orders VALUES ('w', 'only-order'), ('w', 'plan-and-order'), ('w', 'order-and-repo')`,
		`INSERT INTO repositories VALUES ('w', 'only-repo'), ('w', 'order-and-repo')`,
	} {
		if _, err := db.ExecContext(ctx, text); err != nil {
			return fmt.Errorf("%w: %s", err, text)
		}
	}
	stored := map[string]any{
		"d-plan": "only-plan", "d-contract": "only-contract", "d-order": "only-order", "d-repo": "only-repo",
		"d-two-a": "plan-and-contract", "d-two-b": "plan-and-order", "d-two-c": "order-and-repo",
		"d-none": "retries", "d-path": "payments/retries", "d-typed": "plan:only-plan", "d-empty": "", "d-null": nil,
		"d-prefix-like": "only-plan/extra", "d-other-workspace": "other-workspace-plan",
	}
	for id, scope := range stored {
		if _, err := db.ExecContext(ctx, `INSERT INTO decisions (workspace_id, id, scope) VALUES ('w', '`+id+`', `+literal(scope)+`)`); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO decisions (workspace_id, id, scope) VALUES ('x', 'd-plan', 'other-workspace-plan')`); err != nil {
		return err
	}
	for _, text := range statementsOf(migration) {
		if _, err := db.ExecContext(ctx, text); err != nil {
			return fmt.Errorf("%w: %s", err, text)
		}
	}
	want := map[string]string{
		"w/d-plan": "plan:only-plan", "w/d-contract": "contract:only-contract", "w/d-order": "wo:only-order", "w/d-repo": "repo:only-repo",
		"w/d-two-a": "plan-and-contract", "w/d-two-b": "plan-and-order", "w/d-two-c": "order-and-repo",
		"w/d-none": "retries", "w/d-path": "payments/retries", "w/d-typed": "plan:only-plan", "w/d-empty": "", "w/d-null": "",
		"w/d-prefix-like": "only-plan/extra", "w/d-other-workspace": "other-workspace-plan",
		"x/d-plan": "plan:other-workspace-plan",
	}
	rows, err := db.QueryContext(ctx, `SELECT workspace_id, id, COALESCE(scope, '') FROM decisions`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var workspace, id, scope string
		if err := rows.Scan(&workspace, &id, &scope); err != nil {
			return err
		}
		got[workspace+"/"+id] = scope
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var wrong []string
	for key, scope := range want {
		if got[key] != scope {
			wrong = append(wrong, fmt.Sprintf("%s is %q, want %q", key, got[key], scope))
		}
	}
	if len(got) != len(want) || len(wrong) != 0 {
		slices.Sort(wrong)
		return Violatedf("after the migration %d scopes are stored and %d were written: %s", len(got), len(want), strings.Join(wrong, "; "))
	}
	// The migration reads what is stored and writes the grammar only, so a second run changes nothing.
	for _, text := range statementsOf(migration) {
		if _, err := db.ExecContext(ctx, text); err != nil {
			return err
		}
	}
	var again string
	if err := db.QueryRowContext(ctx, `SELECT scope FROM decisions WHERE workspace_id = 'w' AND id = 'd-plan'`).Scan(&again); err != nil {
		return err
	}
	if again != "plan:only-plan" {
		return Violatedf("a second run left %q", again)
	}
	return nil
}

func literal(value any) string {
	if value == nil {
		return "NULL"
	}
	return "'" + strings.ReplaceAll(value.(string), "'", "''") + "'"
}

// statementsOf cuts a migration file at each statement end, after its comment lines.
func statementsOf(text string) []string {
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			kept = append(kept, line)
		}
	}
	var out []string
	for part := range strings.SplitSeq(strings.Join(kept, "\n"), ";") {
		if statement := strings.TrimSpace(part); statement != "" {
			out = append(out, statement)
		}
	}
	return out
}

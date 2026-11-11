package contract

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
)

// A repository's integration switch becomes its review target (ADR-0020): off reviews on a branch, and
// on, which opened a pull request, reviews on one. Every other setting stays, and a target nobody offers
// is refused.
func TestReviewTargetMigrationMapsTheIntegrationSwitch(t *testing.T) {
	t.Parallel()
	db, err := sqlite.OpenDB(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE repositories (
  workspace_id    TEXT   NOT NULL,
  id              TEXT   NOT NULL,
  remote          TEXT   NOT NULL DEFAULT '',
  default_branch  TEXT   NOT NULL DEFAULT '',
  base_branch     TEXT   NOT NULL DEFAULT '',
  forge_binding   TEXT   NOT NULL DEFAULT '',
  merge_method    TEXT   NOT NULL DEFAULT 'merge',
  integration     BIGINT NOT NULL DEFAULT 0,
  start_from      TEXT   NOT NULL DEFAULT 'merged',
  merge_authority TEXT   NOT NULL DEFAULT 'person_merges',
  PRIMARY KEY (workspace_id, id)
)`,
		`INSERT INTO repositories (workspace_id, id, integration, merge_method) VALUES ('w', 'off', 0, 'squash')`,
		`INSERT INTO repositories (workspace_id, id, integration, merge_method) VALUES ('w', 'on', 1, 'rebase')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapters", "sqlite", "migrations", "0040_review_target.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][2]string{"off": {"branch", "squash"}, "on": {"pull_request", "rebase"}} {
		var target, method string
		if err := db.QueryRow(`SELECT review_target, merge_method FROM repositories WHERE id = ?`, id).Scan(&target, &method); err != nil {
			t.Fatal(err)
		}
		if target != want[0] || method != want[1] {
			t.Errorf("%s: review target %q, merge method %q", id, target, method)
		}
	}
	if _, err := db.Exec(`SELECT integration FROM repositories`); err == nil {
		t.Error("the integration switch is still a column")
	}
	if _, err := db.Exec(`INSERT INTO repositories (workspace_id, id) VALUES ('w', 'new')`); err != nil {
		t.Fatal(err)
	}
	var target string
	if err := db.QueryRow(`SELECT review_target FROM repositories WHERE id = 'new'`).Scan(&target); err != nil || target != "branch" {
		t.Errorf("a new record's target %q: %v", target, err)
	}
	if _, err := db.Exec(`UPDATE repositories SET review_target = 'merge_queue' WHERE id = 'new'`); err == nil {
		t.Error("a target nobody offers went in")
	}
}

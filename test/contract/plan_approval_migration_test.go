package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
)

func readMigration(t *testing.T, name string) string {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapters", "sqlite", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(script)
}

// The approvals table is made again so that a plan approval can have no lane. The requests a ledger
// holds stay as they were, every other kind still needs a lane, and only a member decides a plan.
func TestPlanApprovalsMigrationKeepsRequestsAndLoosensTheLaneForPlansOnly(t *testing.T) {
	t.Parallel()
	db, err := sqlite.OpenDB(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		readMigration(t, "0026_approvals.sql"),
		`CREATE TABLE plan_versions (workspace_id TEXT NOT NULL, plan_id TEXT NOT NULL, version BIGINT NOT NULL, digest TEXT NOT NULL,
  source TEXT, documents TEXT NOT NULL, decisions TEXT NOT NULL, PRIMARY KEY (workspace_id, plan_id, version))`,
		`CREATE TABLE contracts (workspace_id TEXT NOT NULL, id TEXT NOT NULL, version BIGINT NOT NULL, PRIMARY KEY (workspace_id, id, version))`,
		`INSERT INTO approval_requests VALUES ('w', 'a1', 'lane-1', 'order-1', 2, 'tool_call', 'Bash', 'sha256:x', '{}', 'ls', '', 'Bash: ls',
  'lane:lane-1', 'approved', 'member:m1', 'member', 'sha256:x', 5, '', 1, 1, 0, 'hook', '')`,
		`INSERT INTO plan_versions VALUES ('w', 'plan-1', 1, 'gitblob:abc', NULL, '[]', '[]')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(readMigration(t, "0034_plan_approvals.sql")); err != nil {
		t.Fatal(err)
	}

	var lane, decidedBy string
	var generation int64
	if err := db.QueryRow(`SELECT lane_id, generation, decided_by FROM approval_requests WHERE id = 'a1'`).Scan(&lane, &generation, &decidedBy); err != nil ||
		lane != "lane-1" || generation != 2 || decidedBy != "member:m1" {
		t.Fatalf("the request became lane %q generation %d decided by %q: %v", lane, generation, decidedBy, err)
	}
	var fileDigest, records, workOrder string
	if err := db.QueryRow(`SELECT file_digest, records, work_order FROM plan_versions`).Scan(&fileDigest, &records, &workOrder); err != nil ||
		fileDigest != "gitblob:abc" || records != "[]" || workOrder != "" {
		t.Fatalf("the version became file digest %q, records %q, work order %q: %v", fileDigest, records, workOrder, err)
	}

	insert := func(kind, lane, decidedBy, decidedKind, plan string) error {
		_, err := db.Exec(`INSERT INTO approval_requests
  (workspace_id, id, lane_id, work_order_id, generation, kind, subject, subject_digest, input, command, path, summary, requested_by, state,
   decided_by, decided_kind, decided_digest, decided_at, reason, policy_version, raised_at, expires_at, channel, used_by, plan_id, plan_version, detail)
VALUES ('w', ?, ?, ?, ?, ?, 's', 'sha256:y', '', '', '', 'S', 'system:intake', 'pending', ?, ?, ?, NULL, '', 0, 1, 0, '', '', ?, ?, '')`,
			kind+lane+decidedBy+plan, nullIf(lane), nullIf(lane), nullInt(lane), kind, nullIf(decidedBy), nullIf(decidedKind), nullIf(strings.Replace(decidedBy, decidedBy, "sha256:y", 1)), nullIf(plan), nullInt(plan))
		return err
	}
	if err := insert("plan", "", "", "", "plan-1"); err != nil {
		t.Fatalf("a plan with no lane: %v", err)
	}
	for name, refused := range map[string]struct {
		err        error
		constraint string
	}{
		"a tool call with no lane":             {insert("tool_call", "", "", "", ""), "approval_names_its_lane"},
		"a contract request that names a plan": {insert("contract", "lane-1", "", "", "plan-1"), "approval_plan_only"},
		"an orchestrator's decision of a plan": {insert("plan", "", "orchestrator:w1", "orchestrator", "plan-2"), "approval_plan_is_a_members"},
	} {
		if refused.err == nil || !strings.Contains(refused.err.Error(), refused.constraint) {
			t.Errorf("%s: got %v, want the table to refuse it by %s", name, refused.err, refused.constraint)
		}
	}
	var indexes int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name LIKE 'approval_requests_%'`).Scan(&indexes); err != nil || indexes != 4 {
		t.Fatalf("%d indexes after the migration: %v", indexes, err)
	}
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(s string) any {
	if s == "" {
		return nil
	}
	return 1
}

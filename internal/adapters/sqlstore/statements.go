package sqlstore

// statement is one named SQL statement. The name appears in traces.
type statement struct {
	name string
	text string
}

// every lists all statements, so a check can prepare each on another database.
var every []*statement

func define(name, text string) *statement {
	st := &statement{name: name, text: text}
	every = append(every, st)
	return st
}

// Statement is one statement in a dialect's positional form.
type Statement struct {
	Name string
	Text string
}

// Statements lists every statement the Store can run, as a dialect sends it, so a
// check can prepare each one on a real server (D7).
func Statements(dialect Dialect) []Statement {
	out := make([]Statement, 0, len(every)+1)
	for _, st := range every {
		out = append(out, Statement{Name: st.name, Text: compile(st.text, dialect.Placeholder).text})
	}
	return append(out, Statement{Name: StatementReadClock, Text: dialect.ClockSQL()})
}

var (
	stmtTakeRevision = define(StatementTakeRevision,
		`UPDATE workspaces SET rev = rev + 1 WHERE id = :workspace_id RETURNING rev`)

	// stmtCreateWorkspace is a revision statement too: it creates the row or
	// increments it, so it takes the workspace lock as the first statement of its
	// transaction. A returned rev other than 1 means the workspace already exists.
	stmtCreateWorkspace = define(StatementCreateWorkspace,
		`INSERT INTO workspaces (id, name, fleet_mode, rev, policy_version, wip_limit)
VALUES (:workspace_id, :name, :fleet_mode, 1, 0, :wip_limit)
ON CONFLICT (id) DO UPDATE SET rev = workspaces.rev + 1
RETURNING rev`)

	stmtReadWorkspace = define("workspace.read",
		`SELECT id, name, fleet_mode, rev, policy_version, wip_limit FROM workspaces WHERE id = :workspace_id`)

	stmtAppendEvent = define("event.append",
		`INSERT INTO events
  (workspace_id, rev, id, type, actor_kind, actor_id, subject_kind, subject_id,
   data, occurred_at, recorded_at, traceparent)
VALUES
  (:workspace_id, :rev, :id, :type, :actor_kind, :actor_id, :subject_kind, :subject_id,
   :data, :occurred_at, :recorded_at, :traceparent)`)

	stmtListEvents = define("event.list",
		`SELECT rev, id, type, actor_kind, actor_id, subject_kind, subject_id,
       data, occurred_at, recorded_at, traceparent
  FROM events
 WHERE workspace_id = :workspace_id AND rev > :after
 ORDER BY rev
 LIMIT :limit`)

	stmtFindEvent = define("event.find",
		`SELECT rev, id, type, actor_kind, actor_id, subject_kind, subject_id,
       data, occurred_at, recorded_at, traceparent
  FROM events
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtMigrationApplied = define("migration.applied",
		`SELECT count(*) FROM schema_migrations WHERE version = :version`)

	stmtRecordMigration = define("migration.record",
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (:version, :name, :applied_at)`)
)

// createMigrations runs before any migration, so it cannot be one.
const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version    BIGINT NOT NULL PRIMARY KEY,
  name       TEXT   NOT NULL,
  applied_at BIGINT NOT NULL
)`

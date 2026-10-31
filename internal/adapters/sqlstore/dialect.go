// Package sqlstore implements the Store port over database/sql. Its statements
// keep `:name` parameters and CAST, so they stay dialect-neutral (D7), and a
// Dialect supplies what differs between databases.
package sqlstore

// Dialect supplies what differs between databases.
type Dialect interface {
	// Placeholder renders the parameter at a 1-based position.
	Placeholder(position int) string
	// ClockSQL reads database time in epoch milliseconds.
	ClockSQL() string
	// Migrations lists the schema changes, in order.
	Migrations() ([]Migration, error)
}

// Migration is one schema change.
type Migration struct {
	Version int64
	Name    string
	SQL     string
}

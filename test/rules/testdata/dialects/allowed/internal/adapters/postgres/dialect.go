package postgres

// Postgres's own statements may name what only Postgres runs.
const (
	clock  = "SELECT clock_timestamp()"
	search = "to_tsvector tsquery jsonb"
	scope  = `SET LOCAL search_path TO "x"`
	lock   = "SELECT pg_advisory_lock(1)"
)

package sqlite

// SQLite's own statements may name what only SQLite runs.
const (
	begin  = "BEGIN IMMEDIATE"
	clock  = "SELECT unixepoch('subsec')"
	search = "SELECT -bm25(search_fts) FROM search_fts USING fts5"
)

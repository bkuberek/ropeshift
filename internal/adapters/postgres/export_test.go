package postgres

import (
	"database/sql"

	"github.com/jackc/pgx/v5/stdlib"
)

// SQLDB opens a database/sql handle on the state's pool, for a test that looks at the server around the
// Stores. The caller closes it, which leaves the pool open.
func (s *State) SQLDB() *sql.DB { return stdlib.OpenDBFromPool(s.pool) }

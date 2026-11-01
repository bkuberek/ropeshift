// Package sqlstore implements the Store port over database/sql. Its statements
// keep `:name` parameters and CAST, so they stay dialect-neutral (D7), and a
// Dialect supplies what differs between databases.
package sqlstore

import (
	"cmp"
	"fmt"
	"slices"
)

// Dialect supplies what differs between databases.
type Dialect interface {
	// Placeholder renders the parameter at a 1-based position.
	Placeholder(position int) string
	// ClockSQL reads database time in epoch milliseconds.
	ClockSQL() string
	// Migrations lists the schema changes, in order.
	Migrations() ([]Migration, error)
	// MatchSQL selects workspace_id, kind, record_id, and score from search_documents for
	// the rows that match :query, where a higher score is a better match.
	MatchSQL() string
	// MatchQuery renders search terms as that statement's :query. A term holds only
	// letters and digits.
	MatchQuery(terms []string) string
}

// Migration is one schema change.
type Migration struct {
	Version int64
	Name    string
	SQL     string
}

// MergeMigrations puts a dialect's own changes in order among the neutral ones. A version
// both hold is a mistake, because one of the two would never run.
func MergeMigrations(neutral, own []Migration) ([]Migration, error) {
	merged := slices.Concat(neutral, own)
	slices.SortFunc(merged, func(a, b Migration) int { return cmp.Compare(a.Version, b.Version) })
	for i := 1; i < len(merged); i++ {
		if merged[i].Version == merged[i-1].Version {
			return nil, fmt.Errorf("migrations %s and %s share version %d", merged[i-1].Name, merged[i].Name, merged[i].Version)
		}
	}
	return merged, nil
}

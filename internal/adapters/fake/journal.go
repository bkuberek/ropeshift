package fake

import (
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/ports"
)

// Journal is the in-memory journal: the SQLite adapter on an in-memory database, so
// the statements cannot drift from a second implementation (D6).
type Journal struct {
	*sqlite.Journal
}

// NewJournal returns an empty journal. The caller migrates it.
func NewJournal() (*Journal, error) {
	journal, err := sqlite.OpenMemoryJournal()
	if err != nil {
		return nil, err
	}
	return &Journal{Journal: journal}, nil
}

var _ ports.Journal = (*Journal)(nil)

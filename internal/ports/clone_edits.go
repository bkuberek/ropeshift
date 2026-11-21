package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// CloneEdits reads what a person's clone on a worker's machine holds that intake and lanes cannot read (FR-UI-14). It
// only reads: it fetches nothing and changes no ref, index, or file.
type CloneEdits interface {
	// Edits lists the paths of the clone whose top is at clone that git reports as changed or untracked, and those
	// that commits of the checked-out branch changed which no branch of its remote holds, at most limit of them. Cut
	// says git listed more. A path that holds no clone is ErrNotFound.
	Edits(ctx context.Context, clone string, limit int) (ledger.CloneEdits, error)
}

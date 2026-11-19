package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// RepositoryCreator is the worker's creation of the code repositories a plan approval names: the folder, git init,
// the forge repository, and origin.
type RepositoryCreator interface {
	// Check runs every check that comes before a write, in order from the folder on, writes nothing, and answers within
	// 30 seconds or refuses with "GitHub did not answer within 30 seconds; read the preview again". Its answer says
	// where the path leads, every link followed, once it read the path, whether the check passed or refused.
	Check(ctx context.Context, job ledger.RepositoryCreation) (ledger.CreationCheck, error)
	// Create runs the job or resumes it from its journal, and reports what it did. A refusal is a report with outcome
	// refused and nothing written. It never deletes what it made.
	Create(ctx context.Context, job ledger.RepositoryCreation) (ledger.TemplateWriteReport, error)
}

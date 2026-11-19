package worker

import (
	"context"
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// checkLimit is how long the forge checks of a repository creation may take.
const checkLimit = 30 * time.Second

// RepositoryCreator is this worker's creation of the code repositories a plan approval names.
func (w *Worker) RepositoryCreator() ports.RepositoryCreator {
	return &repositoryCreator{w: w, limit: checkLimit}
}

type repositoryCreator struct {
	w     *Worker
	limit time.Duration
}

func (c *repositoryCreator) Check(ctx context.Context, job ledger.RepositoryCreation) (ledger.CreationCheck, error) {
	return ledger.CreationCheck{}, nil
}

func (c *repositoryCreator) Create(ctx context.Context, job ledger.RepositoryCreation) (ledger.TemplateWriteReport, error) {
	return ledger.TemplateWriteReport{}, nil
}

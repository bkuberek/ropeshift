package entry

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// errWorkerNotStarted says the daemon's own worker, which creates the repositories a plan approval names, has not
// started yet.
var errWorkerNotStarted = errors.New("this daemon's worker has not started yet")

// Check runs the checks of a repository a plan approval creates, on the worker this daemon runs (FR-REPO-16).
func (s *soloWorker) Check(ctx context.Context, job ledger.RepositoryCreation) (ledger.CreationCheck, error) {
	subject := s.worker.Load()
	if subject == nil {
		return ledger.CreationCheck{}, errWorkerNotStarted
	}
	return subject.RepositoryCreator().Check(ctx, job)
}

// Create creates a repository a plan approval names, on the worker this daemon runs, or goes on with it.
func (s *soloWorker) Create(ctx context.Context, job ledger.RepositoryCreation) (ledger.TemplateWriteReport, error) {
	subject := s.worker.Load()
	if subject == nil {
		return ledger.TemplateWriteReport{}, errWorkerNotStarted
	}
	return subject.RepositoryCreator().Create(ctx, job)
}

var _ ports.RepositoryCreator = (*soloWorker)(nil)

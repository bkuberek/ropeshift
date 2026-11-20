package fake

import (
	"context"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// RepositoryCreator is the worker's repository creation held in memory. It records each job it is given, answers the
// check and the report a test set for a creation's key, and fails a check or a create for a key when told to. A key
// with nothing set passes its check and is written. The key names the approval as well as the entry, so the same
// entry of two approvals, or a retry of one, is told apart.
type RepositoryCreator struct {
	mu      sync.Mutex
	checks  map[string]ledger.CreationCheck
	reports map[string]ledger.TemplateWriteReport
	// checks, reports, checkErrors, and createErrors are by the creation's key.
	checkErrors  map[string]error
	createErrors map[string]error
	checked      []ledger.RepositoryCreation
	created      []ledger.RepositoryCreation
}

// NewRepositoryCreator returns a creator that passes every check and writes every job.
func NewRepositoryCreator() *RepositoryCreator {
	return &RepositoryCreator{
		checks: map[string]ledger.CreationCheck{}, reports: map[string]ledger.TemplateWriteReport{},
		checkErrors: map[string]error{}, createErrors: map[string]error{},
	}
}

// SetCheck sets what the check of the job with key answers.
func (c *RepositoryCreator) SetCheck(key string, check ledger.CreationCheck) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[key] = check
}

// SetReport sets what the creation of the job with key reports.
func (c *RepositoryCreator) SetReport(key string, report ledger.TemplateWriteReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports[key] = report
}

// FailCheck makes the check of the job with key fail with err. A nil err clears it.
func (c *RepositoryCreator) FailCheck(key string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkErrors[key] = err
}

// FailCreate makes the creation of the job with key fail with err. A nil err clears it.
func (c *RepositoryCreator) FailCreate(key string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.createErrors[key] = err
}

// Checked lists the jobs the creator was asked to check, in order.
func (c *RepositoryCreator) Checked() []ledger.RepositoryCreation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ledger.RepositoryCreation(nil), c.checked...)
}

// Created lists the jobs the creator was asked to create, in order, those that failed included.
func (c *RepositoryCreator) Created() []ledger.RepositoryCreation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ledger.RepositoryCreation(nil), c.created...)
}

// Check records the job and answers the check set for its key. A memory disk holds no link, so a check that names
// no place the path leads leads it to the path itself.
func (c *RepositoryCreator) Check(ctx context.Context, job ledger.RepositoryCreation) (ledger.CreationCheck, error) {
	if err := ctx.Err(); err != nil {
		return ledger.CreationCheck{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = append(c.checked, job)
	if err := c.checkErrors[job.Key]; err != nil {
		return ledger.CreationCheck{}, err
	}
	check := c.checks[job.Key]
	if check.Leads == "" {
		check.Leads = job.Path
	}
	return check, nil
}

// Create records the job and reports what a test set for its key, or a written outcome when it set none.
func (c *RepositoryCreator) Create(ctx context.Context, job ledger.RepositoryCreation) (ledger.TemplateWriteReport, error) {
	if err := ctx.Err(); err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created = append(c.created, job)
	if err := c.createErrors[job.Key]; err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	if report, ok := c.reports[job.Key]; ok {
		return report, nil
	}
	return ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeWritten, Project: job.Project}, nil
}

var _ ports.RepositoryCreator = (*RepositoryCreator)(nil)

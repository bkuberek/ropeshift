package fake

import (
	"context"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// RepositoryCreator is the worker's repository creation held in memory. It records each job it is given, answers the
// check and the report a test set for a name, and fails a check or a create for a name when told to. A name with
// nothing set passes its check and is written.
type RepositoryCreator struct {
	mu      sync.Mutex
	checks  map[string]ledger.CreationCheck
	reports map[string]ledger.TemplateWriteReport
	// checkErrors and createErrors are what a check or a create of a name fails with.
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

// SetCheck sets what the check of the job named name answers.
func (c *RepositoryCreator) SetCheck(name string, check ledger.CreationCheck) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks[name] = check
}

// SetReport sets what the creation of the job named name reports.
func (c *RepositoryCreator) SetReport(name string, report ledger.TemplateWriteReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports[name] = report
}

// FailCheck makes the check of the job named name fail with err, and FailCreate its creation. A nil err clears it.
func (c *RepositoryCreator) FailCheck(name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkErrors[name] = err
}

// FailCreate makes the creation of the job named name fail with err. A nil err clears it.
func (c *RepositoryCreator) FailCreate(name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.createErrors[name] = err
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

// Check records the job and answers the check set for its name.
func (c *RepositoryCreator) Check(ctx context.Context, job ledger.RepositoryCreation) (ledger.CreationCheck, error) {
	if err := ctx.Err(); err != nil {
		return ledger.CreationCheck{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = append(c.checked, job)
	if err := c.checkErrors[job.Name]; err != nil {
		return ledger.CreationCheck{}, err
	}
	return c.checks[job.Name], nil
}

// Create records the job and reports what a test set for its name, or a written outcome when it set none.
func (c *RepositoryCreator) Create(ctx context.Context, job ledger.RepositoryCreation) (ledger.TemplateWriteReport, error) {
	if err := ctx.Err(); err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created = append(c.created, job)
	if err := c.createErrors[job.Name]; err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	if report, ok := c.reports[job.Name]; ok {
		return report, nil
	}
	return ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeWritten, Project: job.Project}, nil
}

var _ ports.RepositoryCreator = (*RepositoryCreator)(nil)

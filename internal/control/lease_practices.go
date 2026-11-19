package control

import (
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/workorders"
)

// planningPractices is what a planning lease carries of the practices check (FR-PRAC-8): for each active repository of
// the project, by name, the commit and age of its latest result, its open findings, the findings a member
// acknowledged, and the practices the check could not settle. It only tells a planning lane what the check found, and
// nothing is decided by it (FR-PRAC-11), so it fails nothing: a repository whose records cannot be read is listed as
// unreadable and warned of, and one that cannot be listed leaves the lease with no summaries. The lease, the worker's
// other leases, and the rest of desired state are served either way.
func planningPractices(tx ports.ReadTx, warn func(string)) []ledger.PracticesSummary {
	say := func(format string, args ...any) {
		if warn != nil {
			warn(fmt.Sprintf(format, args...))
		}
	}
	all, err := tx.Repositories().Repositories()
	if err != nil {
		say("the practices findings of a planning lease are left out, because the project's repositories could not be read: %v", err)
		return nil
	}
	repositories, err := chooseRepositories(all, nil)
	if err != nil {
		say("the practices findings of a planning lease are left out, because the project's repositories could not be chosen: %v", err)
		return nil
	}
	now, err := tx.Now()
	if err != nil {
		say("the practices findings of a planning lease are left out, because the time could not be read: %v", err)
		return nil
	}
	var out []ledger.PracticesSummary
	for _, repository := range repositories {
		summary, err := repositoryPractices(tx, repository, now)
		if err != nil {
			say("the practices findings of repository %s are left out of a planning lease, because they could not be read: %v", repository.ID, err)
			summary = ledger.PracticesSummary{Repository: repository.ID, Name: repository.Name, Unreadable: true}
		}
		out = append(out, summary)
	}
	return out
}

// repositoryPractices reads one repository's summary: its latest result and the acknowledgements, or not checked yet
// when no result was reported.
func repositoryPractices(tx ports.ReadTx, repository ledger.Repository, now int64) (ledger.PracticesSummary, error) {
	summary := ledger.PracticesSummary{Repository: repository.ID, Name: repository.Name}
	record, err := tx.Practices().Result(repository.ID)
	if errors.Is(err, ports.ErrNotFound) {
		return summary, nil
	}
	if err != nil {
		return summary, err
	}
	acknowledgements, err := tx.Practices().Acknowledgements(repository.ID)
	if err != nil {
		return summary, err
	}
	return summarizePractices(summary, record, acknowledgements, now).Capped(), nil
}

// summarizePractices adds a repository's latest result to its summary, with each finding open or acknowledged by what
// the members acknowledged. An acknowledgement of a finding the result does not hold lists nothing.
func summarizePractices(summary ledger.PracticesSummary, record ledger.PracticesRecord, acknowledgements []ledger.PracticesAcknowledgement, now int64) ledger.PracticesSummary {
	reasons := map[string]string{}
	for _, ack := range acknowledgements {
		reasons[ack.Finding] = ack.Reason
	}
	result := record.Result
	summary.Checked, summary.Commit, summary.AgeMs = true, result.Commit, max(now-record.CheckedAt, 0)
	for _, finding := range result.Findings {
		if reason, done := reasons[finding.ID]; done {
			summary.Acknowledged = append(summary.Acknowledged, ledger.PracticesAcknowledgedFinding{ID: finding.ID, Reason: reason})
			continue
		}
		practice, _ := practices.Lookup(finding.Practice)
		summary.Open = append(summary.Open, ledger.PracticesOpenFinding{ID: finding.ID, Severity: finding.Severity, Detail: finding.Detail, Fix: practice.Fix})
	}
	for _, status := range result.Practices {
		if status.Status == practices.StatusUnknown {
			summary.Unknown = append(summary.Unknown, ledger.PracticesUnknownPractice{Practice: status.ID, Reason: status.Reason})
		}
	}
	return summary
}

// carriesPractices says a work order of the kind reads the practices findings in its lease: only planning work does.
func carriesPractices(kind workorders.Kind) bool { return kind == workorders.KindPlan }

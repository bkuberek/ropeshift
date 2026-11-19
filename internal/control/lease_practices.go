package control

import (
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

// planningPractices is what a planning lease carries of the practices check (FR-PRAC-8): for each active repository of
// the project, by name, the commit and age of its latest result, its open findings, the findings a member
// acknowledged, and the practices the check could not settle. It only tells a planning lane what the check found, and
// nothing is decided by it (FR-PRAC-11).
func planningPractices(tx ports.ReadTx) ([]ledger.PracticesSummary, error) {
	all, err := tx.Repositories().Repositories()
	if err != nil {
		return nil, err
	}
	repositories, err := chooseRepositories(all, nil)
	if err != nil {
		return nil, err
	}
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	var out []ledger.PracticesSummary
	for _, repository := range repositories {
		summary := ledger.PracticesSummary{Repository: repository.ID, Name: repository.Name}
		record, err := tx.Practices().Result(repository.ID)
		if errors.Is(err, ports.ErrNotFound) {
			out = append(out, summary)
			continue
		}
		if err != nil {
			return nil, err
		}
		acknowledgements, err := tx.Practices().Acknowledgements(repository.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, summarizePractices(summary, record, acknowledgements, now))
	}
	return out, nil
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

package practices

import (
	"slices"
	"time"
)

// PracticeStatus is the status of one practice in a result.
type PracticeStatus struct {
	ID     ID     `json:"id"`
	Status Status `json:"status"`
	// Reason says why a practice is unknown or not applicable.
	Reason string `json:"reason,omitempty"`
}

// Result is what one check of a repository came to.
type Result struct {
	Repository   string    `json:"repository"`
	Commit       string    `json:"commit"`
	BaseBranch   string    `json:"base_branch"`
	Worker       string    `json:"worker"`
	CheckedAt    time.Time `json:"checked_at"`
	TableVersion int       `json:"table_version"`
	RulesVersion int       `json:"rules_version"`
	// ScannedFrom is the commit the secret scan started from, empty for a scan of the whole history.
	ScannedFrom string `json:"scanned_from,omitempty"`
	// Skipped counts the files the secret scan skipped for their size or binary content.
	Skipped int `json:"skipped,omitempty"`
	// Practices holds one status per practice, in table order.
	Practices []PracticeStatus `json:"practices"`
	// Findings are sorted by severity, then practice table order, then ID.
	Findings []Finding `json:"findings"`
}

// sortFindings orders findings by severity, then practice table order, then ID.
func sortFindings(findings []Finding) {
	slices.SortStableFunc(findings, func(a, b Finding) int {
		if c := a.Severity.rank() - b.Severity.rank(); c != 0 {
			return c
		}
		if c := order(a.Practice) - order(b.Practice); c != 0 {
			return c
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
}

package ledger

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"ropeshift.com/ropeshift/internal/practices"
)

// PracticesSummary is what a planning lease carries of one active repository's practices check: the latest result's
// commit and age, its open findings, the findings a member acknowledged, and the practices the check could not settle.
// A repository no check has reported is Checked false, with nothing else.
type PracticesSummary struct {
	Repository string `json:"repository"`
	Name       string `json:"name"`
	Checked    bool   `json:"checked"`
	// Unreadable says the control plane could not read this repository's result or acknowledgements, so it names no
	// finding, and a repository that reads so is not one nothing was found in.
	Unreadable bool `json:"unreadable,omitempty"`
	// Commit is the commit the latest result read.
	Commit string `json:"commit,omitempty"`
	// AgeMs is the time between the latest result's check and the read of the lease, in milliseconds.
	AgeMs int64 `json:"age_ms,omitempty"`
	// Open are the findings no member acknowledged, at most PracticesOpenLimit of them.
	Open []PracticesOpenFinding `json:"open,omitempty"`
	// More counts the open findings left out of Open.
	More int `json:"more,omitempty"`
	// Acknowledged are the findings a member acknowledged, each with the reason.
	Acknowledged []PracticesAcknowledgedFinding `json:"acknowledged,omitempty"`
	// Unknown are the practices the check could not settle, each with the reason.
	Unknown []PracticesUnknownPractice `json:"unknown,omitempty"`
}

// PracticesOpenLimit is the most open findings of one repository that a lease or a brief carries.
const PracticesOpenLimit = 50

// Capped is the summary with at most PracticesOpenLimit open findings: the most serious first, then by ID, so the cut
// is the same whatever order the result listed them in. More counts every finding left out, those an earlier cut left
// out included. The summary it is called on is unchanged.
func (s PracticesSummary) Capped() PracticesSummary {
	open := slices.Clone(s.Open)
	slices.SortStableFunc(open, func(a, b PracticesOpenFinding) int {
		return cmp.Or(severityOrder(a.Severity)-severityOrder(b.Severity), strings.Compare(a.ID, b.ID))
	})
	if len(open) > PracticesOpenLimit {
		s.More += len(open) - PracticesOpenLimit
		open = open[:PracticesOpenLimit]
	}
	s.Open = open
	return s
}

// severityOrder puts the most serious severity first, and a severity the table does not hold last.
func severityOrder(severity practices.Severity) int {
	switch severity {
	case practices.High:
		return 0
	case practices.Medium:
		return 1
	case practices.Low:
		return 2
	}
	return 3
}

// PracticesOpenFinding is a finding no member acknowledged, with the fix its practice's table row gives.
type PracticesOpenFinding struct {
	ID       string             `json:"id"`
	Severity practices.Severity `json:"severity"`
	Detail   string             `json:"detail"`
	Fix      string             `json:"fix"`
}

// PracticesAcknowledgedFinding is a finding a member acknowledged, with the member's reason.
type PracticesAcknowledgedFinding struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// PracticesUnknownPractice is a practice the check could not settle, and why.
type PracticesUnknownPractice struct {
	Practice practices.ID `json:"practice"`
	Reason   string       `json:"reason"`
}

// AgeWords says how long ago something was, from milliseconds, in the largest whole unit.
func AgeWords(ms int64) string {
	seconds := max(ms, 0) / 1000
	switch {
	case seconds < 60:
		return strconv.FormatInt(seconds, 10) + "s"
	case seconds < 3600:
		return strconv.FormatInt(seconds/60, 10) + "m"
	case seconds < 48*3600:
		return strconv.FormatInt(seconds/3600, 10) + "h"
	}
	return strconv.FormatInt(seconds/86400, 10) + "d"
}

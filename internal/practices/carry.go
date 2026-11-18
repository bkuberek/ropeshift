package practices

import "slices"

// CarryForward adds to the result of an incremental secret scan the `secrets` findings of the result the scan
// continued from. An incremental scan reads the commits after the one the earlier scan ended at, and a secret a
// commit added stays in the history, so the findings of the earlier result still hold. A result that scanned the whole
// history, one whose scan could not run, and one that continued from another commit than the earlier result read
// carry nothing. It returns a copy and changes neither result.
func CarryForward(previous, next Result) Result {
	if next.ScannedFrom == "" || next.ScannedFrom != previous.Commit {
		return next
	}
	at := slices.IndexFunc(next.Practices, func(p PracticeStatus) bool { return p.ID == Secrets })
	if at < 0 || (next.Practices[at].Status != StatusMet && next.Practices[at].Status != StatusFinding) {
		return next
	}
	known := map[string]bool{}
	for _, f := range next.Findings {
		known[f.ID] = true
	}
	var carried []Finding
	for _, f := range previous.Findings {
		if f.Practice == Secrets && !known[f.ID] {
			carried = append(carried, f)
		}
	}
	if len(carried) == 0 {
		return next
	}
	next.Practices = slices.Clone(next.Practices)
	next.Practices[at] = PracticeStatus{ID: Secrets, Status: StatusFinding}
	next.Findings = append(slices.Clone(next.Findings), carried...)
	sortFindings(next.Findings)
	return next
}

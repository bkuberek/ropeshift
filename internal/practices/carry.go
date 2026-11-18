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

// SecretsUnverified says why the secrets of a result's history cannot be read from it: the scan did not read every
// commit it should have, so a result that found nothing cannot say there is nothing. A `secrets` practice that is met
// becomes unknown with the reason. One with findings stays a finding, because a secret that was found is no less found,
// and one that is unknown or not applicable stays as it is. It returns a copy and changes nothing it was given.
func SecretsUnverified(result Result, reason string) Result {
	at := slices.IndexFunc(result.Practices, func(p PracticeStatus) bool { return p.ID == Secrets })
	if at < 0 || result.Practices[at].Status != StatusMet {
		return result
	}
	result.Practices = slices.Clone(result.Practices)
	result.Practices[at] = PracticeStatus{ID: Secrets, Status: StatusUnknown, Reason: reason}
	return result
}

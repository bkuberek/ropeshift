package delivery

import "ropeshift.com/ropeshift/internal/ledger"

// Place gives each of one work order's events, in revision order, its time on the timeline (FR-EST-3). A
// control plane event sits at its recorded time. A worker's event, which carries the time its worker or one
// of its lanes reported, sits at that time, clamped between the recorded time of the previous event about
// the work order and its own recorded time, so a buffered upload neither reorders the timeline nor moves a
// boundary past what the ledger saw. No event sits before the one placed before it. These times measure;
// they decide nothing.
func Place(events []ledger.Event) []int64 {
	out := make([]int64, len(events))
	var previousRecorded, previousPlaced int64
	for i, e := range events {
		at := e.RecordedAt
		if e.OccurredAt != 0 {
			at = min(max(e.OccurredAt, previousRecorded), e.RecordedAt)
		}
		if i > 0 {
			at = max(at, previousPlaced)
		}
		out[i], previousRecorded, previousPlaced = at, e.RecordedAt, at
	}
	return out
}

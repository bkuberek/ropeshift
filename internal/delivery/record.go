package delivery

import (
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Keys are what an estimate matches comparable work on, all known before the work starts (FR-EST-6): its
// kind, its declared size, the roster agent it ran as and that agent's capability class, its repository and
// that repository's merge authority level, and the number of criteria in its pinned contract version.
type Keys struct {
	Kind            string `json:"kind"`
	Size            string `json:"size"`
	Agent           string `json:"agent,omitempty"`
	CapabilityClass string `json:"capability_class,omitempty"`
	Repository      string `json:"repository,omitempty"`
	MergeAuthority  string `json:"merge_authority,omitempty"`
	Criteria        int64  `json:"criteria"`
}

// Usage is what every lane that served a work order reported, its review lanes' included. CostLowerBound
// says some of it was never reported, because a turn a kill stopped, or a holder a person replaced, reports
// nothing.
type Usage struct {
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	CostUSD        float64 `json:"cost_usd"`
	CostLowerBound bool    `json:"cost_lower_bound,omitempty"`
}

// Facts are what a delivery record holds beside a timeline, read from the work order's other records.
// Change is the change size of its final lane, absent when none was recorded. LaneCapKnown says capacity
// intervals were recorded for all of its time, so a wait for a lane names its cause. Reviews are the
// timelines of its review work orders, whose lanes' time counts as its own.
type Facts struct {
	Keys
	Model        string
	Milestone    string
	Plan         string
	Usage        Usage
	Change       *ledger.ChangeSize
	LaneCapKnown bool
	Reviews      []Timeline
}

// Record summarizes one `change` or `rework` work order that reached a terminal state (FR-EST-4). Its four
// durations come from the timeline: elapsed time from the first `ready` to the end, delivery time without
// any wait for a lane, lane time while a lane of it or of one of its reviews held a place under the lane cap,
// and person time in its waits for a person. ByKind is the time in each segment kind, and PersonWaits the
// length of each wait for a person, where adjacent person segments, such as a wait a park split, are one.
type Record struct {
	WorkOrder string `json:"work_order"`
	Version   int    `json:"version"`
	Keys
	Model        string             `json:"model,omitempty"`
	Milestone    string             `json:"milestone,omitempty"`
	Plan         string             `json:"plan,omitempty"`
	Outcome      workorders.State   `json:"outcome"`
	Rounds       int64              `json:"rounds"`
	Started      int64              `json:"started"`
	Ended        int64              `json:"ended"`
	ElapsedMs    int64              `json:"elapsed_ms"`
	DeliveryMs   int64              `json:"delivery_ms"`
	LaneMs       int64              `json:"lane_ms"`
	PersonMs     int64              `json:"person_ms"`
	ByKind       map[Kind]int64     `json:"by_kind"`
	PersonWaits  []int64            `json:"person_waits"`
	Usage        Usage              `json:"usage"`
	Change       *ledger.ChangeSize `json:"change,omitempty"`
	LaneCapKnown bool               `json:"lane_cap_known"`
}

// RecordOf summarizes a finished timeline. Only a `change` or a `rework` that reached a terminal state has a
// record.
func RecordOf(t Timeline, f Facts) (Record, bool) {
	kind := workorders.Kind(f.Kind)
	if t.Terminal == "" || (kind != workorders.KindChange && kind != workorders.KindRework) {
		return Record{}, false
	}
	out := Record{
		WorkOrder: t.WorkOrder, Version: Version, Keys: f.Keys, Model: f.Model, Milestone: f.Milestone, Plan: f.Plan,
		Outcome: t.Terminal, Started: t.Start, Ended: t.End, ElapsedMs: t.End - t.Start, ByKind: map[Kind]int64{}, PersonWaits: []int64{},
		Usage: f.Usage, Change: f.Change, LaneCapKnown: f.LaneCapKnown,
	}
	var waiting *Segment
	for i, s := range t.Segments {
		length := s.To - s.From
		out.ByKind[s.Kind] += length
		out.Rounds = max(out.Rounds, s.Round)
		out.LaneMs += laneTime(s)
		if s.Kind != Person {
			waiting = nil
			continue
		}
		out.PersonMs += length
		if waiting != nil && waiting.To == s.From {
			out.PersonWaits[len(out.PersonWaits)-1] += length
		} else {
			out.PersonWaits = append(out.PersonWaits, length)
		}
		waiting = &t.Segments[i]
	}
	out.DeliveryMs = out.ElapsedMs - out.ByKind[Queued]
	for _, review := range f.Reviews {
		for _, s := range review.Segments {
			out.LaneMs += laneTime(s)
		}
	}
	return out, true
}

// laneTime is how long a segment held a place under the lane cap: claimed or running.
func laneTime(s Segment) int64 {
	if s.State == workorders.Claimed || s.State == workorders.Running {
		return s.To - s.From
	}
	return 0
}

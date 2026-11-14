package delivery

import (
	"slices"

	"ropeshift.com/ropeshift/internal/workorders"
)

// FoldState is what the delivery fold keeps beside the rows it derives: the derivation version they were
// derived by, the revision of the project's log it has read through, when it first ran, and the work in
// progress it counts, to tell when the project's work-in-progress cap was full. A record whose work began
// before FirstRunAt may lack the capacity intervals that name a wait for a lane, which were recorded only
// from some time before it.
type FoldState struct {
	Version    int      `json:"version"`
	Rev        int64    `json:"rev"`
	FirstRunAt int64    `json:"first_run_at"`
	WIPLimit   int64    `json:"wip_limit"`
	InProgress []string `json:"in_progress"`
	FullSince  int64    `json:"full_since,omitempty"`
}

// Target is the state a trigger moves a work order to, which every trigger names alone.
func Target(trigger workorders.Trigger) (workorders.State, bool) {
	for _, edge := range workorders.Transitions() {
		if edge.Trigger == trigger {
			return edge.To, true
		}
	}
	return "", false
}

// inProgress says a state counts toward the work-in-progress cap, as the product measures count it.
func inProgress(s workorders.State) bool {
	return s == workorders.Claimed || s == workorders.Running || s == workorders.Parked
}

// full says the work in progress stands at its cap. A cap of zero caps nothing.
func (f *FoldState) full() bool { return f.WIPLimit > 0 && int64(len(f.InProgress)) >= f.WIPLimit }

// settle notes a change in whether the cap is full at a time, and returns the span it ended, if any.
func (f *FoldState) settle(was bool, at int64) (Span, bool) {
	switch now := f.full(); {
	case !was && now:
		f.FullSince = at
	case was && !now:
		span := Span{From: f.FullSince, To: max(at, f.FullSince)}
		f.FullSince = 0
		return span, span.To > span.From
	}
	return Span{}, false
}

// Move counts one work order's move to a state at a time, and returns the span of a full cap the move ended.
func (f *FoldState) Move(workOrder string, to workorders.State, at int64) (Span, bool) {
	was := f.full()
	counted := slices.Contains(f.InProgress, workOrder)
	switch {
	case inProgress(to) && !counted:
		f.InProgress = append(f.InProgress, workOrder)
		slices.Sort(f.InProgress)
	case !inProgress(to) && counted:
		f.InProgress = slices.DeleteFunc(f.InProgress, func(id string) bool { return id == workOrder })
	}
	return f.settle(was, at)
}

// Limit changes the work-in-progress cap at a time, and returns the span of a full cap the change ended.
func (f *FoldState) Limit(limit, at int64) (Span, bool) {
	was := f.full()
	f.WIPLimit = limit
	return f.settle(was, at)
}

// Open is the span of a cap that is full now, until a time, or nothing while there is room under it.
func (f *FoldState) Open(until int64) []Span {
	if f.FullSince == 0 || until <= f.FullSince {
		return nil
	}
	return []Span{{From: f.FullSince, To: until}}
}

package planning

import (
	"encoding/json"
	"maps"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The events of a plan revision. Each holds the whole revision as it stands, so a replay needs nothing else.
// A revision is rebased when a member approves a version before any of its decisions were recorded: it goes on, since
// the approved version.
const (
	EventRevisionOpened   = "plan.revision_opened"
	EventRevisionExtended = "plan.revision_extended"
	EventRevisionRebased  = "plan.revision_rebased"
	EventRevisionClosed   = "plan.revision_closed"
)

// RevisionOutcome says how a plan revision ended.
type RevisionOutcome string

// The two ways a revision ends: a member confirmed the approved version against the decisions since, or a member
// approved a later version.
const (
	RevisionConfirmed RevisionOutcome = "confirmed"
	RevisionApproved  RevisionOutcome = "approved"
)

// RevisionWork is a finished work order that decisions reached, with the decisions that did and the rework a
// member made of it once one did.
type RevisionWork struct {
	WorkOrder string   `json:"work_order"`
	Decisions []string `json:"decisions"`
	Rework    string   `json:"rework,omitempty"`
	// ReworkFor are the decisions the rework was made for, in the order Decisions lists them. A revision that
	// recorded its rework before it named them has none.
	ReworkFor []string `json:"rework_for,omitempty"`
}

// Revision is what a member is asked to settle for a plan whose approved version decisions have moved past: the
// decisions a member recorded, answered, or overturned inside it, and the finished work the reversible ones
// reached. A plan has one open revision at most, and unstarted work those decisions bind waits in contract review
// until it closes (ADR-0030).
type Revision struct {
	ID              string `json:"id"`
	PlanID          string `json:"plan_id"`
	ApprovedVersion int64  `json:"approved_version"`
	// DecisionIDs are the decisions since the approved version, in the order they were recorded.
	DecisionIDs []string `json:"decision_ids"`
	// ListedAt is when the revision first listed each decision, which is when it was recorded.
	ListedAt map[string]int64 `json:"listed_at"`
	Finished []RevisionWork   `json:"finished"`
	OpenedAt int64            `json:"opened_at"`
	// ClosedAt is zero while the revision is open.
	ClosedAt int64           `json:"closed_at,omitempty"`
	ClosedBy string          `json:"closed_by,omitempty"`
	Outcome  RevisionOutcome `json:"outcome,omitempty"`
	// Approval is the request whose approval of a later version ended the revision.
	Approval string `json:"approval,omitempty"`
}

// Open says the revision still waits for a member.
func (r Revision) Open() bool { return r.ClosedAt == 0 }

// Names says a decision is one the revision lists.
func (r Revision) Names(decision string) bool { return slices.Contains(r.DecisionIDs, decision) }

// After lists the decisions the revision listed after a moment, in the order it lists them. A decision listed at the
// moment is not one of them, and neither is one with no time on record.
func (r Revision) After(at int64) []string {
	var later []string
	for _, id := range r.DecisionIDs {
		if r.ListedAt[id] > at {
			later = append(later, id)
		}
	}
	return later
}

// Through lists the decisions the revision listed at a moment or before it, in the order it lists them. It is every
// decision After leaves out.
func (r Revision) Through(at int64) []string {
	var earlier []string
	for _, id := range r.DecisionIDs {
		if r.ListedAt[id] <= at {
			earlier = append(earlier, id)
		}
	}
	return earlier
}

// Within lists the decisions the revision lists that are among the ones given, in the order it lists them.
func (r Revision) Within(ids []string) []string {
	return r.listed(ids, true)
}

// Outside lists the decisions the revision lists that are not among the ones given, in the order it lists them. It
// is every decision Within leaves out.
func (r Revision) Outside(ids []string) []string {
	return r.listed(ids, false)
}

func (r Revision) listed(ids []string, among bool) []string {
	var listed []string
	for _, id := range r.DecisionIDs {
		if slices.Contains(ids, id) == among {
			listed = append(listed, id)
		}
	}
	return listed
}

// Extend returns the revision with one more decision, recorded at a moment, and with the finished work orders that
// decision reached under each of them. A decision or a work order it lists already stays listed once, with the
// moment it was first listed.
func (r Revision) Extend(decision string, at int64, finished []string) Revision {
	next := r
	next.DecisionIDs = slices.Clone(r.DecisionIDs)
	next.ListedAt = maps.Clone(r.ListedAt)
	if next.ListedAt == nil {
		next.ListedAt = map[string]int64{}
	}
	if !slices.Contains(next.DecisionIDs, decision) {
		next.DecisionIDs = append(next.DecisionIDs, decision)
		next.ListedAt[decision] = at
	}
	next.Finished = make([]RevisionWork, len(r.Finished))
	for i, work := range r.Finished {
		next.Finished[i] = RevisionWork{WorkOrder: work.WorkOrder, Decisions: slices.Clone(work.Decisions), Rework: work.Rework, ReworkFor: slices.Clone(work.ReworkFor)}
	}
	for _, id := range finished {
		at := slices.IndexFunc(next.Finished, func(work RevisionWork) bool { return work.WorkOrder == id })
		switch {
		case at < 0:
			next.Finished = append(next.Finished, RevisionWork{WorkOrder: id, Decisions: []string{decision}})
		case !slices.Contains(next.Finished[at].Decisions, decision):
			next.Finished[at].Decisions = append(next.Finished[at].Decisions, decision)
		}
	}
	return next
}

// Restrict returns the revision with only the decisions given, in the order the revision lists them, and with the
// finished work only those decisions reached. A finished work order none of them reached is left out. The work
// order keeps its rework only when the rework was made for one of the decisions kept, and then only for those, so
// two revisions a split makes of one never both claim rework that only one of them asked for. A rework recorded
// before it named its decisions is kept only where every decision that reached the work is kept, because that is
// the one case where it is known whose it is.
func (r Revision) Restrict(keep []string) Revision {
	next := r
	next.DecisionIDs = nil
	next.ListedAt = map[string]int64{}
	for _, id := range r.DecisionIDs {
		if slices.Contains(keep, id) {
			next.DecisionIDs = append(next.DecisionIDs, id)
			next.ListedAt[id] = r.ListedAt[id]
		}
	}
	next.Finished = []RevisionWork{}
	for _, work := range r.Finished {
		var decisions []string
		for _, id := range work.Decisions {
			if slices.Contains(keep, id) {
				decisions = append(decisions, id)
			}
		}
		if len(decisions) != 0 {
			next.Finished = append(next.Finished, work.within(keep, decisions))
		}
	}
	return next
}

// within is the work order as a revision that keeps only some of its decisions holds it. The decisions are the ones
// of the work order that are kept.
func (w RevisionWork) within(keep, decisions []string) RevisionWork {
	kept := RevisionWork{WorkOrder: w.WorkOrder, Decisions: decisions}
	if w.Rework == "" {
		return kept
	}
	if len(w.ReworkFor) == 0 {
		if len(decisions) == len(w.Decisions) {
			kept.Rework = w.Rework
		}
		return kept
	}
	for _, id := range w.ReworkFor {
		if slices.Contains(keep, id) {
			kept.ReworkFor = append(kept.ReworkFor, id)
		}
	}
	if len(kept.ReworkFor) != 0 {
		kept.Rework = w.Rework
	}
	return kept
}

// Reworked returns the revision with the rework a member made of one finished work order, for the decisions given.
// A rework that is already on record for the work order takes the decisions beside the ones it has, and a different
// one replaces it. A decision the revision does not list for the work order is left out, and so is a work order the
// revision does not list.
func (r Revision) Reworked(workOrder, rework string, decisions []string) Revision {
	next := r
	next.Finished = slices.Clone(r.Finished)
	for i := range next.Finished {
		work := &next.Finished[i]
		if work.WorkOrder != workOrder {
			continue
		}
		made := slices.Clone(decisions)
		if work.Rework == rework {
			made = append(made, work.ReworkFor...)
		}
		work.Rework, work.ReworkFor = rework, nil
		for _, id := range work.Decisions {
			if slices.Contains(made, id) {
				work.ReworkFor = append(work.ReworkFor, id)
			}
		}
	}
	return next
}

// Close returns the revision as it stands once a member settled it. An approval names the request that approved
// a later version, and a confirmation names none.
func (r Revision) Close(by string, at int64, outcome RevisionOutcome, approval string) Revision {
	next := r
	next.ClosedAt, next.ClosedBy, next.Outcome, next.Approval = at, by, outcome, approval
	return next
}

// RevisionEvent is the event that records a revision as it stands, about the plan it belongs to.
func RevisionEvent(kind string, revision Revision, actor ledger.Actor) (ledger.EventDraft, error) {
	data, err := json.Marshal(revision)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{Type: kind, Actor: actor, Subject: ledger.Subject{Kind: SubjectPlan, ID: revision.PlanID}, Data: data}, nil
}

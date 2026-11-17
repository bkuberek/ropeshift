package planning

import (
	"encoding/json"
	"maps"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The events of a plan revision. Each holds the whole revision as it stands, so a replay needs nothing else.
// A revision is narrowed when a member approves a version that came before some of its decisions: it goes on for
// the decisions recorded after the approval.
const (
	EventRevisionOpened   = "plan.revision_opened"
	EventRevisionExtended = "plan.revision_extended"
	EventRevisionNarrowed = "plan.revision_narrowed"
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

// After lists the decisions the revision listed after a moment, in the order it lists them. A decision with no
// time on record is not one of them.
func (r Revision) After(at int64) []string {
	var later []string
	for _, id := range r.DecisionIDs {
		if r.ListedAt[id] > at {
			later = append(later, id)
		}
	}
	return later
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
		next.Finished[i] = RevisionWork{WorkOrder: work.WorkOrder, Decisions: slices.Clone(work.Decisions), Rework: work.Rework}
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
// finished work only those decisions reached. A finished work order none of them reached is left out.
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
			next.Finished = append(next.Finished, RevisionWork{WorkOrder: work.WorkOrder, Decisions: decisions, Rework: work.Rework})
		}
	}
	return next
}

// Reworked returns the revision with the rework a member made of one finished work order. A work order the
// revision does not list is left out.
func (r Revision) Reworked(workOrder, rework string) Revision {
	next := r
	next.Finished = slices.Clone(r.Finished)
	for i := range next.Finished {
		if next.Finished[i].WorkOrder == workOrder {
			next.Finished[i].Rework = rework
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

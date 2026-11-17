package planning

import (
	"encoding/json"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
)

// The events of a plan revision. Each holds the whole revision as it stands, so a replay needs nothing else.
const (
	EventRevisionOpened   = "plan.revision_opened"
	EventRevisionExtended = "plan.revision_extended"
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
// reached. A plan has one open revision at most, and unstarted work under the plan waits in contract review
// until it closes (ADR-0030).
type Revision struct {
	ID              string `json:"id"`
	PlanID          string `json:"plan_id"`
	ApprovedVersion int64  `json:"approved_version"`
	// DecisionIDs are the decisions since the approved version, in the order they were recorded.
	DecisionIDs []string       `json:"decision_ids"`
	Finished    []RevisionWork `json:"finished"`
	OpenedAt    int64          `json:"opened_at"`
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

// Extend returns the revision with one more decision, and with the finished work orders that decision reached
// under each of them. A decision or a work order it lists already stays listed once.
func (r Revision) Extend(decision string, finished []string) Revision {
	next := r
	next.DecisionIDs = slices.Clone(r.DecisionIDs)
	if !slices.Contains(next.DecisionIDs, decision) {
		next.DecisionIDs = append(next.DecisionIDs, decision)
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

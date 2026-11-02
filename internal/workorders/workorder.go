package workorders

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.dev/ropeshift/internal/ledger"
)

// Kind says what a work order produces.
type Kind string

// The kinds of work order.
const (
	KindPlan           Kind = "plan"
	KindContractReview Kind = "contract_review"
	KindChange         Kind = "change"
	KindRework         Kind = "rework"
)

// State is a state of the work order state machine.
type State string

// The states.
const (
	Intake         State = "intake"
	Planning       State = "planning"
	ContractReview State = "contract_review"
	Ready          State = "ready"
	Claimed        State = "claimed"
	Running        State = "running"
	Parked         State = "parked"
	Lapsed         State = "lapsed"
	Acceptance     State = "acceptance"
	Blocked        State = "blocked"
	Done           State = "done"
	Failed         State = "failed"
	Canceled       State = "canceled"
)

// SubjectKind is the event subject kind of a work order.
const SubjectKind = "work_order"

// EventCreated is the event that starts a work order's history.
const EventCreated = "work_order.created"

// WorkOrder is the projection of one work order's events. Zero values stand for
// absent fields.
type WorkOrder struct {
	ID              string            `json:"id"`
	Kind            Kind              `json:"kind"`
	State           State             `json:"state"`
	Title           string            `json:"title,omitempty"`
	RepositoryID    string            `json:"repository_id,omitempty"`
	Priority        int64             `json:"priority,omitempty"`
	Size            string            `json:"size,omitempty"`
	Requirements    []string          `json:"requirements,omitempty"`
	Needs           map[string]string `json:"needs,omitempty"`
	ContractID      string            `json:"contract_id,omitempty"`
	ContractVersion int64             `json:"contract_version,omitempty"`
	Fence           int64             `json:"fence,omitempty"`
	ClaimID         string            `json:"claim_id,omitempty"`
	LeaseWorker     string            `json:"lease_worker,omitempty"`
	LeaseUntil      int64             `json:"lease_until,omitempty"`
	TakeoverAfter   int64             `json:"takeover_after,omitempty"`
	Branch          string            `json:"branch,omitempty"`
	ResumeLaneID    string            `json:"resume_lane_id,omitempty"`
	ReworkOf        string            `json:"rework_of,omitempty"`
	Attempts        int64             `json:"attempts,omitempty"`
	MaxAttempts     int64             `json:"max_attempts,omitempty"`
	Deadline        int64             `json:"deadline,omitempty"`
	ReviewedBy      string            `json:"reviewed_by,omitempty"`
	Rev             int64             `json:"rev,omitempty"`
	// ReviewsPlan is the plan a `contract_review` work order reads, and ReviewPins the contract
	// versions it reads: a review covers those versions and no later one.
	ReviewsPlan string   `json:"reviews_plan,omitempty"`
	ReviewPins  []string `json:"review_pins,omitempty"`
	// PlanCommit is the commit intake read for an accepted `plan` work order (D17).
	PlanCommit string `json:"plan_commit,omitempty"`
}

// Completed reports a state past its lane's work: the work is waiting for acceptance or was
// accepted.
func (s State) Completed() bool { return s == Acceptance || s == Done }

// ErrInvalid marks a work order the ledger must not accept.
var ErrInvalid = errors.New("invalid work order")

// New validates a client's draft and returns the record in its first state. The
// client generates the ID, which makes a retry idempotent.
func New(draft WorkOrder) (WorkOrder, error) {
	if draft.ID == "" {
		return WorkOrder{}, fmt.Errorf("%w: no id", ErrInvalid)
	}
	switch draft.Kind {
	case KindPlan, KindContractReview, KindChange, KindRework:
	default:
		return WorkOrder{}, fmt.Errorf("%w: kind %q", ErrInvalid, draft.Kind)
	}
	draft.State = Intake
	return draft, nil
}

// NewRework is the work that follows a completed work order when something it rested on
// changed. It keeps what the first one needs and pins contractVersion, and it starts fresh.
func NewRework(id string, of WorkOrder, title string, contractVersion int64) (WorkOrder, error) {
	return New(WorkOrder{
		ID: id, Kind: KindRework, Title: title, RepositoryID: of.RepositoryID, Priority: of.Priority, Size: of.Size,
		Requirements: of.Requirements, Needs: of.Needs, ContractID: of.ContractID, ContractVersion: contractVersion,
		ReworkOf: of.ID, MaxAttempts: of.MaxAttempts,
	})
}

// CreatedEvent is the event that records a new work order.
func CreatedEvent(w WorkOrder, actor ledger.Actor) (ledger.EventDraft, error) {
	data, err := json.Marshal(w)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{
		Type:    EventCreated,
		Actor:   actor,
		Subject: ledger.Subject{Kind: SubjectKind, ID: w.ID},
		Data:    data,
	}, nil
}

// Change is the data of every work order event after the first. It holds what the
// projection takes from the event, so a replay needs nothing else.
type Change struct {
	Trigger    Trigger `json:"trigger,omitempty"`
	Guarded    bool    `json:"guarded,omitempty"`
	Fence      int64   `json:"fence,omitempty"`
	ClaimID    string  `json:"claim_id,omitempty"`
	Worker     string  `json:"worker,omitempty"`
	LeaseUntil int64   `json:"lease_until,omitempty"`
	Lane       string  `json:"lane,omitempty"`
	Generation int64   `json:"generation,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Outcome    Outcome `json:"outcome,omitempty"`
	Attempts   int64   `json:"attempts,omitempty"`
	Payload    []byte  `json:"payload,omitempty"`
	// ContractVersion is the version a pin moved to.
	ContractVersion int64 `json:"contract_version,omitempty"`
	// PlanCommit is the commit intake read for an accepted plan.
	PlanCommit string `json:"plan_commit,omitempty"`
}

// EventType names the event of a transition.
func EventType(trigger Trigger) string { return "work_order." + string(trigger) }

// EventFencedWrite is a write accepted under the fence guard, which changes no
// state.
const EventFencedWrite = "work_order.fenced_write"

// EventReviewPassed records a member's passed review, which changes no state. The
// definition of ready decides the move.
const EventReviewPassed = "work_order.review_passed"

// EventContractPinned records that unstarted work pinned a new contract version, which
// changes no state by itself and voids any review of the last version.
const EventContractPinned = "work_order.contract_pinned"

// EventPlanTakenIn records that intake read an accepted plan's commit, which changes no state.
const EventPlanTakenIn = "work_order.plan_taken_in"

// EventLaneDispatched records a request to run a lane, which changes no state. The
// worker reads it as desired state, and the lane's start moves the work order.
const EventLaneDispatched = "work_order.lane_dispatched"

// EventLaneRestarted records a lane's next generation after a crash, under the same claim.
const EventLaneRestarted = "work_order.lane_restarted"

// EventLaneHandoff records a lane's next generation after a handoff, under the same claim.
const EventLaneHandoff = "work_order.lane_handoff"

// EventLaneParking records a lane beginning to park, which changes no state of the work
// order: renewal keeps covering its lease until the process is gone.
const EventLaneParking = "work_order.lane_parking"

// EventLaneResumed records a woken lane's next generation continuing its earlier session.
const EventLaneResumed = "work_order.lane_resumed"

// EventGenerationBound records what a generation runs on, which changes no state.
const EventGenerationBound = "work_order.generation_bound"

// EventAdmissionHeld records that a worker held a lane back from starting, with why.
const EventAdmissionHeld = "work_order.admission_held"

// EventGenerationResponded records a model response, which starts the resume table's cache
// clock and changes no state.
const EventGenerationResponded = "work_order.generation_responded"

// EventGenerationBaseline records what a generation held at its first call, which changes no state.
const EventGenerationBaseline = "work_order.generation_baseline"

// GenerationBinding is the payload of EventGenerationBound. It records the resolved
// model and not only the alias, because aliases drift (FR-DRV-10).
type GenerationBinding struct {
	SessionID      string `json:"session_id"`
	ModelRequested string `json:"model_requested"`
	ModelResolved  string `json:"model_resolved"`
	ProfileDigest  string `json:"profile_digest"`
	DriverVersion  string `json:"driver_version"`
	DegradedMode   bool   `json:"degraded_mode,omitempty"`
	// FellBack says a generation that meant to resume started fresh from its handoff, because
	// its resume never reached a ready state.
	FellBack bool `json:"fell_back,omitempty"`
	// Carried names the messages this generation's first prompt stated. A binding shows that
	// prompt reached a turn, so a generation that never got there leaves them for the next.
	Carried []string `json:"carried,omitempty"`
	// PackID and PackVersion name the context pack this generation read.
	PackID      string `json:"pack_id,omitempty"`
	PackVersion int64  `json:"pack_version,omitempty"`
}

// PinEvent is the event of unstarted work taking a new contract version.
func PinEvent(id string, version int64, actor ledger.Actor) (ledger.EventDraft, error) {
	return ChangeEvent(EventContractPinned, id, Change{ContractVersion: version}, actor)
}

// ChangeEvent is the event of one change to a work order.
func ChangeEvent(eventType, id string, change Change, actor ledger.Actor) (ledger.EventDraft, error) {
	data, err := json.Marshal(change)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{
		Type:    eventType,
		Actor:   actor,
		Subject: ledger.Subject{Kind: SubjectKind, ID: id},
		Data:    data,
	}, nil
}

// Replay rebuilds work orders from events, in revision order, through the state
// machine. An event the machine has no edge for is an error, so a log that holds
// two owners or a stale write cannot replay. Events about other subjects are
// skipped, because the log is shared by every record kind.
func Replay(events []ledger.Event) (map[string]WorkOrder, error) {
	book := map[string]WorkOrder{}
	for _, e := range events {
		if e.Subject.Kind != SubjectKind {
			continue
		}
		current, known := book[e.Subject.ID]
		next, err := apply(current, known, e)
		if err != nil {
			return nil, fmt.Errorf("rev %d: %s: %w", e.Rev, e.Subject.ID, err)
		}
		book[e.Subject.ID] = next
	}
	return book, nil
}

func apply(current WorkOrder, known bool, e ledger.Event) (WorkOrder, error) {
	if e.Type == EventCreated {
		if known {
			return current, fmt.Errorf("%w: created twice", ErrInvalid)
		}
		var created WorkOrder
		if err := json.Unmarshal(e.Data, &created); err != nil {
			return current, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return created, nil
	}
	if !known {
		return current, fmt.Errorf("%w: %s before creation", ErrInvalid, e.Type)
	}
	if e.Type == EventReviewPassed {
		current.ReviewedBy = e.Actor.ID
		return current, nil
	}
	if e.Type == EventEvidenceRecorded {
		return current, nil
	}
	var change Change
	if err := json.Unmarshal(e.Data, &change); err != nil {
		return current, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if e.Type == EventContractPinned {
		current.ContractVersion, current.ReviewedBy = change.ContractVersion, ""
		return current, nil
	}
	if e.Type == EventPlanTakenIn {
		current.PlanCommit = change.PlanCommit
		return current, nil
	}
	if change.Guarded {
		if change.Fence != current.Fence {
			return current, fmt.Errorf("%w: a write with fence %d under fence %d", ErrInvalid, change.Fence, current.Fence)
		}
		current.Rev++
	}
	if change.Trigger == "" {
		if !change.Guarded {
			return current, fmt.Errorf("%w: %s changes nothing", ErrInvalid, e.Type)
		}
		return current, nil
	}
	edge, ok := Next(current.State, change.Trigger)
	if !ok {
		return current, fmt.Errorf("%w: %s from %s", ErrInvalid, change.Trigger, current.State)
	}
	return applyEdge(current, edge, change)
}

func applyEdge(w WorkOrder, edge Transition, change Change) (WorkOrder, error) {
	switch edge.Trigger {
	case TriggerClaimGranted:
		if change.Fence <= w.Fence {
			return w, fmt.Errorf("%w: grant at fence %d after fence %d", ErrInvalid, change.Fence, w.Fence)
		}
		w.Fence, w.ClaimID, w.LeaseWorker = change.Fence, change.ClaimID, change.Worker
		w.LeaseUntil = change.LeaseUntil
	case TriggerLaneStarted:
		if change.Fence != w.Fence {
			return w, fmt.Errorf("%w: lane started at fence %d under fence %d", ErrInvalid, change.Fence, w.Fence)
		}
		w.ResumeLaneID = ""
	case TriggerReturnedStarted, TriggerReturnedUnstarted:
		w.LeaseUntil = change.LeaseUntil
	case TriggerClaimReleased, TriggerTakeover:
		if change.Fence <= w.Fence {
			return w, fmt.Errorf("%w: ownership ended at fence %d after fence %d", ErrInvalid, change.Fence, w.Fence)
		}
		w.Fence, w.ClaimID, w.LeaseWorker, w.LeaseUntil = change.Fence, "", "", 0
	case TriggerLaneCompleted, TriggerLaneBlocked, TriggerLaneFailedRetry, TriggerAttemptsExhausted:
		if change.Fence <= w.Fence {
			return w, fmt.Errorf("%w: a lane ended at fence %d after fence %d", ErrInvalid, change.Fence, w.Fence)
		}
		w.Fence, w.ClaimID, w.LeaseWorker, w.LeaseUntil = change.Fence, "", "", 0
		w.Attempts = change.Attempts
	case TriggerLaneParked:
		if change.Fence <= w.Fence {
			return w, fmt.Errorf("%w: a lane parked at fence %d after fence %d", ErrInvalid, change.Fence, w.Fence)
		}
		w.Fence, w.ClaimID, w.LeaseWorker, w.LeaseUntil = change.Fence, "", "", 0
	case TriggerWakeFired:
		w.ResumeLaneID = change.Lane
	case TriggerContractChange:
		w.ReviewedBy = ""
	}
	w.State = edge.To
	return w, nil
}

// Move returns the record after a table transition. A leased edge belongs to the
// lease use cases.
func Move(w WorkOrder, trigger Trigger) (Transition, error) {
	edge, ok := Next(w.State, trigger)
	if !ok {
		return Transition{}, fmt.Errorf("%w: %s from %s", ErrIllegalMove, trigger, w.State)
	}
	if edge.Leased {
		return Transition{}, fmt.Errorf("%w: %s", ErrLeasedEdge, trigger)
	}
	return edge, nil
}

// Diff names the fields where two records disagree. It returns nothing when they
// match, so a check can print what a replay and a projection disagree about.
func Diff(a, b WorkOrder) []string {
	var fields []string
	note := func(name string, same bool) {
		if !same {
			fields = append(fields, name)
		}
	}
	note("id", a.ID == b.ID)
	note("kind", a.Kind == b.Kind)
	note("state", a.State == b.State)
	note("title", a.Title == b.Title)
	note("repository_id", a.RepositoryID == b.RepositoryID)
	note("priority", a.Priority == b.Priority)
	note("size", a.Size == b.Size)
	note("requirements", slices.Equal(a.Requirements, b.Requirements))
	note("needs", sameNeeds(a.Needs, b.Needs))
	note("contract_id", a.ContractID == b.ContractID)
	note("contract_version", a.ContractVersion == b.ContractVersion)
	note("fence", a.Fence == b.Fence)
	note("claim_id", a.ClaimID == b.ClaimID)
	note("lease_worker", a.LeaseWorker == b.LeaseWorker)
	note("takeover_after", a.TakeoverAfter == b.TakeoverAfter)
	note("branch", a.Branch == b.Branch)
	note("resume_lane_id", a.ResumeLaneID == b.ResumeLaneID)
	note("rework_of", a.ReworkOf == b.ReworkOf)
	note("attempts", a.Attempts == b.Attempts)
	note("max_attempts", a.MaxAttempts == b.MaxAttempts)
	note("deadline", a.Deadline == b.Deadline)
	note("reviewed_by", a.ReviewedBy == b.ReviewedBy)
	note("rev", a.Rev == b.Rev)
	note("reviews_plan", a.ReviewsPlan == b.ReviewsPlan)
	note("review_pins", slices.Equal(a.ReviewPins, b.ReviewPins))
	note("plan_commit", a.PlanCommit == b.PlanCommit)
	return fields
}

func sameNeeds(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

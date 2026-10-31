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
	Rev             int64             `json:"rev,omitempty"`
}

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

// Replay rebuilds work orders from events, in revision order. Events about other
// subjects are skipped, because the log is shared by every record kind.
func Replay(events []ledger.Event) (map[string]WorkOrder, error) {
	book := map[string]WorkOrder{}
	for _, e := range events {
		if e.Subject.Kind != SubjectKind {
			continue
		}
		current, known := book[e.Subject.ID]
		next, err := apply(current, known, e)
		if err != nil {
			return nil, fmt.Errorf("rev %d: %w", e.Rev, err)
		}
		book[e.Subject.ID] = next
	}
	return book, nil
}

func apply(current WorkOrder, known bool, e ledger.Event) (WorkOrder, error) {
	if e.Type != EventCreated {
		return current, fmt.Errorf("%w: unknown event %q", ErrInvalid, e.Type)
	}
	if known {
		return current, fmt.Errorf("%w: %s created twice", ErrInvalid, e.Subject.ID)
	}
	var created WorkOrder
	if err := json.Unmarshal(e.Data, &created); err != nil {
		return current, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return created, nil
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
	note("rev", a.Rev == b.Rev)
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

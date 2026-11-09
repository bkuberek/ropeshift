package questions

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
)

// The categories every workspace has. A workspace can add its own, and it cannot remove
// these (FR-QD-1).
const (
	CategoryProduct      = "product"
	CategoryDesign       = "design"
	CategoryArchitecture = "architecture"
	CategoryData         = "data"
	CategorySecurity     = "security"
	CategoryCompliance   = "compliance"
	CategoryOperations   = "operations"
	CategoryBusiness     = "business"
)

// BuiltIn lists the categories every workspace has.
func BuiltIn() []string {
	return []string{
		CategoryProduct, CategoryDesign, CategoryArchitecture, CategoryData,
		CategorySecurity, CategoryCompliance, CategoryOperations, CategoryBusiness,
	}
}

// Status is a question's place on its way to an answer.
type Status string

// The statuses of a question.
const (
	StatusOpen       Status = "open"
	StatusTriage     Status = "triage"
	StatusNeedsHuman Status = "needs_human"
	StatusAnswered   Status = "answered"
	StatusDismissed  Status = "dismissed"
)

// Unresolved reports a status that still waits for an answer.
func (s Status) Unresolved() bool {
	return s == StatusOpen || s == StatusTriage || s == StatusNeedsHuman
}

// DecisionStatus says what a decision stands for now.
type DecisionStatus string

// The statuses of a decision.
const (
	DecisionAssumed    DecisionStatus = "assumed"
	DecisionDecided    DecisionStatus = "decided"
	DecisionOverturned DecisionStatus = "overturned"
	DecisionReplaced   DecisionStatus = "replaced"
)

// InForce reports a decision that still stands.
func (s DecisionStatus) InForce() bool { return s == DecisionAssumed || s == DecisionDecided }

// The reversibility of a decision.
const (
	Reversible = "reversible"
	OneWay     = "one_way"
)

// The kinds of record search holds.
const (
	KindQuestion    = "question"
	KindDecision    = "decision"
	KindRequirement = "requirement"
	KindContract    = "contract"
)

// LookupKinds are what rung 1 reads, and OpenKinds what rung 2 reads.
func LookupKinds() []string { return []string{KindDecision, KindRequirement, KindContract} }

// OpenKinds are what rung 2 reads.
func OpenKinds() []string { return []string{KindQuestion} }

// The event subject kinds of this package.
const (
	SubjectQuestion = "question"
	SubjectDecision = "decision"
	SubjectCategory = "category"
	SubjectPolicy   = "policy"
)

// The events that record questions and decisions. Each holds the whole record, so a replay
// needs nothing else.
const (
	EventQuestionChanged  = "question.changed"
	EventQuestionAttached = "question.attached"
	EventDecisionRecorded = "decision.recorded"
	EventDecisionEnded    = "decision.ended"
	EventDecisionNoticed  = "decision.noticed"
	EventCategoryAdded    = "category.added"
	EventPolicyPublished  = "policy.published"
)

// EventExperienceRecorded holds a whole experience record, of a lane or a decision, and
// EventOutcomeFilled the outcome it gains once.
const (
	EventExperienceRecorded = "experience.recorded"
	EventOutcomeFilled      = "experience.outcome"
)

// Errors of the question use cases.
var (
	// ErrInvalid marks a record the ledger must not accept.
	ErrInvalid = errors.New("invalid question or decision")
)

// Question is one question of a workspace. It has categories and no assignee, so members
// find what they can answer (ADR-0007).
type Question struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	NormalizedHash   string   `json:"normalized_hash"`
	Categories       []string `json:"categories,omitempty"`
	Status           Status   `json:"status"`
	Rung             int64    `json:"rung"`
	Blocking         bool     `json:"blocking,omitempty"`
	SubjectKind      string   `json:"subject_kind,omitempty"`
	SubjectID        string   `json:"subject_id,omitempty"`
	BatchID          string   `json:"batch_id,omitempty"`
	AskedBy          string   `json:"asked_by,omitempty"`
	Options          []string `json:"options,omitempty"`
	Recommendation   string   `json:"recommendation,omitempty"`
	EscalationReason string   `json:"escalation_reason,omitempty"`
	LadderTrace      []Step   `json:"ladder_trace,omitempty"`
	ClaimedBy        string   `json:"claimed_by,omitempty"`
	ClaimUntil       int64    `json:"claim_until,omitempty"`
	DecisionID       string   `json:"decision_id,omitempty"`
}

// Step is one rung a question passed, with what it considered there.
type Step struct {
	Rung       int64    `json:"rung"`
	Candidates []string `json:"candidates,omitempty"`
	Outcome    string   `json:"outcome,omitempty"`
}

// Attachment joins one lane to a question it is waiting on or has asked.
type Attachment struct {
	QuestionID string `json:"question_id"`
	Lane       string `json:"lane"`
	Generation int64  `json:"generation"`
	Blocking   bool   `json:"blocking,omitempty"`
	AttachedAt int64  `json:"attached_at"`
}

// Decision is what resolved a question, or a choice recorded by itself. It stays on record
// when it is overturned or replaced (FR-QD-9).
type Decision struct {
	ID            string         `json:"id"`
	Status        DecisionStatus `json:"status"`
	Statement     string         `json:"statement"`
	Rationale     string         `json:"rationale,omitempty"`
	Citations     []string       `json:"citations,omitempty"`
	Scope         string         `json:"scope,omitempty"`
	Reversibility string         `json:"reversibility,omitempty"`
	Categories    []string       `json:"categories,omitempty"`
	DecidedBy     string         `json:"decided_by,omitempty"`
	Supersedes    string         `json:"supersedes,omitempty"`
	QuestionID    string         `json:"question_id,omitempty"`
	PolicyVersion int64          `json:"policy_version,omitempty"`
	// Overlaps names the decisions in force with an overlapping scope when this one was
	// recorded, and NoConflict those its author judged compatible.
	Overlaps   []string `json:"overlaps,omitempty"`
	NoConflict []string `json:"no_conflict,omitempty"`
}

// maxCategoryName bounds a name a person has to read in a list.
const maxCategoryName = 63

// CheckCategoryName accepts a name a workspace can add. A name has no mark a LIKE pattern
// reads, and no capital, so one category cannot appear under two spellings.
func CheckCategoryName(name string) error {
	valid := name != "" && len(name) <= maxCategoryName && name[0] >= 'a' && name[0] <= 'z'
	for _, c := range []byte(name) {
		valid = valid && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-')
	}
	if !valid {
		return fmt.Errorf("%w: category %q must be lowercase words joined by hyphens", ErrInvalid, name)
	}
	return nil
}

// CheckCategories accepts categories a workspace has: the built-in ones and its own.
func CheckCategories(names, added []string) error {
	for _, name := range names {
		if !slices.Contains(BuiltIn(), name) && !slices.Contains(added, name) {
			return fmt.Errorf("%w: unknown category %q", ErrInvalid, name)
		}
	}
	return nil
}

// New validates a draft and returns the record in its first state. The client generates
// the ID, which makes a retry idempotent.
func New(draft Question) (Question, error) {
	if draft.ID == "" {
		return Question{}, fmt.Errorf("%w: a question has no id", ErrInvalid)
	}
	if Normalize(draft.Text) == "" {
		return Question{}, fmt.Errorf("%w: a question has no text", ErrInvalid)
	}
	draft.NormalizedHash = Hash(draft.Text)
	draft.Status = StatusOpen
	draft.Rung = 1
	return draft, nil
}

// NewDecision validates a draft. A new decision is `decided` or `assumed`, because the other
// two statuses only ever follow one of those.
func NewDecision(draft Decision) (Decision, error) {
	if draft.ID == "" {
		return Decision{}, fmt.Errorf("%w: a decision has no id", ErrInvalid)
	}
	if draft.Statement == "" {
		return Decision{}, fmt.Errorf("%w: a decision states nothing", ErrInvalid)
	}
	if draft.Status != DecisionDecided && draft.Status != DecisionAssumed {
		return Decision{}, fmt.Errorf("%w: a decision cannot start as %q", ErrInvalid, draft.Status)
	}
	switch draft.Reversibility {
	case "", Reversible, OneWay:
		return draft, nil
	}
	return Decision{}, fmt.Errorf("%w: reversibility %q", ErrInvalid, draft.Reversibility)
}

// RecordEvent is the event that records one record.
func RecordEvent(eventType, subjectKind, subjectID string, record any, actor ledger.Actor) (ledger.EventDraft, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return ledger.EventDraft{}, err
	}
	return ledger.EventDraft{
		Type:    eventType,
		Actor:   actor,
		Subject: ledger.Subject{Kind: subjectKind, ID: subjectID},
		Data:    data,
	}, nil
}

// Book is what the question events replay to.
type Book struct {
	Questions   map[string]Question
	Attachments map[string]Attachment
	Decisions   map[string]Decision
	Categories  map[string]bool
	Batches     map[string]Batch
	Notices     map[string]Notice
	Outcomes    map[string]measures.Outcome
	Policies    map[string]authority.PolicyVersion
	// Experiences are the records as written, with no outcome, by ExperienceKey.
	Experiences map[string]measures.Experience
}

// ExperienceKey identifies an experience record in a Book.
func ExperienceKey(kind, subject string) string { return kind + "/" + subject }

// AttachmentKey identifies an attachment in a Book.
func AttachmentKey(question, lane string) string { return question + "/" + lane }

// NoticeKey identifies a notice in a Book.
func NoticeKey(decision, lane string) string { return decision + "/" + lane }

// PolicyKey identifies a policy version in a Book.
func PolicyKey(kind authority.PolicyKind, version int64) string {
	return fmt.Sprintf("%s/%d", kind, version)
}

// Replay rebuilds questions and decisions from events, in revision order.
func Replay(events []ledger.Event) (Book, error) {
	book := Book{
		Questions: map[string]Question{}, Attachments: map[string]Attachment{},
		Decisions: map[string]Decision{}, Categories: map[string]bool{}, Batches: map[string]Batch{},
		Notices: map[string]Notice{}, Outcomes: map[string]measures.Outcome{}, Policies: map[string]authority.PolicyVersion{},
		Experiences: map[string]measures.Experience{},
	}
	for _, e := range events {
		if err := book.apply(e); err != nil {
			return Book{}, fmt.Errorf("question replay: rev %d: %w", e.Rev, err)
		}
	}
	return book, nil
}

func (b Book) apply(e ledger.Event) error {
	switch e.Type {
	case EventQuestionChanged:
		var question Question
		if err := json.Unmarshal(e.Data, &question); err != nil {
			return err
		}
		b.Questions[question.ID] = question
	case EventQuestionAttached:
		var attachment Attachment
		if err := json.Unmarshal(e.Data, &attachment); err != nil {
			return err
		}
		b.Attachments[AttachmentKey(attachment.QuestionID, attachment.Lane)] = attachment
	case EventDecisionRecorded, EventDecisionEnded:
		var decision Decision
		if err := json.Unmarshal(e.Data, &decision); err != nil {
			return err
		}
		b.Decisions[decision.ID] = decision
	case EventDecisionNoticed:
		var notice Notice
		if err := json.Unmarshal(e.Data, &notice); err != nil {
			return err
		}
		b.Notices[NoticeKey(notice.DecisionID, notice.Lane)] = notice
	case EventOutcomeFilled:
		var outcome measures.Outcome
		if err := json.Unmarshal(e.Data, &outcome); err != nil {
			return err
		}
		if _, filled := b.Outcomes[e.Subject.ID]; filled {
			return fmt.Errorf("%w: the outcome of %s is filled twice", ErrInvalid, e.Subject.ID)
		}
		b.Outcomes[e.Subject.ID] = outcome
	case EventExperienceRecorded:
		var record measures.Experience
		if err := json.Unmarshal(e.Data, &record); err != nil {
			return err
		}
		key := ExperienceKey(record.SubjectKind, record.SubjectID)
		if _, written := b.Experiences[key]; written {
			return fmt.Errorf("%w: the experience of %s is written twice", ErrInvalid, key)
		}
		b.Experiences[key] = record
	case EventPolicyPublished:
		var version authority.PolicyVersion
		if err := json.Unmarshal(e.Data, &version); err != nil {
			return err
		}
		b.Policies[PolicyKey(version.Kind, version.Version)] = version
	case EventCategoryAdded:
		b.Categories[e.Subject.ID] = true
	case EventBatchChanged:
		var batch Batch
		if err := json.Unmarshal(e.Data, &batch); err != nil {
			return err
		}
		b.Batches[batch.ID] = batch
	case EventBatchDelivered:
		var delivery Delivery
		if err := json.Unmarshal(e.Data, &delivery); err != nil {
			return err
		}
		b.Batches[delivery.Batch.ID] = delivery.Batch
	}
	return nil
}

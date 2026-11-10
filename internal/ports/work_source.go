package ports

import (
	"context"
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

// ErrUnsupported means a source has no such capability. The core tolerates a
// capability disappearing, so this is an answer and not a failure.
var ErrUnsupported = errors.New("unsupported by this source")

// Cursor is an opaque position in a source. An empty cursor reads from the start.
type Cursor string

// ScopeRef names one part of a source, for field discovery.
type ScopeRef struct {
	Kind string
	ID   string
}

// SourceCapabilities says what a source can do.
type SourceCapabilities struct {
	Write    bool
	Webhooks bool
}

// SourceDescription is what a source says about itself. Adapter and Instance are
// the first two parts of every external key it returns.
type SourceDescription struct {
	Adapter      string
	Instance     string
	Capabilities SourceCapabilities
	APIVersions  []string
	BudgetModel  string
	// Remote and DefaultBranch are what a source that reads a git repository knows of it: the URL of the
	// remote work lands on, with no credential in it, and the branch that work lands on. A source with
	// no repository leaves both empty.
	Remote        string
	DefaultBranch string
}

// FieldSchema is one field a source holds.
type FieldSchema struct {
	Name     string
	Type     string
	Required bool
}

// WorkItem is one source record at one version. Digest identifies its content, and
// Source names where this version came from. Path names it inside the source.
type WorkItem struct {
	Key       ledger.ExternalKey
	Digest    string
	Source    string
	Path      string
	Contract  planning.ContractFile
	Statement string
}

// RecordItem is one file that is not a contract, at one version: a goal, a plan, or a document of
// any kind. Digest identifies the content of the file as written. The field of its kind is set, and
// Key's native ID is the file's own `id`. A document of a kind that holds nothing the ledger reads
// as a table, such as a spec or a milestone, sets none of them, and Head says what it is.
type RecordItem struct {
	Key          ledger.ExternalKey
	Source       string
	Path         string
	Digest       string
	Goal         *planning.GoalFile
	Plan         *planning.PlanFile
	Requirements *planning.RequirementsFile
	// File is the decoded file, which is where the other document kinds are.
	File      planning.Document
	Statement string
}

// Head reads what the ledger keeps of this file when it is a document, whatever its kind.
func (r RecordItem) Head() (planning.DocumentHead, bool) { return r.File.Head() }

// ChangeBatch is what a poll found. Items are contracts, and Records are everything else, in the
// order intake needs: goals, plans, requirements, the other documents, then milestones, so that each
// finds what it names. Cursor equals the cursor passed in when nothing moved. A file a source cannot
// read is a problem in the batch and fails no other file.
type ChangeBatch struct {
	Items    []WorkItem
	Records  []RecordItem
	Problems []planning.SourceProblem
	Cursor   Cursor
}

// RecordOf builds the record item of a file that is not a contract, or reports false. Digest is the
// source's name for the content of the file as written.
func RecordOf(doc planning.Document, adapter, instance, path, source, digest string) (RecordItem, bool) {
	item := RecordItem{
		Source: source, Path: path, Digest: digest, Goal: doc.Goal, Plan: doc.Plan, Requirements: doc.Requirements,
		File: doc, Statement: doc.Statement,
	}
	switch head, document := doc.Head(); {
	case doc.Goal != nil:
		item.Key.NativeID = doc.Goal.ID
	case doc.Plan != nil:
		item.Key.NativeID = doc.Plan.ID
	case document:
		item.Key.NativeID = head.ID
	default:
		return RecordItem{}, false
	}
	item.Key.Adapter, item.Key.Instance = adapter, instance
	return item, true
}

// OrderRecords puts goals first, then plans, requirements, the other documents, and milestones last,
// so that a plan finds its goal and a document its plan in one batch. Within a rank, the source's
// own order stays.
func OrderRecords(items []RecordItem) {
	slices.SortStableFunc(items, func(a, b RecordItem) int { return recordRank(a) - recordRank(b) })
}

func recordRank(item RecordItem) int {
	switch {
	case item.Goal != nil:
		return 0
	case item.Plan != nil:
		return 1
	case item.Requirements != nil:
		return 2
	case item.File.Milestone != nil:
		return 4
	}
	return 3
}

// InboundDelivery is one webhook delivery.
type InboundDelivery struct {
	ID      string
	Headers map[string]string
	Body    []byte
}

// ChangeHint says a record may have changed. It carries no state.
type ChangeHint struct {
	Key ledger.ExternalKey
}

// OutboxItem is one write to a source.
type OutboxItem struct {
	ID             string
	Key            ledger.ExternalKey
	Steps          []string
	IdempotencyKey string
}

// WriteStatus is how a write to a source ended.
type WriteStatus string

// The write statuses. Sent, failed, and transition_unavailable are the outbox's
// endings. Unsupported is the answer of a source with no write capability.
const (
	WriteSent                  WriteStatus = "sent"
	WriteFailed                WriteStatus = "failed"
	WriteTransitionUnavailable WriteStatus = "transition_unavailable"
	WriteUnsupported           WriteStatus = "unsupported"
)

// WriteResult is the outcome of one outbox item.
type WriteResult struct {
	Status WriteStatus
	Detail string
}

// SourceHealth is the state of a source's credentials and registration.
type SourceHealth struct {
	State  string
	Detail string
}

// Lineage says how what a source reads now stands to a version the ledger holds from it.
type Lineage string

// The ways two versions of one file can stand.
const (
	// LineageAhead says the read was written knowing the version the ledger holds, so what differs
	// is a change.
	LineageAhead Lineage = "ahead"
	// LineageBehind says the version the ledger holds was written knowing the read, so the read is
	// only old, and nothing changed.
	LineageBehind Lineage = "behind"
	// LineageDiverged says neither knew the other, so each changed a base that moved.
	LineageDiverged Lineage = "diverged"
)

// WorkSource reads planned work from outside the ledger. The contract in the
// architecture's adapter section binds every implementation.
type WorkSource interface {
	// Describe reports capabilities, targeted API versions, and the budget model.
	Describe(ctx context.Context) (SourceDescription, error)
	// Fields discovers the field schema for one scope.
	Fields(ctx context.Context, scope ScopeRef) ([]FieldSchema, error)
	// Poll returns changes since the cursor. It must be a complete path on its own.
	Poll(ctx context.Context, cursor Cursor) (ChangeBatch, error)
	// Read returns current state for one item, or ErrNotFound.
	Read(ctx context.Context, key ledger.ExternalKey) (WorkItem, error)
	// Relate reads how a version it read stands to one the ledger recorded, each named by its
	// Source. A recorded version it can no longer find has nothing to compare, and reads as ahead.
	// A source with one line of history answers ErrUnsupported.
	Relate(ctx context.Context, read, recorded string) (Lineage, error)
	// VerifyDelivery authenticates a webhook and converts it to a hint.
	VerifyDelivery(ctx context.Context, d InboundDelivery) (ChangeHint, error)
	// Apply performs one outbox item and reports the outcome.
	Apply(ctx context.Context, item OutboxItem) (WriteResult, error)
	// Health reports credential and registration state.
	Health(ctx context.Context) (SourceHealth, error)
}

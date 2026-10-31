package ports

import (
	"context"
	"errors"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
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

// ChangeBatch is what a poll found. Cursor equals the cursor passed in when nothing
// moved. A file a source cannot read is a problem in the batch and fails no other
// file.
type ChangeBatch struct {
	Items    []WorkItem
	Problems []planning.SourceProblem
	Cursor   Cursor
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
	// VerifyDelivery authenticates a webhook and converts it to a hint.
	VerifyDelivery(ctx context.Context, d InboundDelivery) (ChangeHint, error)
	// Apply performs one outbox item and reports the outcome.
	Apply(ctx context.Context, item OutboxItem) (WriteResult, error)
	// Health reports credential and registration state.
	Health(ctx context.Context) (SourceHealth, error)
}

package ledger

import "encoding/json"

// The kinds of owner a generation digest names. A lane's and an assistant's digests are records of their
// project's ledger, and an orchestrator's is an installation record, because an orchestrator serves every
// project on its machine.
const (
	DigestOwnerLane         = "lane"
	DigestOwnerAssistant    = "assistant"
	DigestOwnerOrchestrator = "orchestrator"
)

// EventGenerationDigested records that a generation's digest reached the ledger (FR-SLOG-2).
const EventGenerationDigested = "generation.digested"

// GenerationDigest is one generation's digest as the ledger keeps it: its owner and the generation, which
// name it for good, and its body, which is the worker's digest as it sent it. The ledger keeps the first
// digest of a generation, so a worker that sends it again after a crash records nothing new.
type GenerationDigest struct {
	// OwnerKind is a lane, an assistant's conversation, or an orchestrator, and OwnerID its ID.
	OwnerKind  string          `json:"owner_kind"`
	OwnerID    string          `json:"owner_id"`
	Generation int64           `json:"generation"`
	Body       json.RawMessage `json:"body"`
	// RecordedAt is database time.
	RecordedAt int64 `json:"recorded_at,omitempty"`
}

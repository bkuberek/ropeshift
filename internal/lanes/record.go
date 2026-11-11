package lanes

import (
	"crypto/sha256"
	"encoding/hex"
)

// The states a worker journals for a lane. The full lane state machine derives from
// driver events in a later unit, and these are what a restart must know.
const (
	StateRunning = "running"
	StateEnded   = "ended"
	// StateArchived says a lane's worktree went at a member's request.
	StateArchived = "archived"
	// StateParked says a lane's process is gone and its commit is on the way to the ledger,
	// or there. A wake brings it back.
	StateParked = "parked"
)

// Record is what a worker keeps about one lane, so a restart works from its journal
// alone. It holds the digest of the lane's token and never the token.
type Record struct {
	ID string
	// Project is the project the lane's work order belongs to. Empty names the plane's only project.
	Project     string
	WorkOrder   string
	ClaimID     string
	Fence       int64
	Generation  int64
	State       string
	TokenDigest string
	Worktree    string
	Branch      string
	// Report is the outcome the lane reported, as JSON.
	Report []byte
	// SessionID is the session a generation bound to, once it bound.
	SessionID string
	// Progress is what a restart reads a session's stream from.
	Progress Progress
	// Actual is where the lane's state stood, so an adopted run goes on from there.
	Actual ActualState
	// CompactFrom is the state a compaction under way left, kept in the write that moved Actual,
	// because the end of a compaction returns to it.
	CompactFrom ActualState
	// ResumeSession is the session a restarted generation continues.
	ResumeSession string
	// ResetsSeen is how many human resets of the breaker this worker has acted on.
	ResetsSeen int64
	// Guard is how the last stop left the worktree.
	Guard GuardResult
	// Handoff is how far the lane's last handoff got. Once its generation began, its
	// document is what that generation was briefed from.
	Handoff *HandoffProgress
	// Parking is how far the lane's last parking got.
	Parking *ParkProgress
	// Blocking names the questions this lane cannot go on without.
	Blocking []string
	// RunMode is the mode the lane's session started in, which a resume stays in.
	RunMode string
	// Process is the generation whose process runs now. A clear moves Generation and not this,
	// because what a session left on this machine is named by the generation it started in.
	Process int64
	// Owner is the kind of principal this record's token stands for. Empty is a lane.
	Owner string
	// RespawnsSeen is how many of a member's requests for a new generation this worker acted on.
	RespawnsSeen int64
}

// ProcessGeneration is the generation that names this lane's session. A generation no process has
// started in yet is its own.
func (r Record) ProcessGeneration() int64 {
	if r.Process != 0 {
		return r.Process
	}
	return r.Generation
}

// Progress is how far a worker read a session, and what that reading added up to, so an
// adoption continues the counts and drops a record it already took.
type Progress struct {
	// Cursor is the stream position of the last turn this worker finished with.
	Cursor int64     `json:"cursor"`
	Cost   CostMeter `json:"cost"`
	Usage  Usage     `json:"usage"`
	// PriorUSD and PriorUnknown are what earlier generations cost, because each generation
	// has a meter of its own.
	PriorUSD     float64 `json:"prior_usd,omitempty"`
	PriorUnknown int64   `json:"prior_unknown,omitempty"`
	// Instruction is what the session was told and has not finished, because a stopped turn
	// is not continued. Empty means the last turn ended.
	Instruction string `json:"instruction,omitempty"`
	// Carried names the messages this generation's first prompt stated. The worker holds no row
	// for them, and a lane can still reply to one after a restart.
	Carried []string `json:"carried,omitempty"`
	// ContextTokens is what the session held at its last call, and BaselineTokens what it held
	// at its first, which every path carries, so the difference is the conversation.
	ContextTokens  int64 `json:"context_tokens,omitempty"`
	BaselineTokens int64 `json:"baseline_tokens,omitempty"`
	// Pack names the context pack version this generation holds, which a handoff states.
	Pack *PackRef `json:"pack,omitempty"`
	// Compactions counts the compactions this generation asked for, CompactPending says the last
	// waits on its next call's usage, and CompactSeen that its boundary came.
	Compactions    int64 `json:"compactions,omitempty"`
	CompactPending bool  `json:"compact_pending,omitempty"`
	CompactSeen    bool  `json:"compact_seen,omitempty"`
	// OwesBrief says a clear left this generation with a session that does not know its work.
	OwesBrief bool `json:"owes_brief,omitempty"`
}

// TokenDigest is how a worker stores and compares a lane token.
func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// OwnerOrchestrator marks a record an orchestrator's session runs under, which supervision shares
// with a lane's. Its token is the only one the worker takes a decision from.
const OwnerOrchestrator = "orchestrator"

// OpenApproval is a request a lane is waiting on, which a worker keeps until it hears back, so a
// restart goes on waiting and a wait keeps its bound.
type OpenApproval struct {
	ID         string
	Lane       string
	Generation int64
	Tool       string
	// Digest is what a decision on this call binds to, and Seen how a hook names the call, which
	// knows no worktree and so covers its input alone.
	Digest     string
	Seen       string
	DeadlineMs int64
}

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
)

// Record is what a worker keeps about one lane, so a restart works from its journal
// alone. It holds the digest of the lane's token and never the token.
type Record struct {
	ID          string
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
}

// Progress is how far a worker read a session, and what that reading added up to, so an
// adoption continues the counts and drops a record it already took.
type Progress struct {
	// Cursor is the stream position of the last turn this worker finished with.
	Cursor int64     `json:"cursor"`
	Cost   CostMeter `json:"cost"`
	Usage  Usage     `json:"usage"`
}

// TokenDigest is how a worker stores and compares a lane token.
func TokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

package ledger

import (
	"errors"
	"fmt"
)

// ProtocolVersion is the plane protocol this build speaks.
const ProtocolVersion = 1

// The plane's refusals. Each is a different instruction to a worker.
var (
	// ErrBadJoinToken means the token is unknown, wrong, or spent.
	ErrBadJoinToken = errors.New("join token refused")
	// ErrBadCredential means the worker must register again.
	ErrBadCredential = errors.New("worker credential refused")
	// ErrStaleEpoch means a newer session of this worker is open, so this process
	// must open its own or stop.
	ErrStaleEpoch = errors.New("a newer worker epoch is open")
	// ErrNonce means an answer was not to this heartbeat, so it proves nothing about
	// the server.
	ErrNonce = errors.New("heartbeat answered with the wrong nonce")
)

// ProtocolError refuses a worker the control plane cannot speak to. It names both
// sides, so an operator knows which one to upgrade.
type ProtocolError struct {
	Worker int
	Oldest int
	Newest int
}

func (e *ProtocolError) Error() string {
	if e.Worker < e.Oldest {
		return fmt.Sprintf("worker protocol %d is older than this control plane accepts (%d to %d): upgrade the worker", e.Worker, e.Oldest, e.Newest)
	}
	return fmt.Sprintf("worker protocol %d is newer than this control plane accepts (%d to %d): upgrade the control plane", e.Worker, e.Oldest, e.Newest)
}

// NegotiateProtocol accepts a worker one version behind the control plane and
// refuses an older one (NFR-COMPAT-2). A worker ahead of the control plane is
// refused too, because a control plane cannot promise what it has not seen.
func NegotiateProtocol(server, worker int) (int, error) {
	oldest := max(server-1, 1)
	if worker < oldest || worker > server {
		return 0, &ProtocolError{Worker: worker, Oldest: oldest, Newest: server}
	}
	return worker, nil
}

// WorkerAuth identifies a worker on every call after registration. It never appears
// in a JSON body: the HTTP link carries the credential in a header.
type WorkerAuth struct {
	WorkerID   string `json:"-"`
	Credential string `json:"-"`
}

// WorkerIdentity is what a worker keeps across restarts.
type WorkerIdentity struct {
	WorkerID   string
	Credential string
	Epoch      int64
}

// Registration exchanges a join token for a credential. The worker generates its
// ID, so a retry after a lost answer is the same registration.
type Registration struct {
	JoinToken string `json:"join_token"`
	WorkerID  string `json:"worker_id"`
	Name      string `json:"name"`
}

// WorkerCredential is the answer to a registration.
type WorkerCredential struct {
	WorkerID   string `json:"worker_id"`
	Credential string `json:"credential"`
}

// SessionRequest opens a new epoch.
type SessionRequest struct {
	Auth         WorkerAuth        `json:"-"`
	Protocol     int               `json:"protocol"`
	Capabilities map[string]string `json:"capabilities,omitempty"`
}

// Session is what the control plane decided for one epoch.
type Session struct {
	Epoch       int64 `json:"epoch"`
	Protocol    int   `json:"protocol"`
	HeartbeatMs int64 `json:"heartbeat_ms"`
	LeaseTTLMs  int64 `json:"lease_ttl_ms"`
	DesiredRev  int64 `json:"desired_rev"`
}

// HeldLease is a lease a worker believes it holds.
type HeldLease struct {
	WorkOrder string `json:"work_order"`
	Claim     string `json:"claim"`
	Fence     int64  `json:"fence"`
}

// Heartbeat is the application handshake. A process with an open port can still
// pass no traffic, so the server must read the store before it answers, and the
// worker must receive its own nonce back.
type Heartbeat struct {
	Auth          WorkerAuth  `json:"-"`
	Epoch         int64       `json:"epoch"`
	Protocol      int         `json:"protocol"`
	Nonce         string      `json:"nonce"`
	DesiredRev    int64       `json:"desired_rev"`
	EventsThrough int64       `json:"events_through"`
	Leases        []HeldLease `json:"leases"`
}

// RenewedLease is a lease that holds, with what a worker counts from its send.
type RenewedLease struct {
	WorkOrder string `json:"work_order"`
	Fence     int64  `json:"fence"`
	TTLMs     int64  `json:"ttl_ms"`
}

// FencedLease is a lease the worker no longer holds.
type FencedLease struct {
	WorkOrder    string `json:"work_order"`
	Fence        int64  `json:"fence"`
	CurrentFence int64  `json:"current_fence"`
}

// HeartbeatReply answers one heartbeat.
type HeartbeatReply struct {
	Nonce        string         `json:"nonce"`
	ServerTimeMs int64          `json:"server_time_ms"`
	DesiredRev   int64          `json:"desired_rev"`
	FleetMode    FleetMode      `json:"fleet_mode"`
	Leases       []RenewedLease `json:"leases"`
	Fenced       []FencedLease  `json:"fenced"`
}

// DesiredRequest reads desired state. Since is the revision the worker has.
type DesiredRequest struct {
	Auth  WorkerAuth
	Since int64
}

// DesiredLease is one lease this worker should act on.
type DesiredLease struct {
	WorkOrder string `json:"work_order"`
	ClaimID   string `json:"claim_id"`
	Fence     int64  `json:"fence"`
	State     string `json:"state"`
	// Lane is the lane to run under this claim. Empty means hold the lease and start
	// nothing. The fields below come with it, and they are what the lane starts from.
	Lane       string   `json:"lane,omitempty"`
	Repository string   `json:"repository,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Title      string   `json:"title,omitempty"`
	Statement  string   `json:"statement,omitempty"`
	Criteria   []string `json:"criteria,omitempty"`
}

// DesiredState is what the control plane wants of one worker. Unchanged says the
// worker already holds this revision.
type DesiredState struct {
	Rev       int64          `json:"rev"`
	FleetMode FleetMode      `json:"fleet_mode"`
	Unchanged bool           `json:"unchanged,omitempty"`
	Leases    []DesiredLease `json:"leases"`
}

// WatchRequest opens the stream of revision hints.
type WatchRequest struct {
	Auth  WorkerAuth
	After int64
}

// UploadEvent is one journaled event. (worker, Epoch, Seq) identifies it for good,
// so a retried upload stores it once. Epoch is the epoch it was journaled under,
// which may be older than the session that uploads it.
type UploadEvent struct {
	Epoch      int64  `json:"epoch"`
	Seq        int64  `json:"seq"`
	Type       string `json:"type"`
	WorkOrder  string `json:"work_order,omitempty"`
	ClaimID    string `json:"claim_id,omitempty"`
	Fence      int64  `json:"fence,omitempty"`
	Lane       string `json:"lane,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	Payload    []byte `json:"payload,omitempty"`
	OccurredAt int64  `json:"occurred_at"`
}

// The upload types the ledger gives a meaning of its own. Any other type is a fenced
// note on the work order.
const (
	UploadLaneStarted     = "lane_started"
	UploadLaneEnded       = "lane_ended"
	UploadGenerationBound = "generation_bound"
	UploadLaneStatus      = "lane_status"
)

// UploadedEventID is the event ID of one uploaded event. The ledger's unique index
// on event IDs then makes upload idempotent.
func UploadedEventID(worker string, epoch, seq int64) string {
	return fmt.Sprintf("%s:%d:%d", worker, epoch, seq)
}

// UploadBatch is a run of events in sequence order.
type UploadBatch struct {
	Auth   WorkerAuth    `json:"-"`
	Epoch  int64         `json:"epoch"`
	Events []UploadEvent `json:"events"`
}

// The statuses of a rejected event.
const (
	UploadFenced  = "fenced"
	UploadInvalid = "invalid"
)

// UploadResult is the result of one rejected event.
type UploadResult struct {
	Seq          int64  `json:"seq"`
	Status       string `json:"status"`
	CurrentFence int64  `json:"current_fence,omitempty"`
	State        string `json:"state,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// UploadReply carries the high-water mark, plus a result for each event the ledger
// rejected. A rejected event is answered, so it counts toward the mark.
type UploadReply struct {
	HighWater int64          `json:"high_water"`
	Rejected  []UploadResult `json:"rejected,omitempty"`
}

// Worker is one registered worker, as a client sees it.
type Worker struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Epoch           int64             `json:"epoch"`
	Protocol        int               `json:"protocol"`
	Capabilities    map[string]string `json:"capabilities,omitempty"`
	LastHandshakeAt int64             `json:"last_handshake_at,omitempty"`
}

// WorkerRecord is a worker as the ledger holds it. ProofDigest is the digest of its
// credential, so the ledger stores no secret value (NFR-SEC-4).
type WorkerRecord struct {
	Worker
	ProofDigest string
}

// JoinGrant is a join token as the ledger holds it: by digest, and spent once.
type JoinGrant struct {
	ID        string
	Digest    string
	CreatedBy string
	UsedBy    string
}

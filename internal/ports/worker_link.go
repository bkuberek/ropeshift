package ports

import (
	"context"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// WorkerLink is the worker's side of the plane protocol. The control plane
// publishes desired state and the worker converges on it, so a lost notification
// costs latency and never correctness.
type WorkerLink interface {
	// Register exchanges a join token for a worker credential. A retry by the same
	// worker issues a new credential, because the first answer may have been lost.
	Register(ctx context.Context, registration ledger.Registration) (ledger.WorkerCredential, error)
	// OpenSession opens a new worker epoch and negotiates the protocol.
	OpenSession(ctx context.Context, request ledger.SessionRequest) (ledger.Session, error)
	// Heartbeat is the application handshake, lease renewal, and revision exchange.
	Heartbeat(ctx context.Context, beat ledger.Heartbeat) (ledger.HeartbeatReply, error)
	// Desired reads desired state for this worker.
	Desired(ctx context.Context, request ledger.DesiredRequest) (ledger.DesiredState, error)
	// Watch streams revision hints until the context ends. A hint carries no state.
	Watch(ctx context.Context, request ledger.WatchRequest) (<-chan int64, error)
	// Upload sends events in order and returns the high-water mark.
	Upload(ctx context.Context, batch ledger.UploadBatch) (ledger.UploadReply, error)
	// Claim requests a work order for this worker.
	Claim(ctx context.Context, auth ledger.WorkerAuth, request workorders.ClaimRequest) (workorders.Grant, error)
	// Ask records a question for a lane this worker holds, and answers at once.
	Ask(ctx context.Context, ask questions.LaneAsk) (questions.AskResult, error)
}

package fake

import (
	"context"
	"errors"
	"sync/atomic"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// WorkerLink is the in-memory link: direct calls into the control plane's side of
// the protocol, which is passed in as a port because an adapter never imports the
// application. It can misbehave on purpose, so a check can show what a broken
// network costs.
type WorkerLink struct {
	Plane ports.WorkerLink
	// CorruptNonce answers every heartbeat with someone else's nonce.
	CorruptNonce atomic.Bool
	// DropHints loses every revision hint.
	DropHints atomic.Bool
	// LoseUploadAnswer stores a batch and then loses the answer, once.
	LoseUploadAnswer atomic.Bool
	// HangAsk holds every ask until its deadline, as a control plane that took the
	// connection and went quiet would.
	HangAsk atomic.Bool
	// FailLaneFacts fails every read of a lane's facts, as a control plane that is away would.
	FailLaneFacts atomic.Bool
	// FailUpload refuses every upload, as a control plane that is away would.
	FailUpload atomic.Bool
}

// Register calls the plane.
func (l *WorkerLink) Register(ctx context.Context, registration ledger.Registration) (ledger.WorkerCredential, error) {
	return l.Plane.Register(ctx, registration)
}

// OpenSession calls the plane.
func (l *WorkerLink) OpenSession(ctx context.Context, request ledger.SessionRequest) (ledger.Session, error) {
	return l.Plane.OpenSession(ctx, request)
}

// Heartbeat calls the plane.
func (l *WorkerLink) Heartbeat(ctx context.Context, beat ledger.Heartbeat) (ledger.HeartbeatReply, error) {
	reply, err := l.Plane.Heartbeat(ctx, beat)
	if err == nil && l.CorruptNonce.Load() {
		reply.Nonce = "not-" + reply.Nonce
	}
	return reply, err
}

// Desired calls the plane.
func (l *WorkerLink) Desired(ctx context.Context, request ledger.DesiredRequest) (ledger.DesiredState, error) {
	return l.Plane.Desired(ctx, request)
}

// Watch relays hints, unless they are being dropped.
func (l *WorkerLink) Watch(ctx context.Context, request ledger.WatchRequest) (<-chan int64, error) {
	hints, err := l.Plane.Watch(ctx, request)
	if err != nil {
		return nil, err
	}
	relayed := make(chan int64, 1)
	go func() {
		defer close(relayed)
		for hint := range hints {
			if l.DropHints.Load() {
				continue
			}
			select {
			case relayed <- hint:
			case <-ctx.Done():
				return
			}
		}
	}()
	return relayed, nil
}

// ErrAnswerLost is what a caller sees when the fake loses an answer.
var ErrAnswerLost = errAnswerLost{}

type errAnswerLost struct{}

func (errAnswerLost) Error() string { return "the answer was lost" }

// Upload calls the plane.
func (l *WorkerLink) Upload(ctx context.Context, batch ledger.UploadBatch) (ledger.UploadReply, error) {
	if l.FailUpload.Load() {
		return ledger.UploadReply{}, errPlaneAway
	}
	reply, err := l.Plane.Upload(ctx, batch)
	if err == nil && l.LoseUploadAnswer.CompareAndSwap(true, false) {
		return ledger.UploadReply{}, ErrAnswerLost
	}
	return reply, err
}

// Claim calls the plane.
func (l *WorkerLink) Claim(ctx context.Context, auth ledger.WorkerAuth, request workorders.ClaimRequest) (workorders.Grant, error) {
	return l.Plane.Claim(ctx, auth, request)
}

// Ask calls the plane, unless asks are hanging.
func (l *WorkerLink) Ask(ctx context.Context, ask questions.LaneAsk) (questions.AskResult, error) {
	if l.HangAsk.Load() {
		<-ctx.Done()
		return questions.AskResult{}, ctx.Err()
	}
	return l.Plane.Ask(ctx, ask)
}

// LaneFacts calls the plane, unless it is away.
func (l *WorkerLink) LaneFacts(ctx context.Context, request ledger.LaneFactsRequest) (ledger.LaneFacts, error) {
	if l.FailLaneFacts.Load() {
		return ledger.LaneFacts{}, errPlaneAway
	}
	return l.Plane.LaneFacts(ctx, request)
}

var errPlaneAway = errors.New("the control plane is away")

var _ ports.WorkerLink = (*WorkerLink)(nil)

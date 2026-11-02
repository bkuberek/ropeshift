// Package workerlink is the WorkerLink on the HTTP API. The worker speaks the same
// versioned API as every other client (FR-API-3), so this adapter holds no protocol
// of its own. It turns API refusals back into the domain errors the worker acts on.
package workerlink

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// HTTP is a WorkerLink over one API client.
type HTTP struct {
	client *api.Client
}

// New returns a link on a client.
func New(client *api.Client) *HTTP {
	return &HTTP{client: client}
}

func (l *HTTP) as(auth ledger.WorkerAuth) *api.Client {
	return l.client.WithCredential(auth.Credential)
}

// domainError is the inverse of the API's error mapping.
func domainError(err error) error {
	var refused *api.Error
	if !errors.As(err, &refused) {
		return err
	}
	detail := refused.Detail
	switch detail.Code {
	case api.CodeBadCredential:
		return ledger.ErrBadCredential
	case api.CodeBadJoinToken:
		return ledger.ErrBadJoinToken
	case api.CodeStaleEpoch:
		return ledger.ErrStaleEpoch
	case api.CodeProtocol:
		return &ledger.ProtocolError{Worker: detail.Worker, Oldest: detail.Oldest, Newest: detail.Newest}
	case api.CodeFenced:
		return &workorders.FencedError{Current: detail.CurrentFence, State: workorders.State(detail.State)}
	case api.CodeLost:
		return workorders.ErrLost
	case api.CodeIneligible:
		return workorders.ErrIneligible
	case api.CodeLapsed:
		return workorders.ErrLapsed
	case api.CodeSelfApproval:
		return authority.ErrSelfApproval
	case api.CodeNotADecider:
		return authority.ErrDeciderKind
	case api.CodeNotDelegated:
		return authority.ErrNotDelegated
	case api.CodeStaleDigest:
		return authority.ErrStaleDigest
	case api.CodeRequestClosed:
		return authority.ErrApprovalClosed
	case api.CodeNotFound:
		return ports.ErrNotFound
	}
	return err
}

// Register exchanges a join token for a credential.
func (l *HTTP) Register(ctx context.Context, registration ledger.Registration) (ledger.WorkerCredential, error) {
	credential, err := l.client.Register(ctx, registration)
	return credential, domainError(err)
}

// OpenSession opens a new epoch.
func (l *HTTP) OpenSession(ctx context.Context, request ledger.SessionRequest) (ledger.Session, error) {
	session, err := l.as(request.Auth).OpenSession(ctx, request.Auth.WorkerID, request)
	return session, domainError(err)
}

// Heartbeat sends one heartbeat.
func (l *HTTP) Heartbeat(ctx context.Context, beat ledger.Heartbeat) (ledger.HeartbeatReply, error) {
	reply, err := l.as(beat.Auth).Heartbeat(ctx, beat.Auth.WorkerID, beat)
	return reply, domainError(err)
}

// Desired reads desired state.
func (l *HTTP) Desired(ctx context.Context, request ledger.DesiredRequest) (ledger.DesiredState, error) {
	state, err := l.as(request.Auth).Desired(ctx, request.Auth.WorkerID, request.Since)
	return state, domainError(err)
}

// Watch opens the stream of hints.
func (l *HTTP) Watch(ctx context.Context, request ledger.WatchRequest) (<-chan int64, error) {
	hints, err := l.as(request.Auth).Watch(ctx, request.Auth.WorkerID, request.After)
	return hints, domainError(err)
}

// Upload sends one batch.
func (l *HTTP) Upload(ctx context.Context, batch ledger.UploadBatch) (ledger.UploadReply, error) {
	reply, err := l.as(batch.Auth).Upload(ctx, batch.Auth.WorkerID, batch)
	return reply, domainError(err)
}

// Claim requests a work order for this worker.
func (l *HTTP) Claim(ctx context.Context, auth ledger.WorkerAuth, request workorders.ClaimRequest) (workorders.Grant, error) {
	grant, err := l.as(auth).Claim(ctx, request.WorkOrder, api.ClaimRequest{
		ClaimID: request.ClaimID, WorkerID: auth.WorkerID, TTLMs: request.TTLMs,
	})
	if err != nil {
		return workorders.Grant{}, domainError(err)
	}
	out := workorders.Grant{
		Lease:        workorders.Lease{WorkOrder: grant.WorkOrder, ClaimID: grant.ClaimID, Fence: grant.Fence},
		TTLMs:        grant.TTLMs,
		ResumeLaneID: grant.ResumeLaneID,
		Branch:       grant.Branch,
		Replayed:     grant.Replayed,
	}
	if grant.Contract != nil {
		out.ContractID, out.ContractVersion, out.ContractDigest = grant.Contract.ID, grant.Contract.Version, grant.Contract.Digest
	}
	return out, nil
}

// Ask records a question for a lane. A refusal of the question itself comes back as what
// it is, so a lane reads it and can ask again.
func (l *HTTP) Ask(ctx context.Context, ask questions.LaneAsk) (questions.AskResult, error) {
	result, err := l.as(ask.Auth).Ask(ctx, ask.Auth.WorkerID, ask)
	var refused *api.Error
	if errors.As(err, &refused) && refused.Detail.Code == api.CodeInvalid {
		return questions.AskResult{}, fmt.Errorf("%w: %s", questions.ErrInvalid, refused.Detail.Message)
	}
	return result, domainError(err)
}

// LaneFacts reads what a handoff states and only the ledger knows.
func (l *HTTP) LaneFacts(ctx context.Context, request ledger.LaneFactsRequest) (ledger.LaneFacts, error) {
	facts, err := l.as(request.Auth).LaneFacts(ctx, request.Auth.WorkerID, request)
	return facts, domainError(err)
}

// LaneContext reads what a workspace holds, for a lane this worker holds. A refusal of the read
// itself comes back as what it is, so a lane reads it and can ask again.
func (l *HTTP) LaneContext(ctx context.Context, request ledger.LaneContextRequest) (ledger.LaneContext, error) {
	found, err := l.as(request.Auth).LaneContext(ctx, request.Auth.WorkerID, request)
	var refused *api.Error
	if errors.As(err, &refused) && refused.Detail.Code == api.CodeInvalid {
		return ledger.LaneContext{}, fmt.Errorf("%w: %s", workorders.ErrInvalid, refused.Detail.Message)
	}
	return found, domainError(err)
}

var _ ports.WorkerLink = (*HTTP)(nil)

// UseApproval reads where a lane's call stands.
func (l *HTTP) UseApproval(ctx context.Context, use authority.ApprovalUse) (authority.ApprovalAnswer, error) {
	answer, err := l.as(use.Auth).UseApproval(ctx, use.Auth.WorkerID, use)
	return answer, domainError(err)
}

// DecideApproval carries what this worker's orchestrator decided.
func (l *HTTP) DecideApproval(ctx context.Context, decision authority.WorkerDecision) (authority.Approval, error) {
	record, err := l.as(decision.Auth).DecideDelegated(ctx, decision.Auth.WorkerID, decision)
	return record, domainError(err)
}

// WakeDigest reads what this worker's orchestrator could judge now.
func (l *HTTP) WakeDigest(ctx context.Context, request ledger.WakeRequest) (ledger.WakeDigest, error) {
	digest, err := l.as(request.Auth).WakeDigest(ctx, request.Auth.WorkerID, request)
	return digest, domainError(err)
}

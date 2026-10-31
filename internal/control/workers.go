package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// The worker plane's events, and what they are about.
const (
	subjectWorker    = "worker"
	subjectJoinGrant = "join_grant"

	eventJoinTokenCreated = "worker.join_token_created"
	eventWorkerRegistered = "worker.registered"
	eventSessionOpened    = "worker.session_opened"
	eventWorkerReported   = "worker.reported"
)

// opUpload names the transaction that stores one uploaded event.
const opUpload = "plane.upload"

// The architecture's proposed values, which phase 1 validates.
const (
	defaultHeartbeatMs = 15_000
	defaultLeaseTTLMs  = 60_000
)

// watchEvery is how often a watch reads the revision. A hint only shortens the wait
// for the next heartbeat, so this bounds a cost and not correctness.
const watchEvery = 100 * time.Millisecond

// secretBytes sizes join tokens and credentials.
const secretBytes = 32

// WorkerOptions is the policy this control plane gives its workers.
type WorkerOptions struct {
	// Protocol is the version this control plane speaks. Zero means this build's.
	Protocol int
	// HeartbeatMs is how often a worker beats.
	HeartbeatMs int64
	// LeaseTTLMs is the lease a heartbeat renews to.
	LeaseTTLMs int64
}

// Workers is the control plane's side of the plane protocol. It has the shape of
// ports.WorkerLink, so the in-memory link is a direct call and the HTTP handlers
// are a thin map, and no code path exists only for solo mode.
type Workers struct {
	store   ports.Store
	scope   ledger.WorkspaceID
	options WorkerOptions
	leases  *Leases
}

// NewWorkers returns the plane over a Store, for one workspace.
func NewWorkers(store ports.Store, scope ledger.WorkspaceID, options WorkerOptions) *Workers {
	if options.Protocol == 0 {
		options.Protocol = ledger.ProtocolVersion
	}
	if options.HeartbeatMs == 0 {
		options.HeartbeatMs = defaultHeartbeatMs
	}
	if options.LeaseTTLMs == 0 {
		options.LeaseTTLMs = defaultLeaseTTLMs
	}
	return &Workers{store: store, scope: scope, options: options, leases: NewLeases(store)}
}

func newSecret() (string, error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func digestOf(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func sameDigest(secret, digest string) bool {
	return subtle.ConstantTimeCompare([]byte(digestOf(secret)), []byte(digest)) == 1
}

func workerEvent(eventType, id string, actor ledger.Actor) ledger.EventDraft {
	return ledger.EventDraft{Type: eventType, Actor: actor, Subject: ledger.Subject{Kind: subjectWorker, ID: id}}
}

// CreateJoinToken makes a single-use token for one worker. The ledger keeps only its
// digest, so the answer is the one time anyone sees it.
func (w *Workers) CreateJoinToken(ctx context.Context, actor ledger.Actor) (string, error) {
	if actor.Kind != ledger.ActorMember && actor.Kind != ledger.ActorSystem {
		return "", workorders.ErrForbidden
	}
	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	var token string
	err = w.store.Write(ctx, w.scope, "worker.join_token", func(tx ports.WriteTx) error {
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		token = id + "." + secret
		err = tx.Workers().InsertJoinGrant(ledger.JoinGrant{ID: id, Digest: digestOf(secret), CreatedBy: actor.ID})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: eventJoinTokenCreated, Actor: actor, Subject: ledger.Subject{Kind: subjectJoinGrant, ID: id},
		})
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Register exchanges a join token for a credential. The worker generated its ID, so
// a retry by that worker is the same registration, and it issues a new credential
// because the first answer may never have arrived.
func (w *Workers) Register(ctx context.Context, registration ledger.Registration) (ledger.WorkerCredential, error) {
	id, secret, ok := strings.Cut(registration.JoinToken, ".")
	if !ok {
		return ledger.WorkerCredential{}, ledger.ErrBadJoinToken
	}
	credential, err := newSecret()
	if err != nil {
		return ledger.WorkerCredential{}, err
	}
	err = w.store.Write(ctx, w.scope, "worker.register", func(tx ports.WriteTx) error {
		grant, err := tx.Workers().JoinGrant(id)
		if errors.Is(err, ports.ErrNotFound) {
			return ledger.ErrBadJoinToken
		}
		if err != nil {
			return err
		}
		if !sameDigest(secret, grant.Digest) || (grant.UsedBy != "" && grant.UsedBy != registration.WorkerID) {
			return ledger.ErrBadJoinToken
		}
		if grant.UsedBy == "" {
			if err := tx.Workers().SpendJoinGrant(id, registration.WorkerID); err != nil {
				return err
			}
			record := ledger.WorkerRecord{ProofDigest: digestOf(credential)}
			record.ID, record.Name = registration.WorkerID, registration.Name
			if err := tx.Workers().Insert(record); err != nil {
				return err
			}
		} else if err := tx.Workers().ReplaceProof(registration.WorkerID, digestOf(credential)); err != nil {
			return err
		}
		_, err = tx.Append(workerEvent(eventWorkerRegistered, registration.WorkerID, ledger.Actor{Kind: ledger.ActorWorker, ID: registration.WorkerID}))
		return err
	})
	if err != nil {
		return ledger.WorkerCredential{}, err
	}
	return ledger.WorkerCredential{WorkerID: registration.WorkerID, Credential: credential}, nil
}

// reader is what both a read and a write transaction can answer about a worker.
type reader interface {
	Workers() ports.WorkerReader
}

// authenticate reads the worker and checks its credential.
func authenticate(workers ports.WorkerReader, auth ledger.WorkerAuth) (ledger.WorkerRecord, error) {
	record, err := workers.Find(auth.WorkerID)
	if errors.Is(err, ports.ErrNotFound) {
		return ledger.WorkerRecord{}, ledger.ErrBadCredential
	}
	if err != nil {
		return ledger.WorkerRecord{}, err
	}
	if !sameDigest(auth.Credential, record.ProofDigest) {
		return ledger.WorkerRecord{}, ledger.ErrBadCredential
	}
	return record, nil
}

// OpenSession opens a new epoch and negotiates the protocol. The epoch moves in one
// transaction, so an older session is stale from this commit on.
func (w *Workers) OpenSession(ctx context.Context, request ledger.SessionRequest) (ledger.Session, error) {
	session := ledger.Session{HeartbeatMs: w.options.HeartbeatMs, LeaseTTLMs: w.options.LeaseTTLMs}
	err := w.store.Write(ctx, w.scope, "worker.session", func(tx ports.WriteTx) error {
		record, err := authenticate(tx.Workers(), request.Auth)
		if err != nil {
			return err
		}
		if session.Protocol, err = ledger.NegotiateProtocol(w.options.Protocol, request.Protocol); err != nil {
			return err
		}
		next := record.Worker
		next.Protocol, next.Capabilities = session.Protocol, request.Capabilities
		if session.Epoch, err = tx.Workers().OpenEpoch(next); err != nil {
			return err
		}
		draft := workerEvent(eventSessionOpened, record.ID, ledger.Actor{Kind: ledger.ActorWorker, ID: record.ID})
		draft.Data = []byte(fmt.Sprintf(`{"epoch":%d,"protocol":%d}`, session.Epoch, session.Protocol))
		event, err := tx.Append(draft)
		session.DesiredRev = event.Rev
		return err
	})
	if err != nil {
		return ledger.Session{}, err
	}
	return session, nil
}

// checkSession reads the worker and refuses a stale epoch.
func (w *Workers) checkSession(tx reader, auth ledger.WorkerAuth, epoch int64) error {
	record, err := authenticate(tx.Workers(), auth)
	if err != nil {
		return err
	}
	if record.Epoch != epoch {
		return fmt.Errorf("%w: %d, and this session is %d", ledger.ErrStaleEpoch, record.Epoch, epoch)
	}
	return nil
}

// Heartbeat reads the store, renews each lease, and answers with the worker's own
// nonce. Each renewal is its own statement and takes no revision (ADR-0010), and a
// lease that lapsed returns here, because a worker with the current fence has
// nothing else to ask for. A lease past its deadline that no sweep has lapsed is in
// neither list: the worker keeps it, and a later beat returns it.
func (w *Workers) Heartbeat(ctx context.Context, beat ledger.Heartbeat) (ledger.HeartbeatReply, error) {
	if beat.Nonce == "" {
		return ledger.HeartbeatReply{}, fmt.Errorf("%w: a heartbeat needs a nonce", workorders.ErrInvalid)
	}
	reply := ledger.HeartbeatReply{Nonce: beat.Nonce, Leases: []ledger.RenewedLease{}, Fenced: []ledger.FencedLease{}}
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		return w.checkSession(tx, beat.Auth, beat.Epoch)
	})
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: beat.Auth.WorkerID}
	for _, held := range beat.Leases {
		lease := workorders.Lease{WorkOrder: held.WorkOrder, ClaimID: held.Claim, Fence: held.Fence}
		err := w.renewOrReturn(ctx, lease, actor)
		var fenced *workorders.FencedError
		switch {
		case err == nil:
			reply.Leases = append(reply.Leases, ledger.RenewedLease{WorkOrder: held.WorkOrder, Fence: held.Fence, TTLMs: w.options.LeaseTTLMs})
		case errors.As(err, &fenced):
			reply.Fenced = append(reply.Fenced, ledger.FencedLease{WorkOrder: held.WorkOrder, Fence: held.Fence, CurrentFence: fenced.Current})
		case errors.Is(err, ports.ErrNotFound):
			reply.Fenced = append(reply.Fenced, ledger.FencedLease{WorkOrder: held.WorkOrder, Fence: held.Fence})
		case errors.Is(err, workorders.ErrExpired):
		default:
			return ledger.HeartbeatReply{}, err
		}
	}
	err = w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		workspace, err := tx.Workspace()
		if err != nil {
			return err
		}
		reply.DesiredRev, reply.FleetMode = workspace.Rev, workspace.FleetMode
		reply.ServerTimeMs, err = tx.Now()
		return err
	})
	if err != nil {
		return ledger.HeartbeatReply{}, err
	}
	return reply, nil
}

func (w *Workers) renewOrReturn(ctx context.Context, lease workorders.Lease, actor ledger.Actor) error {
	err := w.leases.Renew(ctx, w.scope, lease, w.options.LeaseTTLMs)
	if errors.Is(err, workorders.ErrLapsed) {
		return w.leases.Return(ctx, w.scope, lease, w.options.LeaseTTLMs, actor)
	}
	return err
}

// Desired reads what this worker should hold. A worker that already has this
// revision reads nothing more.
func (w *Workers) Desired(ctx context.Context, request ledger.DesiredRequest) (ledger.DesiredState, error) {
	state := ledger.DesiredState{Leases: []ledger.DesiredLease{}}
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		if _, err := authenticate(tx.Workers(), request.Auth); err != nil {
			return err
		}
		workspace, err := tx.Workspace()
		if err != nil {
			return err
		}
		state.Rev, state.FleetMode = workspace.Rev, workspace.FleetMode
		if request.Since == workspace.Rev {
			state.Unchanged = true
			return nil
		}
		held, err := tx.WorkOrders().HeldBy(request.Auth.WorkerID)
		if err != nil {
			return err
		}
		for _, row := range held {
			lease, err := desiredLease(tx, row)
			if err != nil {
				return err
			}
			state.Leases = append(state.Leases, lease)
		}
		return nil
	})
	if err != nil {
		return ledger.DesiredState{}, err
	}
	return state, nil
}

// desiredLease adds what a dispatched lane starts from.
func desiredLease(tx ports.ReadTx, row workorders.WorkOrder) (ledger.DesiredLease, error) {
	lease := ledger.DesiredLease{WorkOrder: row.ID, ClaimID: row.ClaimID, Fence: row.Fence, State: string(row.State)}
	claim, err := tx.Claims().Find(row.ClaimID)
	if errors.Is(err, ports.ErrNotFound) {
		return lease, nil
	}
	if err != nil {
		return lease, err
	}
	if claim.Lane == "" {
		return lease, nil
	}
	lease.Lane, lease.Repository, lease.Branch, lease.Title = claim.Lane, row.RepositoryID, row.Branch, row.Title
	if row.ContractVersion == 0 {
		return lease, nil
	}
	pinned, err := tx.Planning().Contract(row.ContractID, row.ContractVersion)
	if err != nil {
		return lease, err
	}
	lease.Statement = pinned.Statement
	for _, criterion := range pinned.Criteria {
		lease.Criteria = append(lease.Criteria, criterion.Text)
	}
	return lease, nil
}

// Watch hints that the revision moved. Every hint it drops is one the next heartbeat
// makes up for, so a slow reader gets the newest revision and never blocks the loop.
func (w *Workers) Watch(ctx context.Context, request ledger.WatchRequest) (<-chan int64, error) {
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		_, err := authenticate(tx.Workers(), request.Auth)
		return err
	})
	if err != nil {
		return nil, err
	}
	hints := make(chan int64, 1)
	go func() {
		defer close(hints)
		last := request.After
		ticker := time.NewTicker(watchEvery)
		defer ticker.Stop()
		for {
			var rev int64
			err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
				workspace, err := tx.Workspace()
				rev = workspace.Rev
				return err
			})
			if err == nil && rev > last {
				last = rev
				select {
				case <-hints:
				default:
				}
				hints <- rev
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return hints, nil
}

// Upload stores a batch in order, each event in its own short transaction. An event
// the ledger refused is answered and counts toward the mark, so one fenced event
// cannot hold back the events behind it.
func (w *Workers) Upload(ctx context.Context, batch ledger.UploadBatch) (ledger.UploadReply, error) {
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		return w.checkSession(tx, batch.Auth, batch.Epoch)
	})
	if err != nil {
		return ledger.UploadReply{}, err
	}
	var reply ledger.UploadReply
	for _, event := range batch.Events {
		rejected, err := w.storeEvent(ctx, batch.Auth.WorkerID, event)
		if errors.Is(err, workorders.ErrLapsed) {
			return reply, nil
		}
		if err != nil {
			return reply, err
		}
		if rejected != nil {
			reply.Rejected = append(reply.Rejected, *rejected)
		}
		reply.HighWater = event.Seq
	}
	return reply, nil
}

// storeEvent stores one event once. The lookup comes before the fence, so a retry
// after a lost answer finds its first attempt even if the fence moved since. A lapsed
// lease holds the batch here: the next heartbeat returns it and the worker sends the
// event again, so an outcome is never lost to a lapse.
func (w *Workers) storeEvent(ctx context.Context, worker string, e ledger.UploadEvent) (*ledger.UploadResult, error) {
	id := ledger.UploadedEventID(worker, e.Epoch, e.Seq)
	err := w.store.Write(ctx, w.scope, opUpload, func(tx ports.WriteTx) error {
		if _, err := tx.Event(id); err == nil {
			return nil
		} else if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		return applyUpload(tx, worker, id, e)
	})
	var fenced *workorders.FencedError
	switch {
	case err == nil:
		return nil, nil
	case errors.As(err, &fenced):
		return &ledger.UploadResult{Seq: e.Seq, Status: ledger.UploadFenced, CurrentFence: fenced.Current, State: string(fenced.State)}, nil
	case errors.Is(err, ports.ErrNotFound), errors.Is(err, workorders.ErrInvalid):
		return &ledger.UploadResult{Seq: e.Seq, Status: ledger.UploadInvalid, Detail: err.Error()}, nil
	}
	return nil, err
}

// applyUpload stores one event inside its transaction. A lane's start and end change
// the work order, so they run the same statements as the use cases.
func applyUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	stamp := func(draft *ledger.EventDraft) { draft.ID, draft.OccurredAt = id, e.OccurredAt }
	lease := workorders.Lease{WorkOrder: e.WorkOrder, ClaimID: e.ClaimID, Fence: e.Fence}
	switch e.Type {
	case ledger.UploadLaneStarted:
		_, err := startLane(tx, lease, e.Lane, ledger.Actor{Kind: ledger.ActorWorker, ID: worker}, stamp)
		return err
	case ledger.UploadLaneEnded:
		var result workorders.LaneResult
		if err := json.Unmarshal(e.Payload, &result); err != nil {
			return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
		}
		holder := workorders.Holder{Lease: lease, Lane: e.Lane, Generation: e.Generation}
		return endLane(tx, holder, result, ledger.Actor{Kind: ledger.ActorLane, ID: e.Lane}, stamp)
	}
	draft, err := uploadedDraft(tx, worker, id, e)
	if err != nil {
		return err
	}
	_, err = tx.Append(draft)
	return err
}

// uploadedDraft checks a work order event's fence and generation, and builds the
// event. A worker's own event names no work order and has no fence to check.
func uploadedDraft(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) (ledger.EventDraft, error) {
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: worker}
	if e.WorkOrder == "" {
		draft := workerEvent(eventWorkerReported, worker, actor)
		draft.ID, draft.OccurredAt, draft.Data = id, e.OccurredAt, nil
		return draft, nil
	}
	holder := workorders.Holder{
		Lease: workorders.Lease{WorkOrder: e.WorkOrder, ClaimID: e.ClaimID, Fence: e.Fence},
		Lane:  e.Lane, Generation: e.Generation,
	}
	if err := guardHolder(tx, opUpload, holder); err != nil {
		return ledger.EventDraft{}, err
	}
	if e.Lane != "" {
		actor = ledger.Actor{Kind: ledger.ActorLane, ID: e.Lane}
	}
	draft, err := workorders.ChangeEvent("work_order."+e.Type, e.WorkOrder, workorders.Change{
		Guarded: true, Fence: e.Fence, ClaimID: e.ClaimID, Lane: e.Lane, Generation: e.Generation, Payload: e.Payload,
	}, actor)
	draft.ID, draft.OccurredAt = id, e.OccurredAt
	return draft, err
}

// Claim requests a work order for this worker, through the path a member's manual
// claim takes.
func (w *Workers) Claim(ctx context.Context, auth ledger.WorkerAuth, request workorders.ClaimRequest) (workorders.Grant, error) {
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		_, err := authenticate(tx.Workers(), auth)
		return err
	})
	if err != nil {
		return workorders.Grant{}, err
	}
	request.Worker = auth.WorkerID
	if request.TTLMs == 0 {
		request.TTLMs = w.options.LeaseTTLMs
	}
	return w.leases.Claim(ctx, w.scope, request, ledger.Actor{Kind: ledger.ActorWorker, ID: auth.WorkerID})
}

// List returns every registered worker.
func (w *Workers) List(ctx context.Context) ([]ledger.Worker, error) {
	var workers []ledger.Worker
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		workers, err = tx.Workers().List()
		return err
	})
	return workers, err
}

var _ ports.WorkerLink = (*Workers)(nil)

package control

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
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

// conditionUnpushed is the condition the ledger keeps past a lane's end, because the
// worktree it speaks of stays until someone removes it.
const conditionUnpushed = "unpushed_work"

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
	// Batches says how long questions gather for a person.
	Batches questions.BatchPolicy
	// Windows says how long what a ruling sends a lane stays useful.
	Windows messages.Windows
	// PackTokens is the budget of the pack a claim pins. Zero takes the default.
	PackTokens int64
	// Matcher finds what could answer a question. Nil leaves an orchestrator to search for itself.
	Matcher ports.Matcher
	// Starts places the lane of every claim. Nil places none.
	Starts *Starts
}

// Workers is the control plane's side of the plane protocol. It has the shape of
// ports.WorkerLink, so the in-memory link is a direct call and the HTTP handlers
// are a thin map, and no code path exists only for solo mode.
type Workers struct {
	store   ports.Store
	scope   ledger.WorkspaceID
	options WorkerOptions
	leases  *Leases
	// questions records what a lane asks.
	questions *Questions
	// accepting carries an orchestrator's word as the second principal.
	accepting *Acceptances
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
	return &Workers{
		store: store, scope: scope, options: options, leases: NewLeases(store).Budgeted(options.PackTokens).Starting(options.Starts),
		questions: NewQuestions(store, options.Batches).Telling(options.Windows).Budgeted(options.PackTokens),
		accepting: NewAcceptances(store),
	}
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
	if !authority.Allowed(actor, authority.ActionCreateJoinToken) {
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
		next.Protocol, next.Manifest = session.Protocol, request.Manifest
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
		if state.Archives, err = tx.Lanes().Archiving(request.Auth.WorkerID); err != nil {
			return err
		}
		if state.Orchestrator, err = desiredOrchestrator(tx, request.Auth.WorkerID); err != nil {
			return err
		}
		facts, err := autonomyOf(tx, request.Auth.WorkerID)
		if err != nil {
			return err
		}
		state.MaxRespawnsPerDay = facts.respawns()
		if state.Effects, err = desiredEffects(tx, request.Auth.WorkerID); err != nil {
			return err
		}
		state.Woken, err = wokenFor(tx, request.Auth.WorkerID)
		return err
	})
	if err != nil {
		return ledger.DesiredState{}, err
	}
	return state, nil
}

// wokenFor lists the woken work this worker may claim, the most urgent first.
func wokenFor(tx ports.ReadTx, worker string) ([]ledger.DesiredWoken, error) {
	rows, err := tx.WorkOrders().Woken()
	if err != nil {
		return nil, err
	}
	var out []ledger.DesiredWoken
	for _, row := range rows {
		offered, err := offers(tx, row, worker)
		if err != nil {
			return nil, err
		}
		if offered {
			out = append(out, ledger.DesiredWoken{WorkOrder: row.ID, Lane: row.ResumeLaneID, Repository: row.RepositoryID, Fence: row.Fence})
		}
	}
	return out, nil
}

// offers says a worker may claim this work: its manifest meets the needs, and affinity lets it.
func offers(tx ports.ReadTx, row workorders.WorkOrder, worker string) (bool, error) {
	record, err := tx.Workers().Find(worker)
	if err != nil {
		return false, err
	}
	if runs, _ := record.Manifest.Runs(row.Needs); !runs {
		return false, nil
	}
	return admitsResume(tx.Lanes(), row, worker)
}

// desiredResume reads what a claim on woken work brings back. A generation that left no document
// or no checkpoint leaves those out, and a worker reads that as a degraded handoff.
func desiredResume(tx ports.ReadTx, lane string) (*ledger.DesiredResume, error) {
	row, err := tx.Lanes().Row(lane)
	if err != nil {
		return nil, err
	}
	resume := &ledger.DesiredResume{Generation: row.Generation, Worker: row.Hold.Worker, Degraded: true}
	left, err := tx.Lanes().Handoff(lane, row.Generation)
	switch {
	case err == nil:
		resume.Handoff, resume.Degraded = left.Body, left.Degraded
	case !errors.Is(err, ports.ErrNotFound):
		return nil, err
	}
	checkpoint, err := tx.Lanes().Checkpoint(lane, row.Generation)
	switch {
	case err == nil:
		resume.Pending, resume.Branch, resume.SnapshotRef = checkpoint.PendingInstruction, checkpoint.Branch, checkpoint.SnapshotRef
	case !errors.Is(err, ports.ErrNotFound):
		return nil, err
	}
	generations, err := tx.Lanes().Generations(lane)
	if err != nil {
		return nil, err
	}
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	for _, generation := range generations {
		if generation.Number != row.Generation {
			continue
		}
		resume.SessionID = generation.SessionID
		if generation.LastResponseAt != 0 {
			resume.Responded, resume.SinceResponseMs = true, now-generation.LastResponseAt
		}
	}
	if resume.Approvals, resume.Calls, err = approvalOutcomes(tx.Approvals(), lane, row.Generation); err != nil {
		return nil, err
	}
	resume.Facts, err = messageFacts(tx.Messages(), lane, row.Generation)
	return resume, err
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
	lease.BaseRef, lease.BaseCommit = claim.BaseRef, claim.BaseCommit
	lease.Deadline, lease.Overrides = row.Deadline, claim.Overrides
	lease.Contract, lease.ContractVersion = row.ContractID, row.ContractVersion
	lease.Kind = string(row.Kind)
	if lease.Review, err = desiredReview(tx.Planning(), row); err != nil {
		return lease, err
	}
	if lease.Attempt, err = desiredAttempt(tx, row); err != nil {
		return lease, err
	}
	resets, err := tx.Lanes().Resets(claim.Lane)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return lease, err
	}
	lease.Resets = resets
	respawns, err := tx.Lanes().Respawns(claim.Lane)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return lease, err
	}
	lease.Respawns = respawns
	if lease.Pack, err = desiredPack(tx.Packs(), claim.PackID); err != nil {
		return lease, err
	}
	if lease.ParkWanted, err = parkWanted(tx.Lanes(), claim.Lane); err != nil {
		return lease, err
	}
	now, err := tx.Now()
	if err != nil {
		return lease, err
	}
	if lease.Messages, err = desiredMessages(tx, claim.Lane, now); err != nil {
		return lease, err
	}
	if row.ResumeLaneID != "" {
		if lease.Resume, err = desiredResume(tx, row.ResumeLaneID); err != nil {
			return lease, err
		}
	}
	if row.ContractVersion == 0 {
		return lease, nil
	}
	pinned, err := tx.Planning().Contract(row.ContractID, row.ContractVersion)
	if err != nil {
		return lease, err
	}
	lease.Statement = pinned.Statement
	for _, criterion := range pinned.Criteria {
		lease.Criteria = append(lease.Criteria, criterion.ID+": "+criterion.Text)
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
	if e.Type == ledger.UploadEffectUnknown || e.Type == ledger.UploadEffectSettled {
		return effectUpload(tx, worker, id, e)
	}
	if e.WorkOrder == "" && ledger.IsOrchestratorLane(e.Lane) {
		return orchestratorUpload(tx, worker, id, e)
	}
	stamp := func(draft *ledger.EventDraft) { draft.ID, draft.OccurredAt = id, e.OccurredAt }
	lease := workorders.Lease{WorkOrder: e.WorkOrder, ClaimID: e.ClaimID, Fence: e.Fence}
	switch e.Type {
	case ledger.UploadLaneStarted:
		_, err := startLane(tx, lease, e.Lane, ledger.Actor{Kind: ledger.ActorWorker, ID: worker}, stamp)
		return err
	case ledger.UploadLaneStatus:
		return reportLane(tx, worker, id, e)
	case ledger.UploadLaneGuarded:
		return reportGuard(tx, worker, id, e)
	case ledger.UploadLaneArchived:
		return reportArchived(tx, worker, id, e)
	case ledger.UploadGenerationStarted:
		holder := workorders.Holder{Lease: lease, Lane: e.Lane, Generation: e.Generation - 1}
		_, err := nextGeneration(tx, holder, startReason(e), ledger.Actor{Kind: ledger.ActorWorker, ID: worker}, stamp)
		return err
	case ledger.UploadGenerationBound:
		return bindGeneration(tx, worker, id, e)
	case ledger.UploadHandoffStored:
		return storeHandoff(tx, worker, id, e)
	case ledger.UploadLaneParking:
		return parkingUpload(tx, worker, e, stamp)
	case ledger.UploadLaneParked:
		return parkedUpload(tx, worker, e, stamp)
	case ledger.UploadLaneResumed:
		return resumeUpload(tx, worker, e, stamp)
	case ledger.UploadGenerationResponded:
		return respondGeneration(tx, worker, id, e)
	case ledger.UploadGenerationBaseline:
		return recordBaseline(tx, worker, id, e)
	case ledger.UploadMessage:
		return messageUpload(tx, worker, id, e)
	case ledger.UploadApprovalRaised:
		return approvalUpload(tx, worker, id, e, stamp)
	case ledger.UploadApprovalAnswered:
		return answeredUpload(tx, worker, e)
	case ledger.UploadWakeSignal:
		return signalUpload(tx, worker, e, stamp)
	case ledger.UploadDriverQualified:
		return qualifiedUpload(tx, worker, id, e)
	case ledger.UploadLaneEnded:
		var result workorders.LaneResult
		if err := json.Unmarshal(e.Payload, &result); err != nil {
			return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
		}
		ended, err := endCanceled(tx, worker, e, stamp)
		if ended || err != nil {
			return err
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

// qualifiedUpload records what a worker's probe observed of its driver's version. It belongs to the
// worker and no lane, so no fence guards it, and a document that is not a probe's result is refused.
func qualifiedUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var q lanes.Qualification
	if err := json.Unmarshal(e.Payload, &q); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if err := q.Check(); err != nil || e.WorkOrder != "" || e.Lane != "" {
		return fmt.Errorf("%w: a qualification belongs to a worker: %v", workorders.ErrInvalid, err)
	}
	data, err := json.Marshal(q)
	if err != nil {
		return err
	}
	draft := workerEvent(ledger.EventDriverQualified, worker, ledger.Actor{Kind: ledger.ActorWorker, ID: worker})
	draft.ID, draft.OccurredAt, draft.Data = id, e.OccurredAt, data
	_, err = tx.Append(draft)
	return err
}

// reportLane stores what a worker says about a lane. It travels as a fenced event, so a
// worker that lost its lease cannot move the state anyone reads, and it takes the
// revision like any other write: renewal stays the one write that does not (ADR-0005).
func reportLane(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var status ledger.LaneStatus
	if err := json.Unmarshal(e.Payload, &status); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if err := checkHolder(tx, e); err != nil {
		return err
	}
	err := tx.Lanes().Report(ledger.LaneRow{ID: e.Lane, WorkerID: worker, Status: status, ReportedByEpoch: e.Epoch})
	if err != nil {
		return err
	}
	_, err = tx.Append(ledger.EventDraft{
		ID: id, Type: ledger.EventLaneStatus, OccurredAt: e.OccurredAt, Data: e.Payload,
		Actor:   ledger.Actor{Kind: ledger.ActorWorker, ID: worker},
		Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: e.Lane},
	})
	return err
}

// ranLane refuses a worker that never held this claim. A guard's report passes no fence,
// so a fenced holder can still say what it saved, and this is what keeps that to its own.
func ranLane(tx ports.WriteTx, worker string, e ledger.UploadEvent) error {
	claim, err := tx.Claims().Find(e.ClaimID)
	if err != nil {
		return err
	}
	if claim.Worker != worker {
		return fmt.Errorf("%w: claim %s is not this worker's", workorders.ErrInvalid, e.ClaimID)
	}
	return nil
}

// reportGuard stores how a stop left a worktree. Replaying the same stop is one row,
// because the artifact takes this event's ID.
func reportGuard(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var guard ledger.LaneGuard
	if err := json.Unmarshal(e.Payload, &guard); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if err := ranLane(tx, worker, e); err != nil {
		return err
	}
	if err := tx.Lanes().Guarded(e.Lane, guard.UnpushedWork); err != nil {
		return err
	}
	if guard.Ref != "" {
		artifact := ledger.Artifact{ID: id, Lane: e.Lane, Kind: ledger.ArtifactSalvage, URI: guard.Ref, Digest: guard.Commit}
		if err := tx.Lanes().AddArtifact(artifact); err != nil {
			return err
		}
	}
	return appendLaneEvent(tx, worker, id, ledger.EventLaneGuarded, e)
}

func reportArchived(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	if err := ranLane(tx, worker, e); err != nil {
		return err
	}
	if err := tx.Lanes().Archived(e.Lane); err != nil {
		return err
	}
	return appendLaneEvent(tx, worker, id, ledger.EventLaneArchived, e)
}

func appendLaneEvent(tx ports.WriteTx, worker, id, eventType string, e ledger.UploadEvent) error {
	_, err := tx.Append(ledger.EventDraft{
		ID: id, Type: eventType, OccurredAt: e.OccurredAt, Data: e.Payload,
		Actor:   ledger.Actor{Kind: ledger.ActorWorker, ID: worker},
		Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: e.Lane},
	})
	return err
}

// checkHolder compares a fence and a generation and changes nothing, because a status is
// about the lane and is no event of the work order's. The transaction already holds the
// workspace revision, so nothing can change hands before it commits.
func checkHolder(tx ports.WriteTx, e ledger.UploadEvent) error {
	current, err := tx.WorkOrders().Get(e.WorkOrder)
	if err != nil {
		return err
	}
	if current.Fence != e.Fence {
		return fenced(tx, e.WorkOrder)
	}
	generation, err := tx.Lanes().Generation(e.Lane)
	if err != nil {
		return err
	}
	if generation != e.Generation {
		return fenced(tx, e.WorkOrder)
	}
	return nil
}

// freshnessBeats is how many heartbeats a value stays observed, so one late beat does
// not relabel a lane.
const freshnessBeats = 3

// Lanes reads every lane with its freshness.
func (w *Workers) Lanes(ctx context.Context) ([]ledger.LaneView, error) {
	var out []ledger.LaneView
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		var err error
		out, err = w.LanesIn(tx)
		return err
	})
	return out, err
}

// LanesIn reads every lane with its freshness, inside a read, so a read model sees one moment.
func (w *Workers) LanesIn(tx ports.ReadTx) ([]ledger.LaneView, error) {
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, err
	}
	out := make([]ledger.LaneView, 0, len(rows))
	for _, row := range rows {
		view, err := w.view(tx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

// WorkerFreshness labels what the ledger holds of a worker by the same rule as a lane's. A heartbeat
// writes nothing about a worker, so what confirms one is a session it opened or a lease it renews, and
// a worker that holds no lease reads as last reported once its session is old.
func (w *Workers) WorkerFreshness(tx ports.ReadTx, worker ledger.Worker) (string, error) {
	now, err := tx.Now()
	if err != nil {
		return "", err
	}
	seen := worker.LastHandshakeAt
	held, err := tx.WorkOrders().HeldBy(worker.ID)
	if err != nil {
		return "", err
	}
	for _, order := range held {
		seen = max(seen, order.LeaseUntil-w.options.LeaseTTLMs)
	}
	if now-min(seen, now) <= freshnessBeats*w.options.HeartbeatMs {
		return ledger.FreshnessObserved, nil
	}
	return ledger.FreshnessLastReported, nil
}

// Lane reads one lane with its freshness.
func (w *Workers) Lane(ctx context.Context, id string) (ledger.LaneView, error) {
	var out ledger.LaneView
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		row, err := tx.Lanes().Row(id)
		if err != nil {
			return err
		}
		out, err = w.view(tx, row)
		return err
	})
	return out, err
}

// view labels a status. A heartbeat writes nothing about a lane, so what confirms a value
// is the lease it renews: the one write a heartbeat makes, from the worker that reported
// this value, in the epoch it reported it. The ledger is the source of a lane's end, so
// no worker needs to confirm that.
func (w *Workers) view(tx ports.ReadTx, row ledger.LaneRow) (ledger.LaneView, error) {
	view := ledger.LaneView{
		ID: row.ID, WorkOrder: row.WorkOrder, WorkerID: row.WorkerID, Generation: row.Generation,
		LaneStatus: row.Status, AsOf: row.ReportedAt, Freshness: ledger.FreshnessLastReported,
		ReportedByEpoch: row.ReportedByEpoch, Archived: row.Archived,
	}
	if row.UnpushedWork && !slices.Contains(view.Conditions, conditionUnpushed) {
		view.Conditions = append(slices.Clone(view.Conditions), conditionUnpushed)
	}
	now, err := tx.Now()
	if err != nil {
		return view, err
	}
	if view.Artifacts, err = tx.Lanes().Artifacts(row.ID); err != nil {
		return view, err
	}
	if view.Generations, err = tx.Lanes().Generations(row.ID); err != nil {
		return view, err
	}
	switch claim, err := tx.Claims().OfLane(row.ID); {
	case err == nil:
		view.BaseRef, view.BaseCommit = claim.BaseRef, claim.BaseCommit
	case !errors.Is(err, ports.ErrNotFound):
		return view, err
	}
	if row.Status.ActualState == ledger.StateEnded || row.Status.ActualState == ledger.StateParked {
		view.Freshness = ledger.FreshnessObserved
		return view, nil
	}
	if row.WorkerID == "" {
		return view, nil
	}
	worker, err := tx.Workers().Find(row.WorkerID)
	if errors.Is(err, ports.ErrNotFound) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	order, err := tx.WorkOrders().Get(row.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return view, nil
	}
	if err != nil {
		return view, err
	}
	if worker.Epoch != row.ReportedByEpoch || order.LeaseWorker != row.WorkerID {
		return view, nil
	}
	view.AsOf = min(max(view.AsOf, order.LeaseUntil-w.options.LeaseTTLMs), now)
	if now-view.AsOf <= freshnessBeats*w.options.HeartbeatMs {
		view.Freshness = ledger.FreshnessObserved
	}
	return view, nil
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

// Ask records a question for a lane this worker holds. A lane reaches the ledger only
// through its worker, so the plane checks that this worker holds that work order at that
// fence, and the question is about that work order.
func (w *Workers) Ask(ctx context.Context, ask questions.LaneAsk) (questions.AskResult, error) {
	var reviews string
	var fromPlan bool
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		if err := w.checkSession(tx, ask.Auth, ask.Epoch); err != nil {
			return err
		}
		order, err := tx.WorkOrders().Get(ask.WorkOrder)
		if err != nil {
			return err
		}
		if order.LeaseWorker != ask.Auth.WorkerID || order.Fence != ask.Fence {
			return &workorders.FencedError{Current: order.Fence, State: order.State}
		}
		reviews, fromPlan = order.ReviewsPlan, order.Kind == workorders.KindPlan
		return nil
	})
	if err != nil {
		return questions.AskResult{}, err
	}
	// A planning lane's question goes straight to the person (FR-SESSION-3). The ledger's work order
	// says whether this lane plans, and nothing the lane sent is read for it.
	ask.Ask.Direct = fromPlan
	ask.Ask.SubjectKind, ask.Ask.SubjectID = subjectOf(reviews, ask.WorkOrder)
	return w.questions.Ask(ctx, w.scope, ask.Ask, ledger.Actor{Kind: ledger.ActorLane, ID: ask.Ask.Lane})
}

// subjectOf says what a lane's question is about: its own work order, or for a review lane the plan
// it reads, because that is the work the answer holds back.
func subjectOf(reviewsPlan, workOrder string) (string, string) {
	if reviewsPlan != "" {
		return planning.SubjectPlan, reviewsPlan
	}
	return workorders.SubjectKind, workOrder
}

// Rule records what this worker's orchestrator decided or assumed for a question it holds.
// An orchestrator reaches the ledger through its worker, as a lane does.
func (w *Workers) Rule(ctx context.Context, ruling questions.WorkerRuling) (questions.Decision, error) {
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		return w.checkSession(tx, ruling.Auth, ruling.Epoch)
	})
	if err != nil {
		return questions.Decision{}, err
	}
	actor := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: ruling.Auth.WorkerID}
	return w.questions.Rule(ctx, w.scope, ruling.Question, ruling.Ruling, actor)
}

// Offered lists the work a worker could claim now: ready work whose needs its manifest meets
// and whose affinity lets it (FR-WO-6). Woken work comes first, because finishing started work
// comes first.
func (w *Workers) Offered(ctx context.Context, worker string) ([]workorders.WorkOrder, error) {
	offered := []workorders.WorkOrder{}
	err := w.store.Read(ctx, w.scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != workorders.Ready {
				continue
			}
			admitted, err := offers(tx, row, worker)
			if err != nil {
				return err
			}
			if admitted {
				offered = append(offered, row)
			}
		}
		return nil
	})
	slices.SortStableFunc(offered, func(a, b workorders.WorkOrder) int {
		switch {
		case a.ResumeLaneID != "" && b.ResumeLaneID == "":
			return -1
		case a.ResumeLaneID == "" && b.ResumeLaneID != "":
			return 1
		}
		return cmp.Compare(b.Priority, a.Priority)
	})
	return offered, err
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

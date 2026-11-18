package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The events of a forge account read, on the installation's log.
const (
	subjectForgeAccountRead = "forge_account_read"

	eventForgeAccountReadRequested = "forge_account_read.requested"
	eventForgeAccountReadReported  = "forge_account_read.reported"
)

// ForgeAccountReads is a member's request that a worker read its forge account, so the member can choose an owner
// for a new project's repositories (FR-TPL-6). The request is recorded in the installation, offered in the desired
// state of the worker it names for two minutes, and settled by the worker's first report.
type ForgeAccountReads struct {
	installation ports.Installation
	heartbeatMs  int64
}

// NewForgeAccountReads returns the use case. heartbeatMs is how often a worker beats, which tells a connected worker
// from one that stopped.
func NewForgeAccountReads(installation ports.Installation, heartbeatMs int64) *ForgeAccountReads {
	if heartbeatMs == 0 {
		heartbeatMs = defaultHeartbeatMs
	}
	return &ForgeAccountReads{installation: installation, heartbeatMs: heartbeatMs}
}

// Request records a member's request and offers it to the worker it names. It refuses a forge other than GitHub, a
// worker that is not connected, and a worker whose session speaks a protocol older than ProtocolProjectRemotes, which
// would never report it.
func (f *ForgeAccountReads) Request(ctx context.Context, worker string, in ledger.ForgeAccountRequest, actor ledger.Actor) (ledger.ForgeAccountReadView, error) {
	if !authority.Allowed(actor, authority.ActionCreateRemotes) {
		return ledger.ForgeAccountReadView{}, fmt.Errorf("%w: %w", workorders.ErrForbidden, authority.ErrMemberOnly)
	}
	var view ledger.ForgeAccountReadView
	err := f.installation.Write(ctx, "forge_account_read.request", func(tx ports.SharedTx) error {
		record, err := tx.Workers().Find(worker)
		if err != nil {
			return err
		}
		if err := in.Check(); err != nil {
			return fmt.Errorf("%w: %w", workorders.ErrInvalid, err)
		}
		now := tx.Now()
		if !connected(record.Worker, now, f.heartbeatMs) {
			return invalidTemplateWrite("worker %s is not connected: it has not reached this control plane in the last few heartbeats", record.Name)
		}
		if record.Protocol < ledger.ProtocolProjectRemotes {
			return invalidTemplateWrite("worker %s speaks protocol %d; upgrade it to create a project's remote", record.Name, record.Protocol)
		}
		id, err := newID(now)
		if err != nil {
			return err
		}
		stored := ledger.ForgeAccountReadRecord{
			ID: id, Worker: worker, RequestedBy: actor.ID, Forge: in.Forge, RequestedAt: now, ExpiresAt: now + ledger.ForgeAccountReadTTLMs,
			State: ledger.ForgeAccountReadOffered,
		}
		if err := tx.ForgeAccountReads().Insert(stored); err != nil {
			return err
		}
		data, err := json.Marshal(map[string]string{"member": actor.ID, "worker": worker, "forge": in.Forge})
		if err != nil {
			return err
		}
		if _, err := tx.Append(ledger.EventDraft{
			Type: eventForgeAccountReadRequested, Actor: actor, Subject: ledger.Subject{Kind: subjectForgeAccountRead, ID: id}, Data: data,
		}); err != nil {
			return err
		}
		view = stored.ViewAt(now)
		return nil
	})
	return view, err
}

// View reads one request of a worker as a member reads it.
func (f *ForgeAccountReads) View(ctx context.Context, worker, request string) (ledger.ForgeAccountReadView, error) {
	var record ledger.ForgeAccountReadRecord
	var now int64
	err := f.installation.Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		if now, err = tx.Now(); err != nil {
			return err
		}
		record, err = tx.ForgeAccountReads().Find(request)
		return err
	})
	if err != nil {
		return ledger.ForgeAccountReadView{}, err
	}
	if record.Worker != worker {
		return ledger.ForgeAccountReadView{}, fmt.Errorf("forge account read %s: %w", request, ports.ErrNotFound)
	}
	return record.ViewAt(now), nil
}

// desiredForgeAccountReads lists the reads offered to a worker: its own, not yet reported or expired, and only while
// its session speaks a protocol that reads them.
func desiredForgeAccountReads(tx ports.SharedReadTx, worker ledger.WorkerRecord) ([]ledger.DesiredForgeAccountRead, error) {
	if worker.Protocol < ledger.ProtocolProjectRemotes {
		return nil, nil
	}
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	offered, err := tx.ForgeAccountReads().Offered(worker.ID)
	if err != nil {
		return nil, err
	}
	var out []ledger.DesiredForgeAccountRead
	for _, record := range offered {
		if record.StateAt(now) == ledger.ForgeAccountReadOffered {
			out = append(out, record.Desired())
		}
	}
	return out, nil
}

// reportForgeAccount records a worker's first report of a read. A later one is accepted and changes nothing, and a
// report that comes after the read expired is recorded all the same.
func reportForgeAccount(ctx context.Context, installation ports.Installation, report ledger.ForgeAccountReport) error {
	if report.Request == "" {
		return invalidTemplateWrite("a report names its request")
	}
	switch report.State {
	case ledger.ForgeAccountOK, ledger.ForgeAccountMissing, ledger.ForgeAccountOutdated, ledger.ForgeAccountSignedOut, ledger.ForgeAccountScope:
	default:
		return invalidTemplateWrite("a report's state is ok, gh_missing, gh_outdated, gh_signed_out, or gh_scope_missing, and %q is none", report.State)
	}
	return installation.Write(ctx, "forge_account_read.report", func(tx ports.SharedTx) error {
		if _, err := authenticate(tx.Workers(), report.Auth); err != nil {
			return err
		}
		record, err := tx.ForgeAccountReads().Find(report.Request)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && record.Worker != report.Auth.WorkerID) {
			return fmt.Errorf("forge account read %s: %w", report.Request, ports.ErrNotFound)
		}
		if err != nil {
			return err
		}
		first, err := tx.ForgeAccountReads().Settle(record.ID, report, tx.Now())
		if err != nil || !first {
			return err
		}
		data, err := json.Marshal(map[string]string{"member": record.RequestedBy, "worker": record.Worker, "forge": record.Forge, "state": report.State})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: eventForgeAccountReadReported, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: record.Worker},
			Subject: ledger.Subject{Kind: subjectForgeAccountRead, ID: record.ID}, Data: data,
		})
		return err
	})
}

// ReportForgeAccount records what a worker found of its forge account, on the plane of one project.
func (w *Workers) ReportForgeAccount(ctx context.Context, report ledger.ForgeAccountReport) error {
	return reportForgeAccount(ctx, w.store.Installation(w.scope), report)
}

// ReportForgeAccount records what a worker found of its forge account.
func (p *Plane) ReportForgeAccount(ctx context.Context, report ledger.ForgeAccountReport) error {
	if _, err := p.worker(ctx, report.Auth, nil); err != nil {
		return err
	}
	return reportForgeAccount(ctx, p.installation, report)
}

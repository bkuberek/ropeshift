package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var (
	stmtFindInstallationEvent = defineInstallation("installation_event.find",
		`SELECT `+installationEventColumns+` FROM installation_events WHERE id = :id`)

	stmtInstallationRevision = defineInstallation("installation.revision",
		`SELECT rev FROM installation WHERE id = 1`)
)

// Installation is where the records every project shares change: the installation's own database.
func (s *Store) Installation(scope ledger.WorkspaceID) ports.Installation {
	if s.installation != nil {
		return s.installation
	}
	return ownShared{store: s, scope: scope}
}

// Read runs fn on one read transaction of the shared records.
func (in *Installation) Read(ctx context.Context, fn func(ports.SharedReadTx) error) error {
	return in.read(ctx, "shared.read", func(se *session) error {
		return fn(installationReadTx{session: se})
	})
}

// Write runs fn in one transaction of the shared records, with its events in the installation's log.
// It passes the steps a project's Write does, so the crash matrix kills it at each one.
func (in *Installation) Write(ctx context.Context, op string, fn func(ports.SharedTx) error) error {
	crashpoint.Hit(op, "begun")
	err := in.write(ctx, op, func(t *installationTx) error {
		crashpoint.Hit(op, "opened")
		if err := fn(t); err != nil {
			return err
		}
		if t.events > 0 {
			crashpoint.Hit(op, "before-commit")
		}
		return nil
	})
	if err == nil {
		crashpoint.Hit(op, "committed")
	}
	return err
}

// SaveReport keeps what a worker measured, in the installation's database.
func (in *Installation) SaveReport(ctx context.Context, worker string, epoch int64, report ledger.ResourceReport) error {
	return in.core.saveReport(ctx, ledger.InstallationScope, worker, epoch, report)
}

// installationReadTx is one view of the installation's shared records.
type installationReadTx struct {
	*session
}

func (t installationReadTx) Now() (int64, error) { return t.readClock() }

func (t installationReadTx) Revision() (int64, error) {
	row, err := t.row(stmtInstallationRevision, params{})
	if err != nil {
		return 0, err
	}
	var rev int64
	if err := row.Scan(&rev); err != nil {
		return 0, fmt.Errorf("%s: %w", stmtInstallationRevision.name, err)
	}
	return rev, nil
}

func (t installationReadTx) Events(after int64, limit int) ([]ledger.Event, error) {
	return listInstallationEvents(t.session, after, limit)
}

func (t installationReadTx) Workers() ports.WorkerReader {
	return workerTx{session: t.session, scope: ledger.InstallationScope}
}

func (t installationReadTx) Members() ports.MemberReader {
	return memberTx{session: t.session, scope: ledger.InstallationScope}
}

func (t installationReadTx) Policies() ports.PolicyReader {
	return autonomyPolicies{policyTx{session: t.session, scope: ledger.InstallationScope}}
}

func (t *installationTx) Now() int64 { return t.now }

func (t *installationTx) Event(id string) (ledger.Event, error) {
	row, err := t.row(stmtFindInstallationEvent, params{"id": id})
	if err != nil {
		return ledger.Event{}, err
	}
	e, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Event{}, fmt.Errorf("event %s: %w", id, ports.ErrNotFound)
	}
	return e, err
}

func (t *installationTx) Append(draft ledger.EventDraft) (ledger.Event, error) {
	return t.appendEvent(draft)
}

func (t *installationTx) Workers() ports.WorkerWriter {
	return workerTx{session: t.session, scope: ledger.InstallationScope, now: t.now}
}

func (t *installationTx) Members() ports.MemberWriter {
	return memberTx{session: t.session, scope: ledger.InstallationScope}
}

func (t *installationTx) Policies() ports.PolicyWriter {
	return autonomyPolicies{policyTx{session: t.session, scope: ledger.InstallationScope}}
}

// listInstallationEvents reads the installation's log after a revision.
func listInstallationEvents(se *session, after int64, limit int) ([]ledger.Event, error) {
	rows, err := se.rows(stmtListInstallationEvents, params{"after": after, "limit": int64(limit)})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// autonomyPolicies is the policy area of a shared transaction: the autonomy policy, which governs workers
// and so lives where they do. Every other kind is a project's.
type autonomyPolicies struct {
	policyTx
}

func (t autonomyPolicies) Insert(p authority.PolicyVersion) (bool, error) {
	if p.Kind != authority.PolicyAutonomy {
		return false, fmt.Errorf("%w: the %s policy is a project's", ports.ErrNotShared, p.Kind)
	}
	return t.policyTx.Insert(p)
}

// ownShared is the shared records of a store with no installation, which keeps them in its own database
// under one workspace, with their events in that workspace's log.
type ownShared struct {
	store *Store
	scope ledger.WorkspaceID
}

func (o ownShared) Read(ctx context.Context, fn func(ports.SharedReadTx) error) error {
	return o.store.Read(ctx, o.scope, func(tx ports.ReadTx) error {
		return fn(ownSharedRead{tx.(*readTx)})
	})
}

func (o ownShared) Write(ctx context.Context, op string, fn func(ports.SharedTx) error) error {
	return o.store.Write(ctx, o.scope, op, func(tx ports.WriteTx) error {
		return fn(ownSharedWrite{tx.(*writeTx)})
	})
}

func (o ownShared) SaveReport(ctx context.Context, worker string, epoch int64, report ledger.ResourceReport) error {
	return o.store.saveReport(ctx, o.scope, worker, epoch, report)
}

type ownSharedRead struct {
	t *readTx
}

func (r ownSharedRead) Now() (int64, error) { return r.t.Now() }

// Revision is zero: the store's own workspace revision already counts every change to these records.
func (r ownSharedRead) Revision() (int64, error) { return 0, nil }

func (r ownSharedRead) Events(after int64, limit int) ([]ledger.Event, error) {
	return r.t.Events(after, limit)
}

func (r ownSharedRead) Workers() ports.WorkerReader {
	return workerTx{session: r.t.session, scope: r.t.scope}
}

func (r ownSharedRead) Members() ports.MemberReader {
	return memberTx{session: r.t.session, scope: r.t.scope}
}

func (r ownSharedRead) Policies() ports.PolicyReader {
	return autonomyPolicies{policyTx{session: r.t.session, scope: r.t.scope}}
}

type ownSharedWrite struct {
	t *writeTx
}

func (w ownSharedWrite) Now() int64 { return w.t.now }

func (w ownSharedWrite) Event(id string) (ledger.Event, error) { return w.t.Event(id) }

func (w ownSharedWrite) Append(draft ledger.EventDraft) (ledger.Event, error) {
	return w.t.Append(draft)
}

func (w ownSharedWrite) Workers() ports.WorkerWriter {
	return workerTx{session: w.t.session, scope: w.t.scope, now: w.t.now}
}

func (w ownSharedWrite) Members() ports.MemberWriter {
	return memberTx{session: w.t.session, scope: w.t.scope}
}

func (w ownSharedWrite) Policies() ports.PolicyWriter {
	return autonomyPolicies{policyTx{session: w.t.session, scope: w.t.scope}}
}

var (
	_ ports.Installation = (*Installation)(nil)
	_ ports.Installation = ownShared{}
	_ ports.SharedTx     = (*installationTx)(nil)
	_ ports.SharedTx     = ownSharedWrite{}
)

package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Autonomy holds what keeps a person near automated use (FR-ORCH-6). Every write here is a member's:
// no agent can open a window, choose a level, or stand behind the stated risk. A worker is every
// project's, so each of these reads and writes the installation's records, with the events in its log.
type Autonomy struct {
	store ports.Store
}

// NewAutonomy returns the use cases over a Store.
func NewAutonomy(store ports.Store) *Autonomy { return &Autonomy{store: store} }

// autonomyInForce reads the policy in force. A workspace nobody published one for runs on the
// built-in version, which reading writes nothing for.
func autonomyInForce(policies ports.PolicyReader) (authority.AutonomyPolicy, error) {
	version, err := policies.InForce(authority.PolicyAutonomy)
	if errors.Is(err, ports.ErrNotFound) {
		return authority.DefaultAutonomyPolicy(), nil
	}
	if err != nil {
		return authority.AutonomyPolicy{}, err
	}
	return authority.ReadAutonomy(version)
}

// autonomyFacts is what one worker's standing rests on.
type autonomyFacts struct {
	policy   authority.AutonomyPolicy
	setting  authority.WorkerAutonomy
	window   *authority.RunWindow
	standing authority.AutonomyStanding
}

// lanes is the most lanes this standing lets a worker hold, where zero means no bound.
func (f autonomyFacts) lanes() int64 {
	if f.standing.Level == authority.LevelUnattended {
		return 0
	}
	return f.policy.AttendedMaxLanes
}

// respawns is the most generations a day this standing lets a lane have, where zero means no bound.
func (f autonomyFacts) respawns() int64 {
	if f.standing.Level == authority.LevelUnattended {
		return 0
	}
	return f.policy.AttendedMaxRespawnsPerDay
}

// readAutonomy reads where a worker stands at a time.
func readAutonomy(policies ports.PolicyReader, workers ports.WorkerReader, record ledger.WorkerRecord, now int64) (autonomyFacts, error) {
	policy, err := autonomyInForce(policies)
	if err != nil {
		return autonomyFacts{}, err
	}
	setting, err := workers.Autonomy(record.ID)
	if err != nil {
		return autonomyFacts{}, err
	}
	facts := autonomyFacts{policy: policy, setting: setting}
	facts.standing.Level = policy.LevelFor(record.Manifest.SignInKind(), setting)
	window, err := workers.OpenWindow(record.ID, now)
	switch {
	case err == nil:
		facts.window, facts.standing.WindowOpen = &window, true
	case !errors.Is(err, ports.ErrNotFound):
		return autonomyFacts{}, err
	}
	return facts, nil
}

// holdToAutonomy keeps a claim no member made to what this worker's level lets it take now, and says
// how many lanes that leaves it. A member's claim is a person being there, and work a wake brought
// back was started: its bound is the respawn cap and not a window, because a wake must bring a lane
// back with nobody there to dispatch it. A lane that has started goes on when a window closes.
func holdToAutonomy(tx ports.WriteTx, record ledger.WorkerRecord, order workorders.WorkOrder) (int64, error) {
	if ignoresAutonomy() {
		return 0, nil
	}
	facts, err := readAutonomy(tx.Policies(), tx.Workers(), record, tx.Now())
	if err != nil {
		return 0, err
	}
	if order.ResumeLaneID == "" {
		if !facts.standing.MayClaim() {
			return 0, fmt.Errorf("%w: worker %s is %s, with no run window open", workorders.ErrIneligible, record.ID, facts.standing.Level)
		}
		return facts.lanes(), nil
	}
	if err := withinRespawns(tx.Lanes(), order.ResumeLaneID, facts, tx.Now()); err != nil {
		return 0, err
	}
	return facts.lanes(), nil
}

// dayMs is the span of the bound on respawns.
const dayMs = 24 * 60 * 60 * 1000

// withinRespawns refuses another generation of a lane past what a day allows at this standing.
// Nothing here waits on a window: a member brings such a lane back, or the day passes.
func withinRespawns(lanes ports.LaneReader, lane string, facts autonomyFacts, now int64) error {
	bound := facts.respawns()
	if bound == 0 {
		return nil
	}
	generations, err := lanes.Generations(lane)
	if err != nil {
		return err
	}
	var today int64
	for _, generation := range generations {
		if now-generation.StartedAt < dayMs {
			today++
		}
	}
	if today >= bound {
		return fmt.Errorf("%w: lane %s has run %d generations today, and this worker is held to %d", workorders.ErrIneligible, lane, today, bound)
	}
	return nil
}

// View reads where a worker stands, as a person reads it.
func (a *Autonomy) View(ctx context.Context, scope ledger.WorkspaceID, worker string) (authority.AutonomyView, error) {
	var view authority.AutonomyView
	err := a.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		record, err := tx.Workers().Find(worker)
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		facts, err := readAutonomy(tx.Policies(), tx.Workers(), record, now)
		if err != nil {
			return err
		}
		view = authority.AutonomyView{
			AsOf: now, WorkerID: worker, SignIn: record.Manifest.SignInKind(), Level: facts.standing.Level, Chosen: facts.setting.Level,
			Acknowledged: facts.setting.Acknowledged(), Window: facts.window, MayClaim: facts.standing.MayClaim(),
			MaxLanes: facts.lanes(), MaxRespawnsPerDay: facts.respawns(),
		}
		return nil
	})
	return view, err
}

// SetLevel records the level a member chose. `unattended` on a subscription needs the acknowledgement
// first, so a member reads the risk before anything changes. The claim reads this again, because a
// worker can come back on another sign-in.
func (a *Autonomy) SetLevel(ctx context.Context, scope ledger.WorkspaceID, worker string, level authority.Level, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionSetAutonomy) {
		return workorders.ErrForbidden
	}
	if !level.Valid() {
		return fmt.Errorf("%w: %q is no autonomy level", workorders.ErrInvalid, level)
	}
	return a.store.Installation(scope).Write(ctx, "autonomy.set", func(tx ports.SharedTx) error {
		record, err := tx.Workers().Find(worker)
		if err != nil {
			return err
		}
		setting, err := tx.Workers().Autonomy(worker)
		if err != nil {
			return err
		}
		if setting.Level == level {
			return nil
		}
		if level == authority.LevelUnattended && record.Manifest.SignInKind() != ledger.SignInAPIKey && !setting.Acknowledged() {
			return authority.ErrRiskNotAcknowledged
		}
		setting.Level = level
		if err := tx.Workers().SaveAutonomy(setting); err != nil {
			return err
		}
		crashpoint.Hit("autonomy.set", "saved")
		return appendAutonomy(tx, authority.EventAutonomySet, worker, setting, actor)
	})
}

func appendAutonomy(tx ports.SharedTx, kind, worker string, record any, actor ledger.Actor) error {
	event, err := authority.AutonomyEvent(kind, worker, record, actor)
	if err != nil {
		return err
	}
	if dropsAutonomyEvent() {
		event.Subject.Kind = "note"
	}
	_, err = tx.Append(event)
	return err
}

// AcknowledgeRisk records that a member read the statement, named by its digest, for one worker. It
// is an event, so a person can see who stood behind it and which text they read. It changes no level.
func (a *Autonomy) AcknowledgeRisk(ctx context.Context, scope ledger.WorkspaceID, worker, digest string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionAcknowledgeRisk) {
		return workorders.ErrForbidden
	}
	if digest != authority.RiskDigest() {
		return fmt.Errorf("%w: run ropeshift worker risk, and read what it prints", authority.ErrStaleStatement)
	}
	return a.store.Installation(scope).Write(ctx, "autonomy.acknowledge", func(tx ports.SharedTx) error {
		if _, err := tx.Workers().Find(worker); err != nil {
			return err
		}
		setting, err := tx.Workers().Autonomy(worker)
		if err != nil {
			return err
		}
		if setting.Acknowledged() {
			return nil
		}
		setting.AcknowledgedBy, setting.AcknowledgedKind = actor.ID, string(actor.Kind)
		setting.AcknowledgedDigest, setting.AcknowledgedAt = digest, tx.Now()
		if err := tx.Workers().SaveAutonomy(setting); err != nil {
			return err
		}
		crashpoint.Hit("autonomy.acknowledge", "saved")
		return appendAutonomy(tx, authority.EventRiskAcknowledged, worker, setting, actor)
	})
}

// OpenWindow opens a run window for one worker. The client generates its ID, so a retry returns what
// the first attempt opened. Time is the ledger's, because a window's end is a timer.
func (a *Autonomy) OpenWindow(ctx context.Context, scope ledger.WorkspaceID, worker, id string, lengthMs int64, actor ledger.Actor) (authority.RunWindow, error) {
	if !authority.Allowed(actor, authority.ActionOpenWindow) {
		return authority.RunWindow{}, workorders.ErrForbidden
	}
	if id == "" {
		return authority.RunWindow{}, fmt.Errorf("%w: a window needs an ID", authority.ErrBadWindow)
	}
	length, err := authority.BoundWindow(lengthMs)
	if err != nil {
		return authority.RunWindow{}, err
	}
	var row authority.RunWindow
	err = a.store.Installation(scope).Write(ctx, "autonomy.window_open", func(tx ports.SharedTx) error {
		if _, err := tx.Workers().Find(worker); err != nil {
			return err
		}
		existing, err := tx.Workers().Window(id)
		if err == nil {
			row = existing
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		switch _, err := tx.Workers().OpenWindow(worker, tx.Now()); {
		case err == nil:
			return authority.ErrWindowOpen
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
		row = authority.RunWindow{
			ID: id, WorkerID: worker, OpenedBy: actor.ID, OpenedKind: string(actor.Kind), OpenedAt: tx.Now(), ClosesAt: tx.Now() + length,
		}
		if err := tx.Workers().InsertWindow(row); err != nil {
			return err
		}
		crashpoint.Hit("autonomy.window_open", "inserted")
		return appendAutonomy(tx, authority.EventWindowOpened, worker, row, actor)
	})
	if err != nil {
		return authority.RunWindow{}, err
	}
	return row, nil
}

// CloseWindow ends the window that stands for a worker. Lanes it let start go on: closing a window
// stops new claims, and a member stops a lane. Nothing open is nothing to do.
func (a *Autonomy) CloseWindow(ctx context.Context, scope ledger.WorkspaceID, worker string, actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionOpenWindow) {
		return workorders.ErrForbidden
	}
	return a.store.Installation(scope).Write(ctx, "autonomy.window_close", func(tx ports.SharedTx) error {
		row, err := tx.Workers().OpenWindow(worker, tx.Now())
		if errors.Is(err, ports.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		closed, err := tx.Workers().CloseWindow(row.ID, actor.ID, tx.Now())
		if err != nil || !closed {
			return err
		}
		crashpoint.Hit("autonomy.window_close", "closed")
		row.ClosedBy, row.ClosedAt = actor.ID, tx.Now()
		return appendAutonomy(tx, authority.EventWindowClosed, worker, row, actor)
	})
}

// PublishAutonomyPolicy records a new version of the policy, on the terms of every other kind: only a
// member can, each version is newer than the last, and the same body is a retry.
func (a *Autonomy) PublishAutonomyPolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.AutonomyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := a.store.Installation(scope).Write(ctx, "policy.publish", func(tx ports.SharedTx) error {
		current, err := currentAutonomyVersion(tx)
		if err != nil {
			return err
		}
		if current.Version == draft.Policy.Version && current.Digest == authority.PolicyDigest(draft.Body) {
			record = current
			return nil
		}
		if err := draft.Policy.Check(); err != nil {
			return err
		}
		if draft.Policy.Version <= current.Version {
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrBadLanePolicy, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewAutonomyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// currentAutonomyVersion reads the version in force, and records the built-in one the first time a
// member changes it, so every version a member reviews has the one before it on record.
func currentAutonomyVersion(tx ports.SharedTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(authority.PolicyAutonomy)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInAutonomy(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

func builtInAutonomy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.AutonomyDraft{Body: authority.BuiltInAutonomyBody, Policy: authority.DefaultAutonomyPolicy()}
	return authority.NewAutonomyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// AutonomyVersions lists the versions of the policy, newest first. A workspace nobody published one
// for still has the built-in version in force.
func (a *Autonomy) AutonomyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := a.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyAutonomy)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInAutonomy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

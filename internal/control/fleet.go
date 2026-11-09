package control

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The events of the fleet's control.
const (
	subjectWorkspace  = "workspace"
	eventFleetChanged = "workspace.fleet_changed"
)

// Fleet holds the one control for a whole fleet (FR-LANE-16). A change takes a revision, so
// a worker hears of it on its next beat, or sooner.
type Fleet struct {
	store ports.Store
}

// NewFleet returns the use cases over a Store.
func NewFleet(store ports.Store) *Fleet { return &Fleet{store: store} }

// Read returns a workspace's mode and caps.
func (f *Fleet) Read(ctx context.Context, scope ledger.WorkspaceID) (ledger.Workspace, error) {
	var workspace ledger.Workspace
	err := f.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		workspace, err = tx.Workspace()
		return err
	})
	return workspace, err
}

// SetMode changes the fleet's mode. Only a member can, because it stops a person's work.
func (f *Fleet) SetMode(ctx context.Context, scope ledger.WorkspaceID, mode ledger.FleetMode, actor ledger.Actor) error {
	if !mode.Valid() {
		return fmt.Errorf("%w: fleet mode %q", workorders.ErrInvalid, mode)
	}
	return f.change(ctx, scope, actor, func(current ledger.Workspace) (ledger.FleetMode, ledger.Caps) {
		return mode, current.Caps()
	})
}

// SetCaps replaces the caps a claim is held to. A claim already granted stays.
func (f *Fleet) SetCaps(ctx context.Context, scope ledger.WorkspaceID, caps ledger.Caps, actor ledger.Actor) error {
	if caps.WIPLimit < 0 || caps.RepositoryLimit < 0 || caps.WorkerLimit < 0 {
		return fmt.Errorf("%w: a cap cannot be negative", workorders.ErrInvalid)
	}
	return f.change(ctx, scope, actor, func(current ledger.Workspace) (ledger.FleetMode, ledger.Caps) {
		return current.FleetMode, caps
	})
}

func (f *Fleet) change(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor, next func(ledger.Workspace) (ledger.FleetMode, ledger.Caps)) error {
	if !authority.Allowed(actor, authority.ActionChangeFleet) {
		return workorders.ErrForbidden
	}
	return f.store.Write(ctx, scope, "fleet.change", func(tx ports.WriteTx) error {
		current, err := tx.Workspace()
		if err != nil {
			return err
		}
		mode, caps := next(current)
		if err := tx.SaveFleet(mode, caps); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: eventFleetChanged, Actor: actor, Subject: ledger.Subject{Kind: subjectWorkspace, ID: string(scope)},
			Data: fmt.Appendf(nil, `{"fleet_mode":%q,"wip_limit":%d,"lanes_per_repository":%d,"lanes_per_worker":%d}`,
				mode, caps.WIPLimit, caps.RepositoryLimit, caps.WorkerLimit),
		})
		return err
	})
}

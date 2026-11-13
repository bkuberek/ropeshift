package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// LaneCaps reads and sets the lane caps of the worker this daemon runs (FR-PROJ-7). They live in the
// user's layer of that machine's profile, so the web UI, the CLI, and the file are one setting, and the
// worker reads them again at every start and every measure, with no restart.
type LaneCaps struct {
	installation ports.Installation
	profile      ports.UserProfile
	local        func(context.Context) (string, error)
}

// NewLaneCaps returns the use case. A nil profile or local is a daemon that runs no worker, whose caps it
// cannot reach.
func NewLaneCaps(installation ports.Installation, profile ports.UserProfile, local func(context.Context) (string, error)) *LaneCaps {
	return &LaneCaps{installation: installation, profile: profile, local: local}
}

// View reads a worker's caps, with what is in force and the recommendation it last reported.
func (c *LaneCaps) View(ctx context.Context, worker string) (ledger.WorkerCaps, error) {
	record, err := c.machine(ctx, worker)
	if err != nil {
		return ledger.WorkerCaps{}, err
	}
	caps, err := c.profile.LaneCaps(ctx)
	if err != nil {
		return ledger.WorkerCaps{}, err
	}
	return capsView(record, caps), nil
}

// Set writes a member's caps, and answers with a warning when the per-worker cap stands above the
// recommendation, which it saves all the same.
func (c *LaneCaps) Set(ctx context.Context, worker string, caps lanes.LaneCapSettings, actor ledger.Actor) (ledger.WorkerCaps, error) {
	if !authority.Allowed(actor, authority.ActionSetLaneCaps) {
		return ledger.WorkerCaps{}, workorders.ErrForbidden
	}
	if err := caps.Check(); err != nil {
		return ledger.WorkerCaps{}, fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	record, err := c.machine(ctx, worker)
	if err != nil {
		return ledger.WorkerCaps{}, err
	}
	if err := c.profile.SetLaneCaps(ctx, caps); err != nil {
		if errors.Is(err, lanes.ErrBadProfile) {
			return ledger.WorkerCaps{}, fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
		}
		return ledger.WorkerCaps{}, err
	}
	return capsView(record, caps), nil
}

// machine reads a worker that runs on this daemon's machine, whose user's layer this daemon can reach. A
// worker on another machine keeps its caps in its own user's layer.
func (c *LaneCaps) machine(ctx context.Context, worker string) (ledger.Worker, error) {
	var record ledger.Worker
	err := c.installation.Read(ctx, func(tx ports.SharedReadTx) error {
		found, err := tx.Workers().Find(worker)
		record = found.Worker
		return err
	})
	if err != nil {
		return ledger.Worker{}, err
	}
	if c.profile == nil || c.local == nil {
		return ledger.Worker{}, fmt.Errorf("%w: this daemon runs no worker, so it reaches no machine's lane caps", workorders.ErrInvalid)
	}
	local, err := c.local(ctx)
	if err != nil {
		return ledger.Worker{}, fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if local != worker {
		return ledger.Worker{}, fmt.Errorf("%w: worker %s runs on another machine, so its lane caps are set in the user's profile on that machine", workorders.ErrInvalid, worker)
	}
	return record, nil
}

// capsView puts a worker's caps beside its last recommendation. A cap above the recommendation warns.
func capsView(record ledger.Worker, caps lanes.LaneCapSettings) ledger.WorkerCaps {
	view := ledger.WorkerCaps{Worker: record.ID, MaxLanes: caps.MaxLanes, MaxLanesPerSignIn: caps.MaxLanesPerSignIn, ReportedAt: record.ReportedAt}
	var recommended int64
	if record.Report != nil && record.Report.Recommendation != nil {
		view.Recommendation = record.Report.Recommendation
		recommended = view.Recommendation.MaxLanes
	}
	view.EffectiveMaxLanes = lanes.WorkerCap(caps.MaxLanes, caps.MaxLanes, recommended)
	if caps.MaxLanes != nil && recommended > 0 && *caps.MaxLanes > recommended {
		view.Warning = &ledger.CapWarning{
			Recommended: recommended, Reason: view.Recommendation.Reason,
			Message: fmt.Sprintf("%d lanes is above this worker's recommendation of %d, and is saved: %s.", *caps.MaxLanes, recommended, view.Recommendation.Reason),
		}
	}
	return view
}

package control

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Estimates keeps each open work order's current estimate as the project's history grows (ADR-0026,
// FR-EST-10): after each fold, the delivery tick estimates the open work again, and keeps a new set wherever an
// estimate changed.
type Estimates struct {
	store ports.Store
}

// NewEstimates returns the estimates of a store.
func NewEstimates(store ports.Store) *Estimates { return &Estimates{store: store} }

// Refresh estimates every open `change` and `rework` work order again, and keeps the sets in which an estimate
// changed. An estimate is derived from the log, so it keeps them in a transaction of the fold, which takes no
// revision and appends no event. A read finds whether any changed first, so a tick that finds none holds no
// write. It returns how many sets it kept.
func (e *Estimates) Refresh(ctx context.Context, scope ledger.WorkspaceID) (int, error) {
	stale := false
	err := e.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		sets, err := staleSetsIn(tx)
		stale = len(sets) > 0
		return err
	})
	if err != nil || !stale {
		return 0, err
	}
	kept := 0
	err = e.store.Fold(ctx, scope, func(tx ports.FoldTx) error {
		// The work is read again in the transaction that keeps it, because a write may have estimated it since.
		sets, err := staleSetsIn(tx)
		if err != nil {
			return err
		}
		for _, set := range sets {
			if err := tx.EstimateSets().Add(set); err != nil {
				return err
			}
		}
		kept = len(sets)
		return nil
	})
	return kept, err
}

// staleSetsIn makes, within a read, the sets an estimate changed in.
func staleSetsIn(tx ports.ReadTx) ([]delivery.EstimateSet, error) {
	workspace, err := tx.Workspace()
	if err != nil {
		return nil, err
	}
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	r := estimateReads{
		orders: tx.WorkOrders(), roster: tx.Roster(), repositories: tx.Repositories(), planning: tx.Planning(),
		policies: tx.Policies(), deliveries: tx.Deliveries(), pool: tx.Pool(),
	}
	return r.staleSets(tx.Estimates(), workspace.Rev, now)
}

// estimateGroup is open work estimated in one set: the work of a plan's newest version, or, with no plan, the
// work no plan's newest version covers.
type estimateGroup struct {
	plan    string
	version int64
	orders  []workorders.WorkOrder
}

// estimateGroups gathers the open work: each plan's newest version with the work of the contracts it covers, and
// then the work outside every one of them.
func (r estimateReads) estimateGroups() ([]estimateGroup, error) {
	plans, err := r.planning.Plans()
	if err != nil {
		return nil, err
	}
	covered := map[string]bool{}
	var groups []estimateGroup
	for _, plan := range plans {
		version, err := r.planning.LatestPlanVersion(plan.ID)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows, err := planWork(r.orders, version)
		if err != nil {
			return nil, err
		}
		group := estimateGroup{plan: plan.ID, version: version.Version}
		for _, row := range rows {
			if estimable(row) && !covered[row.ID] {
				covered[row.ID] = true
				group.orders = append(group.orders, row)
			}
		}
		if len(group.orders) > 0 {
			groups = append(groups, group)
		}
	}
	rows, err := r.orders.List()
	if err != nil {
		return nil, err
	}
	var outside estimateGroup
	for _, row := range rows {
		if estimable(row) && !covered[row.ID] {
			outside.orders = append(outside.orders, row)
		}
	}
	if len(outside.orders) > 0 {
		groups = append(groups, outside)
	}
	return groups, nil
}

// staleSets makes each group's set as it would be made now, and returns those in which some estimate says what
// its work order's current estimate does not. A plan version's set holds all of the version's open work, so the
// newest set of a version is whole; the set of work outside every plan version holds only what changed.
func (r estimateReads) staleSets(current ports.EstimateReader, rev, now int64) ([]delivery.EstimateSet, error) {
	groups, err := r.estimateGroups()
	if err != nil {
		return nil, err
	}
	var out []delivery.EstimateSet
	for _, group := range groups {
		set, made, err := r.estimateSet(delivery.EstimateSet{Plan: group.plan, PlanVersion: group.version, Rev: rev}, group.orders, now)
		if err != nil {
			return nil, err
		}
		if !made {
			continue
		}
		var changed []delivery.Estimate
		for _, e := range set.Estimates {
			kept, err := current.Current(e.WorkOrder)
			switch {
			case errors.Is(err, ports.ErrNotFound):
			case err != nil:
				return nil, err
			case kept.SaysTheSame(e):
				continue
			}
			changed = append(changed, e)
		}
		switch {
		case len(changed) == 0:
		case group.plan == "":
			set.Estimates = changed
			out = append(out, set.Seal())
		default:
			out = append(out, set)
		}
	}
	return out, nil
}

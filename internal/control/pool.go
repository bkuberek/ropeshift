package control

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// DeliveryPool shares a project's delivery history with the installation's pool while its estimate policy says
// so, and only then (ADR-0026, FR-EST-13). Other projects that share theirs draw on it at their broadest tier.
type DeliveryPool struct {
	store ports.Store
}

// NewDeliveryPool returns the pool of a store's projects.
func NewDeliveryPool(store ports.Store) *DeliveryPool { return &DeliveryPool{store: store} }

// Share replaces what a project shares with its delivery records as the pool holds them, while its estimate
// policy in force shares history, and takes its share out once it does not. It writes only when what the project
// shares changed, and reports whether it did; a store that keeps no pool never does. The policy is read in the
// project's database and the share written in the installation's, so the write names the policy version it read,
// and a share under an earlier version than a member's later take-out changes nothing.
func (p *DeliveryPool) Share(ctx context.Context, scope ledger.WorkspaceID) (bool, error) {
	var held, digest string
	var version int64
	var records []delivery.Record
	kept := false
	err := p.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		pool := tx.Pool()
		if kept = pool.Kept(); !kept {
			return nil
		}
		var err error
		if held, err = pool.Shared(); err != nil {
			return err
		}
		policy, err := estimatePolicyIn(tx.Policies())
		if err != nil {
			return err
		}
		version = policy.Version
		if !policy.ShareHistory {
			return nil
		}
		if records, err = tx.Deliveries().Records(); err != nil {
			return err
		}
		records = delivery.Pooled(records)
		digest = delivery.PoolDigest(records)
		return nil
	})
	if err != nil || !kept || digest == held {
		return false, err
	}
	return true, p.store.Installation(scope).SharePool(ctx, scope, version, digest, records)
}

// DeliveryTick is one project's delivery tick: the fold keeps its timelines and delivery records from its log, the
// project shares its records with the pool or takes them out, and its open work is estimated again. The share
// runs whether or not the fold failed, so a project's choice about sharing never waits on its fold; the refresh
// runs only after a fold that kept the records current.
func DeliveryTick(ctx context.Context, store ports.Store, scope ledger.WorkspaceID) error {
	_, folded := NewDeliveryFold(store).Fold(ctx, scope)
	_, shared := NewDeliveryPool(store).Share(ctx, scope)
	if folded != nil || shared != nil {
		return errors.Join(folded, shared)
	}
	_, err := NewEstimates(store).Refresh(ctx, scope)
	return err
}

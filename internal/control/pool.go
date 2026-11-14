package control

import (
	"context"

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
// shares changed, and reports whether it did.
func (p *DeliveryPool) Share(ctx context.Context, scope ledger.WorkspaceID) (bool, error) {
	var held, digest string
	var records []delivery.Record
	err := p.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		if held, err = tx.Pool().Shared(); err != nil {
			return err
		}
		policy, err := estimatePolicyIn(tx.Policies())
		if err != nil || !policy.ShareHistory {
			return err
		}
		if records, err = tx.Deliveries().Records(); err != nil {
			return err
		}
		records = delivery.Pooled(records)
		digest = delivery.PoolDigest(records)
		return nil
	})
	if err != nil || digest == held {
		return false, err
	}
	return true, p.store.Installation(scope).SharePool(ctx, scope, digest, records)
}

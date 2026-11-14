package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// EstimatePolicies keeps a project's `[estimate_policy]` (ADR-0026): the versions a member publishes, which set
// how many comparable records a confident estimate needs, how many recent records it reads, how many trials a
// forecast runs, and whether the project shares its delivery history. No calibration process writes it.
type EstimatePolicies struct {
	store ports.Store
}

// NewEstimatePolicies returns the estimate policy of a store.
func NewEstimatePolicies(store ports.Store) *EstimatePolicies { return &EstimatePolicies{store: store} }

func builtInEstimatePolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := delivery.EstimatePolicyDraft{Body: delivery.BuiltInEstimatePolicyBody, Policy: delivery.DefaultEstimatePolicy()}
	return delivery.NewEstimatePolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// currentEstimateVersion reads the version in force, recording the built-in one the first time a write reads it.
func currentEstimateVersion(tx ports.WriteTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(delivery.PolicyEstimate)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInEstimatePolicy(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

// PublishEstimatePolicy records a new version of the estimate policy, on the same terms as every other policy:
// only a member can, each version is newer than the last, and the same body is a retry.
func (e *EstimatePolicies) PublishEstimatePolicy(ctx context.Context, scope ledger.WorkspaceID, draft delivery.EstimatePolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := e.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := currentEstimateVersion(tx)
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
			return fmt.Errorf("%w: version %d is not newer than version %d", delivery.ErrBadEstimatePolicy, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = delivery.NewEstimatePolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// EstimatePolicyVersions lists the versions of the estimate policy, newest first. A project nobody published one
// for has the built-in one in force.
func (e *EstimatePolicies) EstimatePolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := e.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(delivery.PolicyEstimate)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInEstimatePolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

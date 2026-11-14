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

// PublishedEstimatePolicy is an estimate policy version as published, and whether taking the project's records
// out of the installation's pool is still pending, which the next delivery tick retries.
type PublishedEstimatePolicy struct {
	Version        authority.PolicyVersion
	TakeOutPending bool
}

// PublishEstimatePolicy records a new version of the estimate policy, on the same terms as every other policy:
// only a member can, each version is newer than the last, and the same body is a retry. A version that stops
// sharing history then takes the project's records out of the pool. That runs in the installation's database,
// after the policy committed, so when it fails the version is still the one in force: the answer says the
// take-out is pending, and the next delivery tick, which shares only what the policy in force allows, takes them
// out.
func (e *EstimatePolicies) PublishEstimatePolicy(ctx context.Context, scope ledger.WorkspaceID, draft delivery.EstimatePolicyDraft, actor ledger.Actor) (PublishedEstimatePolicy, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return PublishedEstimatePolicy{}, workorders.ErrForbidden
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
		return PublishedEstimatePolicy{}, err
	}
	// A project that stops sharing takes its records out of the pool now, not at its next tick, which a failing
	// fold could hold off. A retry of the same body takes them out again.
	published := PublishedEstimatePolicy{Version: record}
	if !draft.Policy.ShareHistory {
		published.TakeOutPending = e.store.Installation(scope).SharePool(ctx, scope, draft.Policy.Version, "", nil) != nil
	}
	return published, nil
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

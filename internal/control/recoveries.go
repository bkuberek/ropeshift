package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Recoveries holds the use cases that recover stuck work (FR-ORCH-8): the versioned policy that bounds an
// orchestrator, and the acts it and a member take.
type Recoveries struct {
	store ports.Store
}

// NewRecoveries returns the use cases over a Store.
func NewRecoveries(store ports.Store) *Recoveries { return &Recoveries{store: store} }

// currentRecoveryVersion reads the version of the recovery policy in force, as currentPolicy does for
// decisions, so every recovery act names a version a member can read.
func currentRecoveryVersion(tx ports.WriteTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(authority.PolicyRecovery)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInRecoveryPolicy(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

func builtInRecoveryPolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.RecoveryPolicyDraft{Body: authority.BuiltInRecoveryPolicyBody, Policy: authority.DefaultRecoveryPolicy()}
	return authority.NewRecoveryPolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// PublishRecoveryPolicy records a new version of the recovery policy, on the same terms as every other
// policy: only a member can, each version is newer than the last, and the same body is a retry. No
// orchestrator can move the bound it is held to.
func (r *Recoveries) PublishRecoveryPolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.RecoveryPolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := r.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := currentRecoveryVersion(tx)
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
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrBadRecoveryPolicy, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewRecoveryPolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// RecoveryPolicyVersions lists the versions of the recovery policy, newest first. A workspace nothing has
// recovered in yet still has the built-in one in force.
func (r *Recoveries) RecoveryPolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyRecovery)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInRecoveryPolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

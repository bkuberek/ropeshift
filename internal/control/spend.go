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

// Spend holds the use cases of the spend policy (ADR-0037): the versioned record of the values that stop agent
// sessions from spending on waste, which only a member publishes.
type Spend struct {
	store ports.Store
}

// NewSpend returns the use cases over a Store.
func NewSpend(store ports.Store) *Spend { return &Spend{store: store} }

func builtInSpendPolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.SpendPolicyDraft{Body: authority.BuiltInSpendPolicyBody, Policy: authority.DefaultSpendPolicy()}
	return authority.NewSpendPolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// currentSpendVersion reads the version in force. A project nobody configured has the built-in one.
func currentSpendVersion(tx ports.WriteTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(authority.PolicySpend)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInSpendPolicy(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

// PublishSpendPolicy records a new version, on the same terms as every other policy: only a member can, each
// version is newer than the last, the same body is a retry, and a value outside its key's range is refused. No
// plan, repository file, lane, or orchestrator can move a value, because a lane that could would move its own
// limit.
func (s *Spend) PublishSpendPolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.SpendPolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := s.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := currentSpendVersion(tx)
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
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrBadSpendPolicy, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewSpendPolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// SpendPolicyVersions lists the versions, newest first. A project nobody configured still has the built-in
// one in force.
func (s *Spend) SpendPolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicySpend)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInSpendPolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

// desiredSpend reads the spend policy in force into what a lease carries. A project with none published reads
// the built-in version, so a lease always names the values it starts under.
func desiredSpend(tx ports.ReadTx) (*ledger.DesiredSpend, error) {
	policy := authority.DefaultSpendPolicy()
	version, err := tx.Policies().InForce(authority.PolicySpend)
	switch {
	case errors.Is(err, ports.ErrNotFound):
	case err != nil:
		return nil, err
	default:
		if policy, err = authority.ReadSpendPolicy(version); err != nil {
			return nil, err
		}
	}
	return &ledger.DesiredSpend{
		Version: policy.Version, MaxLiveWaitMs: policy.LiveWait().Milliseconds(), MaxToolSleepMs: policy.ToolSleep().Milliseconds(),
		WaitRulesOff: policy.WaitRulesOff, WaitRulePatterns: policy.WaitRulePatterns,
	}, nil
}

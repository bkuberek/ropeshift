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

func builtInInstallationSpendPolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.InstallationSpendPolicyDraft{Body: authority.BuiltInInstallationSpendPolicyBody, Policy: authority.DefaultInstallationSpendPolicy()}
	return authority.NewInstallationSpendPolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// PublishInstallationSpendPolicy records a new version of the installation's spend policy, in the installation,
// as the autonomy policy is, because the orchestrator serves every project on its machine. It has the terms of the
// project's: only a member can, each version is newer than the last, the same body is a retry, and a value outside
// its key's range is refused. Which member may is the surface's rule: no project role covers the installation.
func (s *Spend) PublishInstallationSpendPolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.InstallationSpendPolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := s.store.Installation(scope).Write(ctx, "policy.publish", func(tx ports.SharedTx) error {
		current, err := tx.Policies().InForce(authority.PolicyInstallationSpend)
		if errors.Is(err, ports.ErrNotFound) {
			id, err := newID(tx.Now())
			if err != nil {
				return err
			}
			if current, err = builtInInstallationSpendPolicy(id, tx.Now()); err != nil {
				return err
			}
			if err := insertPolicy(tx, current, policyActor); err != nil {
				return err
			}
		} else if err != nil {
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
		if record, err = authority.NewInstallationSpendPolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// InstallationSpendPolicyVersions lists the versions, newest first. An installation nobody configured still has
// the built-in one in force.
func (s *Spend) InstallationSpendPolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := s.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyInstallationSpend)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInInstallationSpendPolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

// InstallationSpendPolicyInForce reads the installation's spend policy in force.
func (s *Spend) InstallationSpendPolicyInForce(ctx context.Context, scope ledger.WorkspaceID) (authority.InstallationSpendPolicy, error) {
	var policy authority.InstallationSpendPolicy
	err := s.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		policy, err = installationSpendInForce(tx.Policies())
		return err
	})
	return policy, err
}

// installationSpendInForce reads the installation's spend policy from either side of a transaction. An
// installation nobody configured runs on the built-in version, which reading writes nothing for.
func installationSpendInForce(policies ports.PolicyReader) (authority.InstallationSpendPolicy, error) {
	version, err := policies.InForce(authority.PolicyInstallationSpend)
	if errors.Is(err, ports.ErrNotFound) {
		return authority.DefaultInstallationSpendPolicy(), nil
	}
	if err != nil {
		return authority.InstallationSpendPolicy{}, err
	}
	return authority.ReadInstallationSpendPolicy(version)
}

// policiesInForce reads the versions of the policies a lane that starts now starts under. A policy nobody
// published is the built-in version, which is version 1.
func policiesInForce(policies ports.PolicyReader) (ledger.LanePolicies, error) {
	var out ledger.LanePolicies
	for kind, into := range map[authority.PolicyKind]*int64{authority.PolicySpend: &out.Spend, authority.PolicyInstallationSpend: &out.InstallationSpend} {
		version, err := policies.InForce(kind)
		switch {
		case errors.Is(err, ports.ErrNotFound):
			*into = 1
		case err != nil:
			return out, err
		default:
			*into = version.Version
		}
	}
	return out, nil
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

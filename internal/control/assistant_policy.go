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

// assistantInForce reads the assistant policy in force. An installation nobody published one for runs on
// the built-in version, which reading writes nothing for.
func assistantInForce(policies ports.PolicyReader) (authority.AssistantPolicy, error) {
	version, err := policies.InForce(authority.PolicyAssistant)
	if errors.Is(err, ports.ErrNotFound) {
		return authority.BuiltInAssistantPolicy(), nil
	}
	if err != nil {
		return authority.AssistantPolicy{}, err
	}
	return authority.ReadAssistant(version)
}

func builtInAssistant(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.AssistantDraft{Body: authority.BuiltInAssistantBody, Policy: authority.BuiltInAssistantPolicy()}
	return authority.NewAssistantVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// PublishAssistantPolicy records a new version of the assistant policy, in the installation, as the
// autonomy policy is. Only a member can, and the same body again is a retry.
func (c *Conversations) PublishAssistantPolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.AssistantDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := c.store.Installation(scope).Write(ctx, "policy.publish", func(tx ports.SharedTx) error {
		current, err := tx.Policies().InForce(authority.PolicyAssistant)
		if errors.Is(err, ports.ErrNotFound) {
			id, err := newID(tx.Now())
			if err != nil {
				return err
			}
			if current, err = builtInAssistant(id, tx.Now()); err != nil {
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
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrInvalid, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewAssistantVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// AssistantVersions lists the versions of the assistant policy, newest first. An installation nobody
// published one for still has the built-in version in force.
func (c *Conversations) AssistantVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := c.store.Installation(scope).Read(ctx, func(tx ports.SharedReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyAssistant)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInAssistant(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

// AssistantPolicyOf reads the assistant policy in force.
func AssistantPolicyOf(policies ports.PolicyReader) (authority.AssistantPolicy, error) {
	return assistantInForce(policies)
}

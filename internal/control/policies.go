package control

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// builtInPolicyAuthor is who the first version of a workspace's decision policy names.
const builtInPolicyAuthor = "built-in"

var policyActor = ledger.Actor{Kind: ledger.ActorSystem, ID: builtInPolicyAuthor}

// currentPolicy reads the version of the decision policy in force. A workspace starts on the
// built-in version, and the ledger records it the first time a write reads it, so every
// decision names a version a member can read.
func currentPolicy(tx ports.WriteTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(authority.PolicyDecision)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInPolicy(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

func builtInPolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := questions.PolicyDraft{Body: questions.BuiltInPolicyBody, Policy: questions.DefaultPolicy()}
	return questions.NewPolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// policyInForce reads what the version in force says. A body that no longer matches its
// digest stops the decision, because a member reviewed other text.
func policyInForce(tx ports.WriteTx) (questions.Policy, error) {
	version, err := currentPolicy(tx)
	if err != nil {
		return questions.Policy{}, err
	}
	return questions.ReadPolicy(version)
}

func insertPolicy(tx ports.WriteTx, version authority.PolicyVersion, actor ledger.Actor) error {
	added, err := tx.Policies().Insert(version)
	if err != nil {
		return err
	}
	if !added {
		return fmt.Errorf("%w: version %d of the %s policy exists", questions.ErrInvalid, version.Version, version.Kind)
	}
	crashpoint.Hit("policy.publish", "inserted")
	event, err := questions.RecordEvent(questions.EventPolicyPublished, questions.SubjectPolicy, version.ID, version, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// PublishPolicy records a new version of the decision policy, which is in force from then
// on. Only a member can (FR-ORCH-5). A body names its version, and each version is newer
// than the last, so two authors working from one version cannot both win. The same body
// again is a retry.
func (q *Questions) PublishPolicy(ctx context.Context, scope ledger.WorkspaceID, draft questions.PolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := q.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := currentPolicy(tx)
		if err != nil {
			return err
		}
		if current.Version == draft.Policy.Version && current.Digest == authority.PolicyDigest(draft.Body) {
			record = current
			return nil
		}
		added, err := tx.Questions().Categories()
		if err != nil {
			return err
		}
		if err := draft.Policy.Check(added); err != nil {
			return err
		}
		if draft.Policy.Version <= current.Version {
			return fmt.Errorf("%w: version %d is not newer than version %d", questions.ErrInvalid, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = questions.NewPolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// currentLaneVersion reads the version of the lane policy in force, as currentPolicy does for
// decisions, so every lane a policy touched names a version a member can read.
func currentLaneVersion(tx ports.WriteTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(authority.PolicyLane)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInLanePolicy(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

func builtInLanePolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.LanePolicyDraft{Body: authority.BuiltInLanePolicyBody, Policy: authority.DefaultLanePolicy()}
	return authority.NewLanePolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// lanePolicyInForce reads what the version in force says. A body that no longer matches its digest
// stops the write, because a member reviewed other text.
func lanePolicyInForce(tx ports.WriteTx) (authority.LanePolicy, error) {
	version, err := currentLaneVersion(tx)
	if err != nil {
		return authority.LanePolicy{}, err
	}
	return authority.ReadLanePolicy(version)
}

// PublishLanePolicy records a new version of the lane policy, on the same terms as a decision
// policy: only a member can, each version is newer than the last, and the same body is a retry.
func (q *Questions) PublishLanePolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.LanePolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := q.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := currentLaneVersion(tx)
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
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrBadLanePolicy, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewLanePolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// LanePolicyVersions lists the versions of the lane policy, newest first. A workspace nothing has
// happened to yet still has the built-in one in force.
func (q *Questions) LanePolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := q.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyLane)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInLanePolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

// PolicyVersions lists the versions of the decision policy, newest first, so a member can
// review them. A workspace that has decided nothing yet still has the built-in one in force.
func (q *Questions) PolicyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := q.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyDecision)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInPolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

package control

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

// laneEnds are the triggers of a lane's end, which carry its outcome and the reason it gave.
var laneEnds = []workorders.Trigger{
	workorders.TriggerLaneCompleted, workorders.TriggerLaneBlocked, workorders.TriggerLaneFailed, workorders.TriggerLaneFailedRetry,
	workorders.TriggerAttemptsExhausted,
}

// lastLaneEnd reads how a work order's last lane ended: the lane, its outcome, the reason it gave, and
// what it reported. A work order no lane ended reads as nothing.
func lastLaneEnd(tx ports.WriteTx, workOrder string) (workorders.Change, error) {
	events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: workOrder})
	if err != nil {
		return workorders.Change{}, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		var change workorders.Change
		if json.Unmarshal(events[i].Data, &change) != nil {
			continue
		}
		for _, end := range laneEnds {
			if change.Trigger == end {
				return change, nil
			}
		}
	}
	return workorders.Change{}, nil
}

// retryWork returns failed work to `ready` for another attempt, and keeps why with the work order, so the
// next lane's brief carries the failed lane's reason and the retry's (ADR-0014). Work with no attempts
// left gets one more, which only a member's retry or a member's grant reaches. Work that was ready when its
// lane started is ready again, as it is after a takeover, so the definition of ready does not run.
func retryWork(tx ports.WriteTx, current workorders.WorkOrder, reason string, actor ledger.Actor) (workorders.State, error) {
	if _, err := workorders.Move(current, workorders.TriggerRetried); err != nil {
		return "", err
	}
	change := workorders.Change{Trigger: workorders.TriggerRetried, Reason: reason}
	bound := current.MaxAttempts
	if !current.AttemptsLeft() {
		bound = max(current.AttemptBound(), current.Attempts) + 1
		change.MaxAttempts = bound
	}
	won, err := tx.WorkOrders().Retry(current.ID, bound)
	if err != nil {
		return "", err
	}
	if !won {
		return "", fmt.Errorf("%w: work order %s is no longer failed", workorders.ErrIllegalMove, current.ID)
	}
	ended, err := lastLaneEnd(tx, current.ID)
	if err != nil {
		return "", err
	}
	change.Lane = ended.Lane
	event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerRetried), current.ID, change, actor)
	if err != nil {
		return "", err
	}
	if _, err := tx.Append(event); err != nil {
		return "", err
	}
	why := cmp.Or(ended.Reason, string(ended.Outcome), string(workorders.OutcomeFailed))
	note := workorders.ReworkNote{
		WorkOrder: current.ID, Attempt: current.Fence,
		Reason: fmt.Sprintf("The last attempt's lane failed (%s), and the work was retried: %s", why, reason),
	}
	if err := tx.Evidence().RecordRework(note); err != nil {
		return "", err
	}
	return workorders.Ready, nil
}

// Retry is a member's retry of failed work, with why. A member is bound by no recovery policy, so work
// with no attempts left gets one more.
func (r *Recoveries) Retry(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) (workorders.State, error) {
	if !authority.Allowed(actor, authority.ActionRetryWork) {
		return "", workorders.ErrForbidden
	}
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("%w: a retry needs a reason", workorders.ErrInvalid)
	}
	var state workorders.State
	err := r.store.Write(ctx, scope, "work_order.retry", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		state, err = retryWork(tx, current, reason, actor)
		return err
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

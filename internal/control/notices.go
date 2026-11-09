package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	// noticeAttempts bounds what one notice costs a channel that stays down. The Needs you view still
	// holds the item, so a person who looks is told.
	noticeAttempts = 5
	// noticesPerTick bounds what one tick sends, so a first webhook on a long list is no flood.
	noticesPerTick = 20
	// noticeBackoffMs is how long a channel gets before an attempt with no good answer is tried again.
	noticeBackoffMs = 60_000
	// noticeDeadline bounds one channel, on top of what the channel holds itself to.
	noticeDeadline = 15 * time.Second
)

// Notices tells members what waits for them, at least once. Sending happens between two transactions,
// because no transaction may wait on a network, so a kill after the send and before the record repeats
// a notice. The ledger records each attempt before it is made, so a repeat stays visible.
type Notices struct {
	store    ports.Store
	channels func(authority.NotifyPolicy) []ports.Notifier
	backoff  int64
}

// NewNotices returns the use cases over a Store. Channels says what a policy turns on.
func NewNotices(store ports.Store, channels func(authority.NotifyPolicy) []ports.Notifier) *Notices {
	return &Notices{store: store, channels: channels, backoff: noticeBackoffMs}
}

// RetryingAfter sets how long a channel gets before it is asked again, which a run on real time needs.
func (n *Notices) RetryingAfter(ms int64) *Notices {
	n.backoff = ms
	return n
}

func notifyPolicyOf(tx ports.ReadTx) (authority.NotifyPolicy, error) {
	version, err := tx.Policies().InForce(authority.PolicyNotify)
	if errors.Is(err, ports.ErrNotFound) {
		return authority.DefaultNotifyPolicy(), nil
	}
	if err != nil {
		return authority.NotifyPolicy{}, err
	}
	return authority.ReadNotifyPolicy(version)
}

// Deliver tells each channel of each thing it has not heard of. What no longer waits is not in the
// list, so a retry stops when a person acts. One channel that fails stops no other.
func (n *Notices) Deliver(ctx context.Context, scope ledger.WorkspaceID, waiting []ledger.Notice, actor ledger.Actor) error {
	var policy authority.NotifyPolicy
	err := n.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		policy, err = notifyPolicyOf(tx)
		return err
	})
	if err != nil {
		return err
	}
	channels := n.channels(policy)
	var failures []error
	sent := 0
	for _, notice := range waiting {
		for _, channel := range channels {
			if sent == noticesPerTick {
				return errors.Join(failures...)
			}
			attempt, due, err := n.due(ctx, scope, notice, channel.Channel())
			if err != nil {
				return errors.Join(append(failures, err)...)
			}
			if !due {
				continue
			}
			sent++
			notice.Attempt = attempt
			if err := n.deliver(ctx, scope, notice, channel, actor); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", channel.Channel(), err))
			}
		}
	}
	return errors.Join(failures...)
}

// due reads a notice's own events, which are all the record there is.
func (n *Notices) due(ctx context.Context, scope ledger.WorkspaceID, notice ledger.Notice, channel string) (int64, bool, error) {
	var attempt int64
	var due bool
	err := n.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectNotice, ID: notice.Delivery})
		if err != nil {
			return err
		}
		lines, err := ledger.ReplayNotices(events)
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		attempt, due = 1, true
		for _, line := range lines {
			if line.Channel != channel {
				continue
			}
			attempt = line.Attempts + 1
			due = !line.Delivered && line.Attempts < noticeAttempts && now-line.LastAt >= n.backoff<<max(line.Attempts-1, 0)
		}
		return nil
	})
	return attempt, due, err
}

func (n *Notices) record(ctx context.Context, scope ledger.WorkspaceID, op, eventType string, report ledger.NoticeReport, actor ledger.Actor) error {
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return n.store.Write(ctx, scope, op, func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type: eventType, Actor: actor, Data: data, Subject: ledger.Subject{Kind: ledger.SubjectNotice, ID: report.Delivery},
		})
		return err
	})
}

// deliver makes one attempt. The word comes after the send on a context of its own, so a tick that was
// told to stop still says what it did.
func (n *Notices) deliver(ctx context.Context, scope ledger.WorkspaceID, notice ledger.Notice, channel ports.Notifier, actor ledger.Actor) error {
	report := ledger.NoticeReport{
		Delivery: notice.Delivery, Channel: channel.Channel(), Attempt: notice.Attempt, Kind: notice.Kind, Reason: notice.Reason, Since: notice.Since,
	}
	if !forgetsAttempts() {
		if err := n.record(ctx, scope, "notice.attempt", ledger.EventNoticeAttempted, report, actor); err != nil {
			return err
		}
	}
	crashpoint.Hit("notify", "attempted")
	if recordsBeforeSending() {
		if err := n.record(context.WithoutCancel(ctx), scope, "notice.record", ledger.EventNoticeDelivered, report, actor); err != nil {
			return err
		}
		crashpoint.Hit("notify", "recorded-first")
	}
	bounded, cancel := context.WithTimeout(ctx, noticeDeadline)
	defer cancel()
	sent := channel.Notify(bounded, notice)
	crashpoint.Hit("notify", "sent")
	eventType := ledger.EventNoticeDelivered
	if sent != nil {
		eventType, report.Failure = ledger.EventNoticeFailed, sent.Error()
	}
	if err := n.record(context.WithoutCancel(ctx), scope, "notice.record", eventType, report, actor); err != nil {
		return err
	}
	crashpoint.Hit("notify", "recorded")
	return sent
}

// List says how each notice stands on each channel, oldest first, which is where a person sees a
// channel that heard something twice.
func (n *Notices) List(ctx context.Context, scope ledger.WorkspaceID) ([]ledger.NoticeLine, error) {
	var lines []ledger.NoticeLine
	err := n.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		events, err := ReadAllEvents(tx.Events)
		if err != nil {
			return err
		}
		lines, err = ledger.ReplayNotices(events)
		return err
	})
	return lines, err
}

// currentNotifyVersion reads the version in force. A workspace nobody configured has the built-in one,
// which turns nothing on.
func currentNotifyVersion(tx ports.WriteTx) (authority.PolicyVersion, error) {
	version, err := tx.Policies().InForce(authority.PolicyNotify)
	if !errors.Is(err, ports.ErrNotFound) {
		return version, err
	}
	id, err := newID(tx.Now())
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	if version, err = builtInNotifyPolicy(id, tx.Now()); err != nil {
		return authority.PolicyVersion{}, err
	}
	return version, insertPolicy(tx, version, policyActor)
}

func builtInNotifyPolicy(id string, createdAt int64) (authority.PolicyVersion, error) {
	draft := authority.NotifyPolicyDraft{Body: authority.BuiltInNotifyPolicyBody, Policy: authority.DefaultNotifyPolicy()}
	return authority.NewNotifyPolicyVersion(id, builtInPolicyAuthor, draft, createdAt)
}

// PublishNotifyPolicy records a new version, on the same terms as every other policy: only a member
// can, each version is newer than the last, and the same body is a retry. No agent can turn a channel
// on or change where one points, because a notice leaves the machine.
func (n *Notices) PublishNotifyPolicy(ctx context.Context, scope ledger.WorkspaceID, draft authority.NotifyPolicyDraft, actor ledger.Actor) (authority.PolicyVersion, error) {
	if !authority.Allowed(actor, authority.ActionPublishPolicy) {
		return authority.PolicyVersion{}, workorders.ErrForbidden
	}
	var record authority.PolicyVersion
	err := n.store.Write(ctx, scope, "policy.publish", func(tx ports.WriteTx) error {
		current, err := currentNotifyVersion(tx)
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
			return fmt.Errorf("%w: version %d is not newer than version %d", authority.ErrBadNotifyPolicy, draft.Policy.Version, current.Version)
		}
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		if record, err = authority.NewNotifyPolicyVersion(id, actor.ID, draft, tx.Now()); err != nil {
			return err
		}
		return insertPolicy(tx, record, actor)
	})
	if err != nil {
		return authority.PolicyVersion{}, err
	}
	return record, nil
}

// NotifyVersions lists the versions, newest first. A workspace nobody configured still has the built-in
// one in force.
func (n *Notices) NotifyVersions(ctx context.Context, scope ledger.WorkspaceID) ([]authority.PolicyVersion, error) {
	var versions []authority.PolicyVersion
	err := n.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		versions, err = tx.Policies().Versions(authority.PolicyNotify)
		return err
	})
	if err != nil || len(versions) != 0 {
		return versions, err
	}
	version, err := builtInNotifyPolicy(builtInPolicyAuthor, 0)
	if err != nil {
		return nil, err
	}
	return []authority.PolicyVersion{version}, nil
}

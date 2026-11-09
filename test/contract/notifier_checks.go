package contract

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// NotifierChecks are what telling a member promises: at least once, with each attempt on record, and
// nothing leaves unless a member said where it goes.
var NotifierChecks = []Check{
	{"NT1", "each channel hears of a thing once, and a quiet tick says nothing", checkNoticeOnce},
	{"NT2", "a channel that refused is asked again, later, and the ledger says why", checkNoticeRetry},
	{"NT3", "a channel that stays down is asked a bounded number of times", checkNoticeGivesUp},
	{"NT4", "a send the record never heard of repeats, and the repeat stays visible", checkNoticeRepeats},
	{"NT5", "one channel that fails stops no other", checkNoticeChannelsApart},
	{"NT6", "only a member says where a notice goes, and no address may hold a secret", checkNotifyPolicy},
	{"NT7", "a tick sends a bounded number of notices", checkNoticeBound},
	{"NT8", "what no longer waits is not sent", checkNoticeStops},
}

const noticeBackoffMs = 60_000

var noticeActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "notify-tick"}

func (r *Rig) notices(s ports.Store, channels ...ports.Notifier) *control.Notices {
	return control.NewNotices(s, func(authority.NotifyPolicy) []ports.Notifier { return channels })
}

func waiting(r *Rig, ids ...string) []ledger.Notice {
	out := make([]ledger.Notice, 0, len(ids))
	for _, id := range ids {
		out = append(out, ledger.NewNotice(r.Scope, ledger.NoticeWork, "blocked", id, 1))
	}
	return out
}

func checkNoticeOnce(r *Rig) error {
	s := r.Store()
	a, b := fake.NewNotifier("a"), fake.NewNotifier("b")
	notices := r.notices(s, a, b)
	items := waiting(r, "wo-1")
	for range 2 {
		if err := notices.Deliver(r.Ctx, r.Scope, items, noticeActor); err != nil {
			return err
		}
	}
	for _, channel := range []*fake.Notifier{a, b} {
		heard := channel.Notices()
		if len(heard) != 1 || heard[0].Attempt != 1 || heard[0].Delivery != items[0].Delivery {
			return Violatedf("%s heard %+v", channel.Channel(), heard)
		}
	}
	lines, err := notices.List(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(lines) != 2 || !lines[0].Delivered || lines[0].Attempts != 1 {
		return Violatedf("lines %+v", lines)
	}
	return nil
}

func checkNoticeRetry(r *Rig) error {
	s := r.Store()
	channel := fake.NewNotifier("a")
	channel.Fail(errors.New("the webhook answered 500"))
	notices := r.notices(s, channel)
	items := waiting(r, "wo-1")
	if err := notices.Deliver(r.Ctx, r.Scope, items, noticeActor); err == nil {
		return Violatedf("a refusal reached nobody")
	}
	lines, err := notices.List(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(lines) != 1 || lines[0].Delivered || lines[0].Failure != "the webhook answered 500" {
		return Violatedf("lines %+v", lines)
	}
	channel.Fail(nil)
	if err := notices.Deliver(r.Ctx, r.Scope, items, noticeActor); err != nil {
		return err
	}
	if heard := channel.Notices(); len(heard) != 0 {
		return Violatedf("a retry came at once: %+v", heard)
	}
	r.DB.ShiftClock(noticeBackoffMs + 1)
	if err := notices.Deliver(r.Ctx, r.Scope, items, noticeActor); err != nil {
		return err
	}
	heard := channel.Notices()
	if len(heard) != 1 || heard[0].Attempt != 2 {
		return Violatedf("heard %+v", heard)
	}
	return nil
}

func checkNoticeGivesUp(r *Rig) error {
	s := r.Store()
	channel := fake.NewNotifier("a")
	channel.Fail(errors.New("down"))
	notices := r.notices(s, channel)
	items := waiting(r, "wo-1")
	for range 12 {
		_ = notices.Deliver(r.Ctx, r.Scope, items, noticeActor)
		r.DB.ShiftClock(noticeBackoffMs * 100)
	}
	lines, err := notices.List(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(lines) != 1 || lines[0].Attempts != 5 {
		return Violatedf("lines %+v", lines)
	}
	return nil
}

// lostRecord is a store that stops hearing, as one does to a process killed between a send and its
// record.
type lostRecord struct {
	ports.Store
	lose atomic.Bool
}

func newLostRecord(store ports.Store) *lostRecord { return &lostRecord{Store: store} }

func (l *lostRecord) Write(ctx context.Context, scope ledger.WorkspaceID, op string, fn func(ports.WriteTx) error) error {
	if l.lose.Load() {
		return errors.New("the process died")
	}
	return l.Store.Write(ctx, scope, op, fn)
}

// sentThenLost takes a notice, and then the store stops hearing.
type sentThenLost struct {
	*fake.Notifier
	store *lostRecord
}

func (c sentThenLost) Notify(ctx context.Context, notice ledger.Notice) error {
	err := c.Notifier.Notify(ctx, notice)
	c.store.lose.Store(true)
	return err
}

func checkNoticeRepeats(r *Rig) error {
	s := r.Store()
	inner := fake.NewNotifier("a")
	lost := newLostRecord(s)
	items := waiting(r, "wo-1")
	if err := r.notices(lost, sentThenLost{inner, lost}).Deliver(r.Ctx, r.Scope, items, noticeActor); err == nil {
		return Violatedf("a record that failed reached nobody")
	}
	lost.lose.Store(false)
	r.DB.ShiftClock(noticeBackoffMs + 1)
	notices := r.notices(s, inner)
	if err := notices.Deliver(r.Ctx, r.Scope, items, noticeActor); err != nil {
		return err
	}
	heard := inner.Notices()
	if len(heard) != 2 || heard[0].Delivery != heard[1].Delivery || heard[1].Attempt != 2 {
		return Violatedf("heard %+v", heard)
	}
	lines, err := notices.List(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(lines) != 1 || lines[0].Attempts != 2 || !lines[0].Delivered {
		return Violatedf("the repeat left no trace: %+v", lines)
	}
	return nil
}

func checkNoticeChannelsApart(r *Rig) error {
	s := r.Store()
	down, up := fake.NewNotifier("down"), fake.NewNotifier("up")
	down.Fail(errors.New("no"))
	if err := r.notices(s, down, up).Deliver(r.Ctx, r.Scope, waiting(r, "wo-1"), noticeActor); err == nil {
		return Violatedf("a channel's failure said nothing")
	}
	if heard := up.Notices(); len(heard) != 1 {
		return Violatedf("heard %+v", heard)
	}
	return nil
}

func checkNotifyPolicy(r *Rig) error {
	s := r.Store()
	notices := r.notices(s)
	publish := func(policy authority.NotifyPolicy, actor ledger.Actor) error {
		_, err := notices.PublishNotifyPolicy(r.Ctx, r.Scope, authority.NotifyPolicyDraft{Body: fmt.Sprintf("%+v", policy), Policy: policy}, actor)
		return err
	}
	for _, actor := range []ledger.Actor{{Kind: ledger.ActorOrchestrator, ID: "o"}, {Kind: ledger.ActorLane, ID: "l"}, {Kind: ledger.ActorWorker, ID: "w"}, {Kind: ledger.ActorSystem, ID: "t"}} {
		if err := publish(authority.NotifyPolicy{Version: 2, WebhookURL: "https://hooks.example.com/r"}, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s said where a notice goes: %v", actor.Kind, err)
		}
	}
	for _, bad := range []authority.NotifyPolicy{
		{Version: 2, WebhookURL: "https://user:hunter2@hooks.example.com/r"},
		{Version: 2, WebhookURL: "https://hooks.example.com/r?token=hunter2"},
		{Version: 2, WebhookURL: "https://hooks.example.com/r#token"},
		{Version: 2, WebhookURL: "http://hooks.example.com/r"},
		{Version: 2, WebhookURL: "ftp://hooks.example.com/r"},
		{Version: 2, WebhookURL: "https://"},
		{Version: 2, WebhookURL: "https://hooks.example.com/r", WebhookSecretEnv: "hunter2-the-key-itself"},
		{Version: 2, WebhookSecretEnv: "NO_WEBHOOK"},
		{Version: 1, WebhookURL: "https://hooks.example.com/r"},
	} {
		if err := publish(bad, r.Actor); !errors.Is(err, authority.ErrBadNotifyPolicy) {
			return Violatedf("%+v published: %v", bad, err)
		}
	}
	for _, good := range []authority.NotifyPolicy{
		{Version: 2, WebhookURL: "https://hooks.example.com/r", WebhookSecretEnv: "ROPESHIFT_WEBHOOK_KEY"},
		{Version: 3, WebhookURL: "http://127.0.0.1:9000/r"},
		{Version: 4, WebhookURL: "http://localhost:9000/r"},
		{Version: 5, WebhookURL: "http://[::1]:9000/r"},
		{Version: 6, Desktop: true},
	} {
		if err := publish(good, r.Actor); err != nil {
			return Violatedf("%+v refused: %v", good, err)
		}
	}
	versions, err := notices.NotifyVersions(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(versions) != 6 || versions[0].Version != 6 {
		return Violatedf("versions %+v", versions)
	}
	for _, version := range versions {
		if strings.Contains(version.Settings, "hunter2") {
			return Violatedf("the ledger holds a secret: %s", version.Settings)
		}
	}
	return nil
}

func checkNoticeBound(r *Rig) error {
	s := r.Store()
	channel := fake.NewNotifier("a")
	ids := make([]string, 50)
	for i := range ids {
		ids[i] = fmt.Sprintf("wo-%d", i)
	}
	if err := r.notices(s, channel).Deliver(r.Ctx, r.Scope, waiting(r, ids...), noticeActor); err != nil {
		return err
	}
	if heard := channel.Notices(); len(heard) != 20 {
		return Violatedf("one tick sent %d", len(heard))
	}
	return nil
}

func checkNoticeStops(r *Rig) error {
	s := r.Store()
	channel := fake.NewNotifier("a")
	channel.Fail(errors.New("down"))
	notices := r.notices(s, channel)
	items := waiting(r, "wo-1")
	_ = notices.Deliver(r.Ctx, r.Scope, items, noticeActor)
	channel.Fail(nil)
	r.DB.ShiftClock(noticeBackoffMs + 1)
	if err := notices.Deliver(r.Ctx, r.Scope, nil, noticeActor); err != nil {
		return err
	}
	if heard := channel.Notices(); len(heard) != 0 {
		return Violatedf("heard %+v", heard)
	}
	return nil
}

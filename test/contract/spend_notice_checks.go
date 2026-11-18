package contract

import (
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// SpendNoticeChecks are what a spend alert promises (FR-SPEND-19, ADR-0037): it reaches the web UI and the channels a
// member chose for its kind and no others, a channel that failed leaves it visible in the web UI, and it is told once for
// each limit and scope.
var SpendNoticeChecks = []Check{
	{"SN1", "each spend notice kind reaches the web UI and the channels the policy chose for it, and no channel beyond the choice", checkSpendNoticesFollowTheChoice},
	{"SN2", "a spend notice with no choice reaches the web UI only, and a notice of another kind still reaches every channel", checkSpendNoticeDefaults},
	{"SN3", "a webhook that failed leaves the spend notice visible in the web UI, and a new choice moves the next delivery", checkAFailedWebhookLeavesTheNoticeVisible},
	{"SN4", "a project's soft and hard notices are told once per limit and window across repeated reports, and a raised limit is told again", checkAProjectNoticeIsToldOncePerLimit},
	{"SN5", "a work order's soft and hard notices are told once per limit, and name the work order's kind", checkAWorkOrderNoticeIsToldOncePerLimit},
	{"SN6", "a published spend policy names the keys before and after and the member, once, and a retry names nothing", checkAPolicyChangeNamesTheKeysAndTheMember},
	{"SN7", "a raise of a limit says what it was and what it is, and the member who raised it", checkARaiseIsNoticed},
	{"SN8", "the notices a member has not heard reach each channel the policy chose, the project named, and no-progress and waiting notices go the same way", checkWhatWaitsReachesTheChosenChannels},
	{"SN9", "editing project_warn_share moves the soft notice at once, and a new limit is told again", checkEditingTheWarnShareMovesTheNotice},
	{"SN10", "editing spend_channels adds or takes away a channel for the next notice of a kind, with no restart", checkEditingTheChannelsMovesDelivery},
	{"SN11", "an assistant's spend is tallied apart from the work's, and tells one soft notice a day at assistant_daily_warn", checkTheAssistantsDailyWarning},
}

func checkTheAssistantsDailyWarning(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	record := opened.Conversation
	if err := r.projectPolicy(t.store, 2, "assistant_daily_warn", "5"); err != nil {
		return err
	}
	upload := func(generation int64, usd float64) error {
		return t.runUpload(record, ledger.UploadGenerationUsage, generation, ledger.GenerationUsage{CostUSD: usd, KnownTurns: 2})
	}
	tally := func(scope string) (float64, error) {
		var total float64
		err := t.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			var err error
			total, err = tx.Lanes().SpendSince(scope, 0)
			return err
		})
		return total, err
	}
	warned := func() ([]ledger.SpendNotice, error) {
		notices, err := r.spendNotices(t.store, ledger.NoticeSpendSoftLimit)
		return ofScope(notices, ledger.NoticeScopeAssistant), err
	}
	for _, step := range []struct {
		generation int64
		usd, total float64
		notices    int
	}{{1, 3, 3, 0}, {1, 5.5, 5.5, 1}, {1, 6, 6, 1}, {1, 6, 6, 1}, {1, 2, 6, 1}, {2, 1, 7, 1}} {
		if err := upload(step.generation, step.usd); err != nil {
			return err
		}
		if got, err := tally(ledger.SpendScopeAssistant); err != nil || !near(got, step.total) {
			return Violatedf("after a report of %v in generation %d the assistant's day reads %v, want %v: %v", step.usd, step.generation, got, step.total, err)
		}
		if got, err := warned(); err != nil || len(got) != step.notices {
			return Violatedf("after a report of %v the assistant has %d warnings, want %d: %v", step.usd, len(got), step.notices, err)
		}
	}
	got, err := warned()
	if err != nil || !near(got[0].LimitUSD, 5) || !near(got[0].ChargedUSD, 5.5) || got[0].Subject != "" {
		return Violatedf("the warning reads %+v: %v", got, err)
	}
	if work, err := tally(ledger.SpendScopeWork); err != nil || work != 0 {
		return Violatedf("the assistant's spend reached the work's tally, which reads %v: %v", work, err)
	}
	// A warning of 0 is none.
	if err := r.projectPolicy(t.store, 3); err != nil {
		return err
	}
	if err := upload(2, 50); err != nil {
		return err
	}
	if got, err := warned(); err != nil || len(got) != 1 {
		return Violatedf("an assistant with no warning set told %+v: %v", got, err)
	}
	return nil
}

func checkEditingTheWarnShareMovesTheNotice(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	soft := func() ([]ledger.SpendNotice, error) {
		notices, err := r.spendNotices(s.store, ledger.NoticeSpendSoftLimit)
		return ofScope(notices, ledger.NoticeScopeProjectDay), err
	}
	if err := r.projectPolicy(s.store, 2, "project_daily_limit", "10", "project_warn_share", "0.5"); err != nil {
		return err
	}
	if err := s.spent(4); err != nil {
		return err
	}
	if got, err := soft(); err != nil || len(got) != 0 {
		return Violatedf("4 spent of a day of 10 with a warning at half told %+v: %v", got, err)
	}
	if err := s.spent(5.5); err != nil {
		return err
	}
	if got, err := soft(); err != nil || len(got) != 1 || !near(got[0].LimitUSD, 5) {
		return Violatedf("5.5 spent told %+v, want one soft notice at 5: %v", got, err)
	}
	// A later version moves the warning to nine tenths, which is a limit no notice has told, and the next report tells it.
	if err := r.projectPolicy(s.store, 3, "project_daily_limit", "10", "project_warn_share", "0.9"); err != nil {
		return err
	}
	if err := s.spent(6); err != nil {
		return err
	}
	if got, err := soft(); err != nil || len(got) != 1 {
		return Violatedf("6 spent under a warning at nine tenths told %+v: %v", got, err)
	}
	if err := s.spent(9.5); err != nil {
		return err
	}
	if got, err := soft(); err != nil || len(got) != 2 || !near(got[1].LimitUSD, 9) {
		return Violatedf("9.5 spent told %+v, want a second soft notice at 9: %v", got, err)
	}
	// A warning of 0 is none, and a limit of 0 is none.
	if err := r.projectPolicy(s.store, 4, "project_daily_limit", "10", "project_warn_share", "0"); err != nil {
		return err
	}
	if err := s.spent(9.9); err != nil {
		return err
	}
	if got, err := soft(); err != nil || len(got) != 2 {
		return Violatedf("a warning of 0 told %+v: %v", got, err)
	}
	return nil
}

func checkEditingTheChannelsMovesDelivery(r *Rig) error {
	s := r.Store()
	c := newChannels()
	notices := r.notices(s, c.all()...)
	hard := func(id string) []ledger.Notice {
		return []ledger.Notice{ledger.NewNotice(r.Scope, ledger.NoticeSpendHardLimit, ledger.NoticeScopeProjectDay, id, 1)}
	}
	for version, step := range []struct {
		choice  map[string][]string
		id      string
		desktop int
		webhook int
	}{
		{map[string][]string{ledger.NoticeSpendHardLimit: {authority.NoticeChannelWebhook}}, "n-1", 0, 1},
		{map[string][]string{ledger.NoticeSpendHardLimit: {authority.NoticeChannelWebhook, authority.NoticeChannelDesktop}}, "n-2", 1, 2},
		{map[string][]string{ledger.NoticeSpendHardLimit: {authority.NoticeChannelDesktop}}, "n-3", 2, 2},
		{nil, "n-4", 2, 2},
	} {
		if err := r.publishNotify(notices, spendChoice(int64(version+2), step.choice)); err != nil {
			return err
		}
		if err := notices.Deliver(r.Ctx, r.Scope, hard(step.id), noticeActor); err != nil {
			return err
		}
		if got := len(c.desktop.Notices()); got != step.desktop {
			return Violatedf("after choice %d the desktop has heard %d, want %d", version, got, step.desktop)
		}
		if got := len(c.webhook.Notices()); got != step.webhook {
			return Violatedf("after choice %d the webhook has heard %d, want %d", version, got, step.webhook)
		}
	}
	if got := len(c.web.Notices()); got != 4 {
		return Violatedf("the web UI heard %d of four notices", got)
	}
	return nil
}

// spendNotices reads every spend notice the ledger holds, in order.
func (r *Rig) spendNotices(store ports.Store, kinds ...string) ([]ledger.SpendNotice, error) {
	var out []ledger.SpendNotice
	err := store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectSpendNotice, ID: ledger.SpendNoticeSubject})
		if err != nil {
			return err
		}
		for _, event := range events {
			notice, err := ledger.ReadSpendNotice(event)
			if err != nil {
				return err
			}
			if len(kinds) == 0 || slices.Contains(kinds, notice.Kind) {
				out = append(out, notice)
			}
		}
		return nil
	})
	return out, err
}

// ofScope keeps the notices of one scope.
func ofScope(notices []ledger.SpendNotice, scope string) []ledger.SpendNotice {
	var out []ledger.SpendNotice
	for _, notice := range notices {
		if notice.Scope == scope {
			out = append(out, notice)
		}
	}
	return out
}

// spent tells the ledger what the lane's generation has spent so far.
func (s *statusRig) spent(usd float64) error {
	reply, err := s.usage(ledger.GenerationUsage{CostUSD: usd, KnownTurns: 4})
	if err == nil && len(reply.Rejected) != 0 {
		return Violatedf("a usage report was refused: %+v", reply.Rejected)
	}
	return err
}

// counted says how many notices a kind and a scope have, after a report.
type counted struct{ soft, hard int }

func (r *Rig) countScope(store ports.Store, scope string) (counted, error) {
	notices, err := r.spendNotices(store, ledger.NoticeSpendSoftLimit, ledger.NoticeSpendHardLimit)
	if err != nil {
		return counted{}, err
	}
	var c counted
	for _, notice := range ofScope(notices, scope) {
		if notice.Kind == ledger.NoticeSpendSoftLimit {
			c.soft++
		} else {
			c.hard++
		}
	}
	return c, nil
}

func checkAProjectNoticeIsToldOncePerLimit(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := r.projectPolicy(s.store, 2, "project_daily_limit", "10"); err != nil {
		return err
	}
	for _, step := range []struct {
		spent float64
		want  counted
		why   string
	}{
		{7, counted{0, 0}, "under the soft limit of 8"},
		{8.5, counted{1, 0}, "past the soft limit"},
		{9, counted{1, 0}, "the next report, still under the hard limit"},
		{9.5, counted{1, 0}, "and the next"},
		{9.5, counted{1, 0}, "the same report again"},
		{10.2, counted{1, 1}, "past the hard limit"},
		{10.5, counted{1, 1}, "the next report past it"},
	} {
		if err := s.spent(step.spent); err != nil {
			return err
		}
		got, err := r.countScope(s.store, ledger.NoticeScopeProjectDay)
		if err != nil {
			return err
		}
		if got != step.want {
			return Violatedf("after %v spent, %s: the day has %+v soft and hard notices, want %+v", step.spent, step.why, got, step.want)
		}
	}
	// No weekly limit is set, so no notice names the week.
	if got, err := r.countScope(s.store, ledger.NoticeScopeProjectWeek); err != nil || got != (counted{}) {
		return Violatedf("the week, with no limit, has %+v: %v", got, err)
	}
	notices, err := r.spendNotices(s.store, ledger.NoticeSpendSoftLimit, ledger.NoticeSpendHardLimit)
	if err != nil {
		return err
	}
	day := ofScope(notices, ledger.NoticeScopeProjectDay)
	now, err := r.Clock(s.store)
	if err != nil {
		return err
	}
	if len(day) != 2 || !near(day[0].LimitUSD, 8) || !near(day[1].LimitUSD, 10) || day[0].ChargedUSD < 8 || day[1].ChargedUSD < 10 ||
		day[0].WindowStart != ledger.WindowStart(ledger.WindowDay, now) || day[0].Subject != "" {
		return Violatedf("the day's notices read %+v, want the soft limit of 8 and the hard limit of 10 for today", day)
	}
	// A raise moves both limits, so passing the raised ones is told again, once.
	if _, err := control.NewSpend(s.store).RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 20}, theMember); err != nil {
		return err
	}
	for _, step := range []struct {
		spent float64
		want  counted
	}{{15, counted{1, 1}}, {17, counted{2, 1}}, {17.5, counted{2, 1}}, {20.5, counted{2, 2}}, {21, counted{2, 2}}} {
		if err := s.spent(step.spent); err != nil {
			return err
		}
		if got, err := r.countScope(s.store, ledger.NoticeScopeProjectDay); err != nil || got != step.want {
			return Violatedf("after a raise to 20 and %v spent the day has %+v, want %+v: %v", step.spent, got, step.want, err)
		}
	}
	return nil
}

func checkAWorkOrderNoticeIsToldOncePerLimit(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	for _, step := range []struct {
		spent float64
		want  counted
	}{{12, counted{0, 0}}, {13, counted{1, 0}}, {13.5, counted{1, 0}}, {26, counted{1, 1}}, {27, counted{1, 1}}} {
		if err := s.spent(step.spent); err != nil {
			return err
		}
		if got, err := r.countScope(s.store, ledger.NoticeScopeWorkOrder); err != nil || got != step.want {
			return Violatedf("after %v spent the work order has %+v, want %+v: %v", step.spent, got, step.want, err)
		}
	}
	notices, err := r.spendNotices(s.store, ledger.NoticeSpendSoftLimit, ledger.NoticeSpendHardLimit)
	if err != nil {
		return err
	}
	order := ofScope(notices, ledger.NoticeScopeWorkOrder)
	if len(order) != 2 || order[0].Subject != s.grant.WorkOrder || !near(order[0].LimitUSD, 12.5) || !near(order[1].LimitUSD, 25) {
		return Violatedf("the work order's notices read %+v", order)
	}
	return nil
}

func checkAPolicyChangeNamesTheKeysAndTheMember(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	if err := r.projectPolicy(s, 2, "hard_multiplier", "3", "project_daily_limit", "10"); err != nil {
		return err
	}
	changed := func() ([]ledger.SpendNotice, error) {
		notices, err := r.spendNotices(s, ledger.NoticeSpendPolicyChanged)
		return ofScope(notices, ledger.NoticeScopeProject), err
	}
	got, err := changed()
	if err != nil {
		return err
	}
	want := []ledger.SpendKeyChange{{Key: "hard_multiplier", Before: "2", After: "3"}, {Key: "project_daily_limit", Before: "0", After: "10"}}
	if len(got) != 1 || !slices.Equal(got[0].Changes, want) || got[0].By != theMember.ID || got[0].PolicyVersion != 2 {
		return Violatedf("the first publish noticed %+v, want %+v by %s", got, want, theMember.ID)
	}
	// The same body again is a retry and says nothing, and a revert is a publish like any other.
	if err := r.projectPolicy(s, 2, "hard_multiplier", "3", "project_daily_limit", "10"); err != nil {
		return err
	}
	if got, err = changed(); err != nil || len(got) != 1 {
		return Violatedf("a retry of the publish left %d notices: %v", len(got), err)
	}
	if err := r.projectPolicy(s, 3); err != nil {
		return err
	}
	if got, err = changed(); err != nil || len(got) != 2 || got[1].Changes[0] != (ledger.SpendKeyChange{Key: "hard_multiplier", Before: "3", After: "2"}) {
		return Violatedf("a revert noticed %+v: %v", got, err)
	}
	// The installation's keys are named the same way.
	next, err := authority.DefaultInstallationSpendPolicy().Set("orchestrator_daily_warn", "40")
	if err != nil {
		return err
	}
	next.Version = 2
	if _, err := spend.PublishInstallationSpendPolicy(r.Ctx, r.Scope, authority.InstallationSpendPolicyDraft{Body: next.Body(), Policy: next}, theMember); err != nil {
		return err
	}
	notices, err := r.spendNotices(s, ledger.NoticeSpendPolicyChanged)
	if err != nil {
		return err
	}
	installation := ofScope(notices, ledger.NoticeScopeInstallation)
	if len(installation) != 1 || len(installation[0].Changes) != 1 || installation[0].Changes[0] != (ledger.SpendKeyChange{Key: "orchestrator_daily_warn", Before: "0", After: "40"}) ||
		installation[0].By != theMember.ID {
		return Violatedf("the installation's publish noticed %+v", installation)
	}
	// A publish that is refused leaves no notice.
	if err := r.projectPolicy(s, 4, "project_daily_limit", "-1"); err == nil {
		return Violatedf("a negative limit was published")
	}
	if all, err := r.spendNotices(s, ledger.NoticeSpendPolicyChanged); err != nil || len(all) != 3 {
		return Violatedf("the ledger holds %d policy notices, want 3: %v", len(all), err)
	}
	return nil
}

func checkARaiseIsNoticed(r *Rig) error {
	s := r.Store()
	spend := control.NewSpend(s)
	id, _, err := r.stopped(s, []ledger.GenerationUsage{{CostUSD: 30, KnownTurns: 3}})
	if err != nil {
		return err
	}
	if _, err := spend.Raise(r.Ctx, r.Scope, control.SpendRaise{WorkOrder: id, ToUSD: 40}, theMember); err != nil {
		return err
	}
	if err := r.projectPolicy(s, 2, "project_daily_limit", "10"); err != nil {
		return err
	}
	if err := r.projectSpent(s, 12); err != nil {
		return err
	}
	if _, err := spend.RaiseProject(r.Ctx, r.Scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 30}, theMember); err != nil {
		return err
	}
	notices, err := r.spendNotices(s, ledger.NoticeSpendPolicyChanged)
	if err != nil {
		return err
	}
	raises := ofScope(notices, ledger.NoticeScopeLimitRaised)
	if len(raises) != 2 {
		return Violatedf("two raises left %d notices: %+v", len(raises), raises)
	}
	if raises[0].Subject != id || raises[0].By != theMember.ID || len(raises[0].Changes) != 1 ||
		raises[0].Changes[0] != (ledger.SpendKeyChange{Key: "hard_limit", Before: "25", After: "40"}) {
		return Violatedf("the work order's raise noticed %+v", raises[0])
	}
	if raises[1].By != theMember.ID || len(raises[1].Changes) != 1 ||
		raises[1].Changes[0] != (ledger.SpendKeyChange{Key: "project_daily_limit", Before: "10", After: "30"}) {
		return Violatedf("the project's raise noticed %+v", raises[1])
	}
	return nil
}

func checkWhatWaitsReachesTheChosenChannels(r *Rig) error {
	s := r.Store()
	c := newChannels()
	notices := r.notices(s, c.all()...)
	spend := control.NewSpend(s)
	err := r.publishNotify(notices, spendChoice(2, map[string][]string{
		ledger.NoticeSpendNoProgress: {authority.NoticeChannelWebhook},
		ledger.NoticeSpendWaiting:    {authority.NoticeChannelDesktop},
	}))
	if err != nil {
		return err
	}
	// No-progress and waiting notices have no producer in this build: a test records them as a later slice's worker will.
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "spend"}
	for _, kind := range []string{ledger.NoticeSpendNoProgress, ledger.NoticeSpendWaiting} {
		if err := spend.Note(r.Ctx, r.Scope, ledger.SpendNotice{Kind: kind, Scope: ledger.NoticeScopeLane, Subject: "lane-1"}, system); err != nil {
			return err
		}
	}
	if err := spend.Note(r.Ctx, r.Scope, ledger.SpendNotice{Kind: ledger.NoticeWork, Scope: ledger.NoticeScopeLane}, system); err == nil {
		return Violatedf("a notice of another kind was recorded as a spend notice")
	}
	waiting, err := notices.SpendWaiting(r.Ctx, r.Scope, "alpha")
	if err != nil {
		return err
	}
	if len(waiting) != 2 {
		return Violatedf("%d spend notices wait, want the two", len(waiting))
	}
	for _, notice := range waiting {
		if notice.Project != "alpha" || notice.Text[:7] != "alpha: " || notice.Delivery == "" {
			return Violatedf("a notice reads %+v, want the project named", notice)
		}
	}
	if err := notices.Deliver(r.Ctx, r.Scope, waiting, noticeActor); err != nil {
		return err
	}
	if got := heard(c.web); !slices.Equal(got, []string{ledger.NoticeSpendNoProgress, ledger.NoticeSpendWaiting}) {
		return Violatedf("the web UI heard %v", got)
	}
	if got := heard(c.webhook); !slices.Equal(got, []string{ledger.NoticeSpendNoProgress}) {
		return Violatedf("the webhook heard %v, want the no-progress notice alone", got)
	}
	if got := heard(c.desktop); !slices.Equal(got, []string{ledger.NoticeSpendWaiting}) {
		return Violatedf("the desktop heard %v, want the waiting notice alone", got)
	}
	// What was heard is not heard again, and what the ledger holds is the same two.
	again, err := notices.SpendWaiting(r.Ctx, r.Scope, "alpha")
	if err != nil {
		return err
	}
	if err := notices.Deliver(r.Ctx, r.Scope, again, noticeActor); err != nil {
		return err
	}
	if got := heard(c.web); len(got) != 2 {
		return Violatedf("a second tick told the web UI again: %v", got)
	}
	// An outbound notice holds nothing a lane or a member wrote: no subject, no lane name.
	for _, notice := range append(c.webhook.Notices(), c.desktop.Notices()...) {
		if len(notice.Text) > 120 || notice.Reason != "" && notice.Reason != ledger.NoticeScopeLane {
			return Violatedf("a notice leaves with %+v", notice)
		}
	}
	return nil
}

// channels are the three a policy can turn on, as notifiers that keep what they hear.
type channels struct{ web, desktop, webhook *fake.Notifier }

func newChannels() channels {
	return channels{web: fake.NewNotifier("web"), desktop: fake.NewNotifier("desktop"), webhook: fake.NewNotifier("webhook")}
}

func (c channels) all() []ports.Notifier { return []ports.Notifier{c.web, c.desktop, c.webhook} }

// heard lists the kinds a notifier heard, in order.
func heard(n *fake.Notifier) []string {
	var kinds []string
	for _, notice := range n.Notices() {
		kinds = append(kinds, notice.Kind)
	}
	return kinds
}

// spendChoice is a notify policy that turns the desktop and a webhook on and chooses, for each spend kind, the channels it
// goes through.
func spendChoice(version int64, choice map[string][]string) authority.NotifyPolicy {
	return authority.NotifyPolicy{Version: version, Desktop: true, WebhookURL: "https://hooks.example.com/ropeshift", SpendChannels: choice}
}

func (r *Rig) publishNotify(notices *control.Notices, policy authority.NotifyPolicy) error {
	_, err := notices.PublishNotifyPolicy(r.Ctx, r.Scope, authority.NotifyPolicyDraft{Body: fmt.Sprintf("%+v", policy), Policy: policy}, theMember)
	return err
}

// oneOfEachKind is a spend notice of every kind, which a tick would hand the notifier.
func oneOfEachKind(r *Rig) []ledger.Notice {
	var out []ledger.Notice
	for i, kind := range ledger.SpendNoticeKinds {
		out = append(out, ledger.NewNotice(r.Scope, kind, "", fmt.Sprintf("n-%d", i), int64(i+1)))
	}
	return out
}

func checkSpendNoticesFollowTheChoice(r *Rig) error {
	s := r.Store()
	c := newChannels()
	notices := r.notices(s, c.all()...)
	err := r.publishNotify(notices, spendChoice(2, map[string][]string{
		ledger.NoticeSpendSoftLimit:     {},
		ledger.NoticeSpendHardLimit:     {authority.NoticeChannelDesktop, authority.NoticeChannelWebhook},
		ledger.NoticeSpendNoProgress:    {authority.NoticeChannelWebhook},
		ledger.NoticeSpendWaiting:       {authority.NoticeChannelDesktop},
		ledger.NoticeSpendPolicyChanged: {authority.NoticeChannelDesktop, authority.NoticeChannelWebhook},
	}))
	if err != nil {
		return err
	}
	if err := notices.Deliver(r.Ctx, r.Scope, oneOfEachKind(r), noticeActor); err != nil {
		return err
	}
	for _, want := range []struct {
		channel *fake.Notifier
		kinds   []string
	}{
		{c.web, ledger.SpendNoticeKinds},
		{c.desktop, []string{ledger.NoticeSpendHardLimit, ledger.NoticeSpendWaiting, ledger.NoticeSpendPolicyChanged}},
		{c.webhook, []string{ledger.NoticeSpendHardLimit, ledger.NoticeSpendNoProgress, ledger.NoticeSpendPolicyChanged}},
	} {
		if got := heard(want.channel); !slices.Equal(got, want.kinds) {
			return Violatedf("%s heard %v, want %v", want.channel.Channel(), got, want.kinds)
		}
	}
	// A quiet tick says nothing again, and a repeated report of a limit is the same notice.
	if err := notices.Deliver(r.Ctx, r.Scope, oneOfEachKind(r), noticeActor); err != nil {
		return err
	}
	if got := heard(c.web); len(got) != len(ledger.SpendNoticeKinds) {
		return Violatedf("the web UI heard %v of the same five twice", got)
	}
	return nil
}

func checkSpendNoticeDefaults(r *Rig) error {
	s := r.Store()
	c := newChannels()
	notices := r.notices(s, c.all()...)
	// Channels turned on and no choice made: every spend kind reaches the web UI alone, and a notice of another kind
	// reaches all three, as before.
	if err := r.publishNotify(notices, spendChoice(2, nil)); err != nil {
		return err
	}
	mixed := append(oneOfEachKind(r), waiting(r, "wo-1")...)
	if err := notices.Deliver(r.Ctx, r.Scope, mixed, noticeActor); err != nil {
		return err
	}
	if got := heard(c.web); len(got) != 6 {
		return Violatedf("the web UI heard %v, want the five spend notices and the one of work", got)
	}
	for _, channel := range []*fake.Notifier{c.desktop, c.webhook} {
		if got := heard(channel); !slices.Equal(got, []string{ledger.NoticeWork}) {
			return Violatedf("%s heard %v, want the notice of work alone", channel.Channel(), got)
		}
	}
	return nil
}

func checkAFailedWebhookLeavesTheNoticeVisible(r *Rig) error {
	s := r.Store()
	c := newChannels()
	c.webhook.Fail(errors.New("the webhook answered 500"))
	notices := r.notices(s, c.all()...)
	choice := map[string][]string{ledger.NoticeSpendHardLimit: {authority.NoticeChannelDesktop, authority.NoticeChannelWebhook}}
	if err := r.publishNotify(notices, spendChoice(2, choice)); err != nil {
		return err
	}
	hard := []ledger.Notice{ledger.NewNotice(r.Scope, ledger.NoticeSpendHardLimit, ledger.NoticeScopeProjectDay, "n-1", 1)}
	if err := notices.Deliver(r.Ctx, r.Scope, hard, noticeActor); err == nil {
		return Violatedf("a webhook that refused reached nobody")
	}
	lines, err := notices.List(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	byChannel := map[string]ledger.NoticeLine{}
	for _, line := range lines {
		byChannel[line.Channel] = line
	}
	if len(lines) != 3 || !byChannel["web"].Delivered || !byChannel["desktop"].Delivered || byChannel["webhook"].Delivered || byChannel["webhook"].Failure == "" {
		return Violatedf("the notice stands %+v, want it delivered to the web UI and the desktop and refused by the webhook with its reason", lines)
	}
	if byChannel["web"].Kind != ledger.NoticeSpendHardLimit || byChannel["web"].Reason != ledger.NoticeScopeProjectDay {
		return Violatedf("the web UI reads the notice as %+v", byChannel["web"])
	}
	// A new version that takes the webhook out of the choice moves the next delivery at once: the failed channel is no
	// longer one the notice goes to, and it is asked no more.
	if err := r.publishNotify(notices, spendChoice(3, map[string][]string{ledger.NoticeSpendHardLimit: {authority.NoticeChannelDesktop}})); err != nil {
		return err
	}
	r.DB.ShiftClock(noticeBackoffMs + 1)
	again := []ledger.Notice{ledger.NewNotice(r.Scope, ledger.NoticeSpendHardLimit, ledger.NoticeScopeProjectDay, "n-2", 2)}
	if err := notices.Deliver(r.Ctx, r.Scope, again, noticeActor); err != nil {
		return err
	}
	if got := heard(c.desktop); len(got) != 2 {
		return Violatedf("the desktop heard %v, want both notices", got)
	}
	return nil
}

package server_test

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// limitedProject gives the project a daily limit of $10 and a weekly limit of $50 by a member's publish, and has its lanes
// spend $12 of today.
func limitedProject(t *testing.T, d *talkDaemon) {
	t.Helper()
	policy, err := authority.DefaultSpendPolicy().Set("project_daily_limit", "10")
	if err != nil {
		t.Fatal(err)
	}
	if policy, err = policy.Set("project_weekly_limit", "50"); err != nil {
		t.Fatal(err)
	}
	policy.Version = 2
	if _, err := d.client.PublishPolicy(t.Context(), policy.Body()); err != nil {
		t.Fatal(err)
	}
	err = d.Store.Write(t.Context(), apitest.Scope, "spend.fixture", func(tx ports.WriteTx) error {
		if _, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: talkMember, Subject: ledger.Subject{Kind: "note", ID: "project-spend"}, Data: []byte(`{}`)}); err != nil {
			return err
		}
		return tx.Lanes().AddSpend(ledger.SpendScopeWork, 12)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func projectSpendOf(t *testing.T, d *talkDaemon) ledger.ProjectSpend {
	t.Helper()
	view, err := d.client.Spend(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return view.ProjectSpend
}

// The spend a member reads says where the project stands in the day and the week, against its limits (FR-SPEND-6).
func TestTheSpendViewReadsTheProjectsDayAndWeek(t *testing.T) {
	d := startTalk(t)
	if spend := projectSpendOf(t, d); spend.Day.HardLimit != 0 || spend.Week.HardLimit != 0 || spend.Day.SpentUSD != 0 {
		t.Errorf("a project with no limit reads %+v", spend)
	}
	limitedProject(t, d)
	spend := projectSpendOf(t, d)
	if math.Abs(spend.Day.HardLimit-10) > 1e-9 || math.Abs(spend.Day.SoftLimit-8) > 1e-9 || math.Abs(spend.Day.SpentUSD-12) > 1e-9 ||
		math.Abs(spend.Week.HardLimit-50) > 1e-9 || math.Abs(spend.Week.SpentUSD-12) > 1e-9 || !spend.Day.Past() || spend.Week.Past() {
		t.Errorf("the project reads %+v", spend)
	}
	if spend.Day.EndsAt-spend.Day.StartsAt != 24*60*60*1000 || spend.Week.EndsAt-spend.Week.StartsAt != 7*24*60*60*1000 {
		t.Errorf("the windows read %+v and %+v", spend.Day, spend.Week)
	}
}

// A member raises the project's daily or weekly limit, by a share or to an amount, and the answer says the limit before
// and after (FR-SPEND-6, FR-SPEND-8). A raise that names neither or both, that does not lift the limit above what the
// window was charged, or of a limit nobody set, is refused with its reason.
func TestAMemberRaisesAProjectsLimitForItsWindow(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	_, err := d.client.RaiseProjectSpend(ctx, api.RaiseProjectSpend{Window: ledger.WindowDay, ToUSD: 50})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	limitedProject(t, d)
	change, err := d.client.RaiseProjectSpend(ctx, api.RaiseProjectSpend{Window: ledger.WindowDay, ByPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if change.Window != ledger.WindowDay || math.Abs(change.BeforeUSD-10) > 1e-9 || math.Abs(change.AfterUSD-20) > 1e-9 || math.Abs(change.SpentUSD-12) > 1e-9 || change.ByPercent != 100 {
		t.Errorf("a raise of the day's 10 by half again recorded %+v", change)
	}
	if spend := projectSpendOf(t, d); math.Abs(spend.Day.HardLimit-20) > 1e-9 || math.Abs(spend.Day.RaisedTo-20) > 1e-9 || spend.Day.Past() {
		t.Errorf("after the raise the day reads %+v", spend.Day)
	}
	if change, err = d.client.RaiseProjectSpend(ctx, api.RaiseProjectSpend{Window: ledger.WindowWeek, ToUSD: 90}); err != nil || change.Window != ledger.WindowWeek || math.Abs(change.BeforeUSD-50) > 1e-9 {
		t.Errorf("a raise of the week to 90 recorded %+v: %v", change, err)
	}
	for name, in := range map[string]api.RaiseProjectSpend{
		"neither":              {Window: ledger.WindowDay},
		"both":                 {Window: ledger.WindowDay, ByPercent: 10, ToUSD: 200},
		"a limit not higher":   {Window: ledger.WindowDay, ToUSD: 20},
		"a limit lower":        {Window: ledger.WindowDay, ToUSD: 5},
		"a negative share":     {Window: ledger.WindowDay, ByPercent: -5},
		"a limit past the cap": {Window: ledger.WindowDay, ToUSD: 2_000_000},
		"no window":            {ToUSD: 300},
		"a month":              {Window: "month", ToUSD: 300},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.client.RaiseProjectSpend(ctx, in)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
	if spend := projectSpendOf(t, d); math.Abs(spend.Day.HardLimit-20) > 1e-9 {
		t.Errorf("a refused raise moved the limit to %v", spend.Day.HardLimit)
	}
}

// A delegate's raise of a project's limit is a confirmation card showing the limit before and after, nothing changes until
// a member confirms it, and no delegate policy can exempt it (ADR-0024, FR-SPEND-8).
func TestADelegatesRaiseOfAProjectLimitIsACardNoDelegatePolicyExempts(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	limitedProject(t, d)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	_, err := delegate.RaiseProjectSpend(ctx, api.RaiseProjectSpend{Window: ledger.WindowDay, ByPercent: 100})
	card := preparedCard(t, err)
	detail := cardDetail(t, card)
	if detail.Operation != authority.OpSpendRaiseProject || !strings.Contains(string(detail.Changes), "daily_limit_usd") ||
		!strings.Contains(string(detail.Changes), "10") || !strings.Contains(string(detail.Changes), "20") {
		t.Errorf("the raise's card is %+v: %s", detail, detail.Changes)
	}
	if spend := projectSpendOf(t, d); math.Abs(spend.Day.HardLimit-10) > 1e-9 || spend.Day.RaisedTo != 0 {
		t.Errorf("a delegate's call moved the limit: %+v", spend.Day)
	}
	// The card is for the day it was made in, and its arguments say which, so a confirmation that comes in a later day does
	// not lift that day's limit with the amount a member read in this one.
	var arguments api.RaiseProjectSpend
	if err := json.Unmarshal(detail.Arguments, &arguments); err != nil || arguments.Window != ledger.WindowDay || arguments.ToUSD != 20 ||
		arguments.WindowStart != projectSpendOf(t, d).Day.StartsAt {
		t.Errorf("the card's arguments are %s, want the day, the amount, and the day's start %d: %v", detail.Arguments, projectSpendOf(t, d).Day.StartsAt, err)
	}
	exempt := "[delegate_policy]\nversion = 2\nexempt = [\"" + authority.OpSpendRaiseProject + "\"]\n"
	if _, err := d.client.PublishPolicy(ctx, exempt); err == nil {
		t.Error("a delegate policy that exempts the project raise was published")
	}
	if _, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true}); err != nil {
		t.Fatalf("the member could not confirm the raise: %v", err)
	}
	if spend := projectSpendOf(t, d); math.Abs(spend.Day.HardLimit-20) > 1e-9 {
		t.Errorf("after the card the day reads %+v", spend.Day)
	}
}

// A delegate does not choose the window its card binds to: whatever start it names, the card holds the window the call was
// made in, and shows it, so a member reads which day or week the amount lifts and a later confirmation of it is refused.
func TestADelegateCannotChooseTheWindowOfItsProjectRaise(t *testing.T) {
	d := startTalk(t)
	limitedProject(t, d)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	today := projectSpendOf(t, d)
	day := int64(24 * 60 * 60 * 1000)
	for name, in := range map[string]api.RaiseProjectSpend{
		"no start":                      {Window: ledger.WindowDay, ToUSD: 20},
		"a start next week":             {Window: ledger.WindowDay, ToUSD: 20, WindowStart: today.Day.StartsAt + 7*day},
		"a start yesterday":             {Window: ledger.WindowDay, ByPercent: 100, WindowStart: today.Day.StartsAt - day},
		"a start of the week for a day": {Window: ledger.WindowDay, ToUSD: 20, WindowStart: today.Week.StartsAt - 7*day},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := delegate.RaiseProjectSpend(t.Context(), in)
			detail := cardDetail(t, preparedCard(t, err))
			var arguments api.RaiseProjectSpend
			if err := json.Unmarshal(detail.Arguments, &arguments); err != nil || arguments.WindowStart != today.Day.StartsAt {
				t.Errorf("the card binds to the window starting %d, want the current day's, %d: %s", arguments.WindowStart, today.Day.StartsAt, detail.Arguments)
			}
			want := "day starting " + time.UnixMilli(today.Day.StartsAt).UTC().Format("2006-01-02 15:04 MST")
			if !strings.Contains(string(detail.Changes), want) {
				t.Errorf("the card shows %s, and it should say %q", detail.Changes, want)
			}
		})
	}
}

// A raise names the window it was made for, and one asked for a window that is over is refused with that reason and lifts
// nothing: a card confirmed the day after it was made would otherwise lift a day nobody read. A raise that names the window
// it is made in is taken.
func TestARaiseForAWindowThatIsOverIsRefused(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	limitedProject(t, d)
	today := projectSpendOf(t, d).Day.StartsAt
	for name, in := range map[string]api.RaiseProjectSpend{
		"the day before":       {Window: ledger.WindowDay, ToUSD: 20, WindowStart: today - 24*60*60*1000},
		"the day after":        {Window: ledger.WindowDay, ToUSD: 20, WindowStart: today + 24*60*60*1000},
		"a start of no window": {Window: ledger.WindowWeek, ToUSD: 90, WindowStart: today + 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.client.RaiseProjectSpend(ctx, in)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
			if !strings.Contains(err.Error(), "is over") {
				t.Errorf("the refusal reads %q, and it should say the window is over", err)
			}
		})
	}
	if spend := projectSpendOf(t, d); math.Abs(spend.Day.HardLimit-10) > 1e-9 || spend.Day.RaisedTo != 0 {
		t.Errorf("a refused raise moved the limit: %+v", spend.Day)
	}
	change, err := d.client.RaiseProjectSpend(ctx, api.RaiseProjectSpend{Window: ledger.WindowDay, ToUSD: 20, WindowStart: today})
	if err != nil || change.WindowStart != today || math.Abs(change.AfterUSD-20) > 1e-9 {
		t.Errorf("a raise that names today recorded %+v: %v", change, err)
	}
}

// No agent can raise a project's limit: a caller that holds no member's session is refused.
func TestNoAgentCanRaiseAProjectLimit(t *testing.T) {
	d := startTalk(t)
	limitedProject(t, d)
	credential, _ := joinWorker(t, d.Daemon)
	for name, setup := range map[string]func(*api.ProjectClient) *api.ProjectClient{
		"a caller with nothing":               func(c *api.ProjectClient) *api.ProjectClient { return c },
		"the worker's credential":             func(c *api.ProjectClient) *api.ProjectClient { return c.WithCredential(credential) },
		"a lane's token where a session goes": withSession("a-lane-token"),
	} {
		plain := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
		t.Cleanup(plain.Close)
		caller := setup(plain)
		t.Run(name, func(t *testing.T) {
			_, err := caller.RaiseProjectSpend(t.Context(), api.RaiseProjectSpend{Window: ledger.WindowDay, ToUSD: 500})
			refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
		})
	}
	if spend := projectSpendOf(t, d); math.Abs(spend.Day.HardLimit-10) > 1e-9 {
		t.Errorf("a refused call moved the limit to %+v", spend.Day)
	}
}

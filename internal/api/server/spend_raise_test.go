package server_test

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// stoppedAtItsLimit makes a work order whose lane reported $30 against the size's hard limit of $25.
func stoppedAtItsLimit(t *testing.T, d *talkDaemon, id string) {
	t.Helper()
	claimWork(t, d, id, "claim-"+id)
	err := d.Store.Write(t.Context(), apitest.Scope, "spend.fixture", func(tx ports.WriteTx) error {
		if err := tx.Lanes().Start("lane-"+id, id, 1, ledger.LanePolicies{}); err != nil {
			return err
		}
		if err := tx.Lanes().StartGeneration(ledger.Generation{Lane: "lane-" + id, Number: 1, StartReason: ledger.StartInitial}); err != nil {
			return err
		}
		if _, err := tx.Lanes().ReportUsage("lane-"+id, 1, ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: talkMember, Subject: ledger.Subject{Kind: "note", ID: id}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hardLimitOf(t *testing.T, d *talkDaemon, id string) ledger.WorkOrderSpend {
	t.Helper()
	spend, err := d.client.WorkOrderSpend(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return spend
}

// A member raises a work order's hard limit by a share or to an amount, and the answer says the limit before and after
// (FR-SPEND-7, FR-SPEND-8). A raise that names neither or both, or that does not raise the limit, is refused with its
// reason, and a work order nobody made is not found.
func TestAMemberRaisesAWorkOrdersHardLimit(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	stoppedAtItsLimit(t, d, "wo-raise")
	change, err := d.client.RaiseSpend(ctx, "wo-raise", api.RaiseSpend{ByPercent: 50})
	if err != nil {
		t.Fatal(err)
	}
	if change.Act != ledger.SpendActRaise || math.Abs(change.BeforeUSD-25) > 1e-9 || math.Abs(change.AfterUSD-37.5) > 1e-9 || math.Abs(change.ChargedUSD-30) > 1e-9 || change.ByPercent != 50 {
		t.Errorf("a raise of 25 by half recorded %+v", change)
	}
	if spend := hardLimitOf(t, d, "wo-raise"); math.Abs(spend.HardLimit-37.5) > 1e-9 || math.Abs(spend.RaisedTo-37.5) > 1e-9 || slices.Contains(spend.Conditions, ledger.ConditionSpendLimit) {
		t.Errorf("after the raise the work order reads %+v", spend)
	}
	if change, err = d.client.RaiseSpend(ctx, "wo-raise", api.RaiseSpend{ToUSD: 80}); err != nil || math.Abs(change.BeforeUSD-37.5) > 1e-9 || math.Abs(change.AfterUSD-80) > 1e-9 {
		t.Errorf("a raise to 80 recorded %+v: %v", change, err)
	}
	for name, in := range map[string]api.RaiseSpend{
		"neither":              {},
		"both":                 {ByPercent: 10, ToUSD: 200},
		"a limit not higher":   {ToUSD: 80},
		"a limit lower":        {ToUSD: 5},
		"a negative share":     {ByPercent: -5},
		"a limit past the cap": {ToUSD: 2_000_000},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.client.RaiseSpend(ctx, "wo-raise", in)
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
	_, err = d.client.RaiseSpend(ctx, "no-such-work-order", api.RaiseSpend{ToUSD: 90})
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	if spend := hardLimitOf(t, d, "wo-raise"); math.Abs(spend.HardLimit-80) > 1e-9 {
		t.Errorf("a refused raise moved the limit to %v", spend.HardLimit)
	}
}

// A resume takes work a hard limit parked, and a work order that is running is not that.
func TestAResumeTakesOnlyWorkAHardLimitParked(t *testing.T) {
	d := startTalk(t)
	stoppedAtItsLimit(t, d, "wo-resume")
	_, err := d.client.ResumeSpend(t.Context(), "wo-resume")
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	_, err = d.client.ResumeSpend(t.Context(), "no-such-work-order")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

// A delegate's raise or resume is a confirmation card showing the limit before and after, nothing changes until a member
// confirms it, and no delegate policy can exempt either (ADR-0024, FR-SPEND-8).
func TestADelegatesRaiseOrResumeIsACardNoDelegatePolicyExempts(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	stoppedAtItsLimit(t, d, "wo-card")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	_, err := delegate.RaiseSpend(ctx, "wo-card", api.RaiseSpend{ByPercent: 100})
	raise := preparedCard(t, err)
	detail := cardDetail(t, raise)
	if detail.Operation != authority.OpSpendRaise || !strings.Contains(string(detail.Changes), "hard_limit_usd") ||
		!strings.Contains(string(detail.Changes), "25") || !strings.Contains(string(detail.Changes), "50") {
		t.Errorf("the raise's card is %+v: %s", detail, detail.Changes)
	}
	_, err = delegate.ResumeSpend(ctx, "wo-card")
	resume := preparedCard(t, err)
	if detail = cardDetail(t, resume); detail.Operation != authority.OpSpendResume {
		t.Errorf("the resume's card is %+v", detail)
	}
	if spend := hardLimitOf(t, d, "wo-card"); math.Abs(spend.HardLimit-25) > 1e-9 || spend.RaisedTo != 0 {
		t.Errorf("a delegate's call moved the limit: %+v", spend)
	}
	for _, op := range []string{authority.OpSpendRaise, authority.OpSpendResume} {
		exempt := "[delegate_policy]\nversion = 2\nexempt = [\"" + op + "\"]\n"
		if _, err := d.client.PublishPolicy(ctx, exempt); err == nil {
			t.Errorf("a delegate policy that exempts %s was published", op)
		}
	}
	if _, err := d.Client.DecideApproval(ctx, raise.ID, api.DecideApproval{Digest: raise.SubjectDigest, Allow: true}); err != nil {
		t.Fatalf("the member could not confirm the raise: %v", err)
	}
	if spend := hardLimitOf(t, d, "wo-card"); math.Abs(spend.HardLimit-50) > 1e-9 {
		t.Errorf("after the card the limit reads %+v", spend)
	}
}

// No agent can raise a limit: a caller that holds no member's session, as a lane or a worker does not, is refused.
func TestNoAgentCanRaiseASpendLimit(t *testing.T) {
	d := startTalk(t)
	stoppedAtItsLimit(t, d, "wo-agent")
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
			_, err := caller.RaiseSpend(t.Context(), "wo-agent", api.RaiseSpend{ToUSD: 500})
			refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
			_, err = caller.ResumeSpend(t.Context(), "wo-agent")
			refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
		})
	}
	if spend := hardLimitOf(t, d, "wo-agent"); math.Abs(spend.HardLimit-25) > 1e-9 {
		t.Errorf("a refused call moved the limit to %+v", spend)
	}
}

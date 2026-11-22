package server_test

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// parkedAtItsLimit parks a work order's lane on the wake a hard spend limit leaves, with $30 charged against the $25 limit:
// the lane waits on the limit rising or on a person.
func parkedAtItsLimit(t *testing.T, d *talkDaemon, id string) {
	t.Helper()
	ctx := t.Context()
	if _, err := d.client.CreateWorkOrder(ctx, d.workIn(id, "Add the retry budget")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Review(ctx, id); err != nil {
		t.Fatal(err)
	}
	parkTheLane(t, d, id)
}

// parkTheLane claims a ready work order, starts its lane, and parks it as parkedAtItsLimit describes.
func parkTheLane(t *testing.T, d *talkDaemon, id string) {
	t.Helper()
	parkTheLaneOn(t, d, id, lanes.SpendLimitWake(id))
}

// parkTheLaneOn parks the lane of a ready work order on a wake expression.
func parkTheLaneOn(t *testing.T, d *talkDaemon, id, expr string) {
	t.Helper()
	ctx := t.Context()
	worker := ledger.Actor{Kind: ledger.ActorWorker, ID: d.auth.WorkerID}
	grant, err := control.NewLeases(d.Store).Claim(ctx, apitest.Scope, workorders.ClaimRequest{
		WorkOrder: id, ClaimID: "claim-" + id, Worker: d.auth.WorkerID, TTLMs: apitest.LeaseTTL,
	}, talkMember)
	if err != nil {
		t.Fatal(err)
	}
	lane := "lane-" + id
	generation, err := control.NewLeases(d.Store).StartLane(ctx, apitest.Scope, grant.Lease, lane, worker)
	if err != nil {
		t.Fatal(err)
	}
	err = d.Store.Write(ctx, apitest.Scope, "spend.fixture", func(tx ports.WriteTx) error {
		if _, err := tx.Lanes().ReportUsage(lane, generation, ledger.GenerationUsage{CostUSD: 30, KnownTurns: 3}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: talkMember, Subject: ledger.Subject{Kind: "note", ID: id}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	holder := workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}
	wakes := control.NewWakes(d.Store)
	wake := "wake-" + id
	if err := wakes.BeginParking(ctx, apitest.Scope, holder, parking.Draft{ID: wake, Expr: expr}, worker); err != nil {
		t.Fatal(err)
	}
	if fired, err := wakes.CommitParked(ctx, apitest.Scope, holder, ledger.ParkedCommit{Wake: wake, Checkpoint: ledger.Checkpoint{Pushed: true}}, worker); err != nil || fired != "" {
		t.Fatalf("the lane parked with %q fired: %v", fired, err)
	}
	if order, err := d.client.WorkOrder(ctx, id); err != nil || order.State != workorders.Parked {
		t.Fatalf("the work order reads %s: %v", order.State, err)
	}
}

func messageOf(t *testing.T, err error, status int, code string) string {
	t.Helper()
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != status || refused.Detail.Code != code {
		t.Fatalf("got %v, want %d %s", err, status, code)
	}
	return refused.Detail.Message
}

// A member's own work retry, lane reset, and lane respawn are held to the limit that stopped the work, as an
// orchestrator's are: each is a 409 with the code `spend_limit`, and its message names the work order, the limit, what is
// charged, and the raise that is the way out. Nothing moves (FR-SPEND-8).
func TestAMembersActsAtTheHardLimitAreRefusedWithTheWayOut(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	stoppedAtItsLimit(t, d, "wo-member")
	acts := map[string]func() error{
		"work retry": func() error {
			_, err := d.client.Retry(ctx, "wo-member", api.Retry{Reason: "try once more"})
			return err
		},
		"lane reset":   func() error { _, err := d.client.ResetLane(ctx, "wo-member"); return err },
		"lane respawn": func() error { _, err := d.client.RespawnLane(ctx, "wo-member"); return err },
	}
	for name, act := range acts {
		t.Run(name, func(t *testing.T) {
			message := messageOf(t, act(), http.StatusConflict, api.CodeSpendLimit)
			for _, want := range []string{"wo-member", "hard spend limit of $25.00", "$30.00 charged", "raise it with ropeshift spend raise"} {
				if !strings.Contains(message, want) {
					t.Errorf("a member's %s at the limit was refused with %q, which leaves out %q", name, message, want)
				}
			}
		})
	}
	if spend := hardLimitOf(t, d, "wo-member"); math.Abs(spend.HardLimit-25) > 1e-9 {
		t.Errorf("a refused act moved the limit to %v", spend.HardLimit)
	}
	if _, err := d.client.RaiseSpend(ctx, "wo-member", api.RaiseSpend{ToUSD: 100}); err != nil {
		t.Fatal(err)
	}
	for name, act := range acts {
		var refused *api.Error
		if err := act(); errors.As(err, &refused) && refused.Detail.Code == api.CodeSpendLimit {
			t.Errorf("a member's %s was still refused for the limit once it was raised: %v", name, err)
		}
	}
}

// A delegate holds the action that resumes a lane, and the wake of a lane a limit parked waits on the limit rising or on a
// person, so without the check a delegate could end a spend stop with no card and no member. It is refused, and a member's
// resume of the same lane goes through (FR-SPEND-8).
func TestADelegatesLaneResumeCannotEndASpendStop(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	parkedAtItsLimit(t, d, "wo-parked")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	_, err := delegate.ResumeLane(ctx, "wo-parked")
	message := messageOf(t, err, http.StatusForbidden, api.CodeForbidden)
	if !strings.Contains(message, "hard spend limit") || !strings.Contains(message, "ropeshift spend raise") {
		t.Errorf("a delegate's resume was refused with %q, which does not say why or what a member does", message)
	}
	if order, err := d.client.WorkOrder(ctx, "wo-parked"); err != nil || order.State != workorders.Parked {
		t.Fatalf("a refused resume left the work order %s: %v", order.State, err)
	}
	resumed, err := d.client.ResumeLane(ctx, "wo-parked")
	if err != nil || len(resumed.Fired) != 1 {
		t.Fatalf("a member's resume fired %v: %v", resumed.Fired, err)
	}
	if order, err := d.client.WorkOrder(ctx, "wo-parked"); err != nil || order.State != workorders.Ready {
		t.Errorf("a member's resume left the work order %s: %v", order.State, err)
	}
}

// A raise that leaves the limit at or below what the work order is charged would wake the lane only to stop it again, so
// it is refused with the figure it fell short of, and the limit stays where it was.
func TestARaiseBelowChargedSpendIsRefusedWithTheFigure(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	stoppedAtItsLimit(t, d, "wo-short")
	for name, in := range map[string]api.RaiseSpend{"an amount": {ToUSD: 29}, "the charged amount": {ToUSD: 30}, "a share": {ByPercent: 10}} {
		t.Run(name, func(t *testing.T) {
			_, err := d.client.RaiseSpend(ctx, "wo-short", in)
			message := messageOf(t, err, http.StatusBadRequest, api.CodeInvalid)
			if !strings.Contains(message, "would stay below charged spend of $30.00") || !strings.Contains(message, "wo-short") {
				t.Errorf("a raise short of the charged spend was refused with %q", message)
			}
		})
	}
	if spend := hardLimitOf(t, d, "wo-short"); math.Abs(spend.HardLimit-25) > 1e-9 {
		t.Errorf("a refused raise moved the limit to %v", spend.HardLimit)
	}
	if change, err := d.client.RaiseSpend(ctx, "wo-short", api.RaiseSpend{ToUSD: 31}); err != nil || math.Abs(change.AfterUSD-31) > 1e-9 {
		t.Errorf("a raise above the charged spend recorded %+v: %v", change, err)
	}
}

// A delegate's raise by a share is a card for the amount it comes to when it is asked, and a member who confirms it later
// lifts the limit to that amount and no further: the share is not read again against whatever limit is in force by then
// (F7).
func TestADelegatesRaiseByAShareIsACardForTheAmountItCameTo(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	stoppedAtItsLimit(t, d, "wo-share")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	_, err := delegate.RaiseSpend(ctx, "wo-share", api.RaiseSpend{ByPercent: 50})
	card := preparedCard(t, err)
	detail := cardDetail(t, card)
	if got := string(detail.Arguments); got != `{"to_usd":37.5}` {
		t.Errorf("the card holds %s, want the amount 50 percent over 25 comes to", got)
	}
	if !strings.Contains(string(detail.Changes), "37.5") {
		t.Errorf("the card shows %s", detail.Changes)
	}
	// The limit moves before the card is confirmed.
	if _, err := d.client.RaiseSpend(ctx, "wo-share", api.RaiseSpend{ToUSD: 40}); err != nil {
		t.Fatal(err)
	}
	_, _ = d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	if spend := hardLimitOf(t, d, "wo-share"); spend.HardLimit > 40+1e-9 {
		t.Errorf("a card for 37.5 lifted a limit of 40 to %v", spend.HardLimit)
	}

	// A card made when the limit was 40 asks for the amount over 40, and a confirmation that follows is that amount.
	_, err = delegate.RaiseSpend(ctx, "wo-share", api.RaiseSpend{ByPercent: 25})
	next := preparedCard(t, err)
	if got := string(cardDetail(t, next).Arguments); got != `{"to_usd":50}` {
		t.Errorf("the second card holds %s, want 25 percent over 40", got)
	}
	if _, err := d.Client.DecideApproval(ctx, next.ID, api.DecideApproval{Digest: next.SubjectDigest, Allow: true}); err != nil {
		t.Fatalf("the member could not confirm the raise: %v", err)
	}
	if spend := hardLimitOf(t, d, "wo-share"); math.Abs(spend.HardLimit-50) > 1e-9 {
		t.Errorf("after the card the limit reads %v", spend.HardLimit)
	}
}

// The delegate's other call on a raise, one that names an amount, is left as it was sent.
func TestADelegatesRaiseToAnAmountIsACardForThatAmount(t *testing.T) {
	d := startTalk(t)
	stoppedAtItsLimit(t, d, "wo-amount")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	_, err := delegate.RaiseSpend(t.Context(), "wo-amount", api.RaiseSpend{ToUSD: 80})
	if got := string(cardDetail(t, preparedCard(t, err)).Arguments); got != `{"to_usd":80}` {
		t.Errorf("the card holds %s", got)
	}
	var _ = authority.OpSpendRaise
}

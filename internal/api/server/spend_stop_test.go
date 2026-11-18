package server_test

import (
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// spendItems are the spend stops Needs you holds.
func spendItems(t *testing.T, d *talkDaemon) []api.NeedItem {
	t.Helper()
	var out []api.NeedItem
	for _, item := range readView[api.NeedsYou](t, d.Daemon, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedSpend {
			out = append(out, item)
		}
	}
	return out
}

// itemsAbout are every item of Needs you that names a work order, of any kind.
func itemsAbout(t *testing.T, d *talkDaemon, id string) []string {
	t.Helper()
	var out []string
	for _, item := range readView[api.NeedsYou](t, d.Daemon, api.ViewNeedsYou, "").Items {
		if strings.Contains(item.ID, id) {
			out = append(out, item.Kind+":"+item.ID)
		}
	}
	return out
}

// leftByTheLane keeps a digest and a handoff with a narrative for the first generation of a parked lane, as its worker
// uploads them.
func leftByTheLane(t *testing.T, d *talkDaemon, lane, workOrder string) {
	t.Helper()
	body, err := json.Marshal(lanes.Digest{Source: lanes.DigestStream, Turns: 9, ToolCalls: map[string]int64{"Edit": 4, "Bash": 2}, FilesWritten: []string{"retry.go"}, StartedAt: 1, EndedAt: 9, Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	handoff, hash, err := (lanes.Handoff{
		Schema: lanes.HandoffSchema, Lane: lane, Generation: 1, Reason: "spend_limit", Deterministic: lanes.HandoffFacts{WorkOrder: workOrder},
		Narrative: &lanes.Narrative{Done: []string{"the budget type"}, InProgress: []string{"the retry loop"}, NextSteps: []string{"wire the loop to the budget"}, Risks: []string{"a flaky test"}, CarriesNextStep: true},
	}).Seal()
	if err != nil {
		t.Fatal(err)
	}
	err = d.Store.Write(t.Context(), apitest.Scope, "spend.stop.fixture", func(tx ports.WriteTx) error {
		if err := tx.Lanes().SaveDigest(ledger.GenerationDigest{OwnerKind: ledger.DigestOwnerLane, OwnerID: lane, Generation: 1, Body: body}); err != nil {
			return err
		}
		artifact := ledger.Artifact{ID: "artifact-" + lane, Lane: lane, Kind: ledger.ArtifactHandoff, URI: ledger.HandoffURI(lane, 1), Digest: hash}
		if err := tx.Lanes().AddArtifact(artifact); err != nil {
			return err
		}
		if err := tx.Lanes().SaveHandoff(ledger.HandoffRecord{ArtifactID: artifact.ID, Lane: lane, Generation: 1, Digest: hash, Body: handoff}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: talkMember, Subject: ledger.Subject{Kind: "note", ID: lane}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A work order a hard spend limit parked is one item in Needs you: what it is charged against its limits, what Resume
// once would grant, what its lane left, and the four things a member can do. It replaces the plain "resume" item a
// parked lane would otherwise raise, so the stop is asked once (FR-SPEND-7).
func TestASpendStopIsOneItemInNeedsYou(t *testing.T) {
	d := startTalk(t)
	parkedAtItsLimit(t, d, "wo-stop")
	leftByTheLane(t, d, "lane-wo-stop", "wo-stop")
	items := spendItems(t, d)
	if len(items) != 1 {
		t.Fatalf("a work order parked at its hard limit raises %d spend items, want one", len(items))
	}
	item, need := items[0], items[0].Spend
	if item.ID != "wo-stop:spend_limit" || need == nil || need.WorkOrder.ID != "wo-stop" {
		t.Fatalf("the item is %+v", item)
	}
	if math.Abs(need.Spend.HardLimit-25) > 1e-9 || math.Abs(need.Spend.SoftLimit-12.5) > 1e-9 || math.Abs(need.Spend.Charged.ChargedUSD-30) > 1e-9 || need.Spend.Remaining != 0 {
		t.Errorf("the item reads %+v", need.Spend)
	}
	if need.Resume == nil || need.Resume.Basis != ledger.AllowanceMeanGeneration || math.Abs(need.Resume.AllowanceUSD-30) > 1e-9 || math.Abs(need.Resume.LimitUSD-60) > 1e-9 {
		t.Errorf("Resume once reads %+v, want the mean generation of 30 lifting the limit to 60", need.Resume)
	}
	if want := []string{api.ActSpendRaise, api.ActSpendResume, api.ActSpendBlock, api.ActSpendReplan}; !slices.Equal(need.Actions, want) {
		t.Errorf("the item offers %v, want %v", need.Actions, want)
	}
	if len(need.Lanes) != 1 || need.Lanes[0].Lane != "lane-wo-stop" || need.Lanes[0].Digest == nil || need.Lanes[0].Digest.Turns != 9 ||
		need.Lanes[0].Handoff == nil || need.Lanes[0].Handoff.Narrative == nil || need.Lanes[0].Handoff.Narrative.NextSteps[0] != "wire the loop to the budget" {
		t.Errorf("the item's lanes read %+v", need.Lanes)
	}
	if need.WaitingShare != nil {
		t.Errorf("the item reads a waiting share of %v before any model call was attributed", *need.WaitingShare)
	}
	// The stop is the only thing Needs you says of the work order: no long-parked or resume item stands beside it.
	if about := itemsAbout(t, d, "wo-stop"); len(about) != 1 {
		t.Errorf("Needs you holds %v about the work order, want the one spend item", about)
	}
	// A second lane that served the work order is read in the same item.
	err := d.Store.Write(t.Context(), apitest.Scope, "spend.fixture", func(tx ports.WriteTx) error {
		if err := tx.Lanes().Start("lane-earlier", "wo-stop", 1, ledger.LanePolicies{}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: talkMember, Subject: ledger.Subject{Kind: "note", ID: "second"}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if items = spendItems(t, d); len(items) != 1 || len(items[0].Spend.Lanes) != 2 {
		t.Errorf("two lanes of one work order read as %d items with %d lanes, want one item with two", len(items), len(items[0].Spend.Lanes))
	}
}

// A stop that has waited past the long-parked time is still one item in Needs you, and the Stuck view reads it as long
// parked with no plain resume: a resume that raised no limit would wake a lane to stop it again.
func TestALongSpendStopOffersNoPlainResume(t *testing.T) {
	clock := apitest.NewClock()
	d := startTalkWith(t, apitest.Options{Clock: clock.Dialect})
	parkedAtItsLimit(t, d, "wo-long")
	clock.Advance(25 * time.Hour)
	if about := itemsAbout(t, d, "wo-long"); !slices.Equal(about, []string{"spend:wo-long:spend_limit"}) {
		t.Errorf("Needs you holds %v about a long spend stop, want the one spend item", about)
	}
	var found []api.StuckItem
	for _, item := range readView[api.Stuck](t, d.Daemon, api.ViewStuck, "").Items {
		if item.ID == "wo-long" {
			found = append(found, item)
		}
	}
	if len(found) != 1 || found[0].Reason != api.ReasonLongParked || !slices.Equal(found[0].Actions, []string{api.ActCancel}) {
		t.Errorf("the Stuck view reads %+v, want one long-parked item that offers only cancel", found)
	}
}

// Raising the limit past what was charged ends the stop, and the item leaves with it.
func TestARaisedLimitTakesTheItemOutOfNeedsYou(t *testing.T) {
	d := startTalk(t)
	parkedAtItsLimit(t, d, "wo-raised")
	if len(spendItems(t, d)) != 1 {
		t.Fatal("the stop raised no item")
	}
	if _, err := d.client.RaiseSpend(t.Context(), "wo-raised", api.RaiseSpend{ByPercent: 100}); err != nil {
		t.Fatal(err)
	}
	if items := spendItems(t, d); len(items) != 0 {
		t.Errorf("a raised limit leaves %d spend items", len(items))
	}
}

// A member blocks a spend stop or sends it to replanning, and the work order leaves Needs you's spend items for the
// place it went. Each is refused for work no limit parked, and neither counts an interruption of its own.
func TestAMemberBlocksOrReplansASpendStop(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	parkedAtItsLimit(t, d, "wo-block")
	parkedAtItsLimit(t, d, "wo-replan")
	ended, err := d.client.BlockSpend(ctx, "wo-block")
	if err != nil || ended.State != string(workorders.Blocked) || ended.Reason != workorders.ReasonSpendLimit || ended.WorkOrder != "wo-block" {
		t.Fatalf("a block answered %+v: %v", ended, err)
	}
	if ended, err = d.client.ReplanSpend(ctx, "wo-replan"); err != nil || ended.State != string(workorders.Planning) {
		t.Fatalf("a replan answered %+v: %v", ended, err)
	}
	if order, err := d.client.WorkOrder(ctx, "wo-block"); err != nil || order.State != workorders.Blocked {
		t.Errorf("the blocked work order reads %s: %v", order.State, err)
	}
	if items := spendItems(t, d); len(items) != 0 {
		t.Errorf("work whose stop ended leaves %d spend items", len(items))
	}
	// Blocked work is a stuck item, with the reason a member reads, and the CLI's resolve and replan take it from there.
	if about := itemsAbout(t, d, "wo-block"); !slices.Contains(about, "work:wo-block") {
		t.Errorf("blocked work is not in Needs you: %v", about)
	}
	for name, act := range map[string]func(string) error{
		"block":  func(id string) error { _, err := d.client.BlockSpend(ctx, id); return err },
		"replan": func(id string) error { _, err := d.client.ReplanSpend(ctx, id); return err },
	} {
		t.Run(name+" of work no limit parked", func(t *testing.T) {
			stoppedAtItsLimit(t, d, "wo-claimed-"+name)
			refusedWith(t, act("wo-claimed-"+name), http.StatusBadRequest, api.CodeInvalid)
			refusedWith(t, act("wo-block"), http.StatusBadRequest, api.CodeInvalid)
			refusedWith(t, act("no-such-work-order"), http.StatusNotFound, api.CodeNotFound)
		})
	}
	report, err := d.client.Measures(ctx, "24h")
	if err != nil {
		t.Fatal(err)
	}
	if n := report.InterruptionsByKind[measures.InterruptionForced]; n != 0 {
		t.Errorf("blocking and replanning counted %d interruptions between them", n)
	}
}

// A delegate's block or replan is a confirmation card that shows the work order leaving `parked`, nothing changes until a
// member confirms it, and no delegate policy exempts either; a caller with no member's session, an agent included, is
// refused (FR-SPEND-8).
func TestADelegatesBlockOrReplanIsACardAndNoAgentCanEndAStop(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	parkedAtItsLimit(t, d, "wo-card-stop")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	_, err := delegate.BlockSpend(ctx, "wo-card-stop")
	block := preparedCard(t, err)
	if detail := cardDetail(t, block); detail.Operation != authority.OpSpendBlock || !strings.Contains(string(detail.Changes), "blocked") {
		t.Errorf("the block's card is %+v: %s", detail, detail.Changes)
	}
	_, err = delegate.ReplanSpend(ctx, "wo-card-stop")
	if detail := cardDetail(t, preparedCard(t, err)); detail.Operation != authority.OpSpendReplan || !strings.Contains(string(detail.Changes), "planning") {
		t.Errorf("the replan's card is %+v: %s", detail, detail.Changes)
	}
	if order, err := d.client.WorkOrder(ctx, "wo-card-stop"); err != nil || order.State != workorders.Parked {
		t.Errorf("a delegate's call left the work order %s: %v", order.State, err)
	}
	for _, op := range []string{authority.OpSpendBlock, authority.OpSpendReplan} {
		exempt := "[delegate_policy]\nversion = 2\nexempt = [\"" + op + "\"]\n"
		if _, err := d.client.PublishPolicy(ctx, exempt); err == nil {
			t.Errorf("a delegate policy that exempts %s was published", op)
		}
	}
	credential, _ := joinWorker(t, d.Daemon)
	for name, caller := range map[string]*api.ProjectClient{
		"a caller with nothing":   api.NewUnixClient(d.Socket).Project(apitest.ProjectName),
		"the worker's credential": api.NewUnixClient(d.Socket).Project(apitest.ProjectName).WithCredential(credential),
	} {
		t.Cleanup(caller.Close)
		t.Run(name, func(t *testing.T) {
			_, err := caller.BlockSpend(ctx, "wo-card-stop")
			refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
			_, err = caller.ReplanSpend(ctx, "wo-card-stop")
			refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
		})
	}
	if _, err := d.Client.DecideApproval(ctx, block.ID, api.DecideApproval{Digest: block.SubjectDigest, Allow: true}); err != nil {
		t.Fatalf("the member could not confirm the block: %v", err)
	}
	if order, err := d.client.WorkOrder(ctx, "wo-card-stop"); err != nil || order.State != workorders.Blocked {
		t.Errorf("after the card the work order reads %s: %v", order.State, err)
	}
}

// TestGoldenSpendStop writes what Needs you reads of a spend stop, and what the work order's page reads of its spend,
// from a real ledger.
func TestGoldenSpendStop(t *testing.T) {
	d := startTalk(t)
	parkedAtItsLimit(t, d, "wo-stop")
	leftByTheLane(t, d, "lane-wo-stop", "wo-stop")
	holdsGolden(t, "spend-needs-you", steadyTimes(steady(pretty(t, readAny(t, d.Daemon, api.ViewNeedsYou)))))
	spend, err := d.client.WorkOrderSpend(t.Context(), "wo-stop")
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "work-order-spend", steady(pretty(t, spend)))
}

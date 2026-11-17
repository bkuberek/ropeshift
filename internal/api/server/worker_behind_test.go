package server_test

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// A claim whose lane a delegate's own layer started under a delegate policy that restricts a raise is held by
// a worker on a protocol behind the delegated layer's with no lane: it renews the bare claim and starts nothing
// until it is upgraded (FR-AUTH-16). Nothing else says so, because the claim never lapses, so the stuck list
// and Needs you name the work, the worker, and what it needs. A worker that reads the record, a claim no delegate
// restricted, and work whose lane already runs are not that.
func TestAClaimHeldUntilItsWorkerUpgradesIsStuckAndNeedsYou(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	no := false
	draft := authority.DelegatePolicyDraft{
		Body:   "[delegate_policy]\nversion = 2\nexempt = []\nraise_model_and_effort = false\n",
		Policy: authority.DelegatePolicy{Version: 2, Exempt: []string{}, RaiseModelAndEffort: &no},
	}
	if _, err := control.NewApprovals(d.Store).PublishDelegatePolicy(ctx, apitest.Scope, draft, talkMember); err != nil {
		t.Fatal(err)
	}
	outside := asDelegate(t, d.socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	const raise = "[agent]\neffort = \"max\"\n"
	for _, start := range []struct {
		id     string
		client *api.ProjectClient
	}{{"wo-behind", outside}, {"wo-member", d.client}} {
		claimWork(t, d, start.id, "claim-"+start.id)
		if _, err := start.client.StartLane(ctx, start.id, api.StartLane{LaneID: "lane-" + start.id, Overrides: raise}); err != nil {
			t.Fatalf("%s: %v", start.id, err)
		}
	}
	held := func() (api.StuckItem, bool) {
		t.Helper()
		var found []api.StuckItem
		for _, item := range readView[api.Stuck](t, d.Daemon, api.ViewStuck, "").Items {
			if item.Reason == api.ReasonWorkerBehind {
				found = append(found, item)
			}
		}
		for _, item := range readView[api.NeedsYou](t, d.Daemon, api.ViewNeedsYou, "").Items {
			if item.Work != nil && item.Work.Reason == api.ReasonWorkerBehind {
				if item.Kind != api.NeedWork || len(found) != 1 || item.ID != found[0].ID || item.Since != found[0].Since || item.Work.Detail != found[0].Detail {
					t.Errorf("Needs you holds %+v beside the stuck list's %+v", item, found)
				}
			}
		}
		if len(found) == 0 {
			return api.StuckItem{}, false
		}
		if len(found) != 1 {
			t.Fatalf("%d items wait for an upgrade: %+v", len(found), found)
		}
		return found[0], true
	}
	// A control plane accepts a worker one protocol behind its own, so a session on a protocol two behind is one
	// that an earlier build opened and that outlives the upgrade. The test records such a session the way opening
	// one does.
	behind := func(protocol int) {
		t.Helper()
		err := d.Store.Installation(apitest.Scope).Write(ctx, "test.worker_behind", func(tx ports.SharedTx) error {
			record, err := tx.Workers().Find(d.auth.WorkerID)
			if err != nil {
				return err
			}
			next := record.Worker
			next.Protocol = protocol
			if _, err := tx.Workers().OpenEpoch(next); err != nil {
				return err
			}
			_, err = tx.Append(ledger.EventDraft{Type: "worker.session_opened", Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: record.ID}, Subject: ledger.Subject{Kind: "worker", ID: record.ID}})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if item, found := held(); found {
		t.Fatalf("a worker on the current protocol reads the lane, and its claim is stuck: %+v", item)
	}

	behind(ledger.ProtocolDelegatedLayers - 1)
	item, found := held()
	if !found {
		t.Fatal("a worker a version behind holds the claim of a restricted lane, and no view says so")
	}
	if item.ID != "wo-behind" || item.WorkOrder == nil || item.WorkOrder.ID != "wo-behind" || item.WorkOrder.State != workorders.Claimed {
		t.Errorf("the item is %+v", item)
	}
	if item.Since == 0 {
		t.Errorf("the item says nothing of when the lane was asked for: %+v", item)
	}
	if !slices.Equal(item.Actions, []string{api.ActCancel}) {
		t.Errorf("it offers %v", item.Actions)
	}
	for _, want := range []string{"atlas", d.auth.WorkerID, "upgraded", "start this lane"} {
		if !strings.Contains(item.Detail, want) {
			t.Errorf("the detail %q does not say %q", item.Detail, want)
		}
	}

	behind(ledger.ProtocolVersion)
	if item, found := held(); found {
		t.Errorf("a worker upgraded to the current protocol leaves the claim stuck: %+v", item)
	}
}

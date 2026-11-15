package server_test

import (
	"net/url"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// allowedIn is what the plans view says a member may do to the work order a world holds in each state, with
// no lane to remove where none ended. It is written out here, apart from the domain's rule, so a view that
// drifts from the state machine fails.
var allowedIn = map[workorders.State][]string{
	workorders.Intake:         {"cancel"},
	workorders.Planning:       {"cancel"},
	workorders.ContractReview: {"send_back", "cancel"},
	workorders.Ready:          {"claim", "cancel"},
	workorders.Claimed:        {"start_lane", "release", "cancel"},
	workorders.Running:        {"cancel"},
	workorders.Parked:         {"cancel"},
	workorders.Lapsed:         {"reassign", "cancel"},
	// A lane that ended leaves a worktree a member may ask to remove, so these end with the archive.
	workorders.Acceptance:  {"cancel", "archive_lane"},
	workorders.Integrating: {"cancel", "archive_lane"},
	workorders.Blocked:     {"resolve", "replan", "cancel", "archive_lane"},
	workorders.Done:        {"archive_lane"},
	workorders.Failed:      {"retry", "archive_lane"},
	workorders.Canceled:    {},
}

// The read model says, for every work order in every state, what a member may do to it now, so no view
// holds a copy of the state machine (FR-UI-7).
func TestThePlansViewSaysWhatEachWorkOrderAllows(t *testing.T) {
	t.Parallel()
	d := aWorldInEveryState(t)
	plans := readView[api.Plans](t, d, api.ViewPlans, "")
	for _, state := range workorders.States() {
		want, ok := allowedIn[state]
		if !ok {
			t.Fatalf("the table above says nothing of %s", state)
		}
		id := apitest.WorkIn(state)
		got, listed := plans.WorkActions[id]
		if !listed {
			t.Errorf("the plans view says nothing of %s, which stands in %s", id, state)
			continue
		}
		if !slices.Equal(got.Actions, want) {
			t.Errorf("%s allows %v, want %v", state, got.Actions, want)
		}
	}
}

// A work order that no plan holds is as much a work order as the rest.
func TestEveryWorkOrderTheViewListsHasItsActions(t *testing.T) {
	t.Parallel()
	d := aWorldInEveryState(t)
	plans := readView[api.Plans](t, d, api.ViewPlans, "")
	listed := slices.Clone(plans.Unplanned)
	for _, plan := range plans.Plans {
		listed = append(listed, plan.WorkOrders...)
	}
	if len(listed) == 0 {
		t.Fatal("the world lists no work")
	}
	for _, order := range listed {
		if _, ok := plans.WorkActions[order.ID]; !ok {
			t.Errorf("%s is listed and has no actions entry", order.ID)
		}
	}
}

// A claim that asked for its lane starts no second one, whether or not its worker has reported the lane yet.
func TestAClaimThatAskedForALaneAllowsNoSecondStart(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	apitest.NewWorld(t, d)
	createReady(t, d, "wo-claim")
	ctx := t.Context()
	if _, err := d.Client.Claim(ctx, "wo-claim", api.ClaimRequest{ClaimID: "claim-1", WorkerID: apitest.WorldWorker}); err != nil {
		t.Fatal(err)
	}
	allowed := func() []string { return readView[api.Plans](t, d, api.ViewPlans, "").WorkActions["wo-claim"].Actions }
	if got := allowed(); !slices.Equal(got, []string{"start_lane", "release", "cancel"}) {
		t.Errorf("a claim with no lane allows %v", got)
	}
	if _, err := d.Client.StartLane(ctx, "wo-claim", api.StartLane{LaneID: "lane-1"}); err != nil {
		t.Fatal(err)
	}
	if got := allowed(); !slices.Equal(got, []string{"release", "cancel"}) {
		t.Errorf("a claim whose lane was asked for allows %v", got)
	}
}

// A lane that runs holds its worktree, and one that ended leaves it to remove.
func TestARunningLaneHoldsItsWorktree(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo-run", "A lane works on this")
	allowed := func() []string { return readView[api.Plans](t, d, api.ViewPlans, "").WorkActions["wo-run"].Actions }
	if got := allowed(); !slices.Equal(got, []string{"cancel"}) {
		t.Errorf("work whose lane runs allows %v", got)
	}
	lane.End(workorders.OutcomeCompleted, "")
	if got := allowed(); !slices.Equal(got, []string{"cancel", "archive_lane"}) {
		t.Errorf("work whose lane ended allows %v", got)
	}
}

// Failed work waits for a member, who retries it. It stands in Needs you and in the stuck view, from the
// time it failed, and retrying it takes it out again.
func TestFailedWorkWaitsForAMember(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	// No attempt is left for the first, and two are for the second, which fails a minute later.
	w.Running("wo-spent", "Try the flaky call").End(workorders.OutcomeFailed, workorders.ReasonCrashed)
	clock.Advance(time.Minute)
	createReady(t, d, "wo-left")
	w.Run("wo-left").EndSaying(workorders.OutcomeFailed, workorders.ReasonCrashed, "the disk was full")

	needs := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	var failed []api.NeedItem
	for _, item := range needs.Items {
		if item.Work != nil && item.Work.Reason == api.ReasonFailed {
			failed = append(failed, item)
		}
	}
	if len(failed) != 2 {
		t.Fatalf("Needs you holds %d failed items among %d", len(failed), len(needs.Items))
	}
	// The longest wait comes first, and a work order waits from the time it failed.
	if failed[0].ID != "wo-spent" || failed[1].ID != "wo-left" || failed[0].Since >= failed[1].Since {
		t.Errorf("the order is %s at %d, then %s at %d", failed[0].ID, failed[0].Since, failed[1].ID, failed[1].Since)
	}
	spent, left := failed[0].Work, failed[1].Work
	for _, work := range []*api.WorkNeed{spent, left} {
		if !slices.Equal(work.Actions, []string{api.ActRetry}) {
			t.Errorf("failed work offers %v", work.Actions)
		}
		if work.WorkOrder == nil || work.WorkOrder.State != workorders.Failed || work.Lane == nil {
			t.Errorf("the item holds %+v", work)
		}
	}
	if !spent.ExtraAttempt || left.ExtraAttempt {
		t.Errorf("a retry adds an attempt for the first %v and for the second %v, want true and false", spent.ExtraAttempt, left.ExtraAttempt)
	}
	if left.Detail != "crashed: the disk was full" {
		t.Errorf("the item says %q of why it failed", left.Detail)
	}

	stuck := readView[api.Stuck](t, d, api.ViewStuck, "")
	if len(stuck.Items) != 2 || stuck.Items[0].ID != "wo-spent" || stuck.Items[0].Reason != api.ReasonFailed {
		t.Errorf("the stuck view holds %+v", stuck.Items)
	}

	if _, err := d.Client.Retry(t.Context(), "wo-spent", api.Retry{Reason: "the network came back"}); err != nil {
		t.Fatal(err)
	}
	needs = readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	for _, item := range needs.Items {
		if item.ID == "wo-spent" {
			t.Errorf("a retried work order still waits: %+v", item.Work)
		}
	}
}

// What a lane left on a machine is a lane a member may ask to clear, once nothing runs in it.
func TestAnEndedLaneOffersItsWorktreeToArchive(t *testing.T) {
	t.Parallel()
	d := aWorldInEveryState(t)
	floor := readView[api.ShopFloor](t, d, api.ViewShopFloor, "")
	byWork := map[string]api.FloorLane{}
	for _, lane := range floor.Lanes {
		byWork[lane.WorkOrder] = lane
	}
	for _, c := range []struct {
		work string
		want bool
	}{
		{apitest.WorkIn(workorders.Failed), true},
		{apitest.WorkIn(workorders.Acceptance), true},
		{apitest.WorkIn(workorders.Running), false},
		{apitest.WorkIn(workorders.Parked), false},
	} {
		lane, ok := byWork[c.work]
		if !ok {
			t.Errorf("the floor holds no lane of %s", c.work)
			continue
		}
		if lane.Archivable != c.want {
			t.Errorf("the lane of %s is archivable %v in state %s, want %v", c.work, lane.Archivable, lane.ActualState, c.want)
		}
	}
}

// The palette offers what a member can do to a work order it found, so the search says it, as the plans view does.
func TestASearchedWorkOrderSaysWhatItAllows(t *testing.T) {
	t.Parallel()
	d := aWorldInEveryState(t)
	plans := readView[api.Plans](t, d, api.ViewPlans, "")
	found := search(t, d, url.Values{"category": {api.SearchWorkOrder}})
	if len(found.Records) == 0 {
		t.Fatal("the search found no work order")
	}
	for _, record := range found.Records {
		if want := plans.WorkActions[record.ID].Actions; !slices.Equal(record.Actions, want) && (len(record.Actions) != 0 || len(want) != 0) {
			t.Errorf("%s: the search says %v, and the plans view %v", record.ID, record.Actions, want)
		}
	}
	// Only a work order has actions to offer.
	for _, record := range search(t, d, url.Values{"category": {api.SearchLane}}).Records {
		if len(record.Actions) != 0 {
			t.Errorf("lane %s offers %v", record.ID, record.Actions)
		}
	}
}

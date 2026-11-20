package server_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// A planning branch whose plan is still a placeholder is never offered for approval. The first time, ropeshift sends the
// plan work order back itself, with what is missing as the reason, and records that as a system event. When the next
// version is still unfinished, the work order's card in Needs you says what is missing and offers Send back with that as
// its reason, so a member is called only when ropeshift cannot go on alone.

const secondPlanHead = "def4560000000000000000000000000000000000"

// aPlanLane walks a `plan` work order through one more lane that ends with a branch at head.
func aPlanLane(t *testing.T, d *apitest.Daemon, id string, attempt int, head string) {
	t.Helper()
	ctx := t.Context()
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if attempt == 1 {
		work := control.LedgerWork{
			Order:     workorders.WorkOrder{ID: id, Kind: workorders.KindPlan, Title: "Plan the harbor", MaxAttempts: 3},
			Statement: "Plan the harbor.",
			Criteria:  []planning.Criterion{{ID: "c1", Text: "The plan is written"}},
		}
		if _, err := control.NewWorkOrders(d.Store).Record(ctx, apitest.Scope, work, member); err != nil {
			t.Fatal(err)
		}
		if _, err := control.NewReviews(d.Store).Pass(ctx, apitest.Scope, id, member); err != nil {
			t.Fatal(err)
		}
	}
	leases := control.NewLeases(d.Store)
	worker := ledger.Actor{Kind: ledger.ActorWorker, ID: "worker-a"}
	suffix := id + "-" + string(rune('0'+attempt))
	grant, err := leases.Claim(ctx, apitest.Scope, workorders.ClaimRequest{WorkOrder: id, ClaimID: "claim-" + suffix, Worker: worker.ID, TTLMs: 60_000}, worker)
	if err != nil {
		t.Fatal(err)
	}
	lane := "lane-" + suffix
	generation, err := leases.StartLane(ctx, apitest.Scope, grant.Lease, lane, worker)
	if err != nil {
		t.Fatal(err)
	}
	result := laneEnd(workorders.CheckResult{Name: "unit", Criterion: "c1", Passed: true})
	result.Head = head
	if err := leases.EndLane(ctx, apitest.Scope, workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}, result, worker); err != nil {
		t.Fatal(err)
	}
}

// proposePlans reads the branch a plan lane left, as the control plane's tick does.
func proposePlans(t *testing.T, d *apitest.Daemon, source *fake.WorkSource) {
	t.Helper()
	open := func(context.Context, string, string) (ports.WorkSource, error) { return source, nil }
	if _, err := control.NewIntake(d.Store).ProposePlans(t.Context(), apitest.Scope, open); err != nil {
		t.Fatal(err)
	}
}

func reworkEvents(t *testing.T, d *apitest.Daemon, id string) []ledger.Event {
	t.Helper()
	var out []ledger.Event
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: workorders.SubjectKind, ID: id})
		for _, event := range events {
			if event.Type == workorders.EventType(workorders.TriggerRework) {
				out = append(out, event)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func workNeedOf(view api.NeedsYou, id string) *api.WorkNeed {
	for _, item := range view.Items {
		if item.Work != nil && item.Work.WorkOrder != nil && item.Work.WorkOrder.ID == id && item.Work.Reason == api.ReasonAcceptance {
			return item.Work
		}
	}
	return nil
}

func noPlanApproval(t *testing.T, view api.NeedsYou) {
	t.Helper()
	for _, item := range view.Items {
		if item.Kind == api.NeedPlanApproval {
			t.Fatalf("a placeholder plan is offered for approval: %+v", item.PlanApproval)
		}
	}
}

func TestAPlanHeldBackAsUnfinishedIsSentBackByRopeshiftTheFirstTime(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	aPlanLane(t, d, "wo-plan", 1, laneHead)
	proposePlans(t, d, sourceOf(templatePlanFiles(t)))

	row, err := d.Client.WorkOrder(t.Context(), "wo-plan")
	if err != nil {
		t.Fatal(err)
	}
	if row.State == workorders.Acceptance {
		t.Fatalf("the plan work order still waits for acceptance: %s", row.State)
	}
	events := reworkEvents(t, d, "wo-plan")
	if len(events) != 1 {
		t.Fatalf("the work went back %d times, want once", len(events))
	}
	if events[0].Actor != (ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}) || !strings.Contains(string(events[0].Data), planning.PlaceholderPlanTitle) {
		t.Errorf("the send-back is %+v: %s", events[0].Actor, events[0].Data)
	}
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	noPlanApproval(t, view)
	if need := workNeedOf(view, "wo-plan"); need != nil {
		t.Errorf("a member is called before ropeshift tried once itself: %+v", need)
	}
	// The same branch read again sends nothing more.
	proposePlans(t, d, sourceOf(templatePlanFiles(t)))
	if got := len(reworkEvents(t, d, "wo-plan")); got != 1 {
		t.Errorf("the work went back %d times", got)
	}
}

func TestAPlanStillUnfinishedAfterTheSendBackCallsAMemberWithWhatIsMissing(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	source := sourceOf(templatePlanFiles(t))
	aPlanLane(t, d, "wo-plan", 1, laneHead)
	proposePlans(t, d, source)
	aPlanLane(t, d, "wo-plan", 2, secondPlanHead)
	proposePlans(t, d, source)
	proposePlans(t, d, source)

	if got := len(reworkEvents(t, d, "wo-plan")); got != 1 {
		t.Errorf("ropeshift sent the work back %d times, want once", got)
	}
	row, err := d.Client.WorkOrder(t.Context(), "wo-plan")
	if err != nil || row.State != workorders.Acceptance {
		t.Fatalf("the work order reads %+v: %v", row, err)
	}
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	noPlanApproval(t, view)
	need := workNeedOf(view, "wo-plan")
	if need == nil {
		t.Fatalf("Needs you holds no card for the unfinished plan: %+v", view.Items)
	}
	if len(need.Unfinished) == 0 || !slices.ContainsFunc(need.Unfinished, func(line string) bool { return strings.Contains(line, planning.PlaceholderPlanTitle) }) {
		t.Errorf("the card lists %v", need.Unfinished)
	}
	if !strings.HasPrefix(need.SendBackReason, "The plan is not finished:") || !strings.Contains(need.SendBackReason, planning.PlaceholderGoalTitle) || !slices.Contains(need.Actions, api.ActRework) {
		t.Errorf("the card offers %q with actions %v", need.SendBackReason, need.Actions)
	}
}

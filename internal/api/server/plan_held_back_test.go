package server_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/apitest"
)

// A plan that is still a placeholder is never offered for approval. On a planning branch, ropeshift sends the plan work
// order back itself the first time, and then calls a member with what is missing. On the repository, the request that
// stands for the version only says what is missing and cannot be approved, and it replaces any older request.

// templatePlanFiles are the plan a new project starts from, as `ropeshift new` writes it: a plan, a goal, and a
// requirements file that still say what the templates say.
func templatePlanFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for path, name := range map[string]string{"plan/plan.md": "workspace/plan/plan.md", "plan/goal.md": "workspace/plan/goal.md", "plan/requirements.md": "workspace/plan/requirements.md"} {
		text, err := templates.Render(name, templates.Data{Project: "harbor"})
		if err != nil {
			t.Fatal(err)
		}
		files[path] = text
	}
	return files
}

func sourceOf(files map[string]string) *fake.WorkSource {
	source := fake.NewWorkSource()
	for path, text := range files {
		source.Write(path, []byte(text))
	}
	return source
}

// withBody replaces the prose after a file's front matter.
func withBody(text, body string) string {
	at := strings.LastIndex(text, "\n+++\n")
	return text[:at+len("\n+++\n")] + "\n" + body + "\n"
}

// planTitled writes the plan's own title, and goalWritten writes the goal's, each leaving the rest as it was.
func planTitled(files map[string]string, title string) {
	files["plan/plan.md"] = withBody(strings.ReplaceAll(files["plan/plan.md"], planning.PlaceholderPlanTitle, title), "The API first, then the page.")
}

func goalWritten(files map[string]string) {
	text := strings.NewReplacer(planning.PlaceholderGoalTitle, "Boats book a berth online", planning.PlaceholderMeasure, "A boat books a berth without calling the office").Replace(files["plan/goal.md"])
	files["plan/goal.md"] = withBody(text, "Berths are booked by phone today.")
}

func pendingPlanRequests(t *testing.T, view api.NeedsYou) []*api.PlanApprovalNeed {
	t.Helper()
	var out []*api.PlanApprovalNeed
	for _, item := range view.Items {
		if item.Kind == api.NeedPlanApproval && item.PlanApproval != nil {
			out = append(out, item.PlanApproval)
		}
	}
	return out
}

func decide(t *testing.T, d *apitest.Daemon, need *api.PlanApprovalNeed) error {
	t.Helper()
	_, err := d.Client.DecideApproval(t.Context(), need.Approval.ID, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: true})
	return err
}

// The owner was asked to approve the template, which said nothing. Its request now stands only to say what is missing.
func TestAPlanThatIsStillTheTemplateOffersNoApproval(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	w.Planned(sourceOf(templatePlanFiles(t)))
	needs := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(needs) != 1 || len(needs[0].Unfinished) == 0 {
		t.Fatalf("the template plan's request lists %+v", needs)
	}
	if err := decide(t, d, needs[0]); err == nil {
		t.Error("a placeholder plan was approved")
	}
}

// A goal is fixed last: the plan's own file never changes, so its digest does not, and the request has to be asked again.
func TestAPlanWhoseGoalIsFixedLastIsOfferedAgain(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	files := templatePlanFiles(t)
	planTitled(files, "Harbor berth booking")
	source := sourceOf(files)
	w.Planned(source)
	held := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(held) != 1 || !slices.ContainsFunc(held[0].Unfinished, func(line string) bool { return strings.Contains(line, planning.PlaceholderGoalTitle) }) {
		t.Fatalf("the plan with the template's goal lists %+v", held)
	}
	goalWritten(files)
	source.Write("plan/goal.md", []byte(files["plan/goal.md"]))
	w.Planned(source)
	offered := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(offered) != 1 || len(offered[0].Unfinished) != 0 || offered[0].Version != held[0].Version {
		t.Fatalf("the plan with its goal written is %+v", offered)
	}
	if err := decide(t, d, offered[0]); err != nil {
		t.Errorf("the finished plan was refused: %v", err)
	}
}

// A version held back replaces the older request for the plan, so no request that can be approved is left, and approving
// it would fail on a digest that moved.
func TestAPlanVersionHeldBackReplacesTheOlderRequest(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	source := retrySource("The first draft.")
	w.Planned(source)
	first := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(first) != 1 || len(first[0].Unfinished) != 0 {
		t.Fatalf("the finished plan's request is %+v", first)
	}
	source.Write("plan.md", []byte(strings.Replace(retryPlan, "Retry budget", planning.PlaceholderPlanTitle, 1)))
	w.Planned(source)
	now := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(now) != 1 || now[0].Version != 2 || len(now[0].Unfinished) == 0 {
		t.Fatalf("the held back version leaves %+v", now)
	}
	if err := decide(t, d, first[0]); err == nil {
		t.Error("the older request could still be approved")
	}
}

// A plan that keeps a template sentence and says more is written, so it is offered.
func TestAPlanThatKeepsATemplateSentenceAndSaysMoreIsOffered(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	files := templatePlanFiles(t)
	planTitled(files, "Harbor berth booking")
	goalWritten(files)
	files["plan/plan.md"] = withBody(files["plan/plan.md"], planning.PlaceholderPlanProse+" The API ships first, behind a flag.")
	w.Planned(sourceOf(files))
	needs := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(needs) != 1 || len(needs[0].Unfinished) != 0 {
		t.Fatalf("a written plan reads as %+v", needs)
	}
}

const (
	secondPlanHead = "def4560000000000000000000000000000000000"
	thirdPlanHead  = "0a1b2c0000000000000000000000000000000000"
)

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

// proposePlans reads the branch a plan lane left, as the control plane's tick does, and says how often it was opened.
func proposePlans(t *testing.T, d *apitest.Daemon, source *fake.WorkSource) int {
	t.Helper()
	opened := 0
	open := func(context.Context, string, string) (ports.WorkSource, error) {
		opened++
		return source, nil
	}
	if _, err := control.NewIntake(d.Store).ProposePlans(t.Context(), apitest.Scope, open); err != nil {
		t.Fatal(err)
	}
	return opened
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
	if needs := pendingPlanRequests(t, view); len(needs) != 0 {
		t.Fatalf("a placeholder plan on a branch is offered for approval: %+v", needs[0])
	}
}

func TestAPlanHeldBackAsUnfinishedIsSentBackByRopeshiftTheFirstTime(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	aPlanLane(t, d, "wo-plan", 1, laneHead)
	source := sourceOf(templatePlanFiles(t))
	if opened := proposePlans(t, d, source); opened != 1 {
		t.Fatalf("the branch was opened %d times", opened)
	}
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
}

func TestAPlanStillUnfinishedAfterTheSendBackCallsAMemberWithWhatIsMissing(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	source := sourceOf(templatePlanFiles(t))
	aPlanLane(t, d, "wo-plan", 1, laneHead)
	proposePlans(t, d, source)
	aPlanLane(t, d, "wo-plan", 2, secondPlanHead)
	if opened := proposePlans(t, d, source); opened != 1 {
		t.Fatalf("the second branch was opened %d times", opened)
	}
	// What is held back at a commit is not read again until the lane commits something new.
	if opened := proposePlans(t, d, source); opened != 0 {
		t.Errorf("a branch held back was read again %d times", opened)
	}
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
	if !slices.ContainsFunc(need.Unfinished, func(line string) bool { return strings.Contains(line, planning.PlaceholderPlanTitle) }) {
		t.Errorf("the card lists %v", need.Unfinished)
	}
	if !strings.HasPrefix(need.SendBackReason, "The plan is not finished:") || !strings.Contains(need.SendBackReason, planning.PlaceholderGoalTitle) || !slices.Contains(need.Actions, api.ActRework) {
		t.Errorf("the card offers %q with actions %v", need.SendBackReason, need.Actions)
	}
}

// A member's own send-back in between is not the first time over again: ropeshift tries once for a work order.
func TestAMembersSendBackDoesNotMakeRopeshiftSendThePlanBackAgain(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	source := sourceOf(templatePlanFiles(t))
	aPlanLane(t, d, "wo-plan", 1, laneHead)
	proposePlans(t, d, source)
	aPlanLane(t, d, "wo-plan", 2, secondPlanHead)
	proposePlans(t, d, source)
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := control.NewAcceptances(d.Store).Rework(t.Context(), apitest.Scope, "wo-plan", "Write the goal first.", member); err != nil {
		t.Fatal(err)
	}
	aPlanLane(t, d, "wo-plan", 3, thirdPlanHead)
	proposePlans(t, d, source)
	if got := len(reworkEvents(t, d, "wo-plan")); got != 2 {
		t.Errorf("the work went back %d times, once by ropeshift and once by the member", got)
	}
	if need := workNeedOf(readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""), "wo-plan"); need == nil || len(need.Unfinished) == 0 {
		t.Errorf("a member is not called: %+v", need)
	}
}

const alphaPlan = `+++
kind = "plan"
id = "alpha-v1"
title = "Alpha accounts"
goal = "alpha-goal"
+++

The API first.
`

const alphaGoal = `+++
kind = "goal"
id = "alpha-goal"
title = "Customers manage accounts"
success_measures = ["Support requests fall by half"]
+++

Support asked for it.
`

const alphaContract = `+++
kind = "contract"
id = "alpha-api"
title = "The accounts API"
plan = "alpha-v1"

[[criteria]]
id = "c1"
text = "It serves accounts"

[[work_orders]]
id = "alpha-order"
title = "Serve accounts"
size = "small"
+++

Serve the endpoints.
`

// Approving one plan of a branch accepts the work order and merges the whole branch, so a finished plan beside a
// placeholder is held with it, and the missing list names the plan that lacks.
func TestABranchWithAFinishedPlanAndAPlaceholderIsHeldWhole(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	files := templatePlanFiles(t)
	source := sourceOf(files)
	source.Write("plan/alpha.md", []byte(alphaPlan))
	source.Write("plan/alpha-goal.md", []byte(alphaGoal))
	source.Write("plan/alpha-api.md", []byte(alphaContract))
	aPlanLane(t, d, "wo-plan", 1, laneHead)
	proposePlans(t, d, source)
	noPlanApproval(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	events := reworkEvents(t, d, "wo-plan")
	if len(events) != 1 {
		t.Fatalf("the branch was sent back %d times, want once", len(events))
	}
	reason := string(events[0].Data)
	if !strings.Contains(reason, "Plan harbor-v1:") || strings.Contains(reason, "Plan alpha-v1:") {
		t.Errorf("the missing list is %s", reason)
	}
}

// A goal fixed last on a branch is read at the lane's next commit and offered.
func TestABranchWhoseGoalIsFixedLastIsOffered(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	files := templatePlanFiles(t)
	planTitled(files, "Harbor berth booking")
	source := sourceOf(files)
	aPlanLane(t, d, "wo-plan", 1, laneHead)
	proposePlans(t, d, source)
	if len(reworkEvents(t, d, "wo-plan")) != 1 {
		t.Fatal("ropeshift did not send the plan with the template's goal back")
	}
	goalWritten(files)
	source.Write("plan/goal.md", []byte(files["plan/goal.md"]))
	aPlanLane(t, d, "wo-plan", 2, secondPlanHead)
	proposePlans(t, d, source)
	needs := pendingPlanRequests(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if len(needs) != 1 || len(needs[0].Unfinished) != 0 || needs[0].Approval.Kind != authority.KindPlan {
		t.Fatalf("the plan with its goal written is %+v", needs)
	}
}

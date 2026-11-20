package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
)

// plannedDaemon serves a goal, a plan, a requirement, and one contract.
func plannedDaemon(t *testing.T) *apitest.Daemon {
	t.Helper()
	daemon := apitest.Start(t)
	source := fake.NewWorkSource()
	source.Write("goal.md", []byte(contract.GoalText("a-goal", "The goal")))
	source.Write("plan.md", []byte(contract.PlanText("a-plan", "The plan", "a-goal")))
	source.Write("requirements.md", []byte(contract.RequirementsText("payments", "PAY-12", "It retries")))
	text := "+++\nkind = \"contract\"\nid = \"alpha\"\ntitle = \"Alpha\"\nplan = \"a-plan\"\n\n[[criteria]]\nid = \"c1\"\ntext = \"It works\"\n\n[[work_orders]]\nid = \"alpha-order\"\ntitle = \"The work\"\n+++\n\nThe statement.\n"
	source.Write("contract.md", []byte(text))
	report, err := control.NewIntake(daemon.Store).Poll(t.Context(), apitest.Scope, source)
	if err != nil || len(report.Problems) != 0 {
		t.Fatalf("report %+v: %v", report, err)
	}
	return daemon
}

func TestAPlanShowsItsGoalContractsAndWork(t *testing.T) {
	t.Parallel()
	daemon := plannedDaemon(t)
	plans, err := daemon.Client.Plans(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Title != "The plan" {
		t.Fatalf("plans %+v", plans)
	}
	view, err := daemon.Client.Plan(t.Context(), plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Goal == nil || view.Goal.Title != "The goal" || view.Plan.ID != plans[0].ID {
		t.Errorf("view %+v", view)
	}
	if len(view.Contracts) != 1 || view.Contracts[0].Title != "Alpha" {
		t.Errorf("contracts %+v", view.Contracts)
	}
	if len(view.WorkOrders) != 1 || view.WorkOrders[0].Title != "The work" {
		t.Errorf("work orders %+v", view.WorkOrders)
	}
	_, err = daemon.Client.Plan(t.Context(), "absent")
	wantRefused(t, err, http.StatusNotFound, api.CodeNotFound)
}

func TestARequirementReadsByItsKey(t *testing.T) {
	t.Parallel()
	daemon := plannedDaemon(t)
	requirement, err := daemon.Client.Requirement(t.Context(), "PAY-12")
	if err != nil {
		t.Fatal(err)
	}
	if requirement.Text != "It retries" || requirement.Version != 1 {
		t.Errorf("requirement %+v", requirement)
	}
	all, err := daemon.Client.Requirements(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || !reflect.DeepEqual(all[0], requirement) {
		t.Errorf("list %+v", all)
	}
	_, err = daemon.Client.Requirement(t.Context(), "ABSENT")
	wantRefused(t, err, http.StatusNotFound, api.CodeNotFound)
}

func TestSendingBackMovesWorkToPlanning(t *testing.T) {
	t.Parallel()
	daemon := plannedDaemon(t)
	rows, err := daemon.Client.WorkOrders(t.Context(), workorders.ContractReview)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %+v: %v", rows, err)
	}
	if err := daemon.Client.SendBack(t.Context(), rows[0].ID, api.SendBack{Reason: "The criteria are wrong"}); err != nil {
		t.Fatal(err)
	}
	row, err := daemon.Client.WorkOrder(t.Context(), rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != workorders.Planning {
		t.Errorf("state %s", row.State)
	}
	wantRefused(t, daemon.Client.SendBack(t.Context(), rows[0].ID, api.SendBack{Reason: "Again"}), http.StatusConflict, api.CodeIllegalMove)
	wantRefused(t, daemon.Client.SendBack(t.Context(), rows[0].ID, api.SendBack{}), http.StatusBadRequest, api.CodeInvalid)
}

func TestDocumentsAreReadableAndVersioned(t *testing.T) {
	t.Parallel()
	knowledge := fake.NewKnowledgeSource()
	knowledge.Write("design.md", "# One\n")
	knowledge.Write("design.md", "# Two\n")
	daemon := apitest.StartWith(t, apitest.Options{Knowledge: []ports.KnowledgeSource{knowledge}})
	list, err := daemon.Client.Documents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Path != "design.md" || list[0].Title != "Two" || list[0].Body != "" {
		t.Fatalf("list %+v", list)
	}
	versions, err := daemon.Client.DocumentVersions(t.Context(), api.DocumentRef{Path: "design.md"})
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions %+v: %v", versions, err)
	}
	earlier, err := daemon.Client.Document(t.Context(), api.DocumentRef{Path: "design.md", Version: versions[1].Version})
	if err != nil {
		t.Fatal(err)
	}
	if earlier.Body != "# One\n" {
		t.Errorf("earlier %+v", earlier)
	}
	current, err := daemon.Client.Document(t.Context(), api.DocumentRef{Path: "design.md", Instance: list[0].Instance})
	if err != nil {
		t.Fatal(err)
	}
	if current.Body != "# Two\n" {
		t.Errorf("current %+v", current)
	}
	_, err = daemon.Client.Document(t.Context(), api.DocumentRef{Path: "absent.md"})
	wantRefused(t, err, http.StatusNotFound, api.CodeNotFound)
}

// manifestPlanText is a plan that lists the platform's manifest.
const manifestPlanText = "+++\nkind = \"plan\"\nid = \"platform-v1\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nHow.\n"

// manifestEntries are a manifest's billing, whose clone the daemon's worker keeps beside the planning repository's,
// and web, which approving a version whose work names it creates.
var manifestEntries = []planning.ManifestRepository{
	{Name: "billing", Remote: "git@example.invalid:acme/billing.git", Path: "../billing"},
	{Name: "web", Path: "../web", Create: "github:acme"},
}

// planningMachine is the daemon's worker's machine, which keeps the planning repository's clone at /work/plan and a
// clone of billing beside it, on a daemon whose one repository is the planning repository.
func planningMachine(t *testing.T, options apitest.Options) *apitest.Daemon {
	t.Helper()
	d, _ := planningMachineWith(t, options, nil)
	return d
}

// planningMachineWith is planningMachine with the creator that makeCreator gives for the machine, which the daemon
// creates the repositories a plan approval names with. It returns the machine too.
func planningMachineWith(t *testing.T, options apitest.Options, makeCreator func(*fake.Checkouts) ports.RepositoryCreator) (*apitest.Daemon, *fake.Checkouts) {
	t.Helper()
	machine := fake.NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/plan", ID: "id-plan", DefaultBranch: "main"})
	machine.Place(ports.Clone{Path: "/work/billing", ID: "id-billing", Remote: manifestEntries[0].Remote, DefaultBranch: "main"})
	options.Checkouts = machine
	if makeCreator != nil {
		options.Creator = makeCreator(machine)
	}
	d := apitest.StartWith(t, options)
	planner, err := control.NewRepositories(d.Store).Detect(t.Context(), apitest.Scope, control.DetectedRepository{ID: "id-plan", Directory: "plan", DefaultBranch: "main"},
		ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := machine.Keep(t.Context(), apitest.Scope, planner.ID, "/work/plan"); err != nil {
		t.Fatal(err)
	}
	return d, machine
}

// wantPlannedPreview checks that a waiting version's preview has billing registering and web planned.
func wantPlannedPreview(t *testing.T, registration *api.ManifestRegistration) {
	t.Helper()
	if registration == nil || !registration.Preview || len(registration.Repositories) != 2 {
		t.Fatalf("the version previews %+v", registration)
	}
	want := map[string]api.RepositoryAdded{
		"billing": {Name: "billing", ManifestPath: "../billing", Path: "/work/billing", Remote: manifestEntries[0].Remote, Outcome: ledger.Registered},
		"web":     {Name: "web", ManifestPath: "../web", Outcome: ledger.Planned, Reason: "no work order of this version targets web"},
	}
	for _, got := range registration.Repositories {
		expected := want[got.Name]
		if got.ManifestPath != expected.ManifestPath || got.Path != expected.Path || got.Remote != expected.Remote || got.Outcome != expected.Outcome || got.Reason != expected.Reason {
			t.Errorf("the preview of %s reads %+v, want %+v", got.Name, got, expected)
		}
	}
}

// FR-REPO-16: the plan approval page reads, for a version whose manifest lists a repository to create that no
// work order names, that approving plans it and creates nothing, beside a clone approving registers.
func TestGoldenPlanWithAPlannedRepository(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := planningMachine(t, apitest.Options{Clock: clock.Dialect})
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(manifestPlanText))
	source.Write("repositories.md", []byte("+++\nkind = \"repositories\"\nid = \"platform\"\nplan = \"platform-v1\"\n\n[[repository]]\nname = \"billing\"\nremote = \"git@example.invalid:acme/billing.git\"\npath = \"../billing\"\n\n[[repository]]\nname = \"web\"\npath = \"../web\"\ncreate = \"github:acme\"\n+++\n"))
	source.Write("contract.md", []byte("+++\nkind = \"contract\"\nid = \"alpha\"\ntitle = \"Alpha\"\nplan = \"platform-v1\"\n\n[[criteria]]\nid = \"c1\"\ntext = \"It works\"\n\n[[work_orders]]\nid = \"alpha-order\"\ntitle = \"The work\"\nrepo = \"billing\"\n+++\n\nThe statement.\n"))
	if _, err := control.NewIntake(d.Store).Poll(t.Context(), apitest.Scope, source); err != nil {
		t.Fatal(err)
	}
	plans, err := d.Client.Plans(t.Context())
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	view, err := d.Client.Plan(t.Context(), plans[0].ID)
	if err != nil || view.Approval == nil {
		t.Fatalf("the plan reads %+v: %v", view.Approval, err)
	}
	wantPlannedPreview(t, view.Approval.Registration)
	steadySet(&view)
	holdsGolden(t, "plan-planned", steady(pretty(t, view)))
}

// FR-REPO-8: a later version a planning branch asks about, which the ledger holds only once a member approves it,
// previews what approving registers from the manifest and the work its request carries from the branch.
func TestABranchVersionPreviewsItsManifest(t *testing.T) {
	t.Parallel()
	d := planningMachine(t, apitest.Options{})
	ctx := t.Context()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(manifestPlanText))
	if _, err := control.NewIntake(d.Store).Poll(ctx, apitest.Scope, source); err != nil {
		t.Fatal(err)
	}
	plans, err := d.Client.Plans(ctx)
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	first, err := d.Client.Plan(ctx, plans[0].ID)
	if err != nil || first.Approval == nil || first.Approval.Registration != nil {
		t.Fatalf("the first version, which lists no manifest, reads %+v: %v", first.Approval, err)
	}
	if _, err := d.Client.DecideApproval(ctx, first.Approval.Approval.ID, api.DecideApproval{Digest: first.Approval.Approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	branch := askedByBranch(t, d, plans[0].ID, 2)
	view, err := d.Client.Plan(ctx, plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Approval == nil || view.Approval.Approval.ID != branch.ID {
		t.Fatalf("the plan reads the request %+v, want the branch's", view.Approval)
	}
	wantPlannedPreview(t, view.Approval.Registration)
}

// askedByBranch records the request a planning branch raises for a version of a plan, which the ledger holds only
// once a member approves it, with the manifest and the work its request carries from the branch: a plan the
// ledger does not hold yet is empty.
func askedByBranch(t *testing.T, d *apitest.Daemon, plan string, version int64) authority.Approval {
	t.Helper()
	detail, err := json.Marshal(planning.PlanDetail{Title: "The platform", Ref: "platform-v1", Manifest: manifestEntries, Targets: []string{"billing"}, Source: "abc1234"})
	if err != nil {
		t.Fatal(err)
	}
	branch := authority.Approval{
		ID: "branch-approval", Kind: authority.KindPlan, WorkOrder: "plan-1", Subject: fmt.Sprintf("platform-v1@%d", version), SubjectDigest: "sha256:branch",
		Summary: fmt.Sprintf("Plan The platform, version %d", version), RequestedBy: "lane:lane-1", State: authority.ApprovalPending, RaisedAt: 1,
		Plan: plan, PlanVersion: version, Detail: string(detail),
	}
	err = d.Store.Write(t.Context(), apitest.Scope, "test.approval", func(tx ports.WriteTx) error {
		if _, err := tx.Approvals().Insert(branch); err != nil {
			return err
		}
		event, err := authority.ApprovalEvent(branch, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return branch
}

package server_test

import (
	"net/http"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
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

package server_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// aWorkspaceOfClones lays a workspace out on the worker's machine, with a design repository and a code
// repository in the folder it ignores.
func aWorkspaceOfClones(in *apitest.Installation) {
	clone := func(path, id string, layout fake.CloneLayout) {
		in.Checkouts.Place(ports.Clone{Path: path, ID: id, Remote: "git@example.invalid:acme/" + id + ".git", DefaultBranch: "main"})
		in.Layouts.Set(path, layout)
	}
	clone("/work/ws", "ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{".gitignore", "README.md"}})
	clone("/work/ws/repos/design", "design", fake.CloneLayout{Plan: ports.PlanContent{Files: 1}, Tracked: []string{".gitignore", "docs/a.md", "plan/goal.md", "tools/go.mod"}})
	clone("/work/ws/repos/api", "api", fake.CloneLayout{Tracked: []string{"go.mod"}})
}

// The route reads the layout around a clone on the worker's machine and answers the plan, and a plan records
// nothing: the project holds no repository afterwards.
func TestTheAdoptionPlanRouteReadsTheLayoutAndRecordsNothing(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	aWorkspaceOfClones(in)
	_, _, client := in.In(t, apitest.ProjectName)

	plan, err := client.AdoptionPlan(t.Context(), api.AdoptionPlanRequest{Root: "/work/ws", LeaveOut: []string{"tools"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Shape != planning.ShapeConsolidate || plan.Design == nil || plan.Design.Name != "design" || plan.Digest == "" || plan.Digest != plan.ComputeDigest() {
		t.Fatalf("the plan reads shape %s, design %+v, digest %q", plan.Shape, plan.Design, plan.Digest)
	}
	if want := []string{"docs/a.md", "plan/goal.md"}; !reflect.DeepEqual(plan.Takes, want) {
		t.Errorf("the plan takes %v, want %v", plan.Takes, want)
	}
	if len(plan.CodeTrees) != 1 || !plan.CodeTrees[0].LeftOut {
		t.Errorf("the code trees read %+v", plan.CodeTrees)
	}
	if list, err := client.Repositories(t.Context()); err != nil || len(list.Repositories) != 0 {
		t.Errorf("a plan registered %+v: %v", list.Repositories, err)
	}
	if kept, err := in.Checkouts.Of(t.Context(), ledger.WorkspaceID(apitest.ProjectName+"-id")); err != nil || len(kept) != 0 {
		t.Errorf("a plan kept checkouts %v: %v", kept, err)
	}
	if _, err := client.AdoptionPlan(t.Context(), api.AdoptionPlanRequest{Root: "relative"}); err == nil {
		t.Error("a relative root was planned")
	} else {
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
}

// A project the adoption has yet to create has no route of its own, so the installation's route reads the plan
// with no record of any project.
func TestTheAdoptionPlanOfAProjectNotCreatedYetReadsNoRecords(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	aWorkspaceOfClones(in)

	plan, err := in.Client.Client.AdoptionPlan(t.Context(), api.AdoptionPlanRequest{Root: "/work/ws", NewProject: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Shape != planning.ShapeConsolidate || !strings.Contains(strings.Join(plan.Notes, " "), "does not exist yet") {
		t.Errorf("the plan reads shape %s with notes %v", plan.Shape, plan.Notes)
	}
}

// A daemon with no worker whose clones it reads answers that, and so does one with no layout reads.
func TestTheAdoptionPlanRouteNeedsAWorkersMachine(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	_, err := d.Client.AdoptionPlan(t.Context(), api.AdoptionPlanRequest{Root: "/work/ws"})
	refusedWith(t, err, http.StatusNotImplemented, api.CodeUnavailable)
}

// Work that adopts keeps the restructuring a member chose, and a delegate cannot ask for one: it is a member's
// alone, where creating work is a card.
func TestAdoptWorkKeepsItsRestructuringAndOnlyAMemberChoosesOne(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	_, _, client := in.In(t, apitest.ProjectName)
	chosen := &planning.Restructure{Shape: planning.ShapeWorkspaceFiles, Digest: planning.DigestSHA256 + strings.Repeat("d", 64)}
	create := func(c *api.ProjectClient, id string, restructure *planning.Restructure) (workorders.WorkOrder, error) {
		return c.CreateWorkOrder(t.Context(), api.CreateWorkOrder{
			ID: id, Kind: workorders.KindPlan, Title: "Adopt", Statement: "Onboard.", Adopt: true, Restructure: restructure,
			Criteria: []planning.Criterion{{ID: "c1", Text: "Onboarded."}},
		})
	}
	made, err := create(client, "adopt-1", chosen)
	if err != nil || !reflect.DeepEqual(made.Restructure, chosen) {
		t.Fatalf("a member's adopt work reads %+v: %v", made.Restructure, err)
	}
	if read, err := client.WorkOrder(t.Context(), "adopt-1"); err != nil || !reflect.DeepEqual(read.Restructure, chosen) {
		t.Errorf("the work order reads %+v: %v", read.Restructure, err)
	}
	if _, err := create(client, "adopt-2", &planning.Restructure{Shape: planning.ShapeSingle, Digest: chosen.Digest}); err == nil {
		t.Error("the single shape was kept")
	} else {
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
	// The request the plan was read with and the digests of its parts are kept as they were sent, and a request
	// that cannot be kept is refused.
	whole := &planning.Restructure{
		Shape: planning.ShapeWorkspaceFiles, Digest: chosen.Digest, Root: "/work/ws", Paths: []string{"/work/libs"}, Depth: 2,
		Planning: "ws", Design: "docs", Parts: map[string]string{planning.PartFiles: chosen.Digest, planning.PartLayout: chosen.Digest},
	}
	if kept, err := create(client, "adopt-5", whole); err != nil || !reflect.DeepEqual(kept.Restructure, whole) {
		t.Errorf("a restructuring with its request reads %+v: %v", kept.Restructure, err)
	}
	if read, err := client.WorkOrder(t.Context(), "adopt-5"); err != nil || !reflect.DeepEqual(read.Restructure, whole) {
		t.Errorf("the work order reads %+v: %v", read.Restructure, err)
	}
	relative := *whole
	relative.Root = "ws"
	if _, err := create(client, "adopt-6", &relative); err == nil {
		t.Error("a request with a relative root was kept")
	} else {
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}

	created := aDelegate(t, in.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, in.Socket, created.Token, apitest.ProjectName)
	_, err = create(delegate, "adopt-3", chosen)
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	// The same work with no restructuring is the card it always was.
	_, err = create(delegate, "adopt-4", nil)
	preparedCard(t, err)
	// Reading the layout is a member's alone too, in a project and in one not created yet.
	aWorkspaceOfClones(in)
	_, err = delegate.AdoptionPlan(t.Context(), api.AdoptionPlanRequest{Root: "/work/ws"})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	_, err = asDelegateOn(t, in.Socket, created.Token).AdoptionPlan(t.Context(), api.AdoptionPlanRequest{Root: "/work/ws"})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
}

// asDelegateOn is a client with no project that presents a delegate credential.
func asDelegateOn(t *testing.T, socket, token string) *api.Client {
	t.Helper()
	client := api.NewUnixClient(socket)
	t.Cleanup(client.Close)
	client.UseDelegate(token)
	client.UseWhy(aWhy)
	return client
}

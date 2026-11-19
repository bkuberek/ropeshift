package server_test

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
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

// The route answers the digest of each part of the plan beside it, as `parts`, so a client that cannot compute
// them sends them back with the restructuring it chooses. A client of this module reads the plan and ignores them.
func TestTheAdoptionPlanRouteAnswersTheDigestOfEachPart(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	aWorkspaceOfClones(in)
	var reply api.AdoptionPlanReply
	if err := in.Client.Do(t.Context(), http.MethodPost, api.AdoptionPlanPath, api.AdoptionPlanRequest{Root: "/work/ws", NewProject: true}, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Digest == "" || len(reply.Parts) != len(planning.PlanParts) || !reflect.DeepEqual(reply.Parts, reply.AdoptionPlan.Parts()) {
		t.Errorf("the plan with digest %q answers the parts %v, want %v", reply.Digest, reply.Parts, reply.AdoptionPlan.Parts())
	}
	// The plan's own digest does not hold them: it is what it was.
	if reply.Digest != reply.ComputeDigest() {
		t.Errorf("the digest %s is not the plan's, %s", reply.Digest, reply.ComputeDigest())
	}
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
	single := &planning.Restructure{Shape: planning.ShapeSingle, Digest: chosen.Digest}
	if kept, err := create(client, "adopt-2", single); err != nil || !reflect.DeepEqual(kept.Restructure, single) {
		t.Errorf("a restructuring of the single shape, which adds the instruction files it lacks, reads %+v: %v", kept.Restructure, err)
	}
	if _, err := create(client, "adopt-7", &planning.Restructure{Shape: "other", Digest: chosen.Digest}); err == nil {
		t.Error("a shape no layout has was kept")
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

// The route reads each repository's practices only when the request asks, in process on the worker's machine and with
// no forge binding, and the plan's digest is the one the plan has without them.
func TestTheAdoptionPlanRouteListsPracticesOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	aWorkspaceOfClones(in)
	in.Practices.SetSnapshot("/work/ws/repos/api", practices.Snapshot{Commit: "1111111111111111111111111111111111111111", Paths: []string{"go.mod", "main.go"}})
	_, _, client := in.In(t, apitest.ProjectName)

	plain, err := client.AdoptionPlan(t.Context(), api.AdoptionPlanRequest{Root: "/work/ws"})
	if err != nil {
		t.Fatal(err)
	}
	for _, repository := range plain.Repositories {
		if repository.Practices != nil {
			t.Errorf("%s holds practices in a plan that asked for none", repository.Path)
		}
	}
	if len(in.Practices.SnapshotCalls) != 0 {
		t.Fatalf("a plan that asked for none read %v", in.Practices.SnapshotCalls)
	}
	asked, err := client.AdoptionPlan(t.Context(), api.AdoptionPlanRequest{Root: "/work/ws", Practices: true})
	if err != nil {
		t.Fatal(err)
	}
	var found *practices.Result
	for _, repository := range asked.Repositories {
		if repository.Path == "/work/ws/repos/api" {
			found = repository.Practices
		}
	}
	if found == nil || found.BaseBranch != "main" || len(found.Findings) == 0 {
		t.Fatalf("the api repository holds %+v", found)
	}
	if asked.Digest != plain.Digest {
		t.Errorf("the plan with practices has the digest %s, want %s", asked.Digest, plain.Digest)
	}
	if len(in.Practices.SettingsCalls) != 0 {
		t.Errorf("the forge was read: %v", in.Practices.SettingsCalls)
	}
}

// The route answers the table's words for each practice a finding of the plan names, once, beside the plan, so the
// web UI prints the same why and fix as the command line without carrying the table.
func TestTheAdoptionPlanRouteAnswersTheGuideOfEachPracticeFound(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	aWorkspaceOfClones(in)
	in.Practices.SetSnapshot("/work/ws/repos/api", practices.Snapshot{Commit: "1111111111111111111111111111111111111111", Paths: []string{"go.mod"}})
	var reply api.AdoptionPlanReply
	if err := in.Client.Do(t.Context(), http.MethodPost, api.AdoptionPlanPath, api.AdoptionPlanRequest{Root: "/work/ws", NewProject: true, Practices: true}, &reply); err != nil {
		t.Fatal(err)
	}
	guide, ok := reply.Guides[string(practices.Codeowners)]
	want, _ := practices.Lookup(practices.Codeowners)
	if !ok || guide.Fix != want.Fix || guide.Why != want.Why || guide.Template != "codeowners" || guide.Command != want.Command() {
		t.Errorf("the guide of codeowners reads %+v, want the table's words", guide)
	}
	for id := range reply.Guides {
		if !slices.ContainsFunc(reply.Repositories, func(r planning.PlannedRepository) bool {
			return r.Practices != nil && slices.ContainsFunc(r.Practices.Findings, func(f practices.Finding) bool { return string(f.Practice) == id })
		}) {
			t.Errorf("the reply holds the guide of %s, which no finding names", id)
		}
	}
	plain := api.AdoptionPlanReply{}
	if err := in.Client.Do(t.Context(), http.MethodPost, api.AdoptionPlanPath, api.AdoptionPlanRequest{Root: "/work/ws", NewProject: true}, &plain); err != nil || len(plain.Guides) != 0 {
		t.Errorf("a plan that asked for no practices answers the guides %v: %v", plain.Guides, err)
	}
}

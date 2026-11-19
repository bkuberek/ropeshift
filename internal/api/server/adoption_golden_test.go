package server_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/apitest"
)

// placeClone puts a clone on the worker's machine with the layout its tip holds.
func placeClone(in *apitest.Installation, path, id string, layout fake.CloneLayout) {
	in.Checkouts.Place(ports.Clone{Path: path, ID: id, Remote: "git@example.invalid:acme/" + id + ".git", DefaultBranch: "main"})
	in.Layouts.Set(path, layout)
}

// TestGoldenAdoptionPlans writes the plan of an adoption in each shape, and in the layouts that change what the
// web UI offers, as the Import page reads them from the plan route.
func TestGoldenAdoptionPlans(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		shape   planning.LayoutShape
		place   func(*apitest.Installation)
		request api.AdoptionPlanRequest
	}{
		{
			name: "adoption-plan-single", shape: planning.ShapeSingle,
			place: func(in *apitest.Installation) {
				placeClone(in, "/work/solo", "solo", fake.CloneLayout{Tracked: []string{"README.md", "AGENTS.md", "docs/overview.md", "go.mod"}})
			},
			request: api.AdoptionPlanRequest{Root: "/work/solo"},
		},
		{
			name: "adoption-plan-single-lacking", shape: planning.ShapeSingle,
			place: func(in *apitest.Installation) {
				placeClone(in, "/work/solo", "solo", fake.CloneLayout{Tracked: []string{"README.md", "docs/overview.md", "go.mod"}})
			},
			request: api.AdoptionPlanRequest{Root: "/work/solo"},
		},
		{
			name: "adoption-plan-planned", shape: planning.ShapePlanned,
			place: func(in *apitest.Installation) {
				placeClone(in, "/work/ws", "ws", fake.CloneLayout{Ignored: []string{"repos"}, Plan: ports.PlanContent{Files: 2}, Tracked: []string{".gitignore", "README.md", "plan/goal.md"}})
				placeClone(in, "/work/ws/repos/api", "api", fake.CloneLayout{Tracked: []string{"go.mod"}})
				placeClone(in, "/work/ws/repos/web", "web", fake.CloneLayout{Tracked: []string{"package.json"}})
			},
			request: api.AdoptionPlanRequest{Root: "/work/ws"},
		},
		{
			name: "adoption-plan-consolidate", shape: planning.ShapeConsolidate,
			place:   aWorkspaceOfClones,
			request: api.AdoptionPlanRequest{Root: "/work/ws"},
		},
		{
			name: "adoption-plan-consolidate-left-out", shape: planning.ShapeConsolidate,
			place:   aWorkspaceOfClones,
			request: api.AdoptionPlanRequest{Root: "/work/ws", LeaveOut: []string{"tools"}},
		},
		{
			name: "adoption-plan-workspace-files", shape: planning.ShapeWorkspaceFiles,
			place: func(in *apitest.Installation) {
				placeClone(in, "/work/design", "design", fake.CloneLayout{Tracked: []string{"README.md", "docs/a.md", "docs/b.md"}})
				placeClone(in, "/work/api", "api", fake.CloneLayout{Tracked: []string{"go.mod"}})
				placeClone(in, "/work/web", "web", fake.CloneLayout{Tracked: []string{"package.json"}})
			},
			request: api.AdoptionPlanRequest{Root: "/work/design", Paths: []string{"/work/api", "/work/web"}},
		},
		{
			name: "adoption-plan-practices", shape: planning.ShapePlanned,
			place: func(in *apitest.Installation) {
				placeClone(in, "/work/ws", "ws", fake.CloneLayout{Ignored: []string{"repos"}, Plan: ports.PlanContent{Files: 2}, Tracked: []string{".gitignore", "README.md", "plan/goal.md"}})
				placeClone(in, "/work/ws/repos/api", "api", fake.CloneLayout{Tracked: []string{"go.mod"}})
				placeClone(in, "/work/ws/repos/web", "web", fake.CloneLayout{Tracked: []string{"package.json"}})
				tip := "1111111111111111111111111111111111111111"
				snapshot := func(ignore bool, read func(string) (string, bool), paths ...string) practices.Snapshot {
					ignored := map[string]bool{}
					for _, probe := range practices.Probes(paths) {
						ignored[probe] = ignore
					}
					return practices.Snapshot{Commit: tip, Paths: paths, Read: read, Ignored: ignored}
				}
				in.Practices.SetSnapshot("/work/ws", snapshot(true, nil, ".gitignore", "README.md", "plan/goal.md", "CODEOWNERS", ".pre-commit-config.yaml", "SECURITY.md"))
				in.Practices.SetSnapshot("/work/ws/repos/api", snapshot(false, func(string) (string, bool) {
					return "module acme.test/api\n\nrequire example.test/dep v1.0.0\n", true
				}, "go.mod", "main.go"))
				in.Practices.SetSnapshot("/work/ws/repos/web", snapshot(true, func(string) (string, bool) {
					return `{"dependencies": {"left-pad": "1.3.0"}}`, true
				}, ".gitignore", "package.json", "package-lock.json", "CODEOWNERS", "SECURITY.md"))
			},
			request: api.AdoptionPlanRequest{Root: "/work/ws", Practices: true},
		},
		{
			name: "adoption-plan-undecided", shape: planning.ShapeWorkspaceFiles,
			place: func(in *apitest.Installation) {
				placeClone(in, "/work/api", "api", fake.CloneLayout{Tracked: []string{"go.mod"}})
				placeClone(in, "/work/web", "web", fake.CloneLayout{Tracked: []string{"package.json"}})
			},
			request: api.AdoptionPlanRequest{Root: "/work/api", Paths: []string{"/work/web"}},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			in := apitest.StartInstallation(t, "beta")
			one.place(in)
			_, _, client := in.In(t, apitest.ProjectName)
			plan, err := client.AdoptionPlan(t.Context(), one.request)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Shape != one.shape {
				t.Fatalf("the layout reads as the %s shape, want %s", plan.Shape, one.shape)
			}
			if one.request.Practices {
				// The route answers the table's words for each practice found beside the plan.
				holdsGolden(t, one.name, pretty(t, api.AdoptionPlanReply{AdoptionPlan: plan, Parts: plan.Parts(), Guides: api.AdoptionGuides(plan)}))
				return
			}
			holdsGolden(t, one.name, pretty(t, plan))
		})
	}
}

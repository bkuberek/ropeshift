package server_test

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

func milestoneFile(id, priority string, dependsOn ...string) string {
	out := "+++\nkind = \"milestone\"\nid = \"" + id + "\"\ntitle = \"Milestone " + id + "\"\nplan = \"launch\"\npriority = " + priority + "\n"
	if len(dependsOn) != 0 {
		out += "depends_on = [\"" + strings.Join(dependsOn, `", "`) + "\"]\n"
	}
	return out + "\n[[exit_criteria]]\nid = \"e1\"\ntext = \"" + id + " is shown to a person\"\n+++\n\nWhat " + id + " delivers.\n"
}

// roadmapContract writes a contract of the plan `launch`. Each work order is "<id>:<size>".
func roadmapContract(id, milestone string, shared bool, uses []string, orders ...string) string {
	out := "+++\nkind = \"contract\"\nid = \"" + id + "\"\ntitle = \"Contract " + id + "\"\nplan = \"launch\"\n"
	if milestone != "" {
		out += "milestone = \"" + milestone + "\"\n"
	}
	if shared {
		out += "shared = true\n"
	}
	if len(uses) != 0 {
		out += "uses = [\"" + strings.Join(uses, `", "`) + "\"]\n"
	}
	out += "\n[[criteria]]\nid = \"c1\"\ntext = \"It works\"\n"
	for _, order := range orders {
		name, size, _ := strings.Cut(order, ":")
		out += "\n[[work_orders]]\nid = \"" + name + "\"\ntitle = \"Work " + name + "\"\n"
		if size != "" {
			out += "size = \"" + size + "\"\n"
		}
	}
	return out + "+++\n\nThe statement of " + id + ".\n"
}

// launchDaemon serves one plan with three milestones and work that has to run in layers:
//
//	build: client (shared) has c1 (large); core uses client and has k1 (medium) and k2 (small)
//	ship, after build: ui has u1 (small)
//	polish, after ship: no contract yet
//	extras, which waits for nothing: docs has d1
func launchDaemon(t *testing.T) *apitest.Daemon {
	t.Helper()
	daemon := apitest.Start(t)
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \"launch\"\ntitle = \"Launch\"\n+++\n\nHow.\n"))
	source.Write("m-build.md", []byte(milestoneFile("build", "1")))
	source.Write("m-ship.md", []byte(milestoneFile("ship", "2", "build")))
	source.Write("m-polish.md", []byte(milestoneFile("polish", "3", "ship")))
	source.Write("m-extras.md", []byte(milestoneFile("extras", "4")))
	source.Write("client.md", []byte(roadmapContract("client", "build", true, nil, "c1:large")))
	source.Write("core.md", []byte(roadmapContract("core", "build", false, []string{"client"}, "k1:medium", "k2:small")))
	source.Write("ui.md", []byte(roadmapContract("ui", "ship", false, nil, "u1:small")))
	source.Write("docs.md", []byte(roadmapContract("docs", "extras", false, nil, "d1")))
	report, err := control.NewIntake(daemon.Store).Poll(t.Context(), apitest.Scope, source)
	if err != nil || len(report.Problems) != 0 {
		t.Fatalf("report %+v: %v", report, err)
	}
	return daemon
}

// idsByTitle names each work order of the world by the id its file gave it.
func idsByTitle(t *testing.T, daemon *apitest.Daemon) map[string]string {
	t.Helper()
	ids := map[string]string{}
	err := daemon.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		for _, row := range rows {
			ids[strings.TrimPrefix(row.Title, "Work ")] = row.ID
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func named(ids map[string]string, layer []string) []string {
	byID := map[string]string{}
	for name, id := range ids {
		byID[id] = name
	}
	out := make([]string, len(layer))
	for i, id := range layer {
		out[i] = byID[id]
	}
	return out
}

func finish(t *testing.T, daemon *apitest.Daemon, id string) {
	t.Helper()
	moveTo(t, daemon, id, workorders.Done)
}

// moveTo puts a work order in a state the way the end of its life would leave it.
func moveTo(t *testing.T, daemon *apitest.Daemon, id string, state workorders.State) {
	t.Helper()
	err := daemon.Store.Write(t.Context(), apitest.Scope, "note", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil {
			return err
		}
		if moved, err := tx.WorkOrders().Move(id, current.State, state); err != nil || !moved {
			t.Fatalf("%s did not move to %s: %v", id, state, err)
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: "note.added", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: "note", ID: id},
			Data: []byte(`{"id":"` + id + `"}`),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func launchPlan(t *testing.T, daemon *apitest.Daemon) api.PlanProgress {
	t.Helper()
	for _, group := range readView[api.Roadmap](t, daemon, api.ViewRoadmap, "").Goals {
		for _, plan := range group.Plans {
			if plan.Plan.Title == "Launch" {
				return plan
			}
		}
	}
	t.Fatal("the roadmap holds no plan titled Launch")
	return api.PlanProgress{}
}

func TestRoadmapListsMilestonesInPriorityOrderWithTheirProgress(t *testing.T) {
	daemon := launchDaemon(t)
	plan := launchPlan(t, daemon)
	type row struct {
		id, state   string
		priority    int64
		dependsOn   []string
		done, total int
	}
	var got []row
	for _, m := range plan.Milestones {
		if m.Title == "" || len(m.ExitCriteria) != 1 || m.ExitCriteria[0].ID != "e1" {
			t.Errorf("milestone %+v", m)
		}
		got = append(got, row{id: m.ID, state: m.State, priority: m.Priority, dependsOn: m.DependsOn, done: m.Done, total: m.Total})
	}
	want := []row{
		{id: "build", state: api.MilestoneNotStarted, priority: 1, total: 3},
		{id: "ship", state: api.MilestoneWaiting, priority: 2, dependsOn: []string{"build"}, total: 1},
		{id: "polish", state: api.MilestoneWaiting, priority: 3, dependsOn: []string{"ship"}},
		{id: "extras", state: api.MilestoneNotStarted, priority: 4, total: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("milestones %+v", got)
	}
	for i := range want {
		if got[i].id != want[i].id || got[i].state != want[i].state || got[i].priority != want[i].priority ||
			!slices.Equal(got[i].dependsOn, want[i].dependsOn) || got[i].done != want[i].done || got[i].total != want[i].total {
			t.Errorf("milestone %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	if plan.Total != 5 {
		t.Errorf("the plan holds %d work orders, want 5", plan.Total)
	}
}

func TestRoadmapShowsWhichWorkCanRunTogether(t *testing.T) {
	daemon := launchDaemon(t)
	ids := idsByTitle(t, daemon)
	layers := launchPlan(t, daemon).Layers
	var got [][]string
	for _, layer := range layers {
		names := named(ids, layer)
		slices.Sort(names)
		got = append(got, names)
	}
	want := [][]string{{"c1", "d1"}, {"k1", "k2"}, {"u1"}}
	if len(got) != len(want) {
		t.Fatalf("layers %v, want %v", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("layer %d is %v, want %v", i, got[i], want[i])
		}
	}
}

func TestRoadmapShowsTheCriticalPathByRemainingWeight(t *testing.T) {
	daemon := launchDaemon(t)
	ids := idsByTitle(t, daemon)
	path := launchPlan(t, daemon).CriticalPath
	// c1 (large, 3) then k1 (medium, 2) then u1 (small, 1) outweigh every other chain.
	if got := named(ids, path.WorkOrders); !slices.Equal(got, []string{"c1", "k1", "u1"}) || path.Weight != 6 {
		t.Fatalf("critical path %v of %d", got, path.Weight)
	}
	// The shared work is done: it leaves the path, and the rest of the chain is what remains.
	finish(t, daemon, ids["c1"])
	plan := launchPlan(t, daemon)
	if got := named(ids, plan.CriticalPath.WorkOrders); !slices.Equal(got, []string{"k1", "u1"}) || plan.CriticalPath.Weight != 3 {
		t.Errorf("critical path after c1 %v of %d", got, plan.CriticalPath.Weight)
	}
	// Done work stays in the layers, so progress still counts it.
	if got := named(ids, plan.Layers[0]); !slices.Contains(got, "c1") {
		t.Errorf("layer 0 is %v, and the finished work left it", got)
	}
	build := plan.Milestones[0]
	if build.Done != 1 || build.Total != 3 || build.State != api.MilestoneInProgress {
		t.Errorf("build %+v", build)
	}
	if ship := plan.Milestones[1]; ship.State != api.MilestoneWaiting {
		t.Errorf("ship waits for the rest of build: %+v", ship)
	}
	for _, name := range []string{"k1", "k2"} {
		finish(t, daemon, ids[name])
	}
	plan = launchPlan(t, daemon)
	if plan.Milestones[0].State != api.MilestoneDone || plan.Milestones[1].State != api.MilestoneNotStarted {
		t.Errorf("milestones %+v", plan.Milestones)
	}
	if got := named(ids, plan.CriticalPath.WorkOrders); !slices.Equal(got, []string{"d1"}) && !slices.Equal(got, []string{"u1"}) {
		t.Errorf("critical path with the build done %v", got)
	}
}

func TestRoadmapOfAPlanWithoutMilestonesStillShowsItsWorkInLayers(t *testing.T) {
	daemon := plannedDaemon(t)
	plan := readView[api.Roadmap](t, daemon, api.ViewRoadmap, "").Goals[0].Plans[0]
	if plan.Milestones == nil || len(plan.Milestones) != 0 {
		t.Errorf("milestones %+v", plan.Milestones)
	}
	if len(plan.Layers) != 1 || len(plan.Layers[0]) != 1 {
		t.Errorf("layers %v", plan.Layers)
	}
	if len(plan.CriticalPath.WorkOrders) != 1 || plan.CriticalPath.Weight != 1 {
		t.Errorf("critical path %+v", plan.CriticalPath)
	}
}

func TestRoadmapWeighsCanceledWorkAsNothingAndFailedWorkAsItself(t *testing.T) {
	daemon := launchDaemon(t)
	ids := idsByTitle(t, daemon)
	// Failed work has to be redone, so it keeps its weight on the path.
	moveTo(t, daemon, ids["k1"], workorders.Failed)
	path := launchPlan(t, daemon).CriticalPath
	if got := named(ids, path.WorkOrders); !slices.Equal(got, []string{"c1", "k1", "u1"}) || path.Weight != 6 {
		t.Errorf("critical path with k1 failed %v of %d", got, path.Weight)
	}
	// Canceled work will never run, so it leaves the weight, like work that is done.
	moveTo(t, daemon, ids["c1"], workorders.Canceled)
	path = launchPlan(t, daemon).CriticalPath
	if got := named(ids, path.WorkOrders); !slices.Equal(got, []string{"k1", "u1"}) || path.Weight != 3 {
		t.Errorf("critical path with c1 canceled %v of %d", got, path.Weight)
	}
	moveTo(t, daemon, ids["k1"], workorders.Canceled)
	path = launchPlan(t, daemon).CriticalPath
	if got := named(ids, path.WorkOrders); !slices.Equal(got, []string{"k2", "u1"}) || path.Weight != 2 {
		t.Errorf("critical path with k1 canceled too %v of %d", got, path.Weight)
	}
}

// A layer and the critical path name work orders by ID, which says nothing to a person, so the plan names
// each work order it places: its title, state, size, and milestone.
func TestRoadmapNamesEachWorkOrderItPlaces(t *testing.T) {
	daemon := launchDaemon(t)
	ids := idsByTitle(t, daemon)
	plan := launchPlan(t, daemon)
	byID := map[string]api.PlannedWork{}
	for _, work := range plan.Work {
		byID[work.ID] = work
	}
	if len(plan.Work) != plan.Total {
		t.Errorf("the plan names %d work orders of %d: %+v", len(plan.Work), plan.Total, plan.Work)
	}
	for _, layer := range plan.Layers {
		for _, id := range layer {
			if _, named := byID[id]; !named {
				t.Errorf("layer work %s is not named", id)
			}
		}
	}
	core := byID[ids["k1"]]
	if core.Title != "Work k1" || core.Size != "medium" || core.Milestone != "build" || core.State != workorders.ContractReview {
		t.Errorf("k1 reads %+v", core)
	}
	if docs := byID[ids["d1"]]; docs.Milestone != "extras" || docs.Title != "Work d1" {
		t.Errorf("d1 reads %+v", docs)
	}
}

// ADR-0023: a milestone the code already delivers reads as existing, and the milestone after it does not
// wait for it.
func TestRoadmapShowsAMilestoneTheCodeDelivers(t *testing.T) {
	daemon := apitest.Start(t)
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \"launch\"\ntitle = \"Launch\"\n+++\n\nHow.\n"))
	source.Write("m-today.md", []byte(strings.Replace(milestoneFile("today", "0"), "priority = 0\n", "priority = 0\nexisting = true\n", 1)))
	source.Write("m-build.md", []byte(milestoneFile("build", "1", "today")))
	source.Write("core.md", []byte(roadmapContract("core", "build", false, nil, "k1:small")))
	if report, err := control.NewIntake(daemon.Store).Poll(t.Context(), apitest.Scope, source); err != nil || len(report.Problems) != 0 {
		t.Fatalf("report %+v: %v", report, err)
	}
	plan := launchPlan(t, daemon)
	if len(plan.Milestones) != 2 {
		t.Fatalf("milestones %+v", plan.Milestones)
	}
	if today := plan.Milestones[0]; today.ID != "today" || today.State != api.MilestoneExisting || !today.Existing || today.Total != 0 {
		t.Errorf("the milestone the code delivers reads %+v", today)
	}
	if build := plan.Milestones[1]; build.ID != "build" || build.State != api.MilestoneNotStarted {
		t.Errorf("the milestone after it reads %+v", build)
	}
}

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// ADR-0023: before a lane that adopts a project starts, the worker writes the inventory of the repository it
// works in and of every repository it reads into its context folder, each at the commit its claim recorded,
// and one it cannot read is there with the reason.
func TestALaneThatAsksForAnInventoryFindsItInItsContextFolder(t *testing.T) {
	worktrees := fake.NewWorktrees()
	inventory := fake.NewInventory()
	inventory.Put("repo-plan", planning.RepositoryInventory{Documents: []planning.InventoryDocument{{Path: "README.md", Digest: "gitblob:1", FrontMatter: planning.FrontMatterNone}}})
	inventory.Put("repo-api", planning.RepositoryInventory{Branches: []planning.InventoryBranch{{Name: "feature/retry", Ref: "refs/heads/feature/retry", Tip: "t", Ahead: 2}}})
	checkouts := map[string]string{"repo-plan": "/checkouts/plan", "repo-api": "/checkouts/api"}
	var projects []string
	run := &laneRun{
		lease: ledger.DesiredLease{
			Project: "p1", WorkOrder: "wo", Kind: "plan", Lane: "lane-1", Repository: "repo-plan", RepositoryName: "plan",
			BaseRef: "origin/main", BaseCommit: "base-plan", Inventory: true,
			Reads: []ledger.RepositoryRead{
				{Repository: "repo-api", Name: "api", Ref: "origin/main", Commit: "base-api"},
				{Repository: "repo-web", Name: "web", Detail: "no checkout of it on this machine"},
			},
		},
		rt: &LaneRuntime{
			Provisioner: Provisioner{Worktrees: worktrees},
			Inventory:   inventory,
			Repository: func(_ context.Context, project, id string) (string, error) {
				projects = append(projects, project)
				if path, ok := checkouts[id]; ok {
					return path, nil
				}
				return "", fmt.Errorf("no checkout of %s", id)
			},
		},
	}
	if err := run.writeInventory(t.Context(), "/checkouts/plan", "/lanes/lane-1"); err != nil {
		t.Fatal(err)
	}
	text, ok := worktrees.ContextFile("/lanes/lane-1", "inventory.json")
	if !ok {
		t.Fatal("no inventory in the lane's context folder")
	}
	var written planning.Inventory
	if err := json.Unmarshal([]byte(text), &written); err != nil {
		t.Fatal(err)
	}
	if len(written.Repositories) != 3 {
		t.Fatalf("the inventory holds %+v", written.Repositories)
	}
	api, plan, web := written.Repositories[0], written.Repositories[1], written.Repositories[2]
	if api.Name != "api" || api.BaseCommit != "base-api" || len(api.Branches) != 1 {
		t.Errorf("api reads %+v", api)
	}
	if plan.Name != "plan" || plan.BaseCommit != "base-plan" || plan.BaseBranch != "origin/main" || len(plan.Documents) != 1 {
		t.Errorf("plan reads %+v", plan)
	}
	if web.Name != "web" || web.Detail == "" || web.BaseCommit != "" {
		t.Errorf("web reads %+v", web)
	}
	asked := inventory.Asked()
	if len(asked) != 2 || asked[0].Path != "/checkouts/plan" || asked[1].Path != "/checkouts/api" || asked[1].BaseCommit != "base-api" {
		t.Errorf("the inventory was asked %+v", asked)
	}
	if slices.ContainsFunc(projects, func(project string) bool { return project != "p1" }) {
		t.Errorf("checkouts were looked up in projects %v, and the lane is in p1", projects)
	}
	// The same commits give the same file.
	if err := run.writeInventory(t.Context(), "/checkouts/plan", "/lanes/lane-1"); err != nil {
		t.Fatal(err)
	}
	if again, _ := worktrees.ContextFile("/lanes/lane-1", "inventory.json"); again != text {
		t.Errorf("a second inventory at the same commits differs:\n%s\n%s", text, again)
	}
}

func TestALaneThatAsksForNoInventoryTakesNone(t *testing.T) {
	worktrees := fake.NewWorktrees()
	inventory := fake.NewInventory()
	run := &laneRun{lease: ledger.DesiredLease{WorkOrder: "wo", Lane: "lane-1"}, rt: &LaneRuntime{Provisioner: Provisioner{Worktrees: worktrees}, Inventory: inventory}}
	if err := run.writeInventory(t.Context(), "/checkouts/plan", "/lanes/lane-1"); err != nil || len(inventory.Asked()) != 0 {
		t.Errorf("a lane that asks for none: %v, asked %+v", err, inventory.Asked())
	}
	run.lease.Inventory, run.rt.Inventory = true, nil
	if err := run.writeInventory(t.Context(), "/checkouts/plan", "/lanes/lane-1"); !errors.Is(err, errNoInventory) {
		t.Errorf("a worker with no inventory: %v", err)
	}
	var _ ports.Inventory = inventory
}

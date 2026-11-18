package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// pullInventory writes the inventory of one repository whose remote is remote, on a worker with the forge, and
// reads what it wrote.
func pullInventory(t *testing.T, remote string, forge ports.Forge) planning.RepositoryInventory {
	t.Helper()
	worktrees := fake.NewWorktrees()
	inventory := fake.NewInventory()
	inventory.Put("repo-plan", planning.RepositoryInventory{Remote: remote})
	run := &laneRun{
		lease: ledger.DesiredLease{Project: "p1", WorkOrder: "wo", Lane: "lane-1", Repository: "repo-plan", RepositoryName: "plan", BaseCommit: "base", Inventory: true},
		rt:    &LaneRuntime{Provisioner: Provisioner{Worktrees: worktrees}, Inventory: inventory, Forge: forge},
	}
	if err := run.writeInventory(t.Context(), "/checkouts/plan", "/lanes/lane-1"); err != nil {
		t.Fatal(err)
	}
	text, _ := worktrees.ContextFile("/lanes/lane-1", "inventory.json")
	var written planning.Inventory
	if err := json.Unmarshal([]byte(text), &written); err != nil || len(written.Repositories) != 1 {
		t.Fatalf("the inventory reads %q: %v", text, err)
	}
	return written.Repositories[0]
}

// omission reads the reason a repository's inventory gives for leaving its pull requests out, or nothing.
func omission(repository planning.RepositoryInventory) string {
	for _, omitted := range repository.Omitted {
		if omitted.Part == planning.PartPullRequests {
			return omitted.Reason
		}
	}
	return ""
}

// FR-ADOPT-2: the inventory lists a repository's open pull requests when its remote names a forge repository
// and this worker's sign-in for it is healthy, and otherwise says why it does not.
func TestTheInventoryListsOpenPullRequestsOrSaysWhyNot(t *testing.T) {
	const remote = "git@github.com:acme/widgets.git"
	binding := ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}
	held := []ledger.PullRequestSummary{
		{Number: 12, Title: "Retry a failed charge", Branch: "feature/retry", Base: "main", URL: "https://github.com/acme/widgets/pull/12", UpdatedAt: "2026-10-02T14:05:00Z"},
		{Number: 9, Title: "Draft: new ledger view", Branch: "wip/ledger", Base: "main", URL: "https://github.com/acme/widgets/pull/9", Draft: true, UpdatedAt: "2026-09-30T08:00:00Z"},
	}
	tests := []struct {
		name   string
		remote string
		setup  func(*fake.Forge)
		// forge is false for a worker that has none.
		noForge bool
		reason  string
	}{
		{name: "a binding and a healthy forge", remote: remote, setup: func(f *fake.Forge) { f.HoldOpen(binding, held...) }},
		{name: "a remote that names no forge repository", remote: "/srv/git/widgets.git", reason: "no forge binding"},
		{name: "no remote", reason: "no forge binding"},
		{name: "a worker with no forge", remote: remote, noForge: true, reason: "no forge adapter"},
		{name: "a signed-out forge", remote: remote, setup: func(f *fake.Forge) { f.SignOut(binding.Host, "token expired") }, reason: "signed_out on this worker: token expired"},
		{name: "a missing CLI", remote: remote, setup: func(f *fake.Forge) { f.Remove() }, reason: "missing on this worker"},
		{name: "a forge that cannot be reached", remote: remote, setup: func(f *fake.Forge) { f.Fail = errors.New("connection refused") }, reason: "health could not be read: connection refused"},
		{name: "a listing the forge refuses", remote: remote, setup: func(f *fake.Forge) { f.Limit(ports.ForgeRate{Secondary: true}) }, reason: "could not be listed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forge := &fake.Forge{}
			if tt.setup != nil {
				tt.setup(forge)
			}
			var worker ports.Forge = forge
			if tt.noForge {
				worker = nil
			}
			got := pullInventory(t, tt.remote, worker)
			if tt.reason == "" {
				if omission(got) != "" || got.MorePullRequests || len(got.PullRequests) != 2 || got.PullRequests[0].Number != 9 || got.PullRequests[1] != held[0] || !got.PullRequests[0].Draft {
					t.Errorf("the inventory lists %+v, more %v, omitted %+v", got.PullRequests, got.MorePullRequests, got.Omitted)
				}
				return
			}
			if reason := omission(got); !strings.Contains(reason, tt.reason) || len(got.PullRequests) != 0 {
				t.Errorf("the inventory omits them as %q and lists %+v, want a reason with %q", reason, got.PullRequests, tt.reason)
			}
		})
	}
}

// A repository with more open pull requests than the most a listing holds says so.
func TestTheInventoryListsTheMostOpenPullRequestsAndSaysMoreExist(t *testing.T) {
	forge := &fake.Forge{}
	var pulls []ledger.PullRequestSummary
	for number := int64(1); number <= ports.MaxOpenPullRequests+1; number++ {
		pulls = append(pulls, ledger.PullRequestSummary{Number: number, Branch: fmt.Sprintf("b%d", number)})
	}
	forge.HoldOpen(ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}, pulls...)
	got := pullInventory(t, "https://github.com/acme/widgets", forge)
	if !got.MorePullRequests || len(got.PullRequests) != ports.MaxOpenPullRequests || got.PullRequests[0].Number != 2 || omission(got) != "" {
		t.Errorf("the inventory lists %d pull requests, more %v, omitted %+v", len(got.PullRequests), got.MorePullRequests, got.Omitted)
	}
}

// A repository the inventory could not read has no pull requests listed, and says why.
func TestTheInventoryOmitsThePullRequestsOfARepositoryItCouldNotRead(t *testing.T) {
	worktrees := fake.NewWorktrees()
	run := &laneRun{
		lease: ledger.DesiredLease{
			Project: "p1", WorkOrder: "wo", Lane: "lane-1", Repository: "repo-plan", RepositoryName: "plan", BaseCommit: "base", Inventory: true,
			Reads: []ledger.RepositoryRead{{Repository: "repo-web", Name: "web", Detail: "no checkout of it on this machine"}},
		},
		rt: &LaneRuntime{Provisioner: Provisioner{Worktrees: worktrees}, Inventory: fake.NewInventory(), Forge: &fake.Forge{}},
	}
	if err := run.writeInventory(t.Context(), "/checkouts/plan", "/lanes/lane-1"); err != nil {
		t.Fatal(err)
	}
	text, _ := worktrees.ContextFile("/lanes/lane-1", "inventory.json")
	var written planning.Inventory
	if err := json.Unmarshal([]byte(text), &written); err != nil || len(written.Repositories) != 2 {
		t.Fatalf("the inventory reads %q: %v", text, err)
	}
	for _, repository := range written.Repositories {
		if omission(repository) != unreadReason || len(repository.PullRequests) != 0 {
			t.Errorf("%s lists %+v and omits them as %q", repository.Name, repository.PullRequests, omission(repository))
		}
	}
}

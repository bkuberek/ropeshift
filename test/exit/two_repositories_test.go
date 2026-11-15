package exit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
	"ropeshift.com/ropeshift/test/gittest"
)

// Where the fake forge says the two code repositories live. Their remotes are bare repositories on this
// machine, which name no forge, so each binding is named.
const (
	apiBinding = "github.com/acme/api"
	webBinding = "github.com/acme/web"
)

// writesTheAPI is a lane that adds the api and commits it on its own branch.
const writesTheAPI = `{
  "source": "a lane that adds the api",
  "turns": [[
    {"init": true},
    {"tool": "Bash", "command": "echo 'package api' > api.go && git add api.go && git commit -q -m 'Add the api'"},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "artifacts": ["branch"], "checks": [{"name": "stand-in", "passed": true}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`

// buildsOnTheAPI is the web client's lane: it reaches for a commit in its read-only worktree of the api, which
// it may not make, and commits on its own branch the commit that worktree stands at and the files it holds.
const buildsOnTheAPI = `{
  "source": "a lane that reads the api it builds on and tries to write there",
  "turns": [[
    {"init": true},
    {"tool": "Bash", "command": "git -C ../lane-web.reads/api commit -q --allow-empty -m nope"},
    {"tool": "Bash", "command": "git -C ../lane-web.reads/api log --format=%H -1 > reads.txt && ls ../lane-web.reads/api >> reads.txt && git add reads.txt && git commit -q -m 'Build the client on the api'"},
    {"mcp": "lane_report", "arguments": {"outcome": "completed", "artifacts": ["branch"], "checks": [{"name": "stand-in", "passed": true}]}},
    {"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": {"input_tokens": 3, "output_tokens": 5}}}
  ]]
}`

// planContract is a contract of the plan in the milestone, with one work order that changes repo, and that
// depends on another work order when dependsOn names one.
func planContract(id, title, repo, dependsOn string) string {
	text := "+++\nkind = \"contract\"\nid = \"" + id + "\"\ntitle = \"The " + id + " contract\"\nplan = \"client\"\nmilestone = \"m1\"\n\n" +
		"[[criteria]]\nid = \"c1\"\ntext = \"It works\"\n\n" +
		"[[work_orders]]\nid = \"" + id + "-order\"\ntitle = \"" + title + "\"\nrepo = \"" + repo + "\"\nsize = \"small\"\n"
	if dependsOn != "" {
		text += "depends_on = [\"" + dependsOn + "\"]\n"
	}
	return text + "+++\n\n" + title + ".\n"
}

// twoRepositoryPlan is one plan as the planning repository holds it: one milestone, a contract whose work
// changes the api, and one whose work changes the web client and depends on the api's.
func twoRepositoryPlan() [][2]string {
	return [][2]string{
		{"plans/plan.md", "+++\nkind = \"plan\"\nid = \"client\"\ntitle = \"The client\"\n+++\n\nBuild the api, then the web client on it.\n"},
		{"plans/m1.md", "+++\nkind = \"milestone\"\nid = \"m1\"\ntitle = \"The client ships\"\nplan = \"client\"\npriority = 1\n+++\n\nThe api and its client.\n"},
		{"plans/api.md", planContract("api", "Build the api", "api", "")},
		{"plans/web.md", planContract("web", "Build the web client", "web", "api-order")},
	}
}

// cloneWithRemote makes a repository whose base branch holds these files, gives it a bare repository on this
// machine as its remote, so a push reaches no real remote, and clones that into dir under its name, as a
// person's folder of clones holds it. It returns the clone and the remote. A repository's identity is its
// root commit, so each history begins with a commit that names its repository, as distinct repositories'
// histories begin apart.
func cloneWithRemote(t *testing.T, dir, name string, files ...[2]string) (*gittest.Repo, *gittest.Repo) {
	t.Helper()
	first := gittest.New(t)
	first.Commit("README.txt", "The "+name+" repository.\n")
	for _, file := range files {
		first.Commit(file[0], file[1])
	}
	remote := &gittest.Repo{T: t, Dir: filepath.Join(t.TempDir(), name+".git"), Home: first.Home}
	first.Git("clone", "--quiet", "--bare", first.Dir, remote.Dir)
	clone := &gittest.Repo{T: t, Dir: filepath.Join(dir, name), Home: first.Home}
	first.Git("clone", "--quiet", remote.Dir, clone.Dir)
	return clone, remote
}

// pullRequestOf waits for the open pull request of a branch in the repository a binding names, with the lane's
// commit as its head on the remote, and returns it.
func (in *install) pullRequestOf(gh, binding, branch string, repo *gittest.Repo, head string) ghstate.PullRequest {
	in.t.Helper()
	var found ghstate.PullRequest
	in.waitFor("the pull request of "+branch+" in "+binding, func() bool {
		for _, pr := range doubles.FakeGH(in.t, gh).PullRequests {
			if pr.Repo == binding && pr.Branch == branch && pr.State == "OPEN" && repo.RemoteHead("origin", "refs/heads/"+branch) == head {
				found = pr
				return true
			}
		}
		return false
	})
	return found
}

// mergeOf is the commit the fake forge's merge of a pull request made.
func mergeOf(t *testing.T, gh string, number int64) string {
	t.Helper()
	state := doubles.FakeGH(t, gh)
	pr := state.Find(number)
	if pr == nil || pr.State != "MERGED" || pr.MergeCommit == "" {
		t.Fatalf("pull request %d reads %+v", number, pr)
	}
	return pr.MergeCommit
}

// milestone reads one milestone of the roadmap.
func (in *install) milestone(id string) api.MilestoneProgress {
	in.t.Helper()
	var roadmap api.Roadmap
	if err := json.Unmarshal([]byte(in.must("view", api.ViewRoadmap)), &roadmap); err != nil {
		in.t.Fatal(err)
	}
	for _, goal := range roadmap.Goals {
		for _, plan := range goal.Plans {
			for _, row := range plan.Milestones {
				if row.ID == id {
					return row
				}
			}
		}
	}
	in.t.Fatalf("the roadmap holds no milestone %s: %+v", id, roadmap)
	return api.MilestoneProgress{}
}

// TestOnePlanAcrossTwoRepositoriesMergesInDependencyOrder is C3's acceptance on the real binary, with real
// git, the fake claude, the fake gh, and bare repositories as the remotes: nothing reaches GitHub (ADR-0022,
// FR-REPO-2 to FR-REPO-4, FR-REPO-7, FR-REPO-9 to FR-REPO-12). One `repository add` of a folder registers the
// planning repository and two code repositories, and one `repository set` of both code repositories sends
// their work through pull requests. One plan in the planning repository has a work order in each, and the web
// client's depends on the api's. The api's work merges through its pull request first. The web client's lane
// then reads the api at that merge, through a read-only worktree it cannot write in, though the api's base
// has moved on since, and its review reads the same commit. The web client's pull request merges after the
// api's, and the milestone is done only once both have merged. The web client's claim fetches the api's base
// at the moment another fetch holds git's lock on the api's remote-tracking ref, which intake's own fetch of
// the clone can do on a real machine, and the lane still reads the api.
func TestOnePlanAcrossTwoRepositoriesMergesInDependencyOrder(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	gh := in.aForge()
	// A clone is registered at the path with every link resolved, as git names its top.
	clones, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := [2]string{".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n"}
	plansRepo, _ := cloneWithRemote(t, clones, "plans", profile)
	apiRepo, apiRemote := cloneWithRemote(t, clones, "api", profile, [2]string{"server.go", "package api\n"})
	webRepo, _ := cloneWithRemote(t, clones, "web", profile, [2]string{"app.go", "package web\n"})
	armRefLock, refLockHeld := in.aGitThatLosesTheRefLock(apiRepo.Dir)

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--forge-tick", "100ms")
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })

	// One folder registers all three repositories, and one change sets both code repositories.
	added := in.must("repository", "add", clones)
	for _, name := range []string{"api", "plans", "web"} {
		if !strings.Contains(added, "registered\t"+name+"\t") {
			t.Fatalf("the folder did not register %s:\n%s", name, added)
		}
	}
	in.must("repository", "set", "plans", "--planning")
	// Each remote names no forge, so each binding is named before a pull request can be the review target.
	in.must("repository", "set", "api", "--forge-binding", apiBinding)
	in.must("repository", "set", "web", "--forge-binding", webBinding)
	if set := in.must("repository", "set", "api", "web", "--review-target", "pull_request"); strings.Count(set, "review target\tpull_request\n") != 2 {
		t.Fatalf("one change of both code repositories printed:\n%s", set)
	}
	in.must("repository", "set", "api", "web", "--merge-authority", "review_approves")
	repositories := map[string]api.RepositoryView{}
	for _, name := range []string{"api", "plans", "web"} {
		var view api.RepositoryView
		if err := json.Unmarshal([]byte(in.must("repository", "show", name, "--json")), &view); err != nil {
			t.Fatal(err)
		}
		repositories[name] = view
	}
	for _, name := range []string{"api", "web"} {
		if view := repositories[name]; view.Target() != ledger.ReviewOnPullRequest || view.Level() != ledger.ReviewApproves || view.PlanOfRecord {
			t.Errorf("%s reads %s at %s, planning %t", name, view.Target(), view.Level(), view.PlanOfRecord)
		}
		in.waitFor(name+"'s gh sign-in on record", func() bool {
			return strings.Contains(in.must("repository", "show", name), "forge\tsolo\tok")
		})
	}
	if !repositories["plans"].PlanOfRecord || repositories["plans"].Target() != ledger.ReviewOnBranch {
		t.Errorf("the planning repository reads %+v", repositories["plans"])
	}

	// The plan lands on the planning repository's base branch, where intake reads it, and each work order
	// targets the repository its `repo` names.
	for _, file := range twoRepositoryPlan() {
		plansRepo.Commit(file[0], file[1])
	}
	plansRepo.Git("push", "--quiet", "origin", "main")
	ids := in.workOrders(2)
	apiOrder, webOrder := ids["Build the api"], ids["Build the web client"]
	if got := in.show(apiOrder).RepositoryID; got != repositories["api"].ID {
		t.Errorf("the api's work targets %q, not the api %q", got, repositories["api"].ID)
	}
	if got := in.show(webOrder).RepositoryID; got != repositories["web"].ID {
		t.Errorf("the web client's work targets %q, not the web client %q", got, repositories["web"].ID)
	}
	in.must("work", "review", apiOrder)
	code, stdout, stderr := in.run("work", "review", webOrder)
	if code != cli.ExitNotReady || !strings.Contains(stdout, `not ready: dependencies: dependency "api-order"`) {
		t.Fatalf("the review of work whose dependency is in the other repository: exit %d: %s%s", code, stdout, stderr)
	}

	// The api's work goes through its pull request, and its review merges it.
	in.playing(writesTheAPI)
	in.startLane(apiOrder, "claim-api", "lane-api")
	in.waitFor("the api lane's outcome", func() bool { return in.show(apiOrder).State == workorders.Acceptance })
	apiPR := in.pullRequestOf(gh, apiBinding, "lane/lane-api", apiRepo, apiRepo.Git("rev-parse", "refs/heads/lane/lane-api"))
	in.judgeThroughPR(apiOrder, "claim-api-review", "review-api", reviewSays("pass", passedReason, "api.go:1"))
	in.waitFor("the api's merge to end its work", func() bool { return in.show(apiOrder).State == workorders.Done })
	// From here the next fetch of the api's base in the api's clone finds its ref locked, once.
	armRefLock()
	apiMerge := mergeOf(t, gh, apiPR.Number)
	if tip := apiRemote.Git("rev-parse", "refs/heads/main"); tip != apiMerge {
		t.Fatalf("the api's remote main is %s, want the merge %s", tip, apiMerge)
	}
	if m1 := in.milestone("m1"); m1.State != api.MilestoneInProgress || m1.Done != 1 || m1.Total != 2 {
		t.Errorf("with only the api merged the milestone reads %+v", m1)
	}

	// The api's base moves on past the merge, and the web client's work, ready once the api's is done, reads
	// the api at the merge.
	moved := apiRemote.Advance("main")
	in.waitFor("the web client's work to be ready", func() bool { return in.show(webOrder).State == workorders.Ready })
	in.playing(buildsOnTheAPI)
	in.startLane(webOrder, "claim-web", "lane-web")
	in.waitFor("the web client lane's outcome", func() bool { return in.show(webOrder).State == workorders.Acceptance })
	if !refLockHeld() {
		t.Error("the web client's claim never met a lock on the api's remote-tracking ref")
	}
	// The api's clone has a remote, so its base is the remote's, as a lane's own base is.
	want := []ledger.RepositoryRead{{Repository: repositories["api"].ID, Name: "api", Ref: "origin/main", Commit: apiMerge}}
	if reads := in.lane("lane-web").Reads; !slices.Equal(reads, want) {
		t.Errorf("the web client's lane reads %+v, want %+v", reads, want)
	}
	if prompts := in.firstPrompts(t); !strings.Contains(prompts, "lane-web.reads/api") || !strings.Contains(prompts, apiMerge) {
		t.Errorf("the brief does not name the read-only worktree of the api: %q", prompts)
	}
	webHead := webRepo.Git("rev-parse", "refs/heads/lane/lane-web")
	seen := webRepo.Git("show", webHead+":reads.txt")
	if !strings.HasPrefix(seen, apiMerge+"\n") || !strings.Contains(seen, "api.go") || strings.Contains(seen, moved) {
		t.Errorf("the lane read in the api, which merged as %s and moved on to %s:\n%s", apiMerge, moved, seen)
	}
	if denied := deniedCalls(in); !slices.Contains(denied, lanes.ReasonReadOnlyTree) {
		t.Errorf("a commit in the api was not denied: %q", denied)
	}
	readOnly := filepath.Join(clones, "web-lanes", "lane-web.reads")
	in.waitFor("the read-only worktree to go", func() bool {
		_, err := os.Stat(readOnly)
		return errors.Is(err, os.ErrNotExist)
	})

	// The base moves again, and the review reads what the lane read. A required check still runs when the
	// review passes, so the web client's work waits, and the milestone is not done while it waits.
	webPR := in.pullRequestOf(gh, webBinding, "lane/lane-web", webRepo, webHead)
	in.checksAre(gh, webOrder, webPR.Number, webHead, []string{"CI result"}, ciResult("IN_PROGRESS", ""))
	apiRemote.Advance("main")
	in.judgeThroughPR(webOrder, "claim-web-review", "review-web", reviewSays("pass", passedReason, "reads.txt:1"))
	if reads := in.lane("review-web").Reads; !slices.Equal(reads, want) {
		t.Errorf("the review reads %+v, want what its lane read, %+v", reads, want)
	}
	if prompts := in.firstPrompts(t); !strings.Contains(prompts, "review-web.reads/api") {
		t.Errorf("the review's brief does not name the read-only worktree of the api: %q", prompts)
	}
	in.waitFor("the web client's work to wait for its check", func() bool { return in.show(webOrder).State == workorders.Integrating })
	if m1 := in.milestone("m1"); m1.State != api.MilestoneInProgress || m1.Done != 1 {
		t.Errorf("with the web client approved and not merged the milestone reads %+v", m1)
	}
	if calls := mergeCalls(t, gh); len(calls) != 1 || !slices.Contains(calls[0], apiBinding) {
		t.Fatalf("before the web client's check passed the worker's gh merged %q", calls)
	}

	// Green, the web client's pull request merges after the api's, and the milestone is done.
	doubles.EditFakeGH(t, gh, func(s *ghstate.State) {
		s.Find(webPR.Number).Checks = []ghstate.Check{ciResult("COMPLETED", "SUCCESS")}
	})
	in.waitFor("the web client's merge to end its work", func() bool { return in.show(webOrder).State == workorders.Done })
	calls := mergeCalls(t, gh)
	if len(calls) != 2 || !slices.Contains(calls[0], apiBinding) || !slices.Contains(calls[1], webBinding) || !slices.Contains(calls[1], webHead) {
		t.Fatalf("the worker's gh merged %q, want the api's and then the web client's at %s", calls, webHead)
	}
	mergeOf(t, gh, webPR.Number)
	if m1 := in.milestone("m1"); m1.State != api.MilestoneDone || m1.Done != 2 || m1.Total != 2 {
		t.Errorf("with both merged the milestone reads %+v", m1)
	}
}

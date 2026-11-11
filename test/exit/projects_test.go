package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestTwoProjectsShareOneNeedsYouOnTheBinary is FR-PROJ-8 through the real daemon and the real command
// line: work that waits in two projects shows in one Needs you, each item with its project, in `needs`,
// `top`, and `project list`. Once the second project is archived it shows in none of them, because an
// archived project takes no change.
func TestTwoProjectsShareOneNeedsYouOnTheBinary(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	in.must("project", "create", "beta")
	in.must("work", "create", "--project", ledger.DefaultProjectName, "--id", "wo-home", "--title", "Add the retry budget", "--criterion", "It retries")
	in.must("work", "create", "--project", "beta", "--id", "wo-beta", "--title", "Ship the export", "--criterion", "It exports")

	var view api.NeedsYou
	if err := json.Unmarshal([]byte(in.must("needs", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, item := range view.Items {
		if item.Project != nil {
			labels[item.ID] = item.Project.Name
		}
	}
	if labels["wo-home"] != ledger.DefaultProjectName || labels["wo-beta"] != "beta" {
		t.Errorf("needs labels %v", labels)
	}
	// top shows one project's lanes, so it takes its project as any project command does, and lists what
	// waits in every project above them.
	frame := in.must("top", "--once", "--width", "200", "--project", ledger.DefaultProjectName)
	for _, want := range []string{"wo-home", "wo-beta", "beta "} {
		if !strings.Contains(frame, want) {
			t.Errorf("top lacks %q:\n%s", want, frame)
		}
	}
	list := in.must("project", "list")
	if strings.Count(list, "1 waiting") != 2 {
		t.Errorf("project list:\n%s", list)
	}

	in.must("project", "archive", "beta")
	if needs := in.must("needs"); strings.Contains(needs, "wo-beta") || !strings.Contains(needs, "wo-home") {
		t.Errorf("needs with beta archived:\n%s", needs)
	}
}

// cloneWithContract makes a repository of its own whose base branch holds one contract, and clones it into
// dir under its name, as a person's folder of clones holds it.
func cloneWithContract(t *testing.T, dir, name, title string) string {
	t.Helper()
	origin := gittest.New(t)
	origin.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	origin.Commit("contract.md", contract.ContractText(name, title, "Change "+name+"."))
	path := filepath.Join(dir, name)
	origin.Git("clone", "--quiet", origin.Dir, path)
	return path
}

// orderIn waits for intake to read one work order in a project, and returns it.
func (in *install) orderIn(project string) workorders.WorkOrder {
	in.t.Helper()
	var list api.WorkOrderList
	in.waitFor("intake in "+project, func() bool {
		if err := json.Unmarshal([]byte(in.must("work", "list", "--json", "--project", project)), &list); err != nil {
			in.t.Fatal(err)
		}
		return len(list.WorkOrders) == 1
	})
	return list.WorkOrders[0]
}

func (in *install) stateIn(project, id string) workorders.State {
	in.t.Helper()
	var row workorders.WorkOrder
	if err := json.Unmarshal([]byte(in.must("work", "show", id, "--json", "--project", project)), &row); err != nil {
		in.t.Fatal(err)
	}
	return row.State
}

// TestOneWorkerRunsLanesInTwoProjects is ADR-0021 and ADR-0022 on the real binary: `repository add`
// registers a clone in each of two projects from a folder of clones, refusing the second project the clone
// the first holds without stopping the rest, and one daemon's one worker runs a lane in each project, from
// that project's checkout, with each lane's work in its own project's ledger.
func TestOneWorkerRunsLanesInTwoProjects(t *testing.T) {
	in := newInstall(t)
	in.aLaneThatCommits()
	// A clone is registered at the path with every link resolved, as git names its top.
	clones, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alphaClone := cloneWithContract(t, clones, "alpha", "Alpha")
	betaClone := cloneWithContract(t, clones, "beta", "Beta")
	home := ledger.DefaultProjectName

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms")
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	in.must("project", "create", "beta")
	if out := in.must("repository", "add", alphaClone, "--project", home); !strings.Contains(out, "registered\talpha\t") {
		t.Fatalf("registering alpha:\n%s", out)
	}

	code, out, _ := in.run("repository", "add", clones, "--project", "beta", "--dry-run")
	if code != cli.ExitConflict || !strings.Contains(out, "would register\tbeta\t") || !strings.Contains(out, "refused\t"+alphaClone) || !strings.Contains(out, "project "+home) {
		t.Fatalf("a dry run of the folder in beta: exit %d\n%s", code, out)
	}
	if list := in.must("repository", "list", "--project", "beta"); list != "" {
		t.Fatalf("a dry run registered:\n%s", list)
	}
	code, out, _ = in.run("repository", "add", clones, "--project", "beta")
	if code != cli.ExitConflict || !strings.Contains(out, "registered\tbeta\t") || !strings.Contains(out, "refused\t"+alphaClone) {
		t.Fatalf("the folder in beta: exit %d\n%s", code, out)
	}

	worker := in.workers()[0].ID
	runs := map[string]struct{ order, lane, clone string }{
		home:   {lane: "lane-alpha", clone: alphaClone},
		"beta": {lane: "lane-beta", clone: betaClone},
	}
	for project, run := range runs {
		order := in.orderIn(project)
		run.order = order.ID
		runs[project] = run
		var record api.RepositoryView
		if err := json.Unmarshal([]byte(in.must("repository", "show", filepath.Base(run.clone), "--json", "--project", project)), &record); err != nil {
			t.Fatal(err)
		}
		if order.RepositoryID != record.ID {
			t.Errorf("%s's work targets %q, not its repository %q", project, order.RepositoryID, record.ID)
		}
		in.must("work", "review", run.order, "--project", project)
		in.must("work", "claim", run.order, "--worker", worker, "--claim-id", "claim-"+run.lane, "--project", project)
		in.must("lane", "start", run.order, "--lane", run.lane, "--project", project)
		doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", run.lane))
	}
	for project, run := range runs {
		in.waitFor(project+"'s lane outcome", func() bool { return in.stateIn(project, run.order) == workorders.Acceptance })
		worktree := filepath.Join(filepath.Dir(run.clone), filepath.Base(run.clone)+"-lanes", run.lane)
		if contents, err := os.ReadFile(filepath.Join(worktree, "work.txt")); err != nil || strings.TrimSpace(string(contents)) != "work" {
			t.Errorf("%s's lane did not work in its own checkout: %q, %v", project, contents, err)
		}
	}
	if code, _, _ := in.run("lane", "show", "lane-beta", "--project", home); code != cli.ExitNotFound {
		t.Errorf("the first project sees beta's lane: exit %d", code)
	}
	if code, _, _ := in.run("work", "show", runs["beta"].order, "--project", home); code != cli.ExitNotFound {
		t.Errorf("the first project sees beta's work: exit %d", code)
	}
}

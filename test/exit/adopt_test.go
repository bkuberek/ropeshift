package exit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// The shop's documents before adoption: a README, requirements with the team's own IDs, and a page whose
// front matter belongs to a site generator.
const (
	shopReadme       = "# Shop\n\nA small shop that takes payments.\n"
	shopRequirements = "# Shop requirements\n\nWhat the shop must do, as the team wrote it.\n\n## SHOP-1: One client\n\n" +
		"Every payment call goes through one client.\n\n## SHOP-2: Retries\n\nA failed call retries with the same key.\n"
	shopSitePage = "---\ntitle: Welcome\nlayout: home\n---\n\n# Welcome\n\nThe shop's site.\n"
)

// adoptionStaged is what the adopt lane writes, kept beside the test: the front matter it puts on the
// requirements document, and the plan's own files.
var adoptionStaged = map[string]string{
	"requirements-front-matter": "+++\nkind = \"requirements\"\nid = \"shop\"\ntitle = \"Shop requirements\"\nplan = \"shop-adoption\"\n\n" +
		"[[requirement]]\nid = \"SHOP-1\"\ntext = \"Every payment call goes through one client\"\nstatus = \"met\"\nevidence = [\"shop:client.go\"]\n\n" +
		"[[requirement]]\nid = \"SHOP-2\"\ntext = \"A failed call retries with the same key\"\n+++\n\n",
	"plans/plan.md": "+++\nkind = \"plan\"\nid = \"shop-adoption\"\ntitle = \"Adopt the shop\"\n" +
		"documents = [\"requirements:shop\", \"milestone:m0-today\", \"milestone:m1-retries\"]\n\n" +
		"[[decision]]\nid = \"d1\"\nquestion = \"site/index.md has front matter from the site generator. Should it change?\"\n" +
		"answer = \"Leave it as it is.\"\nrationale = \"The site generator owns it.\"\n\n" +
		"[[left_out]]\nref = \"branch:spike/old-ui\"\nreason = \"An experiment the team abandoned\"\n+++\n\nThe shop as it is, and retries.\n",
	"plans/m0-today.md": "+++\nkind = \"milestone\"\nid = \"m0-today\"\ntitle = \"The shop today\"\nplan = \"shop-adoption\"\npriority = 0\n" +
		"existing = true\n+++\n\nWhat the code does today.\n",
	"plans/m1-retries.md": "+++\nkind = \"milestone\"\nid = \"m1-retries\"\ntitle = \"Retries\"\nplan = \"shop-adoption\"\npriority = 1\n" +
		"depends_on = [\"m0-today\"]\n+++\n\nA failed call retries.\n",
	"plans/client.md": "+++\nkind = \"contract\"\nid = \"client\"\ntitle = \"The payments client\"\nplan = \"shop-adoption\"\nmilestone = \"m0-today\"\n" +
		"shared = true\nexisting = true\n\n[[criteria]]\nid = \"c1\"\ntext = \"Every payment call goes through one client\"\nrequirements = [\"SHOP-1\"]\n+++\n\nThe client.\n",
	"plans/retry.md": "+++\nkind = \"contract\"\nid = \"retry\"\ntitle = \"Retry failed calls\"\nplan = \"shop-adoption\"\nmilestone = \"m1-retries\"\n" +
		"uses = [\"client\"]\n\n[[criteria]]\nid = \"c1\"\ntext = \"A failed call retries with the same key\"\nrequirements = [\"SHOP-2\"]\n\n" +
		"[[work_orders]]\nid = \"retry-finish\"\ntitle = \"Finish the retry branch\"\nrepo = \".\"\nsize = \"small\"\ncontinues = \"feature/retry\"\n+++\n\n" +
		"Finish the retries someone started.\n",
}

// adoptLane is an adopt lane that reads its inventory, onboards the requirements document by putting front
// matter above its prose, writes the plan's files, and commits them on its own branch.
func adoptLane(t *testing.T) string {
	t.Helper()
	staged := t.TempDir()
	for name, text := range adoptionStaged {
		path := filepath.Join(staged, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	onboard := "test -s " + lanes.InventoryFile + " && grep -q feature/retry " + lanes.InventoryFile +
		" && cat " + filepath.Join(staged, "requirements-front-matter") + " docs/requirements.md > requirements.onboarded" +
		" && mv requirements.onboarded docs/requirements.md && cp -R " + filepath.Join(staged, "plans") + " ." +
		" && git add docs plans && git -c user.name=planner -c user.email=planner@example.com commit -q -m 'Onboard the shop'"
	fixture, err := json.Marshal(map[string]any{
		"source": "an adopt lane that onboards the requirements and writes the plan",
		"turns": [][]map[string]any{{
			{"init": true},
			{"hook": "UserPromptSubmit", "input": map[string]any{"prompt": "the brief"}},
			{"tool": "Bash", "command": onboard},
			{"mcp": "lane_report", "arguments": map[string]any{"outcome": "completed", "checks": []map[string]any{{"name": "onboarded", "passed": true, "criterion": "c1"}}}},
			{"emit": map[string]any{"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": map[string]any{"input_tokens": 3, "output_tokens": 5}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(fixture)
}

// runIn runs the CLI in a directory, as a person in that repository would.
func (in *install) runIn(dir string, args ...string) (int, string, string) {
	in.t.Helper()
	cmd := exec.Command(binaries.cli, args...)
	cmd.Dir = dir
	cmd.Env = in.env()
	if in.session != "" {
		cmd.Env = append(cmd.Env, authority.EnvMemberToken+"="+in.session)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), stdout.String(), stderr.String()
	}
	in.t.Fatal(err)
	return 0, "", ""
}

// shopRepository is the existing project: its documents on main, a feature branch someone started and
// main moved on from, and an abandoned experiment. Its directory names the project.
func shopRepository(t *testing.T) *gittest.Repo {
	t.Helper()
	repo := gittest.New(t)
	named := filepath.Join(filepath.Dir(repo.Dir), "shop")
	if err := os.Rename(repo.Dir, named); err != nil {
		t.Fatal(err)
	}
	repo.Dir = named
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("README.md", shopReadme)
	repo.Commit("docs/requirements.md", shopRequirements)
	repo.Commit("site/index.md", shopSitePage)
	repo.Commit("client.go", "package shop\n")
	repo.Git("switch", "--quiet", "-c", "spike/old-ui")
	repo.Commit("old-ui.txt", "abandoned\n")
	repo.Git("switch", "--quiet", "main")
	repo.Git("switch", "--quiet", "-c", "feature/retry")
	repo.Commit("retry.go", "package shop\n\n// Retry is started.\n")
	repo.Git("switch", "--quiet", "main")
	repo.Commit("CHANGELOG.md", "Main moved on.\n")
	return repo
}

// TestAnExistingProjectIsAdoptedAndApprovedAsOnePlan is ADR-0023 end to end on the real binary, with real git
// and the scripted CLI. `ropeshift adopt` in a repository no project holds makes the project, registers the
// repository, and starts the adopt session, whose lane reads its inventory and onboards the project on a
// branch of its own: front matter above the requirements document's prose, a met requirement with evidence,
// an existing contract, a work order that continues the feature branch, and a branch left out. The plan's
// approval groups what exists, continues, is new, and is left out, with the branch's changes, and main does
// not move until the person merges the approved branch. The continued branch's lane then starts from its
// tip, and the branch stays where its author left it. Adopting the planned project again is refused.
func TestAnExistingProjectIsAdoptedAndApprovedAsOnePlan(t *testing.T) {
	in := newInstall(t)
	repo := shopRepository(t)
	mainTip := repo.Git("rev-parse", "main")
	retryTip := repo.Git("rev-parse", "feature/retry")
	fork := repo.Git("merge-base", "main", "feature/retry")
	in.path = doubles.InstallFakeClaude(t, "planning-lane")
	fixtureDir, _, _ := strings.Cut(in.path, string(os.PathListSeparator))
	fixture := filepath.Join(fixtureDir, doubles.FixtureFile)
	if err := os.WriteFile(fixture, []byte(adoptLane(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.home, ".gitconfig"), []byte("[user]\n\tname = Lane\n\temail = lane@example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms")
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})

	code, stdout, stderr := in.runIn(repo.Dir, "adopt", "--json")
	if code != cli.ExitOK {
		t.Fatalf("adopt: exit %d: %s%s", code, stdout, stderr)
	}
	var started struct {
		Project   string `json:"project"`
		WorkOrder string `json:"work_order"`
		Lane      string `json:"lane"`
	}
	if err := json.Unmarshal([]byte(stdout), &started); err != nil || started.Project != "shop" || started.Lane == "" {
		t.Fatalf("adopt printed %q: %v", stdout, err)
	}
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", started.Lane))
	project := []string{"--project", "shop"}
	at := func(args ...string) []string { return append(args, project...) }
	if order := in.showIn("shop", started.WorkOrder); !order.Adopt || order.Kind != workorders.KindPlan {
		t.Fatalf("the adopt session's work is %+v", order)
	}
	in.waitFor("the adopt lane's end", func() bool { return in.showIn("shop", started.WorkOrder).State == workorders.Acceptance })

	// The lane was told to adopt, and read the inventory the worker wrote before it started.
	if prompts := in.firstPrompts(t); !strings.Contains(prompts, "Onboard, do not rewrite") || !strings.Contains(prompts, lanes.InventoryFile) {
		t.Errorf("the brief does not adopt: %q", prompts)
	}
	worktree := filepath.Join(filepath.Dir(repo.Dir), "shop-lanes", started.Lane)
	inventory := readInventory(t, filepath.Join(worktree, lanes.InventoryFile))
	if len(inventory.Repositories) != 1 {
		t.Fatalf("the inventory holds %+v", inventory.Repositories)
	}
	shop := inventory.Repositories[0]
	formats := map[string]string{}
	for _, document := range shop.Documents {
		formats[document.Path] = document.FrontMatter
	}
	if formats["docs/requirements.md"] != planning.FrontMatterNone || formats["site/index.md"] != planning.FrontMatterYAML || formats["README.md"] != planning.FrontMatterNone {
		t.Errorf("the inventory reads the documents as %v", formats)
	}
	var branches []string
	for _, branch := range shop.Branches {
		branches = append(branches, branch.Name)
		if branch.Name == "feature/retry" && (branch.Tip != retryTip || branch.MergeBase != fork || branch.Ahead != 1 || branch.Behind != 1) {
			t.Errorf("the inventory reads feature/retry as %+v", branch)
		}
	}
	if !slices.Contains(branches, "feature/retry") || !slices.Contains(branches, "spike/old-ui") {
		t.Errorf("the inventory lists branches %v", branches)
	}

	// The plan waits, grouped, and main has not moved.
	approval := in.awaitPlanApproval("shop")
	shown := in.must(at("approval", "show", approval.ID)...)
	for _, want := range []string{
		"what exists, by the plan's claim:\n",
		"  requirement:SHOP-1\tEvery payment call goes through one client\tevidence shop:client.go\n",
		"  milestone:m0-today\tThe shop today\trests on contract:client\n",
		"  contract:client\tThe payments client\tevidence shop:client.go\trests on requirement:SHOP-1\n",
		"what continues:\n  contract:retry\tretry-finish\tFinish the retry branch\tcontinues feature/retry in .\n",
		"what is new:\n  milestone:m1-retries\tRetries\n",
		"what is left out:\n  branch:spike/old-ui\tAn experiment the team abandoned\n",
		"the adoption branch changes 6 files:\n",
		"  changed\tdocs/requirements.md\n",
		"  added\tplans/plan.md\n",
		"  added\tplans/retry.md\n",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("approval show lacks %q:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "site/index.md") {
		t.Errorf("the adoption changed the page another tool owns:\n%s", shown)
	}
	if now := repo.Git("rev-parse", "main"); now != mainTip {
		t.Fatalf("main moved from %s to %s before the plan was approved", mainTip, now)
	}
	head := branchesOf(in.acceptanceIn("shop", started.WorkOrder))[0]
	onboarded := repo.Git("show", head.Digest+":docs/requirements.md")
	if !strings.HasPrefix(onboarded, "+++\nkind = \"requirements\"") || !strings.HasSuffix(onboarded+"\n", shopRequirements) {
		t.Errorf("the onboarded requirements lost their prose:\n%s", onboarded)
	}

	in.must(at("approval", "approve", approval.ID, "--digest", approval.SubjectDigest)...)
	in.waitFor("the approved plan's work", func() bool {
		for _, order := range in.workIn("shop") {
			if order.Title == "Finish the retry branch" && order.State == workorders.Ready {
				return true
			}
		}
		return false
	})
	if now := repo.Git("rev-parse", "main"); now != mainTip {
		t.Fatalf("main moved from %s to %s before the person merged", mainTip, now)
	}
	if order := in.showIn("shop", started.WorkOrder); order.State != workorders.Done {
		t.Errorf("the adopt session's work is %s after approval", order.State)
	}

	// The person merges the approved branch, which holds exactly the onboarding.
	repo.Git("merge", "--quiet", "--ff-only", head.Digest)
	if now := repo.Git("rev-parse", "main"); now != head.Digest {
		t.Errorf("main is %s after the merge, want %s", now, head.Digest)
	}
	if page := repo.Git("show", "main:site/index.md"); page+"\n" != shopSitePage {
		t.Errorf("the page another tool owns changed:\n%s", page)
	}

	// The continued branch's work starts from the branch's tip on a lane branch of its own.
	if err := os.WriteFile(fixture, []byte(commitsAFile), 0o600); err != nil {
		t.Fatal(err)
	}
	var retry workorders.WorkOrder
	for _, order := range in.workIn("shop") {
		if order.Title == "Finish the retry branch" {
			retry = order
		}
	}
	if retry.Continues != "feature/retry" {
		t.Fatalf("the continued work is %+v", retry)
	}
	in.must(at("work", "claim", retry.ID, "--worker", in.workers()[0].ID, "--claim-id", "c-retry")...)
	in.must(at("lane", "start", retry.ID, "--lane", "lane-retry")...)
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", "lane-retry"))
	in.waitFor("the continued lane's end", func() bool { return in.showIn("shop", retry.ID).State == workorders.Acceptance })
	var lane struct {
		ContinuesRef    string `json:"continues_ref"`
		ContinuesCommit string `json:"continues_commit"`
		BaseCommit      string `json:"base_commit"`
	}
	if err := json.Unmarshal([]byte(in.must(at("lane", "show", "lane-retry", "--json")...)), &lane); err != nil {
		t.Fatal(err)
	}
	if lane.ContinuesRef != "feature/retry" || lane.ContinuesCommit != retryTip || lane.BaseCommit != fork {
		t.Errorf("the continued lane began at %+v, want feature/retry at %s from %s", lane, retryTip, fork)
	}
	if contents, err := os.ReadFile(filepath.Join(filepath.Dir(repo.Dir), "shop-lanes", "lane-retry", "retry.go")); err != nil || !strings.Contains(string(contents), "Retry is started") {
		t.Errorf("the continued lane does not hold the branch's work: %q, %v", contents, err)
	}
	if now := repo.Git("rev-parse", "refs/heads/feature/retry"); now != retryTip {
		t.Errorf("the continued branch moved from %s to %s", retryTip, now)
	}

	// The project has an approved plan now, so adopting it again is refused.
	code, _, stderr = in.runIn(repo.Dir, "adopt", "--no-attach")
	if code != cli.ExitConflict || !strings.Contains(stderr, "already has an approved plan") || !strings.Contains(stderr, "plan start") {
		t.Errorf("adopting a planned project: exit %d: %s", code, stderr)
	}
	adopting := 0
	for _, order := range in.workIn("shop") {
		if order.Adopt {
			adopting++
		}
	}
	if adopting != 1 {
		t.Errorf("a refused adoption recorded work: %d work orders adopt the project", adopting)
	}
}

// readInventory reads the inventory the worker wrote before the lane started, and holds it to the whole file.
func readInventory(t *testing.T, path string) planning.Inventory {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var inventory planning.Inventory
	if err := json.Unmarshal(raw, &inventory); err != nil || inventory.Version != planning.InventoryVersion {
		t.Fatalf("the inventory reads %q: %v", raw, err)
	}
	if again, err := planning.RenderInventory(inventory); err != nil || !bytes.Equal(again, raw) {
		t.Errorf("the inventory is not what RenderInventory writes of it: %v", err)
	}
	return inventory
}

// showIn reads one work order of a project.
func (in *install) showIn(project, id string) workorders.WorkOrder {
	in.t.Helper()
	var row workorders.WorkOrder
	if err := json.Unmarshal([]byte(in.must("work", "show", id, "--json", "--project", project)), &row); err != nil {
		in.t.Fatal(err)
	}
	return row
}

// workIn lists a project's work orders.
func (in *install) workIn(project string) []workorders.WorkOrder {
	in.t.Helper()
	var list api.WorkOrderList
	if err := json.Unmarshal([]byte(in.must("work", "list", "--json", "--project", project)), &list); err != nil {
		in.t.Fatal(err)
	}
	return list.WorkOrders
}

func (in *install) acceptanceIn(project, id string) workorders.AcceptanceView {
	in.t.Helper()
	var view workorders.AcceptanceView
	if err := json.Unmarshal([]byte(in.must("work", "acceptance", id, "--json", "--project", project)), &view); err != nil {
		in.t.Fatal(err)
	}
	return view
}

// awaitPlanApproval waits for the one request to approve a plan in a project.
func (in *install) awaitPlanApproval(project string) authority.Approval {
	in.t.Helper()
	var found authority.Approval
	in.waitFor("the plan's approval request", func() bool {
		var list api.ApprovalList
		if err := json.Unmarshal([]byte(in.must("approval", "list", "--json", "--project", project)), &list); err != nil {
			in.t.Fatal(err)
		}
		for _, row := range list.Approvals {
			if row.Kind == authority.KindPlan && row.State == authority.ApprovalPending {
				found = row
				return true
			}
		}
		return false
	})
	return found
}

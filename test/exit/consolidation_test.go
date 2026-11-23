package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

const (
	workspaceRules = "# Workspace rules\n\n- Run make check before a pull request.\n"
	designRules    = "# Design rules\n\n- Keep every requirement ID.\n"
	// mergedRulesSubject is the commit the scripted session makes of the two sets of instructions.
	mergedRulesSubject = "docs: merge the agent instructions of design into the workspace"
)

// consolidatedPlan is the plan the scripted adopt session writes, which lists the manifest the worker wrote.
const consolidatedPlan = "+++\nkind = \"plan\"\nid = \"acme-adoption\"\ntitle = \"Adopt acme\"\ndocuments = [\"repositories:acme\"]\n+++\n\n" +
	"The workspace, with the design history it imported.\n"

// acmeWorkspace is a workspace with no plan content whose ignored folder repos/ holds the design repository, with
// its documents, its own agent instructions, and a proof of concept with a build file, and one code repository. Its
// directory names the project.
func acmeWorkspace(t *testing.T) (workspace, design *gittest.Repo) {
	t.Helper()
	workspace = gittest.New(t)
	named := filepath.Join(filepath.Dir(workspace.Dir), "acme")
	if err := os.Rename(workspace.Dir, named); err != nil {
		t.Fatal(err)
	}
	// The plan names each clone by its path with every symbolic link followed, as a temporary directory has one.
	real, err := filepath.EvalSymlinks(named)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Dir = real
	workspace.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	workspace.Commit(".gitignore", "repos/\n")
	workspace.Commit("README.md", "# Acme\n\nThe workspace.\n")
	workspace.Commit("AGENTS.md", workspaceRules)
	moveInto := func(repo *gittest.Repo, name string) {
		at := filepath.Join(workspace.Dir, "repos", name)
		if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(repo.Dir, at); err != nil {
			t.Fatal(err)
		}
		repo.Dir = at
	}
	design = gittest.New(t)
	design.Commit("README.md", "# Acme design\n")
	design.Commit(".gitignore", "dist/\n")
	design.Commit("AGENTS.md", designRules)
	design.Commit("docs/vision.md", "# Vision\n\nWhat acme is for.\n")
	design.Commit("docs/requirements.md", "# Requirements\n\n## ACME-1: Orders\n\nAn order is placed once.\n")
	design.Commit("poc/go.mod", "module poc\n")
	moveInto(design, "design")
	api := gittest.New(t)
	api.Commit("go.mod", "module api\n")
	api.Commit("main.go", "package main\n")
	moveInto(api, "api")
	return workspace, design
}

// consolidatingLane is an adopt lane that finds the import the worker made, merges the design repository's agent
// instructions into the workspace's as one commit, writes the plan, and reports.
func consolidatingLane(t *testing.T) string {
	t.Helper()
	merge := "test -s " + lanes.InventoryFile + " && git show HEAD^2:AGENTS.md > design-rules.md" +
		" && { cat AGENTS.md; echo; cat design-rules.md; } > merged-rules.md && mv merged-rules.md AGENTS.md && rm design-rules.md" +
		" && git add AGENTS.md && git commit -q -m '" + mergedRulesSubject + "'" +
		" && mkdir -p plans && printf %s " + shellQuote(consolidatedPlan) + " > plans/plan.md && git add plans && git commit -q -m 'Plan the adoption'"
	fixture, err := json.Marshal(map[string]any{
		"source": "an adopt lane that merges the design repository's instructions and writes the plan",
		"turns": [][]map[string]any{{
			{"init": true},
			{"hook": "UserPromptSubmit", "input": map[string]any{"prompt": "the brief"}},
			{"tool": "Bash", "command": merge},
			{"mcp": "lane_report", "arguments": map[string]any{"outcome": "completed", "checks": []map[string]any{{"name": "onboarded", "passed": true, "criterion": "c1"}}}},
			{"emit": map[string]any{"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.02, "usage": map[string]any{"input_tokens": 3, "output_tokens": 5}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(fixture)
}

// untouchedState is what a clone holds that a consolidation must leave as it was: every ref, the index, the
// working tree's state, and HEAD.
func untouchedState(repo *gittest.Repo) string {
	return strings.Join([]string{
		repo.Git("for-each-ref", "--format=%(refname) %(objectname)"), repo.Git("ls-files", "--stage"), repo.Git("status", "--porcelain", "--ignored"), repo.Git("rev-parse", "HEAD"),
	}, "\n--\n")
}

// TestAWorkspaceConsolidatesItsDesignRepository is the exit criterion of FR-ADOPT-12 and FR-ADOPT-13 on the real
// binary, with real git and the scripted CLI: in a workspace whose design repository sits in its code folder,
// `adopt --dry-run` lists every change and records nothing, and `adopt --restructure` gives one adoption branch in
// the workspace that holds the design history with its commit IDs, the session's commit of the merged agent
// instructions, and the manifest of the code folder, while the design repository's clone is unchanged and the
// workspace keeps its identity.
func TestAWorkspaceConsolidatesItsDesignRepository(t *testing.T) {
	in := newInstall(t)
	workspace, design := acmeWorkspace(t)
	designCommits := strings.Fields(design.Git("rev-list", "main"))
	designTip := design.Git("rev-parse", "main")
	workspaceRoot := workspace.Git("rev-list", "--first-parent", "--max-parents=0", "main")
	mainTip := workspace.Git("rev-parse", "main")
	designBefore := untouchedState(design)
	in.path = doubles.InstallFakeClaude(t, "planning-lane")
	fixtureDir, _, _ := strings.Cut(in.path, string(os.PathListSeparator))
	if err := os.WriteFile(filepath.Join(fixtureDir, doubles.FixtureFile), []byte(consolidatingLane(t)), 0o600); err != nil {
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

	// The dry run lists every change and records nothing.
	code, stdout, stderr := in.runIn(workspace.Dir, "adopt", "--dry-run", "--json", "--leave-out", "poc")
	if code != cli.ExitOK {
		t.Fatalf("adopt --dry-run: exit %d: %s%s", code, stdout, stderr)
	}
	var plan planning.AdoptionPlan
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatalf("the dry run printed %q: %v", stdout, err)
	}
	if plan.Shape != planning.ShapeConsolidate || plan.Design == nil || plan.Design.Name != "design" || plan.Planning.Path != workspace.Dir {
		t.Fatalf("the dry run reads the layout as %+v", plan)
	}
	var files []string
	for _, file := range plan.Files {
		files = append(files, file.Action+" "+file.Path)
	}
	for _, want := range []string{"add " + planning.ManifestPath, "add " + planning.MakefilePath, "append " + planning.GitignorePath} {
		if !slices.Contains(files, want) {
			t.Errorf("the dry run's files are %v, want %q among them", files, want)
		}
	}
	for _, want := range []string{"docs/requirements.md", "docs/vision.md"} {
		if !slices.Contains(plan.Takes, want) {
			t.Errorf("the dry run takes %v, want %s among them", plan.Takes, want)
		}
	}
	if slices.Contains(plan.Takes, "poc/go.mod") {
		t.Errorf("the dry run takes the tree it leaves out: %v", plan.Takes)
	}
	wantCollisions := []planning.Collision{{Path: ".gitignore", Kept: planning.KeptUnion}, {Path: "AGENTS.md", Kept: planning.KeptWorkspace}, {Path: "README.md", Kept: planning.KeptWorkspace}}
	if !slices.Equal(plan.Collisions, wantCollisions) {
		t.Errorf("the dry run's collisions are %+v, want %+v", plan.Collisions, wantCollisions)
	}
	if len(plan.CodeTrees) != 1 || plan.CodeTrees[0].Path != "poc" || !plan.CodeTrees[0].LeftOut {
		t.Errorf("the dry run's code trees are %+v", plan.CodeTrees)
	}
	for _, repository := range plan.Repositories {
		if repository.Path == design.Dir && repository.Outcome != planning.OutcomeNotRegistered {
			t.Errorf("the dry run registers the design repository: %+v", repository)
		}
	}
	var projects api.ProjectList
	if err := json.Unmarshal([]byte(in.must("project", "list", "--json")), &projects); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(projects.Projects, func(p api.Project) bool { return p.Name == "acme" }) {
		t.Fatalf("the dry run made the project: %+v", projects.Projects)
	}

	// The restructuring gives one adoption branch.
	code, stdout, stderr = in.runIn(workspace.Dir, "adopt", "--restructure", "--leave-out", "poc", "--json", "--no-attach")
	if code != cli.ExitOK {
		t.Fatalf("adopt --restructure: exit %d: %s%s", code, stdout, stderr)
	}
	var started struct {
		Project   string `json:"project"`
		WorkOrder string `json:"work_order"`
		Lane      string `json:"lane"`
	}
	if err := json.Unmarshal([]byte(stdout), &started); err != nil || started.Project != "acme" || started.Lane == "" {
		t.Fatalf("adopt printed %q: %v", stdout, err)
	}
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", started.Lane))
	if order := in.showIn("acme", started.WorkOrder); !order.Adopt || !order.Restructure.Consolidates() || order.Restructure.DesignRoot != design.Roots("main")[0] {
		t.Fatalf("the adopt session's work is %+v", order)
	}
	in.waitFor("the adopt lane's end", func() bool { return in.showIn("acme", started.WorkOrder).State == workorders.Acceptance })
	branches := branchesOf(in.acceptanceIn("acme", started.WorkOrder))
	if len(branches) != 1 {
		t.Fatalf("the adoption left branches %+v", branches)
	}
	head := branches[0].Digest

	// The branch holds the design history with every commit ID, in one merge whose second parent is the design tip.
	for _, commit := range designCommits {
		if _, err := workspace.Try("merge-base", "--is-ancestor", commit, head); err != nil {
			t.Errorf("design commit %s is not on the adoption branch: %v", commit, err)
		}
	}
	line := strings.Split(workspace.Git("log", "--first-parent", "--format=%H %P|%s", mainTip+".."+head), "\n")
	if len(line) != 3 {
		t.Fatalf("the adoption branch's own line is %q, want the session's two commits on the import", line)
	}
	importLine := strings.Fields(strings.SplitN(line[2], "|", 2)[0])
	if subject := strings.SplitN(line[2], "|", 2)[1]; subject != planning.ImportCommitSubject("design") || len(importLine) != 3 || importLine[1] != mainTip || importLine[2] != designTip {
		t.Errorf("the import reads %q, want a merge of %s and %s", line[2], mainTip, designTip)
	}
	if subject := strings.SplitN(line[1], "|", 2)[1]; subject != mergedRulesSubject {
		t.Errorf("the commit after the import is %q, want the merged instructions", line[1])
	}
	rules := workspace.Git("show", head+":AGENTS.md")
	if !strings.Contains(rules, strings.TrimSpace(workspaceRules)) || !strings.Contains(rules, strings.TrimSpace(designRules)) {
		t.Errorf("the merged instructions read:\n%s", rules)
	}
	if blamed := workspace.Git("log", "--format=%H", head, "--", "docs/vision.md"); !slices.Contains(designCommits, strings.Fields(blamed)[0]) {
		t.Errorf("git log on a design file does not read through the merge: %q", blamed)
	}
	// The manifest of the code folder, with the design repository left out, and the tree left out of the merge only.
	manifest := workspace.Git("show", head+":"+planning.ManifestPath)
	if !strings.Contains(manifest, "kind = \"repositories\"") || !strings.Contains(manifest, "repos/api") || strings.Contains(manifest, "repos/design") {
		t.Errorf("the manifest reads:\n%s", manifest)
	}
	if _, err := workspace.Try("cat-file", "-e", head+":poc/go.mod"); err == nil {
		t.Errorf("the tree left out is on the adoption branch")
	}
	if _, err := workspace.Try("cat-file", "-e", designTip+":poc/go.mod"); err != nil {
		t.Errorf("the tree left out is not readable at the design tip: %v", err)
	}

	// The approval lists the consolidation before anything merges.
	approval := in.awaitPlanApproval("acme")
	shown := in.must("approval", "show", approval.ID, "--project", "acme")
	for _, want := range []string{
		"the adoption imports the history of design in merge commit ",
		"whose second parent " + designTip + " is the design repository's tip\n",
		"the paths both repositories held, with the version kept:\n",
		"  AGENTS.md\tthe workspace's version\n",
		"the design repository's code trees left out:\n  poc\n",
		"at person_merges you merge this branch yourself: merge it with a merge commit.",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("approval show lacks %q:\n%s", want, shown)
		}
	}

	// The design clone and the workspace's identity are unchanged, and nothing reached main.
	if after := untouchedState(design); after != designBefore {
		t.Errorf("the design repository's clone changed:\nbefore\n%s\nafter\n%s", designBefore, after)
	}
	if now := workspace.Git("rev-parse", "main"); now != mainTip {
		t.Errorf("main moved from %s to %s", mainTip, now)
	}
	if root := workspace.Git("rev-list", "--first-parent", "--max-parents=0", head); root != workspaceRoot {
		t.Errorf("the adoption branch's first-parent root is %s, want the workspace's %s", root, workspaceRoot)
	}
	var list api.RepositoryList
	if err := json.Unmarshal([]byte(in.must("repository", "list", "--json", "--project", "acme")), &list); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, view := range list.Repositories {
		ids = append(ids, view.ID)
	}
	if !slices.Contains(ids, workspaceRoot) || slices.Contains(ids, design.Roots("main")[0]) {
		t.Errorf("the project's repositories are %v, want the workspace's identity %s and no design repository", ids, workspaceRoot)
	}
}

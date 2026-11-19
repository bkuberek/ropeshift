package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/gittest"
)

// renderTemplates writes a file from the product's templates, as the entry package's renderer does: the worker never
// imports them, and a test that makes real commits reads what the worker would write.
func renderTemplates(name string, data projectfiles.Data) (string, error) {
	converted := templates.Data{Project: data.Project}
	for _, repository := range data.Repositories {
		converted.Repositories = append(converted.Repositories, templates.Repository{Name: repository.Name, Remote: repository.Remote, Path: repository.Path})
	}
	return templates.Render(name, converted)
}

// gitWorkspace is a design repository with two code repositories beside it, all real git clones of origins of their
// own, and a lane's worktree of the design repository on a branch of its own.
type gitWorkspace struct {
	t        *testing.T
	base     string
	env      []string
	kept     *checkouts.File
	worktree gitcli.Worktrees
	lane     string
	tip      string
	ids      map[string]string
	// reads are the clones the lane's lease reads beside the workspace, by name.
	reads []string
}

func newGitWorkspace(t *testing.T, designFiles map[string]string) *gitWorkspace {
	t.Helper()
	w := newEmptyGitWorkspace(t)
	w.clone("ws", designFiles)
	w.clone("api", map[string]string{"go.mod": "module api\n", "README.md": "api\n"})
	w.clone("web", map[string]string{"package.json": "{}\n", "README.md": "web\n"})
	w.begin("api", "web")
	return w
}

// newEmptyGitWorkspace is a folder for clones to be made in, and nothing else.
func newEmptyGitWorkspace(t *testing.T) *gitWorkspace {
	t.Helper()
	gittest.Require(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &gitWorkspace{t: t, base: base, ids: map[string]string{}}
}

// begin makes the lane's worktree of the workspace clone "ws" on a branch of its own, once every clone is made,
// and reads the identity of the workspace and of each clone the lane reads, named by their paths below the base.
func (w *gitWorkspace) begin(reads ...string) {
	w.t.Helper()
	w.kept = &checkouts.File{Path: filepath.Join(w.t.TempDir(), checkouts.FileName), Env: w.env}
	w.worktree = gitcli.Worktrees{Env: w.env}
	ws := w.path("ws")
	w.lane = filepath.Join(w.t.TempDir(), "lane-1")
	made, err := w.worktree.Create(w.t.Context(), ports.WorktreeSpec{Repository: ws, Path: w.lane, Branch: "lane/adopt"})
	if err != nil {
		w.t.Fatal(err)
	}
	w.tip = made.Head
	w.reads = reads
	paths := []string{ws}
	for _, name := range reads {
		paths = append(paths, w.path(name))
	}
	found, _, err := w.kept.Find(w.t.Context(), paths, 1)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, clone := range found {
		w.ids[filepath.Base(clone.Path)] = clone.ID
	}
}

func (w *gitWorkspace) path(name string) string { return filepath.Join(w.base, name) }

// clone makes an origin holding the files, one commit each, and a clone of it beside the others.
func (w *gitWorkspace) clone(name string, files map[string]string) {
	w.t.Helper()
	origin := gittest.NewIn(w.t, filepath.Join(w.base, ".origins", name))
	if w.env == nil {
		w.env = origin.Env()
	}
	for _, file := range slices.Sorted(maps.Keys(files)) {
		origin.Commit(file, files[file])
	}
	origin.Git("clone", "--quiet", origin.Dir, w.path(name))
}

func (w *gitWorkspace) git(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, w.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// run reads the plan as the member's command does, and returns a lane that carries it, on a worker with the real
// adapters.
func (w *gitWorkspace) run(request adoption.Request) *laneRun {
	w.t.Helper()
	return w.runChoosing(request, nil)
}

// runChoosing is run with a say in the restructuring the work carries, as a member's command sets it from the
// plan: choose may change what the plan alone would give.
func (w *gitWorkspace) runChoosing(request adoption.Request, choose func(plan planning.AdoptionPlan, chosen *planning.Restructure)) *laneRun {
	w.t.Helper()
	register := func(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
		var out []planning.PlannedRepository
		for _, one := range clones {
			out = append(out, planning.PlannedRepository{Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.AlreadyRegistered)})
		}
		return out, nil
	}
	plan, err := adoption.Plan(w.t.Context(), request, w.kept, w.kept, register)
	if err != nil {
		w.t.Fatal(err)
	}
	chosen := planning.Restructure{
		Shape: plan.Shape, Digest: plan.Digest, Root: request.Root, Paths: request.Paths, Depth: request.Depth, Planning: request.Planning,
		Design: request.Design, LeaveOut: request.LeaveOut, Parts: plan.Parts(),
	}
	if choose != nil {
		choose(plan, &chosen)
	}
	raw, err := json.Marshal(chosen)
	if err != nil {
		w.t.Fatal(err)
	}
	lease := ledger.DesiredLease{
		Project: "p1", WorkOrder: "wo", Lane: "lane-1", Kind: string(workorders.KindPlan), Adopt: true,
		Repository: w.ids["ws"], RepositoryName: "ws", BaseCommit: w.tip, Restructure: raw,
	}
	for i, name := range w.reads {
		lease.Reads = append(lease.Reads, ledger.RepositoryRead{Repository: w.ids[filepath.Base(name)], Name: filepath.Base(name), Commit: fmt.Sprintf("c%d", i+1)})
	}
	journal, err := fake.NewJournal()
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(w.t.Context()); err != nil {
		w.t.Fatal(err)
	}
	if err := journal.SaveDesired(w.t.Context(), ledger.DesiredState{Rev: 1, Leases: []ledger.DesiredLease{lease}, Projects: []ledger.DesiredProject{{ID: "p1", Name: "shop"}}}); err != nil {
		w.t.Fatal(err)
	}
	runtime := &LaneRuntime{Provisioner: Provisioner{Worktrees: w.worktree, Checkouts: w.kept}, Layouts: w.kept, Render: renderTemplates}
	worker := New(nil, journal, fake.NewClock(time.UnixMilli(1_000_000)), Config{Lanes: runtime})
	run := &laneRun{w: worker, rt: runtime, lease: lease, generation: 1}
	worker.running["lane-1"] = run
	return run
}

// A design repository with clones beside it gets one commit on the lane's branch with the files its layout lacks, as
// real git makes it: in the product's name, with the worktree clean, the manifest reading back, the Makefile, the
// ignore file, and the instruction files written from the templates, and the moves listed and not made.
func TestAGitWorkspaceWithClonesBesideItGetsTheCommitWithTheFilesItLacksAndTheMovesListed(t *testing.T) {
	t.Parallel()
	w := newGitWorkspace(t, map[string]string{"README.md": "a design repository\n", "docs/vision.md": "# Vision\n"})
	ws := w.path("ws")
	run := w.run(adoption.Request{Root: ws, Paths: []string{w.path("api"), w.path("web")}, Depth: 1, Planning: "ws"})

	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	if count := w.git(w.lane, "rev-list", "--count", w.tip+"..HEAD"); count != "1" {
		t.Fatalf("the branch holds %s commits beyond its base, want one", count)
	}
	if subject := w.git(w.lane, "log", "-1", "--format=%s"); subject != "chore: add the workspace files" {
		t.Errorf("the commit's subject is %q", subject)
	}
	if author := w.git(w.lane, "log", "-1", "--format=%an <%ae>"); author != "ropeshift <ropeshift@localhost>" {
		t.Errorf("the commit is by %q", author)
	}
	if files := w.git(w.lane, "show", "--name-only", "--format=", "HEAD"); files != ".gitignore\nAGENTS.md\nCLAUDE.md\nMakefile\nplan/repositories.md" {
		t.Errorf("the commit holds:\n%s", files)
	}
	if status := w.git(w.lane, "status", "--porcelain"); status != "" {
		t.Errorf("the worktree is dirty:\n%s", status)
	}
	if ignore := w.readLane(".gitignore"); !strings.Contains(ignore, "\n/repos/\n") || !strings.Contains(ignore, "\n/.ropeshift/worktrees/\n") {
		t.Errorf("the ignore file reads %q", ignore)
	}
	if agents := w.readLane("AGENTS.md"); !strings.HasPrefix(agents, "# AGENTS.md\n") || !strings.Contains(agents, "the shop project") || !strings.Contains(agents, "| `api` |") {
		t.Errorf("AGENTS.md reads %q", agents)
	}
	if claude := w.readLane("CLAUDE.md"); claude != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md reads %q", claude)
	}
	raw, err := os.ReadFile(filepath.Join(w.lane, "plan", "repositories.md"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repofiles.ParseManifest(raw)
	if err != nil || manifest.ID != "shop" || len(manifest.Repositories) != 2 {
		t.Fatalf("the manifest reads %+v: %v\n%s", manifest, err, raw)
	}
	for i, name := range []string{"api", "web"} {
		got := manifest.Repositories[i]
		if got.Name != name || got.Path != "repos/"+name || got.Remote != filepath.Join(w.base, ".origins", name, "repository") {
			t.Errorf("repository %d reads %+v", i, got)
		}
	}
	makefile := w.readLane("Makefile")
	for _, want := range []string{"api=file://" + filepath.Join(w.base, ".origins", "api", "repository") + " \\\n", "setup:", "fetch:", "status:"} {
		if !strings.Contains(makefile, want) {
			t.Errorf("the Makefile holds no %q:\n%s", want, makefile)
		}
	}

	told := run.restructuredOf()
	if told == nil || told.Commit != w.git(w.lane, "rev-parse", "HEAD") || told.Manifest != "repositories:shop" {
		t.Fatalf("the brief is told %+v", told)
	}
	var commands []string
	for _, move := range told.Moves {
		commands = append(commands, move.Commands...)
	}
	if want := []string{
		"mv " + w.path("api") + " " + filepath.Join(ws, "repos", "api"), "ropeshift repository add " + filepath.Join(ws, "repos", "api"),
		"mv " + w.path("web") + " " + filepath.Join(ws, "repos", "web"), "ropeshift repository add " + filepath.Join(ws, "repos", "web"),
	}; !slices.Equal(commands, want) {
		t.Errorf("the moves are %v, want %v", commands, want)
	}
	for _, name := range []string{"api", "web"} {
		if _, err := os.Stat(filepath.Join(w.path(name), ".git")); err != nil {
			t.Errorf("the clone %s was moved: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(ws, "repos", name)); err == nil {
			t.Errorf("the worker made %s/repos/%s", ws, name)
		}
	}

	// A second run, as a restart makes, finds the commit and makes none, even though the clones have moved.
	if err := os.Rename(w.path("api"), filepath.Join(w.base, "moved")); err != nil {
		t.Fatal(err)
	}
	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("a run after the commit failed: %+v", report)
	}
	if count := w.git(w.lane, "rev-list", "--count", w.tip+"..HEAD"); count != "1" {
		t.Errorf("the branch holds %s commits after a second run", count)
	}
}

// A repository that holds an AGENTS.md keeps it as it is, and gets no second instruction file beside it, while the
// other files it lacks are written.
func TestAGitWorkspaceThatHoldsAnAGENTSFileKeepsItAndGetsTheOtherFilesItLacks(t *testing.T) {
	t.Parallel()
	w := newGitWorkspace(t, map[string]string{"README.md": "a design repository\n", "AGENTS.md": "my rules\n"})
	ws := w.path("ws")
	run := w.run(adoption.Request{Root: ws, Paths: []string{w.path("api"), w.path("web")}, Depth: 1, Planning: "ws"})
	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	if files := w.git(w.lane, "show", "--name-only", "--format=", "HEAD"); files != ".gitignore\nMakefile\nplan/repositories.md" {
		t.Errorf("the commit holds:\n%s", files)
	}
	if agents := w.readLane("AGENTS.md"); agents != "my rules\n" {
		t.Errorf("AGENTS.md reads %q", agents)
	}
	if w.laneHolds("CLAUDE.md") {
		t.Error("a CLAUDE.md was written beside the AGENTS.md the repository holds")
	}
}

// A single repository that lacks an instruction file gets one commit with AGENTS.md from the monorepo template and
// a CLAUDE.md that imports it, and nothing else.
func TestAGitSingleRepositoryThatLacksInstructionsGetsThemInOneCommit(t *testing.T) {
	t.Parallel()
	w := newGitWorkspace(t, map[string]string{"README.md": "a repository\n"})
	ws := w.path("ws")
	run := w.run(adoption.Request{Root: ws, Depth: 1})
	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	if count := w.git(w.lane, "rev-list", "--count", w.tip+"..HEAD"); count != "1" {
		t.Fatalf("the branch holds %s commits beyond its base, want one", count)
	}
	if files := w.git(w.lane, "show", "--name-only", "--format=", "HEAD"); files != "AGENTS.md\nCLAUDE.md" {
		t.Errorf("the commit holds:\n%s", files)
	}
	if agents := w.readLane("AGENTS.md"); !strings.Contains(agents, "rules for every agent that works in this repository") || strings.Contains(agents, "make setup") {
		t.Errorf("AGENTS.md is not the monorepo template:\n%s", agents)
	}
	if told := run.restructuredOf(); told == nil || told.Subject != planning.WorkspaceCommitSubject || len(told.Moves) != 0 || told.Manifest != "" {
		t.Errorf("the brief is told %+v", told)
	}
}

// The line for the worktree folder is not written when git ignores that folder, however the rule got there, and a
// Makefile the repository commits is left as it is.
func TestAGitWorkspaceThatIgnoresTheWorktreeFolderAndHoldsAMakefileGetsNeitherChange(t *testing.T) {
	t.Parallel()
	w := newGitWorkspace(t, map[string]string{"README.md": "a design repository\n", "Makefile": "all:\n", ".gitignore": "repos/\n/.ropeshift/\n", "AGENTS.md": "rules\n"})
	ws := w.path("ws")
	run := w.run(adoption.Request{Root: ws, Paths: []string{w.path("api"), w.path("web")}, Depth: 1, Planning: "ws"})
	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	if files := w.git(w.lane, "show", "--name-only", "--format=", "HEAD"); files != "plan/repositories.md" {
		t.Errorf("the commit holds:\n%s", files)
	}
	if makefile, _ := os.ReadFile(filepath.Join(w.lane, "Makefile")); string(makefile) != "all:\n" {
		t.Errorf("the Makefile reads %q", makefile)
	}
	if ignore, _ := os.ReadFile(filepath.Join(w.lane, ".gitignore")); string(ignore) != "repos/\n/.ropeshift/\n" {
		t.Errorf("the ignore file reads %q", ignore)
	}
}

// A layout that changed between the plan and the work fails the work, with real clones: a clone the plan found
// beside the design repository has gone, and nothing is committed.
func TestAGitWorkspaceWhoseCloneWentFailsWithLayoutChanged(t *testing.T) {
	t.Parallel()
	w := newGitWorkspace(t, map[string]string{"README.md": "a design repository\n"})
	ws := w.path("ws")
	run := w.run(adoption.Request{Root: ws, Paths: []string{w.path("api"), w.path("web")}, Depth: 1, Planning: "ws"})
	if err := os.Rename(w.path("web"), filepath.Join(w.base, "elsewhere")); err != nil {
		t.Fatal(err)
	}
	report, failed := run.restructure(t.Context(), ws, w.lane)
	if !failed || report.Reason != workorders.ReasonLayoutChanged || !strings.Contains(report.Detail, "layout") {
		t.Fatalf("the restructuring reported %+v, failed %v", report, failed)
	}
	if count := w.git(w.lane, "rev-list", "--count", w.tip+"..HEAD"); count != "0" {
		t.Errorf("a changed layout was committed: %s commits", count)
	}
}

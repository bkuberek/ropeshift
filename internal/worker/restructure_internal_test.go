package worker

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/workorders"
)

// FR-ADOPT-14: before an adopt lane that carries a restructuring starts, the worker reads the layout again as the
// plan did, and restructures only when it reads the same one: it commits the workspace files to the lane's branch as
// one bracketed effect, and lists the moves for the person.

const (
	restructuredLane     = "lane-1"
	restructuredWorktree = "/lanes/lane-1"
)

// restructureRig is a worker whose adopt lane carries the restructuring a member chose from a plan, on a machine
// whose clones a test lays out.
type restructureRig struct {
	t         *testing.T
	worktrees *fake.Worktrees
	checkouts *fake.Checkouts
	layouts   *fake.Layouts
	journal   ports.Journal
	worker    *Worker
	run       *laneRun
	request   adoption.Request
	// bare leaves the repository and the reads out of the lease, as the plane of a crash run gives a lane.
	bare bool
}

// renderFile stands in for the templates, which this package never imports: it says which template was written, for
// which project, over which repositories.
func renderFile(name string, data projectfiles.Data) (string, error) {
	var names []string
	for _, repository := range data.Repositories {
		names = append(names, repository.Name)
	}
	return name + " for " + data.Project + " over " + strings.Join(names, ",") + "\n", nil
}

// refusing is what the project's records say of each clone when its lease names no repository and no read: it holds
// a repository of none of them.
func refusing(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
	var out []planning.PlannedRepository
	for _, one := range clones {
		out = append(out, planning.PlannedRepository{
			Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.RegistrationRefused), Reason: "the project holds no repository of it",
		})
	}
	return out, nil
}

// known is what the project's records say of each clone, by identity, as the plan was read when the clones were
// registered: a repository named for its clone's folder.
func known(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
	var out []planning.PlannedRepository
	for _, one := range clones {
		out = append(out, planning.PlannedRepository{Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.AlreadyRegistered)})
	}
	return out, nil
}

func newRestructureRig(t *testing.T) *restructureRig {
	t.Helper()
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := &restructureRig{
		t: t, worktrees: fake.NewWorktrees(), checkouts: fake.NewCheckouts(), layouts: fake.NewLayouts(), journal: journal,
		request: adoption.Request{Root: "/work/ws", Paths: []string{"/work/api", "/work/web"}, Depth: 1, Planning: "ws"},
	}
	// The planning clone holds an ignore file and instructions, so its plan lists the manifest, the ignore lines, and a
	// Makefile, and a test about the files a layout lacks takes them away.
	r.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md", ".gitignore", "AGENTS.md"}})
	r.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	r.clone("/work/web", fake.CloneLayout{Tracked: []string{"package.json"}})
	return r
}

func (r *restructureRig) clone(at string, layout fake.CloneLayout) {
	r.t.Helper()
	name := path.Base(at)
	r.checkouts.Place(ports.Clone{Path: at, ID: "root-" + name, Remote: "git@example.invalid:acme/" + name + ".git", DefaultBranch: "main"})
	r.layouts.Set(at, layout)
}

// plan reads the layout as the member's command did, and the restructuring it sent with the work.
func (r *restructureRig) plan() (planning.AdoptionPlan, planning.Restructure) {
	r.t.Helper()
	plan, err := adoption.Plan(r.t.Context(), r.request, r.checkouts, r.layouts, known)
	if err != nil {
		r.t.Fatal(err)
	}
	return plan, planning.Restructure{
		Shape: plan.Shape, Digest: plan.Digest, Root: r.request.Root, Paths: r.request.Paths, Depth: r.request.Depth,
		Planning: r.request.Planning, Parts: plan.Parts(),
	}
}

// start puts the lane on a worker with the lease the control plane would give it for that work.
func (r *restructureRig) start(restructure any) {
	r.t.Helper()
	lease := ledger.DesiredLease{
		Project: "p1", WorkOrder: "wo", Lane: restructuredLane, Kind: string(workorders.KindPlan), Adopt: true, Inventory: true,
		Repository: "root-ws", RepositoryName: "ws", BaseRef: "origin/main", BaseCommit: "base-commit",
		Reads: []ledger.RepositoryRead{
			{Repository: "root-api", Name: "api", Ref: "origin/main", Commit: "c1"}, {Repository: "root-web", Name: "web", Ref: "origin/main", Commit: "c2"},
		},
	}
	if r.bare {
		lease.Repository, lease.RepositoryName, lease.Reads = "", "", nil
	}
	if restructure != nil {
		raw, err := json.Marshal(restructure)
		if err != nil {
			r.t.Fatal(err)
		}
		lease.Restructure = raw
	}
	if err := r.journal.SaveDesired(r.t.Context(), ledger.DesiredState{
		Rev: 1, Leases: []ledger.DesiredLease{lease}, Projects: []ledger.DesiredProject{{ID: "p1", Name: "shop"}},
	}); err != nil {
		r.t.Fatal(err)
	}
	runtime := &LaneRuntime{
		Provisioner: Provisioner{Worktrees: r.worktrees, Checkouts: r.checkouts}, Layouts: r.layouts, Render: renderFile,
	}
	r.worker = New(nil, r.journal, fake.NewClock(time.UnixMilli(1_000_000)), Config{Lanes: runtime})
	r.run = &laneRun{w: r.worker, rt: runtime, lease: lease, generation: 1}
	r.worker.running[restructuredLane] = r.run
}

// restructure runs the step a lane makes before its inventory, and says what it reported.
func (r *restructureRig) restructure() (workorders.Report, bool) {
	r.t.Helper()
	return r.run.restructure(r.t.Context(), "/work/ws", restructuredWorktree)
}

func (r *restructureRig) commits() []ports.Commit {
	r.t.Helper()
	commits, err := r.worktrees.Commits(r.t.Context(), restructuredWorktree, "base-commit")
	if err != nil {
		r.t.Fatal(err)
	}
	return commits
}

func (r *restructureRig) effect() lanes.Effect {
	r.t.Helper()
	got, err := r.journal.Effect(r.t.Context(), lanes.NewEffect(lanes.EffectRestructure, restructuredLane, 1, restructuredWorktree, "").Key)
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

// A layout that is the one the plan read gets one commit on the lane's branch with the three files, which are
// in the repository file format, and the moves are left to the person as commands.
func TestAnAdoptLaneThatReadsTheSameLayoutGetsTheWorkspaceFilesInOneCommit(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	_, choice := r.plan()
	r.start(choice)
	if report, failed := r.restructure(); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	commits := r.commits()
	if len(commits) != 1 || commits[0].Subject != planning.WorkspaceCommitSubject {
		t.Fatalf("the branch holds %+v", commits)
	}
	if want := []string{".gitignore", "Makefile", "plan/repositories.md"}; !slices.Equal(commits[0].Files, want) {
		t.Errorf("the commit holds %v, want %v", commits[0].Files, want)
	}
	manifest, _ := r.worktrees.FileText(restructuredWorktree, "plan/repositories.md")
	parsed, err := repofiles.ParseManifest([]byte(manifest))
	if err != nil {
		t.Fatalf("the manifest does not read back: %v\n%s", err, manifest)
	}
	var listed []string
	for _, repository := range parsed.Repositories {
		listed = append(listed, repository.Name+" "+repository.Remote+" "+repository.Path+" "+repository.Description)
	}
	if want := []string{"api git@example.invalid:acme/api.git repos/api ", "web git@example.invalid:acme/web.git repos/web "}; !slices.Equal(listed, want) || parsed.ID != "shop" || parsed.Plan != "" {
		t.Errorf("the manifest lists %v for project %q with plan %q, want %v", listed, parsed.ID, parsed.Plan, want)
	}
	if makefile, _ := r.worktrees.FileText(restructuredWorktree, "Makefile"); makefile != "workspace/makefile for shop over api,web\n" {
		t.Errorf("the Makefile reads:\n%s", makefile)
	}
	if ignore, _ := r.worktrees.FileText(restructuredWorktree, ".gitignore"); ignore != "repos/\n/.ropeshift/worktrees/\n" {
		t.Errorf("the ignore file reads %q", ignore)
	}
	if got := r.effect(); got.State != lanes.EffectCompleted || got.Kind != lanes.EffectRestructure {
		t.Errorf("the effect reads %+v", got)
	}
	told := r.run.restructuredOf()
	if told == nil || told.Commit != commits[0].Hash || told.Manifest != "repositories:shop" || !slices.Equal(told.Files, commits[0].Files) {
		t.Fatalf("the brief is told %+v", told)
	}
	var commands []string
	for _, move := range told.Moves {
		commands = append(commands, move.Commands...)
	}
	want := []string{
		"mv /work/api /work/ws/repos/api", "ropeshift repository add /work/ws/repos/api",
		"mv /work/web /work/ws/repos/web", "ropeshift repository add /work/ws/repos/web",
	}
	if !slices.Equal(commands, want) {
		t.Errorf("the moves are %v, want %v", commands, want)
	}
}

// A lease that names no repository and no read, as the plane of a crash run gives a lane, makes the worker read every
// clone as one the project holds no repository of. The plan read with that answer is the plan the worker reads
// again, and it lists the files to commit, which the crash matrix's rows need to reach the effect.
func TestALeaseThatNamesNoRepositoryReadsEveryCloneAsRefusedAndStillCommits(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	r.bare = true
	plan, err := adoption.Plan(t.Context(), r.request, r.checkouts, r.layouts, refusing)
	if err != nil {
		t.Fatal(err)
	}
	r.start(planning.Restructure{
		Shape: plan.Shape, Digest: plan.Digest, Root: r.request.Root, Paths: r.request.Paths, Depth: r.request.Depth,
		Planning: r.request.Planning, Parts: plan.Parts(),
	})
	if report, failed := r.restructure(); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	commits := r.commits()
	if len(commits) != 1 || commits[0].Subject != planning.WorkspaceCommitSubject || len(commits[0].Files) == 0 {
		t.Fatalf("the branch holds %+v", commits)
	}
}

// A Makefile the repository holds is untouched, and the plan the choice came from did not list one.
func TestAnExistingMakefileIsUntouchedByTheRestructuring(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	r.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md", "Makefile"}})
	_, choice := r.plan()
	r.start(choice)
	r.worktrees.SeedFile(restructuredWorktree, "Makefile", "all:\n")
	if report, failed := r.restructure(); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	if text, _ := r.worktrees.FileText(restructuredWorktree, "Makefile"); text != "all:\n" {
		t.Errorf("the Makefile reads %q", text)
	}
	if commits := r.commits(); len(commits) != 1 || slices.Contains(commits[0].Files, "Makefile") {
		t.Errorf("the branch holds %+v", commits)
	}
}

// The line for the folder a lane's worktree goes in is written when the clone does not ignore it, in the same
// commit as the line for the folder of clones, and left out when the clone ignores it already.
func TestTheWorktreeFolderLineIsWrittenOnlyWhenTheCloneDoesNotIgnoreIt(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		ignored bool
		want    string
	}{
		"absent":          {want: "repos/\n/.ropeshift/worktrees/\n"},
		"already ignored": {ignored: true, want: "repos/\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newRestructureRig(t)
			if tt.ignored {
				r.checkouts.Ignore("/work/ws", planning.WorktreesFolder)
			}
			_, choice := r.plan()
			r.start(choice)
			if report, failed := r.restructure(); failed {
				t.Fatalf("the restructuring failed: %+v", report)
			}
			if ignore, _ := r.worktrees.FileText(restructuredWorktree, ".gitignore"); ignore != tt.want {
				t.Errorf("the ignore file reads %q, want %q", ignore, tt.want)
			}
			if commits := r.commits(); len(commits) != 1 {
				t.Errorf("the branch holds %d commits, want the one", len(commits))
			}
		})
	}
}

// A layout that is not the one the plan read fails the work with layout_changed, naming what differs and the
// digests, before any file is written.
func TestALayoutThatChangedFailsTheWorkWithWhatDiffers(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	_, choice := r.plan()
	r.start(choice)
	r.clone("/work/web", fake.CloneLayout{Tracked: []string{"package.json", "docs/a.md"}, Plan: ports.PlanContent{Files: 1}})
	report, failed := r.restructure()
	if !failed || report.Outcome != workorders.OutcomeFailed || report.Reason != workorders.ReasonLayoutChanged {
		t.Fatalf("the restructuring reported %+v, failed %v", report, failed)
	}
	for _, want := range []string{"layout", choice.Digest, "/work/ws", "ropeshift adopt --restructure"} {
		if !strings.Contains(report.Detail, want) {
			t.Errorf("the detail leaves out %q: %s", want, report.Detail)
		}
	}
	if strings.Contains(report.Detail, "files") {
		t.Errorf("the detail names a part that did not change: %s", report.Detail)
	}
	if commits := r.commits(); len(commits) != 0 {
		t.Errorf("a changed layout was committed: %+v", commits)
	}
	if _, written := r.worktrees.FileText(restructuredWorktree, "plan/repositories.md"); written {
		t.Error("a changed layout was written")
	}
}

// A restructuring kept before the request its plan was read with was cannot be read against the layout, and the
// work fails with a reason of its own, restructure_incomplete, that names what it lacks and tells the person to
// plan again and restart. The layout did not change, so the reason is not layout_changed.
func TestARestructuringKeptBeforeTheRequestIsRefusedWithWhatItLacks(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	plan, _ := r.plan()
	r.start(map[string]any{"shape": plan.Shape, "digest": plan.Digest})
	report, failed := r.restructure()
	if !failed || report.Outcome != workorders.OutcomeFailed || report.Reason != workorders.ReasonRestructureIncomplete {
		t.Fatalf("the restructuring reported %+v, failed %v", report, failed)
	}
	for _, want := range []string{"no root and no parts", "ropeshift adopt --dry-run again", "restart with ropeshift adopt --restructure"} {
		if !strings.Contains(report.Detail, want) {
			t.Errorf("the detail leaves out %q: %s", want, report.Detail)
		}
	}
	if commits := r.commits(); len(commits) != 0 {
		t.Errorf("an old restructuring was committed: %+v", commits)
	}
}

// A retry after a crash between the commit and the record finds the commit and makes none: the person may have made
// the moves by then, so the layout is no longer the one the plan read, and it is not read again once the files are
// there.
func TestARetryAfterACrashBetweenTheCommitAndTheRecordMakesNoSecondCommit(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	plan, choice := r.plan()
	r.start(choice)
	// The crashed process committed the files, and died before it recorded the effect as done.
	files, err := adoption.Files(t.Context(), plan, "shop", r.checkouts, renderFile)
	if err != nil {
		t.Fatal(err)
	}
	made, err := r.worktrees.CommitFiles(t.Context(), restructuredWorktree, files, planning.WorkspaceCommitSubject)
	if err != nil || made.Hash == "" {
		t.Fatalf("the commit before the crash: %+v, %v", made, err)
	}
	effect := lanes.NewEffect(lanes.EffectRestructure, restructuredLane, 1, restructuredWorktree, "")
	if _, err := r.journal.BeginEffect(t.Context(), effect); err != nil {
		t.Fatal(err)
	}
	if _, err := r.journal.MarkUnknown(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The person moved a clone while the worker was down, as the plan's commands say, so the layout is not the
	// plan's any more: the clone is in the folder of clones, which the planning clone ignores.
	r.checkouts.Remove("/work/api")
	r.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	r.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md"}})
	if again, err := adoption.Plan(t.Context(), r.request, r.checkouts, r.layouts, known); err != nil || again.Digest == choice.Digest {
		t.Fatalf("the layout after the move reads the digest %s of the plan: %v", again.Digest, err)
	}

	if got := r.worker.find(t.Context(), r.effectOf(), ports.Located{}, false); got != lanes.Found {
		t.Fatalf("a restart looking for the commit finds %s", got)
	}
	if report, failed := r.restructure(); failed {
		t.Fatalf("the retry failed: %+v", report)
	}
	commits := r.commits()
	if len(commits) != 1 || commits[0].Hash != made.Hash {
		t.Errorf("the branch holds %+v after the retry, want the one commit %s", commits, made.Hash)
	}
	told := r.run.restructuredOf()
	if told == nil || told.Commit != made.Hash || told.Manifest != "repositories:shop" {
		t.Errorf("the brief is told %+v", told)
	}
}

// effectOf reads the effect a crash left, as a restart's pass does.
func (r *restructureRig) effectOf() lanes.Effect { return r.effect() }

// A restart looking for the commit finds nothing when the crash came before it, so the effect runs again.
func TestARestartThatFindsNoCommitFindsNothing(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	_, choice := r.plan()
	r.start(choice)
	effect := lanes.NewEffect(lanes.EffectRestructure, restructuredLane, 1, restructuredWorktree, "")
	if got := r.worker.find(t.Context(), effect, ports.Located{}, false); got != lanes.FoundNothing {
		t.Errorf("a branch with no commit reads %s", got)
	}
	if !lanes.EffectRestructure.Observable() || lanes.EffectRestructure.Repeats(lanes.EffectCompleted) {
		t.Error("the restructuring is an effect a restart observes and never repeats from done")
	}
}

// The same lane restructured twice, as a worker that runs it again does, commits once.
func TestARestructuredLaneRunAgainMakesNoSecondCommit(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	_, choice := r.plan()
	r.start(choice)
	for range 2 {
		if report, failed := r.restructure(); failed {
			t.Fatalf("the restructuring failed: %+v", report)
		}
	}
	if commits := r.commits(); len(commits) != 1 {
		t.Errorf("the branch holds %d commits", len(commits))
	}
}

// A lane with no restructuring restructures nothing.
func TestALaneWithNoRestructuringIsLeftAlone(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	r.start(nil)
	if report, failed := r.restructure(); failed || len(r.commits()) != 0 || r.run.restructuredOf() != nil {
		t.Errorf("a lane with no restructuring: %+v, failed %v", report, failed)
	}
}

// consolidating is the restructuring a member chose for a consolidation, as a plan of the layout the rig lays out
// would give it, which reads as another shape.
func (r *restructureRig) consolidating() planning.Restructure {
	r.t.Helper()
	_, choice := r.plan()
	choice.Shape = planning.ShapeConsolidate
	choice.DesignPath, choice.DesignRoot = "/work/design", "abc"
	return choice
}

// A worker whose worktrees cannot import a history fails a consolidation, and commits nothing.
func TestAConsolidationOnAStoreThatImportsNothingFailsTheLane(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	r.start(r.consolidating())
	r.run.rt.Provisioner.Worktrees = struct{ ports.FileCommits }{r.worktrees}
	report, failed := r.restructure()
	if !failed || report.Reason != workorders.ReasonSpawnFailed {
		t.Errorf("a store that imports nothing reported %+v, failed %v", report, failed)
	}
	if commits := r.commits(); len(commits) != 0 || len(r.worktrees.Imports()) != 0 {
		t.Errorf("a store that imports nothing made %+v and %+v", commits, r.worktrees.Imports())
	}
}

// A consolidation the layout no longer reads as fails with layout_changed, and imports nothing.
func TestAConsolidationChosenForALayoutThatIsNoneFailsWithLayoutChanged(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	r.start(r.consolidating())
	report, failed := r.restructure()
	if !failed || report.Reason != workorders.ReasonLayoutChanged || !strings.Contains(report.Detail, string(planning.ShapeWorkspaceFiles)) {
		t.Fatalf("the restructuring reported %+v, failed %v", report, failed)
	}
	if len(r.commits()) != 0 || len(r.worktrees.Imports()) != 0 {
		t.Errorf("a consolidation of no design repository imported %+v", r.worktrees.Imports())
	}
}

// A worker that cannot read layouts or commit files fails the lane, and says why in the log, because nothing about
// the layout was read.
func TestAWorkerThatCannotReadLayoutsFailsTheLaneThatNeedsThem(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	_, choice := r.plan()
	r.start(choice)
	r.run.rt.Layouts = nil
	report, failed := r.restructure()
	if !failed || report.Reason != workorders.ReasonSpawnFailed {
		t.Errorf("a worker with no layouts reported %+v, failed %v", report, failed)
	}
	if commits := r.commits(); len(commits) != 0 {
		t.Errorf("a worker with no layouts committed %+v", commits)
	}
}

// A lane whose restructuring lists a file written from a template, on a worker given no renderer, does not start:
// nothing is written, and the work fails with the reason a worker that cannot restructure gives.
func TestARestructuringThatListsATemplateFileOnAWorkerWithNoRendererDoesNotStart(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	r.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md"}})
	_, choice := r.plan()
	r.start(choice)
	r.run.rt.Render = nil
	report, failed := r.restructure()
	if !failed || report.Reason != workorders.ReasonSpawnFailed {
		t.Fatalf("the restructuring reported %+v, failed %v", report, failed)
	}
	if commits := r.commits(); len(commits) != 0 {
		t.Errorf("a restructuring with no renderer was committed: %+v", commits)
	}
}

// The files a layout lacks are written in the one commit, each from the template its plan line names: the instruction
// files of a workspace from the workspace template, and an AGENTS.md the repository holds is left as it is.
func TestTheFilesALayoutLacksAreWrittenFromTheirTemplatesInTheOneCommit(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	r.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md"}})
	_, choice := r.plan()
	r.start(choice)
	if report, failed := r.restructure(); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	commits := r.commits()
	if want := []string{".gitignore", "AGENTS.md", "CLAUDE.md", "Makefile", "plan/repositories.md"}; len(commits) != 1 || !slices.Equal(commits[0].Files, want) {
		t.Fatalf("the branch holds %+v, want one commit of %v", commits, want)
	}
	for file, want := range map[string]string{
		"AGENTS.md": "workspace/agents.md for shop over api,web\n", "CLAUDE.md": "claude.md for shop over api,web\n",
		"Makefile": "workspace/makefile for shop over api,web\n", ".gitignore": "workspace/gitignore for shop over api,web\n",
	} {
		if got, _ := r.worktrees.FileText(restructuredWorktree, file); got != want {
			t.Errorf("%s reads %q, want %q", file, got, want)
		}
	}
}

// An adopt lane whose repository holds no instruction file is told the starting AGENTS.md rendered for its project when
// it carries no restructuring, and to edit the committed file when the worker wrote one, with no template. One whose
// repository holds an instruction file, in any case, is told neither.
func TestAnAdoptBriefHoldsTheStartingInstructionsOnlyForARepositoryWithNone(t *testing.T) {
	t.Parallel()
	brief := func(t *testing.T, tracked []string, restructure bool) (string, string) {
		t.Helper()
		r := newRestructureRig(t)
		r.clone("/work/ws", fake.CloneLayout{Tracked: tracked})
		var choice any
		if restructure {
			_, choice = r.plan()
		}
		r.start(choice)
		if restructure {
			if report, failed := r.restructure(); failed {
				t.Fatalf("the restructuring failed: %+v", report)
			}
		}
		r.run.startingInstructions(t.Context(), "/work/ws")
		return lanes.Brief(lanes.BriefSpec{
			WorkOrder: "wo", Title: "Adopt", Kind: workorders.KindPlan, Adopt: true, Restructured: r.run.restructuredOf(), InstructionsTemplate: r.run.instructionsText(),
		}), r.run.instructionsText()
	}
	const starting = "workspace/agents.md for shop over api,web\n"
	for name, tt := range map[string]struct {
		tracked     []string
		restructure bool
		template    bool
		edits       bool
	}{
		"none, no restructuring":    {tracked: []string{"README.md", ".gitignore"}, template: true},
		"none, restructuring":       {tracked: []string{"README.md", ".gitignore"}, restructure: true, edits: true},
		"an AGENTS.md":              {tracked: []string{"README.md", "AGENTS.md"}},
		"a lowercase agents.md":     {tracked: []string{"README.md", "agents.md"}},
		"a CLAUDE.md, restructured": {tracked: []string{"README.md", "CLAUDE.md"}, restructure: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			text, kept := brief(t, tt.tracked, tt.restructure)
			if (kept != "") != tt.template {
				t.Errorf("the worker keeps the starting instructions %q, want one: %v", kept, tt.template)
			}
			if got := strings.Contains(text, starting) && strings.Contains(text, "holds no instruction file"); got != tt.template {
				t.Errorf("the brief shows the starting instructions: %v, want %v\n%s", got, tt.template, text)
			}
			if got := strings.Contains(text, "Edit the committed AGENTS.md"); got != tt.edits {
				t.Errorf("the brief tells the lane to edit the committed file: %v, want %v\n%s", got, tt.edits, text)
			}
			if tt.edits && strings.Contains(text, starting) {
				t.Errorf("the brief shows a template beside the file the worker wrote:\n%s", text)
			}
		})
	}
}

// The starting instructions are rendered over the repositories the written files carry, which leave out a name a
// Makefile cannot hold, and from the template of the shape the lane's restructuring chose.
func TestTheStartingInstructionsUseTheFilteredRepositoriesAndTheShapesTemplate(t *testing.T) {
	t.Parallel()
	text := func(t *testing.T, shape planning.LayoutShape) string {
		t.Helper()
		r := newRestructureRig(t)
		r.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md", ".gitignore"}})
		_, choice := r.plan()
		choice.Shape = shape
		r.start(choice)
		r.run.lease.Reads = append(r.run.lease.Reads, ledger.RepositoryRead{Repository: "root-odd", Name: "we$ird", Ref: "origin/main", Commit: "c3"})
		// A restructuring that wrote no AGENTS.md leaves the lane to write it.
		r.run.setRestructured(lanes.Restructured{Files: []string{"Makefile"}})
		r.run.startingInstructions(t.Context(), "/work/ws")
		return r.run.instructionsText()
	}
	if got, want := text(t, planning.ShapeWorkspaceFiles), "workspace/agents.md for shop over api,web\n"; got != want {
		t.Errorf("a workspace starts from %q, want %q", got, want)
	}
	if got, want := text(t, planning.ShapeSingle), "monorepo/agents.md for shop over api,web\n"; got != want {
		t.Errorf("a single repository starts from %q, want %q", got, want)
	}
}

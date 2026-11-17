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
	r.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md"}})
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
		Provisioner: Provisioner{Worktrees: r.worktrees, Checkouts: r.checkouts}, Layouts: r.layouts,
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
	makefile, _ := r.worktrees.FileText(restructuredWorktree, "Makefile")
	if want := planning.RenderWorkspaceMakefile([]planning.WorkspaceRepository{
		{Name: "api", Remote: "git@example.invalid:acme/api.git", Path: "repos/api"}, {Name: "web", Remote: "git@example.invalid:acme/web.git", Path: "repos/web"},
	}); makefile != want {
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
// work fails with a reason that names what it lacks.
func TestARestructuringKeptBeforeTheRequestIsRefusedWithWhatItLacks(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	plan, _ := r.plan()
	r.start(map[string]any{"shape": plan.Shape, "digest": plan.Digest})
	report, failed := r.restructure()
	if !failed || report.Reason != workorders.ReasonLayoutChanged {
		t.Fatalf("the restructuring reported %+v, failed %v", report, failed)
	}
	for _, want := range []string{"root", "parts", "ropeshift adopt --restructure"} {
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
	files, err := adoption.Files(t.Context(), plan, "shop", r.checkouts)
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

// A lane with no restructuring, and one whose shape offers nothing the worker writes yet, restructure nothing.
func TestALaneWithNoRestructuringOrAShapeTheWorkerDoesNotWriteIsLeftAlone(t *testing.T) {
	t.Parallel()
	r := newRestructureRig(t)
	r.start(nil)
	if report, failed := r.restructure(); failed || len(r.commits()) != 0 || r.run.restructuredOf() != nil {
		t.Errorf("a lane with no restructuring: %+v, failed %v", report, failed)
	}
	plan, choice := r.plan()
	choice.Shape = planning.ShapeConsolidate
	choice.DesignPath, choice.DesignRoot = "/work/design", "abc"
	_ = plan
	r.start(choice)
	if report, failed := r.restructure(); failed || len(r.commits()) != 0 {
		t.Errorf("a consolidation: %+v, failed %v", report, failed)
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

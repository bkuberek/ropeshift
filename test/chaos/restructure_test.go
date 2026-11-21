//go:build chaos

package chaos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
)

// witnessCommit is a commit of the workspace files. It happens once per lane, however a crash falls.
const witnessCommit = "commit"

// committingWorktrees is a stand-in worktree that also keeps the commits made to it on disk, as a branch does, so a
// process that starts after a kill finds what the one before it committed. It makes a commit for every call and
// holds nothing back, so a second commit shows on the branch and in the witness, and only the worker keeps it from
// making one.
type committingWorktrees struct{ lastingWorktrees }

// branch is the file that stands for the commits of the worktree at path, one JSON commit to a line, oldest first.
func (w committingWorktrees) branch(at string) string {
	return filepath.Join(w.dir, branchPrefix+strings.ReplaceAll(at, "/", "-"))
}

// branchPrefix begins the name of each file that stands for a branch, which the settled check looks for.
const branchPrefix = "commits"

func (w committingWorktrees) CommitFiles(_ context.Context, at string, files []ports.TreeFile, subject string) (ports.Commit, error) {
	held, err := readBranch(w.branch(at))
	if err != nil {
		return ports.Commit{}, err
	}
	made := ports.Commit{Hash: fmt.Sprintf("commit-%d", len(held)+1), Subject: subject}
	for _, file := range files {
		made.Files = append(made.Files, file.Path)
	}
	raw, err := json.Marshal(made)
	if err != nil {
		return ports.Commit{}, err
	}
	if err := w.log.Append(Record{Kind: Completed, Op: witnessCommit, Key: at, Actor: "driver", Note: subject}); err != nil {
		return ports.Commit{}, err
	}
	file, err := os.OpenFile(w.branch(at), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return ports.Commit{}, err
	}
	_, err = file.Write(append(raw, '\n'))
	return made, errors.Join(err, file.Close())
}

// Commits lists the branch newest first. A worktree that was never made is not found.
func (w committingWorktrees) Commits(ctx context.Context, at, _ string) ([]ports.Commit, error) {
	if _, err := w.Inspect(ctx, at); err != nil {
		return nil, err
	}
	held, err := readBranch(w.branch(at))
	slices.Reverse(held)
	return held, err
}

// BranchChanges lists nothing: the restructure these tests drive reads no push, and a branch of the repository is not a
// worktree's branch file.
func (committingWorktrees) BranchChanges(context.Context, string, string, string) ([]ports.Commit, error) {
	return nil, nil
}

// Tracks says the index holds nothing: these worktrees keep commits in a branch file and no index. A worktree that was
// never made is not found.
func (w committingWorktrees) Tracks(ctx context.Context, at, _ string) (bool, error) {
	_, err := w.Inspect(ctx, at)
	return false, err
}

// readBranch reads the commits a branch file holds, oldest first. A branch with none has no file.
func readBranch(file string) ([]ports.Commit, error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var held []ports.Commit
	if len(bytes.TrimSpace(raw)) == 0 {
		return held, nil
	}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var one ports.Commit
		if err := json.Unmarshal(line, &one); err != nil {
			return nil, fmt.Errorf("a commit of %s: %w", file, err)
		}
		held = append(held, one)
	}
	return held, nil
}

// renderTemplate stands in for the templates a worker writes the files a layout lacks from, which no worker imports.
func renderTemplate(name string, _ projectfiles.Data) (string, error) {
	return "from " + name + "\n", nil
}

// restructureRequest is what `ropeshift adopt --restructure /work/api /work/web` reads in the clone /work/ws: a
// planning clone with no plan content and two clones beside it, which reads as the workspace_files shape.
var restructureRequest = adoption.Request{Root: "/work/ws", Paths: []string{"/work/api", "/work/web"}, Depth: 1, Planning: "ws"}

// restructureClones places the clones restructureRequest reads, in the worker's memory. A process makes them again
// at every start, and they read the same each time.
func restructureClones() (*fake.Checkouts, *fake.Layouts) {
	checkouts, layouts := fake.NewCheckouts(), fake.NewLayouts()
	for _, clone := range []struct{ at, tracked string }{{"/work/ws", "README.md"}, {"/work/api", "go.mod"}, {"/work/web", "package.json"}} {
		name := path.Base(clone.at)
		checkouts.Place(ports.Clone{Path: clone.at, ID: "root-" + name, Remote: "git@example.invalid:acme/" + name + ".git", DefaultBranch: "main"})
		layouts.Set(clone.at, fake.CloneLayout{Tracked: []string{clone.tracked}})
	}
	return checkouts, layouts
}

// aWorkersRegister is the dry run of a registration as a worker answers it from the records its lease holds. The
// plane of a crash run places no lane, so a lease names no repository and no read, and a worker finds that the
// project holds a repository of no clone. The plan's digest leaves out what a registration would do, so the answer
// changes no digest, and the worker's own test of a lease like this one holds that it reads the plan this reads.
func aWorkersRegister(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
	var out []planning.PlannedRepository
	for _, one := range clones {
		out = append(out, planning.PlannedRepository{
			Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.RegistrationRefused), Reason: "the project holds no repository of it",
		})
	}
	return out, nil
}

// restructureChoice plans the layout as a member's command does, and keeps the restructuring the adopt work carries.
func restructureChoice(ctx context.Context) (planning.Restructure, planning.AdoptionPlan, error) {
	checkouts, layouts := restructureClones()
	plan, err := adoption.Plan(ctx, restructureRequest, checkouts, layouts, aWorkersRegister)
	if err != nil {
		return planning.Restructure{}, plan, err
	}
	return planning.Restructure{
		Shape: plan.Shape, Digest: plan.Digest, Root: restructureRequest.Root, Paths: restructureRequest.Paths, Depth: restructureRequest.Depth,
		Planning: restructureRequest.Planning, Parts: plan.Parts(),
	}, plan, nil
}

// TestTheRestructureRowsHaveFilesToCommit holds what the rows kill a worker in: a layout that plans a restructuring
// the worker accepts and files for it to commit. A layout that listed none would leave the effect never made, and
// every row would end with the kill never reached.
func TestTheRestructureRowsHaveFilesToCommit(t *testing.T) {
	choice, plan, err := restructureChoice(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := choice.Validate(); err != nil || len(choice.Missing()) != 0 {
		t.Fatalf("the restructuring reads %+v, which the worker refuses: %v, lacking %v", choice, err, choice.Missing())
	}
	if choice.Shape != planning.ShapeWorkspaceFiles {
		t.Fatalf("the layout reads as the %s shape", choice.Shape)
	}
	checkouts, _ := restructureClones()
	files, err := adoption.Files(t.Context(), plan, ledger.DefaultProjectName, checkouts, renderTemplate)
	if err != nil || len(files) == 0 {
		t.Fatalf("the plan lists %d files to commit: %v", len(files), err)
	}
	again, _, err := restructureChoice(t.Context())
	if err != nil || again.Digest != choice.Digest {
		t.Fatalf("the layout read twice gives the digests %s and %s: %v", choice.Digest, again.Digest, err)
	}
}

// roleWorkerAdopt runs one adopt lane through a real worker whose layout is the one its work was planned
// from, on a worktree that keeps its commits through a kill. The lane ends at its inventory, which this worker has
// no reader for, and everything a row kills it in comes before that.
func roleWorkerAdopt(c *child) error {
	journal, err := sqlite.OpenJournal(c.spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	if err := journal.Migrate(c.ctx); err != nil {
		return err
	}
	client := api.NewUnixClient(c.spec.Socket).Project(ledger.DefaultProjectName)
	defer client.Close()
	dir := filepath.Dir(c.spec.Journal)
	driver := &lastingDriver{AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal}
	driver.Play(driver.start())
	checkouts, layouts := restructureClones()
	subject := worker.New(workerlink.New(client.Client), journal, clock.System{}, workerOf(c, &worker.LaneRuntime{
		Driver: driver,
		Provisioner: worker.Provisioner{
			Profiles: fake.NewProfileSource(), Worktrees: committingWorktrees{lastingWorktrees{dir: dir, log: c.log}}, Checkouts: checkouts,
		},
		Layouts:    layouts,
		Render:     renderTemplate,
		Secrets:    fake.SecretResolver{},
		Repository: func(context.Context, string, string) (string, error) { return "/repository", nil },
	}))
	return claimAndConverge(c, subject, client)
}

// restructureSpec records ready work that adopts the project with the restructuring its plan chose, and the claim
// and the lane a worker takes it with.
func restructureSpec(w *world) (childSpec, error) {
	w.serveOnce()
	choice, _, err := restructureChoice(w.rig.Ctx)
	if err != nil {
		return childSpec{}, err
	}
	ops := w.rig.NewOps(w.store)
	made, err := ops.Record(w.rig.Ctx, w.rig.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: w.rig.ID("adopt"), Kind: workorders.KindPlan, Title: "Adopt the project", Adopt: true, Restructure: &choice,
			MaxAttempts: 1, Needs: map[string]string{"driver": "claude-code"},
		},
		Statement: "Onboard the project.",
		Criteria:  []planning.Criterion{{ID: "c1", Text: "It holds: c1"}},
	}, w.rig.Actor)
	if err != nil {
		return childSpec{}, err
	}
	if _, err := ops.Pass(w.rig.Ctx, w.rig.Scope, made.ID, w.rig.Actor); err != nil {
		return childSpec{}, err
	}
	spec, err := w.workerSpec("worker-adopt")
	spec.WorkOrder, spec.ClaimID, spec.Lane = made.ID, w.rig.ID("claim"), w.rig.ID("lane")
	return spec, err
}

// committedOnce reads the witness, which no retry can rewrite, and the branch: one commit of the workspace files
// was made for the lane, and the branch holds that one, whichever step the worker died at.
func committedOnce(w *world, _ childSpec) error {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	made := 0
	for _, record := range records {
		if record.Kind == Completed && record.Actor == "driver" && record.Op == witnessCommit {
			made++
		}
	}
	if made != 1 {
		return contract.Violatedf("%d commits of the workspace files were made for one lane, want one", made)
	}
	branches, err := filepath.Glob(filepath.Join(w.target.Dir, branchPrefix+"-*"))
	if err != nil || len(branches) != 1 {
		return contract.Violatedf("%d branches hold commits, want the one lane's: %v", len(branches), err)
	}
	held, err := readBranch(branches[0])
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	if len(held) != 1 || held[0].Subject != planning.WorkspaceCommitSubject {
		return contract.Violatedf("the branch holds %+v, want one commit with the subject %q", held, planning.WorkspaceCommitSubject)
	}
	return nil
}

// restructureCases kills a worker at each step of the bracket around the commit of the workspace files, and holds
// that the retry finds the commit or makes it, and never makes a second.
func restructureCases() []crashCase {
	return []crashCase{{
		name: "bracket effect.restructure", op: "effect.restructure", steps: []string{"started", "performed", "finished"},
		prepare: restructureSpec, settled: committedOnce,
	}}
}

var _ ports.FileCommits = committingWorktrees{}

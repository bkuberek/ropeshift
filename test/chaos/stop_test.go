//go:build chaos

package chaos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/gitcli"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/workerlink"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/gittest"
)

// The operations the witness counts, and what a stopping lane's session can do.
const (
	witnessWork  = "work"
	witnessIndex = "index"
	witnessHook  = "hook"
	// laneDied ends a session with no stop, so only the last guard runs.
	laneDied = "died"
	// laneFenced loses the lane's lease while its session runs.
	laneFenced = "fenced"
)

// The broken variants of a stop, each of which the oracle below must catch.
const (
	sabotageTrackedOnly = "a snapshot that omits untracked files"
	sabotageNoGuard     = "a stop that skips the guard"
	sabotageFenced      = "a fenced lane that skips the guard"
	sabotageKeeps       = "an ended lane that keeps its resources"
	sabotageOwnIndex    = "a snapshot built in the lane's own index"
)

// workFiles is what a session leaves, one of each thing a stop must save.
var workFiles = map[string]string{
	"committed.txt":      "a commit the remote lacks\n",
	"README.txt":         "a change to a tracked file\n",
	"deep/untracked.txt": "a file git has not seen\n",
	"staged.txt":         "a file in the index\n",
}

// workDriver is a session that writes real files, and tells the witness first, so no
// database and no ref can be the reason a check passes.
type workDriver struct {
	*lastingDriver
	gitEnv []string
	died   bool
	// fence takes the lease away, where a lane's session has reached its work.
	fence func(ports.SessionSpec)
}

func gitIn(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (d *workDriver) write(dir string) error {
	for name, content := range workFiles {
		if err := d.log.Append(Record{Kind: Attempted, Op: witnessWork, Key: name, Note: content, Actor: "session"}); err != nil {
			return err
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	if _, err := gitIn(dir, d.gitEnv, "add", "--", "committed.txt", "staged.txt"); err != nil {
		return err
	}
	if _, err := gitIn(dir, d.gitEnv, "commit", "--quiet", "-m", "lane work", "--", "committed.txt"); err != nil {
		return err
	}
	digest, err := indexDigest(dir, d.gitEnv)
	if err != nil {
		return err
	}
	return d.log.Append(Record{Kind: Completed, Op: witnessIndex, Key: dir, Note: digest, Actor: "session"})
}

// indexDigest reads the index a lane's worktree uses, by the path git names for it.
func indexDigest(dir string, env []string) (string, error) {
	path, err := gitIn(dir, env, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (d *workDriver) script() fake.DriverScript {
	work := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		if err := d.write(spec.Dir); err != nil {
			fmt.Fprintln(os.Stderr, "session:", err)
		}
	}}
	report := fake.DriverStep{Do: func(spec ports.SessionSpec) { d.report(spec, "") }}
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.01}}
	steps := []fake.DriverStep{{Bound: true}, work, report, turn}
	if d.died {
		steps = []fake.DriverStep{{Bound: true}, work, {Crash: true}}
	}
	if d.fence != nil {
		steps = []fake.DriverStep{{Bound: true}, work, {Do: d.fence}, {Hold: true}}
	}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{steps}}
}

// witnessHooks stands for a repository's setup script, and tells the witness it ran.
type witnessHooks struct{ log *EffectLog }

func (h witnessHooks) Run(_ context.Context, command ports.HookCommand) (ports.HookResult, error) {
	return ports.HookResult{}, h.log.Append(Record{Kind: Completed, Op: witnessHook, Key: command.Dir, Actor: "driver"})
}

func applyStopVariant(variant string) {
	switch variant {
	case sabotageBlindRemoval:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageBlindRemoval)
	case sabotageTrackedOnly:
		gitcli.Sabotaged(gitcli.Sabotage{TrackedOnly: true})
	case sabotageOwnIndex:
		gitcli.Sabotaged(gitcli.Sabotage{OwnIndex: true})
	case sabotageNoGuard, sabotageFenced:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageNoGuard)
	case sabotageKeeps:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageKeepResources)
	}
}

// roleWorkerStop runs one lane to its end on a real repository. It runs one more pass
// after that, as the next tick would, and acts on any request to remove a worktree.
func roleWorkerStop(c *child) error {
	applyStopVariant(c.spec.Variant)
	journal, err := sqlite.OpenJournal(c.spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	if err := journal.Migrate(c.ctx); err != nil {
		return err
	}
	client := api.NewUnixClient(c.spec.Socket)
	defer client.Close()
	dir := filepath.Dir(c.spec.Journal)
	driver := &workDriver{
		lastingDriver: &lastingDriver{AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal},
		gitEnv:        c.spec.GitEnv, died: c.spec.Script == laneDied,
	}
	profiles := fake.NewProfileSource()
	profiles.PutUser(c.spec.Profile)
	subject := worker.New(workerlink.New(client), journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver: driver,
			Provisioner: worker.Provisioner{
				Profiles: profiles, Worktrees: gitcli.Worktrees{Env: c.spec.GitEnv}, Hooks: witnessHooks{log: c.log},
			},
			Secrets:    fake.SecretResolver{},
			Repository: func(context.Context, string) (string, error) { return c.spec.Repository, nil },
		},
	})
	if err := subject.Start(c.ctx); err != nil {
		return err
	}
	grant, err := client.Claim(c.ctx, c.spec.WorkOrder, api.ClaimRequest{
		ClaimID: c.spec.ClaimID, WorkerID: subject.Auth().WorkerID, TTLMs: longTTL,
	})
	var refused *api.Error
	if err != nil && !errors.As(err, &refused) {
		return err
	}
	if c.spec.Script == laneFenced {
		driver.fence = c.loseLease(grant)
		stop := beatUntilDone(c.ctx, subject)
		defer stop()
	}
	driver.Play(driver.script())
	_, err = client.StartLane(c.ctx, c.spec.WorkOrder, api.StartLane{LaneID: c.spec.Lane})
	if err != nil && !errors.As(err, &refused) {
		return err
	}
	if err := subject.Converge(c.ctx); err != nil {
		return err
	}
	if err := subject.Sync(c.ctx); err != nil {
		return err
	}
	if _, err := subject.Reconcile(c.ctx); err != nil {
		return err
	}
	subject.Wait()
	return subject.Upload(c.ctx)
}

// loseLease moves the fence under a running lane, the way a reassignment does.
func (c *child) loseLease(grant api.Grant) func(ports.SessionSpec) {
	return func(spec ports.SessionSpec) {
		holder := workorders.Holder{
			Lease: workorders.Lease{WorkOrder: grant.WorkOrder, ClaimID: grant.ClaimID, Fence: grant.Fence},
			Lane:  spec.Lane, Generation: spec.Generation,
		}
		result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "taken"}}
		if err := control.NewLeases(c.store).EndLane(c.ctx, c.scope, holder, result, c.member()); err != nil {
			fmt.Fprintln(os.Stderr, "losing the lease:", err)
		}
	}
}

// beatUntilDone beats as a running daemon does, which is how a worker learns its fence moved.
func beatUntilDone(ctx context.Context, subject *worker.Worker) func() {
	beating, stop := context.WithCancel(ctx)
	go func() {
		for beating.Err() == nil {
			_, _ = subject.Beat(beating)
			_ = subject.Upload(beating)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	return stop
}

// stopRepo is a repository and, for most cases, a bare remote that holds its history.
func stopRepo(w *world, remote bool) (*gittest.Repo, string) {
	repo := gittest.New(w.t)
	repo.Commit("README.txt", "seed\n")
	if !remote {
		return repo, ""
	}
	bare := filepath.Join(filepath.Dir(repo.Dir), "remote.git")
	repo.Git("init", "--quiet", "--bare", bare)
	repo.Git("remote", "add", "origin", bare)
	repo.Git("push", "--quiet", "origin", "main")
	repo.Git("fetch", "--quiet", "origin")
	return repo, bare
}

func stopSpec(w *world, remote bool, script, profile string) (childSpec, error) {
	w.serveOnce()
	id, err := w.rig.Ready(w.store)
	if err != nil {
		return childSpec{}, err
	}
	spec, err := w.workerSpec("worker-stop")
	if err != nil {
		return childSpec{}, err
	}
	repo, bare := stopRepo(w, remote)
	spec.WorkOrder, spec.ClaimID, spec.Lane = id, w.rig.ID("claim"), w.rig.ID("lane")
	spec.Repository, spec.GitEnv, spec.Path, spec.Script, spec.Profile = repo.Dir, repo.Env(), bare, script, profile
	return spec, nil
}

func (s childSpec) worktree() string {
	return filepath.Join(filepath.Dir(s.Repository), filepath.Base(s.Repository)+"-lanes", s.Lane)
}

// refsHolding lists the refs of a repository whose tree holds this content at this path.
func refsHolding(dir string, env []string, name, content string) []string {
	listed, err := gitIn(dir, env, "for-each-ref", "--format=%(refname)")
	if err != nil {
		return nil
	}
	var found []string
	for ref := range strings.SplitSeq(listed, "\n") {
		if got, err := gitIn(dir, env, "show", ref+":"+name); err == nil && got+"\n" == content {
			found = append(found, ref)
		}
	}
	return found
}

// workSaved is the oracle of a stop. It reads the witness, so nothing a worker wrote can
// vouch for it. Everything a session wrote must be on the remote, or, with no remote, still
// in the worktree and in a local ref, and the lane's index must be as the session left it.
func workSaved(w *world, spec childSpec) error {
	wrote, index, err := witnessed(w)
	if err != nil {
		return err
	}
	for name, content := range wrote {
		if spec.Path != "" {
			if len(refsHolding(spec.Path, spec.GitEnv, name, content)) == 0 {
				return contract.Violatedf("no ref on the remote holds %s", name)
			}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(spec.worktree(), name))
		if err != nil || string(raw) != content {
			return contract.Violatedf("the worktree lost %s: %v", name, err)
		}
		if len(refsHolding(spec.Repository, spec.GitEnv, name, content)) == 0 {
			return contract.Violatedf("no local ref holds %s", name)
		}
	}
	if index != "" {
		got, err := indexDigest(spec.worktree(), spec.GitEnv)
		if err != nil {
			return contract.Violatedf("the worktree went: %v", err)
		}
		if got != index {
			return contract.Violatedf("a stop changed the lane's index")
		}
	}
	return nothingHeld(spec)
}

func witnessed(w *world) (map[string]string, string, error) {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return nil, "", contract.Violatedf("%v", err)
	}
	wrote := map[string]string{}
	index := ""
	for _, record := range records {
		switch record.Op {
		case witnessWork:
			wrote[record.Key] = record.Note
		case witnessIndex:
			index = record.Note
		}
	}
	return wrote, index, nil
}

// workOnRemote is what stays true with the worktree gone.
func workOnRemote(w *world, spec childSpec) error {
	wrote, _, err := witnessed(w)
	if err != nil {
		return err
	}
	for name, content := range wrote {
		if len(refsHolding(spec.Path, spec.GitEnv, name, content)) == 0 {
			return contract.Violatedf("a removal left no ref on the remote holding %s", name)
		}
	}
	return nothingHeld(spec)
}

func nothingHeld(spec childSpec) error {
	journal, err := sqlite.OpenJournal(spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	held, err := journal.Allocations(context.Background())
	if err != nil {
		return err
	}
	if len(held) != 0 {
		return contract.Violatedf("an ended lane still holds %+v", held)
	}
	return nil
}

func oneOutcome(w *world, spec childSpec) error {
	row, err := w.rig.WorkOrder(w.store, spec.WorkOrder)
	if err != nil {
		return err
	}
	if !slices.Contains([]workorders.State{workorders.Acceptance, workorders.Blocked, workorders.Failed, workorders.Ready}, row.State) {
		return contract.Violatedf("the lane has no outcome: %s", row.State)
	}
	return nil
}

// stopCase kills a worker at each step of a stop that has a remote to save to.
func stopCase(op string, steps ...string) crashCase {
	return crashCase{
		name: "stop " + op, op: op, steps: steps,
		prepare: func(w *world) (childSpec, error) { return stopSpec(w, true, "", "") },
		settled: func(w *world, spec childSpec) error {
			if err := oneOutcome(w, spec); err != nil {
				return err
			}
			return workSaved(w, spec)
		},
	}
}

// diedCase runs the same steps in the one guard a session that died leaves.
func diedCase(op string, steps ...string) crashCase {
	c := stopCase(op, steps...)
	c.name = "a session that died at " + op
	c.prepare = func(w *world) (childSpec, error) { return stopSpec(w, true, laneDied, noRestartsProfile) }
	return c
}

// noRemoteCase keeps what it cannot push: the worktree stays, a ref holds it, and the
// ledger says so.
func noRemoteCase() crashCase {
	return crashCase{
		name: "stop with no remote", op: "worker.guard", steps: []string{"surveyed", "snapshotted", "saved"},
		prepare: func(w *world) (childSpec, error) { return stopSpec(w, false, "", "") },
		settled: func(w *world, spec childSpec) error {
			if err := workSaved(w, spec); err != nil {
				return err
			}
			view, err := w.laneView(spec.Lane)
			if err != nil {
				return err
			}
			if !slices.Contains(view.Conditions, "unpushed_work") {
				return contract.Violatedf("the ledger does not show the work: %+v", view)
			}
			return nil
		},
	}
}

const noRestartsProfile = "[lifecycle]\nmax_restarts = 0\n"

func (w *world) laneView(lane string) (ledger.LaneView, error) {
	return control.NewWorkers(w.store, w.rig.Scope, control.WorkerOptions{}).Lane(w.rig.Ctx, lane)
}

// removalCase kills a worker while it takes a worktree away. Work the lane pushed needs no
// force, and the retry must finish the removal once.
func removalCase() crashCase {
	return crashCase{
		name: "remove a worktree", op: "effect.removal", steps: []string{"started", "performed", "finished"},
		prepare: prepareRemoval,
		settled: removalSettled,
	}
}

func prepareRemoval(w *world) (childSpec, error) {
	spec, err := stopSpec(w, true, "", "")
	if err != nil {
		return childSpec{}, err
	}
	code, signaled, stderr, err := runToEnd(w.spec(spec))
	if err != nil {
		return childSpec{}, err
	}
	if signaled || code != exitDone {
		return childSpec{}, fmt.Errorf("the lane before the removal: exit %d: %s", code, stderr)
	}
	return spec, control.NewLeases(w.store).ArchiveLane(w.rig.Ctx, w.rig.Scope, spec.WorkOrder, spec.Lane, false, memberActor)
}

var memberActor = ledger.Actor{Kind: ledger.ActorMember, ID: "member"}

func removalSettled(w *world, spec childSpec) error {
	if _, err := os.Stat(spec.worktree()); !errors.Is(err, os.ErrNotExist) {
		return contract.Violatedf("the worktree stands: %v", err)
	}
	if err := countEvents(w, ledger.EventLaneArchived, spec.Lane, 1); err != nil {
		return err
	}
	return workOnRemote(w, spec)
}

func archivedStepCase() crashCase {
	c := removalCase()
	c.name, c.op, c.steps = "record a removal", "worker.lane", []string{"archived"}
	return c
}

// hookCase kills a worker around a lane's setup hook. It runs at most once, and a run a
// crash left unknown stops the lane for a person.
func hookCase() crashCase {
	return crashCase{
		name: "a lane's setup hook", op: "effect.hook", steps: []string{"started", "performed", "finished"},
		prepare: func(w *world) (childSpec, error) {
			return stopSpec(w, true, "", "[hooks]\non_lane_created = [\"setup\"]\n")
		},
		settled: func(w *world, spec childSpec) error {
			records, err := ReadEffectLog(w.logPath)
			if err != nil {
				return contract.Violatedf("%v", err)
			}
			ran := 0
			for _, record := range records {
				if record.Op == witnessHook {
					ran++
				}
			}
			if ran > 1 {
				return contract.Violatedf("a setup hook ran %d times", ran)
			}
			return oneOutcome(w, spec)
		},
	}
}

// fencedCase is a lane that lost its lease. What it wrote goes to a salvage ref on the
// remote, its branch stays off the remote, and the ledger takes the record from a holder it
// no longer accepts anything else from.
func fencedCase(op string, steps ...string) crashCase {
	return crashCase{
		name: "a fenced lane at " + op, op: op, steps: steps,
		prepare: func(w *world) (childSpec, error) { return stopSpec(w, true, laneFenced, "") },
		settled: fencedSettled,
	}
}

func fencedSettled(w *world, spec childSpec) error {
	wrote, _, err := witnessed(w)
	if err != nil {
		return err
	}
	for name, content := range wrote {
		found := refsHolding(spec.Path, spec.GitEnv, name, content)
		if !slices.ContainsFunc(found, func(ref string) bool { return strings.HasPrefix(ref, "refs/ropeshift/salvage/"+spec.Lane+"/") }) {
			return contract.Violatedf("no salvage ref on the remote holds %s: %v", name, found)
		}
	}
	if heads, err := gitIn(spec.Path, spec.GitEnv, "for-each-ref", "refs/heads/lane"); err != nil || heads != "" {
		return contract.Violatedf("a fenced lane moved a branch: %q: %v", heads, err)
	}
	view, err := w.laneView(spec.Lane)
	if err != nil {
		return err
	}
	if len(wrote) != 0 && !slices.ContainsFunc(view.Artifacts, func(a ledger.Artifact) bool { return strings.Contains(a.URI, "/salvage/") }) {
		return contract.Violatedf("the ledger holds no salvage: %+v", view.Artifacts)
	}
	return nil
}

func stopCases() []crashCase {
	const (
		surveyed, pushed, snapshotted, snapshotPushed, saved = "surveyed", "pushed", "snapshotted", "snapshot-pushed", "saved"
	)
	return []crashCase{
		stopCase("worker.guard", surveyed, pushed, snapshotted, snapshotPushed, saved),
		stopCase("effect.guard", "started", "performed", "finished"),
		stopCase("worktree.snapshot", "read", "added", "committed", "recorded"),
		stopCase("worker.lane", "allocated"),
		diedCase("worker.guard", surveyed, pushed, snapshotted, snapshotPushed, saved),
		diedCase("worktree.snapshot", "read", "added", "committed", "recorded"),
		fencedCase("worker.guard", surveyed, pushed, snapshotted, snapshotPushed, saved),
		noRemoteCase(),
		removalCase(),
		archivedStepCase(),
		hookCase(),
	}
}

// TestStopRandomKills kills a stopping lane at random moments, which can land inside git
// itself, and repeats it as a restart would, until the lane ends. No work may be lost
// however a kill falls.
func TestStopRandomKills(t *testing.T) {
	kills := min(crashKills(12), 12)
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 17))
	for round := range kills {
		w := newWorld(t, "")
		spec, err := stopSpec(w, true, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := killStopThenFinish(w, spec, rng); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	t.Logf("stops killed: %d", kills)
}

func killStopThenFinish(w *world, spec childSpec, rng *rand.Rand) error {
	spec = w.spec(spec)
	for range 3 {
		p, err := startChild(spec)
		if err != nil {
			return err
		}
		time.Sleep(time.Duration(rng.Int64N(int64(250 * time.Millisecond))))
		p.sigkill()
	}
	var last error
	for range 20 {
		code, signaled, stderr, err := runToEnd(spec)
		if err != nil {
			return err
		}
		if !signaled && code == exitDone {
			if err := oneOutcome(w, spec); err == nil {
				return workSaved(w, spec)
			}
			continue
		}
		last = fmt.Errorf("exit %d: %s", code, stderr)
		time.Sleep(50 * time.Millisecond)
	}
	return errors.Join(errors.New("a stop never converged"), last)
}

// checkNoForce is a remote that moved. A guard's push is refused, the worktree stays, and
// the remote keeps what it had.
func checkNoForce(w *world) error {
	repo, bare := stopRepo(w, true)
	ctx := w.rig.Ctx
	worktrees := gitcli.Worktrees{Env: repo.Env()}
	path := filepath.Join(filepath.Dir(repo.Dir), "lanes", "one")
	if _, err := worktrees.Create(ctx, ports.WorktreeSpec{Repository: repo.Dir, Path: path, Branch: "lane/one"}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "one.txt"), []byte("one\n"), 0o600); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", "--", "one.txt"}, {"commit", "--quiet", "-m", "one"}, {"push", "--quiet", "origin", "lane/one"}} {
		if _, err := gitIn(path, repo.Env(), args...); err != nil {
			return err
		}
	}
	on, err := gitIn(bare, repo.Env(), "rev-parse", "refs/heads/lane/one")
	if err != nil {
		return err
	}
	if _, err := gitIn(path, repo.Env(), "commit", "--quiet", "--amend", "-m", "rewritten"); err != nil {
		return err
	}
	_ = worktrees.Push(ctx, path, "origin", "refs/heads/lane/one")
	after, err := gitIn(bare, repo.Env(), "rev-parse", "refs/heads/lane/one")
	if err != nil {
		return err
	}
	if after != on {
		return contract.Violatedf("a guard's push rewrote the remote")
	}
	return nil
}

// sabotageBlindRemoval names the variant that removes a worktree with no second reading.
const sabotageBlindRemoval = "a removal that reads nothing first"

// checkRemovalReads asks for a removal of a clean lane, and leaves new work in the worktree
// before the worker acts. The work must survive.
func checkRemovalReads(w *world) error {
	spec, err := prepareRemoval(w)
	if err != nil {
		return err
	}
	const late = "work that came after the stop\n"
	if err := os.WriteFile(filepath.Join(spec.worktree(), "late.txt"), []byte(late), 0o600); err != nil {
		return err
	}
	spec = w.spec(spec)
	code, signaled, stderr, err := runToEnd(spec)
	if err != nil {
		return err
	}
	if signaled || code != exitDone {
		return fmt.Errorf("the removal: exit %d: %s", code, stderr)
	}
	if len(refsHolding(spec.Path, spec.GitEnv, "late.txt", late)) == 0 {
		if raw, err := os.ReadFile(filepath.Join(spec.worktree(), "late.txt")); err != nil || string(raw) != late {
			return contract.Violatedf("a removal lost work that came after the stop: %v", err)
		}
	}
	return nil
}

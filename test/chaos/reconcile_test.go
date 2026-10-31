//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/workerlink"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// retriesBlind names the variant that looks for nothing before a retry, so a spawn a
// crash interrupted runs twice.
const retriesBlind = "an effect retried without looking for it"

// The operations the witness counts. Each happens once per key, however a crash falls.
const (
	witnessSpawn    = "spawn"
	witnessInput    = "input"
	witnessWorktree = "worktree"
)

// lastingDriver is a session that outlives the process that started it, as one under a
// shim does: a file stands for it, and the witness log records every spawn and input
// where no restart can rewrite them.
type lastingDriver struct {
	*fake.AgentDriver
	dir     string
	log     *EffectLog
	journal ports.Journal
	blind   bool
}

func (d *lastingDriver) marker(id string) string {
	return filepath.Join(d.dir, "session-"+strings.ReplaceAll(id, "/", "-"))
}

func (d *lastingDriver) witness(op, key string) error {
	return d.log.Append(Record{Kind: Completed, Op: op, Key: key, Actor: "driver"})
}

func handleOf(spec ports.SessionSpec) string { return fmt.Sprintf("%s/%d", spec.Lane, spec.Generation) }

func (d *lastingDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if err := d.witness(witnessSpawn, handleOf(spec)); err != nil {
		return ports.SessionHandle{}, err
	}
	if err := os.WriteFile(d.marker(handleOf(spec)), nil, 0o600); err != nil {
		return ports.SessionHandle{}, err
	}
	return d.AgentDriver.Start(ctx, spec)
}

func (d *lastingDriver) Find(_ context.Context, spec ports.SessionSpec) (ports.Located, error) {
	if d.blind {
		return ports.Located{}, nil
	}
	if _, err := os.Stat(d.marker(handleOf(spec))); err != nil {
		return ports.Located{}, nil
	}
	return ports.Located{
		Session: lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true},
		Handle:  ports.SessionHandle{ID: handleOf(spec)},
	}, nil
}

func (d *lastingDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	lane, text, _ := strings.Cut(h.ID, "/")
	generation, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return err
	}
	d.Play(d.rest())
	d.Leave(lane, generation, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *lastingDriver) Send(ctx context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	if err := d.witness(witnessInput, h.ID); err != nil {
		return ports.SendReceipt{}, err
	}
	return d.AgentDriver.Send(ctx, h, in)
}

func (d *lastingDriver) Stop(ctx context.Context, h ports.SessionHandle, how ports.StopMode) error {
	if err := os.Remove(d.marker(h.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return d.AgentDriver.Stop(ctx, h, how)
}

func (d *lastingDriver) report(spec ports.SessionSpec, lane string) {
	raw := []byte(`{"outcome":"completed"}`)
	if spec.Lane != "" {
		lane = spec.Lane
	}
	if _, err := d.journal.SaveReport(context.Background(), lane, raw); err != nil {
		fmt.Fprintln(os.Stderr, "report:", err)
	}
}

// start is a session that ends a turn with no outcome, so it takes the one nudge, and
// reports on the next.
func (d *lastingDriver) start() fake.DriverScript {
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.01}}
	report := fake.DriverStep{Do: func(spec ports.SessionSpec) { d.report(spec, "") }}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, turn}, {report, turn}}}
}

// rest is what an adopted session has left: it reports and ends its turn.
func (d *lastingDriver) rest() fake.DriverScript {
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.02}}
	report := fake.DriverStep{Do: func(ports.SessionSpec) {
		records, err := d.journal.Lanes(context.Background())
		if err != nil || len(records) != 1 {
			fmt.Fprintln(os.Stderr, "lanes:", len(records), err)
			return
		}
		d.report(ports.SessionSpec{}, records[0].ID)
	}}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, report, turn}}}
}

// lastingWorktrees is a worktree a killed process leaves on disk.
type lastingWorktrees struct {
	dir string
	log *EffectLog
}

func (w lastingWorktrees) marker(path string) string {
	return filepath.Join(w.dir, "worktree"+strings.ReplaceAll(path, "/", "-"))
}

func (w lastingWorktrees) Create(_ context.Context, spec ports.WorktreeSpec) (ports.Worktree, error) {
	if err := w.log.Append(Record{Kind: Completed, Op: witnessWorktree, Key: spec.Path, Actor: "driver"}); err != nil {
		return ports.Worktree{}, err
	}
	if err := os.WriteFile(w.marker(spec.Path), []byte(spec.Branch), 0o600); err != nil {
		return ports.Worktree{}, err
	}
	return ports.Worktree{Path: spec.Path, Branch: spec.Branch, Clean: true}, nil
}

func (w lastingWorktrees) Inspect(_ context.Context, path string) (ports.Worktree, error) {
	branch, err := os.ReadFile(w.marker(path))
	if errors.Is(err, os.ErrNotExist) {
		return ports.Worktree{}, fmt.Errorf("worktree %s: %w", path, ports.ErrNotFound)
	}
	return ports.Worktree{Path: path, Branch: string(branch), Clean: true}, err
}

func (w lastingWorktrees) Remove(context.Context, string, string) error { return nil }

// roleWorkerReconcile runs one lane through a real worker, on a session and a worktree
// that survive a kill. The claim and the dispatch are safe to repeat.
func roleWorkerReconcile(c *child) error {
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
	driver := &lastingDriver{
		AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal, blind: c.spec.Variant == retriesBlind,
	}
	driver.Play(driver.start())
	subject := worker.New(workerlink.New(client), journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver:      driver,
			Provisioner: worker.Provisioner{Profiles: fake.NewProfileSource(), Worktrees: lastingWorktrees{dir: dir, log: c.log}},
			Secrets:     fake.SecretResolver{},
			Repository:  func(context.Context, string) (string, error) { return "/repository", nil },
		},
	})
	if err := subject.Start(c.ctx); err != nil {
		return err
	}
	_, err = client.Claim(c.ctx, c.spec.WorkOrder, api.ClaimRequest{
		ClaimID: c.spec.ClaimID, WorkerID: subject.Auth().WorkerID, TTLMs: longTTL,
	})
	var refused *api.Error
	if err != nil && !errors.As(err, &refused) {
		return err
	}
	_, err = client.StartLane(c.ctx, c.spec.WorkOrder, api.StartLane{LaneID: c.spec.Lane})
	if err != nil && !errors.As(err, &refused) {
		return err
	}
	return subject.Converge(c.ctx)
}

func reconcileSpec(w *world) (childSpec, error) {
	w.serveOnce()
	id, err := w.rig.Ready(w.store)
	if err != nil {
		return childSpec{}, err
	}
	spec, err := w.workerSpec("worker-reconcile")
	spec.WorkOrder, spec.ClaimID, spec.Lane = id, w.rig.ID("claim"), w.rig.ID("lane")
	return spec, err
}

// settledOnce reads the witness, which no retry can rewrite: every effect happened at
// most once, one session started, and the ledger holds one start and one end.
func settledOnce(w *world, spec childSpec) error {
	if err := countEvents(w, workorders.EventType(workorders.TriggerLaneStarted), spec.WorkOrder, 1); err != nil {
		return err
	}
	completed, err := countOf(w, workorders.EventType(workorders.TriggerLaneCompleted), spec.WorkOrder)
	if err != nil {
		return err
	}
	if completed != 1 {
		return contract.Violatedf("%d completed outcomes on record for one lane", completed)
	}
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	performed := map[string]int{}
	spawned := 0
	for _, record := range records {
		if record.Kind != Completed || record.Actor != "driver" {
			continue
		}
		performed[record.Op+" "+record.Key]++
		if record.Op == witnessSpawn {
			spawned++
		}
	}
	for key, times := range performed {
		if times > 1 {
			return contract.Violatedf("%s happened %d times", key, times)
		}
	}
	if spawned != 1 {
		return contract.Violatedf("%d sessions started for one lane", spawned)
	}
	return nil
}

// effectCase kills a worker at each step of one kind of effect's bracket.
func effectCase(op string) crashCase {
	return crashCase{
		name: "bracket " + op, op: op, steps: []string{"started", "performed", "finished"},
		prepare: reconcileSpec, settled: settledOnce,
	}
}

// afterACrash kills the worker at a named step of the pass that follows a crash, because
// a pass with nothing to recover from has no window worth killing.
func afterACrash(name, op string, steps ...string) crashCase {
	return crashCase{
		name: name, op: op, steps: steps, settled: settledOnce,
		prepare: func(w *world) (childSpec, error) {
			spec, err := reconcileSpec(w)
			if err != nil {
				return childSpec{}, err
			}
			first := w.spec(spec)
			first.KillOp, first.KillStep = "effect.session", "performed"
			code, signaled, stderr, err := runToEnd(first)
			if err != nil {
				return childSpec{}, err
			}
			if !signaled {
				return childSpec{}, fmt.Errorf("the first crash was never reached: exit %d: %s", code, stderr)
			}
			return spec, nil
		},
	}
}

func reconcileCases() []crashCase {
	return []crashCase{
		effectCase("effect.worktree"),
		effectCase("effect.session"),
		effectCase("effect.input"),
		effectCase("effect.stop"),
		afterACrash("reconcile a crash", "worker.reconcile", "marked", "observed", "planned"),
		afterACrash("adopt a session", "worker.lane", "adopted"),
	}
}

var (
	_ ports.Worktrees   = lastingWorktrees{}
	_ ports.AgentDriver = (*lastingDriver)(nil)
)

//go:build chaos

package chaos

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

const defaultSeed = uint64(20261031)

// crashSeed fixes the kill schedule. Process timing still varies between runs, so
// a seed replays the schedule and not the interleaving.
func crashSeed() uint64 {
	if text := os.Getenv("SEED"); text != "" {
		seed, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			panic(err)
		}
		return seed
	}
	return defaultSeed
}

// crashKills reads the number of kills, which KILLS raises for a long run.
func crashKills(fallback int) int {
	if text := os.Getenv("KILLS"); text != "" {
		kills, err := strconv.Atoi(text)
		if err != nil {
			panic(err)
		}
		return kills
	}
	return fallback
}

// randomRun is one schedule of kills on four worker processes.
type randomRun struct {
	world   *world
	seed    uint64
	kills   int
	ttlMs   int64
	workers int

	killed      atomic.Int64
	atStep      atomic.Int64
	paused      atomic.Int64
	spawned     atomic.Int64
	stop        atomic.Bool
	mu          sync.Mutex
	failures    []string
	stepsKilled map[string]int
}

// randomSteps are the steps a kill aims at, beside the kills at random delays.
var randomSteps = [][2]string{
	{"claim", "begun"}, {"claim", "eligible"}, {"claim", "granted"}, {"claim", "recorded"},
	{"claim", "before-commit"}, {"claim", "committed"},
	{"lane.start", "started"}, {"lane.start", "committed"},
	{"fenced_write", "guarded"}, {"fenced_write", "before-commit"}, {"fenced_write", "committed"},
	{"lapse", "lapsed"}, {"lapse", "committed"},
	{"reassign", "revoked"}, {"reassign", "committed"},
	{"release", "released"}, {"release", "committed"},
	{"return", "restored"}, {"return", "committed"},
	{"lease.renew", "before-commit"}, {"lease.renew", "committed"},
}

func newRandomRun(w *world, kills int) *randomRun {
	return &randomRun{world: w, seed: crashSeed(), kills: kills, ttlMs: 120, workers: 4, stepsKilled: map[string]int{}}
}

func (r *randomRun) fail(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
	r.stop.Store(true)
}

func (r *randomRun) seedWork(workOrders int) error {
	for range workOrders {
		if _, err := r.world.rig.Ready(r.world.store); err != nil {
			return err
		}
	}
	return nil
}

func (r *randomRun) slot(slot int) {
	rng := rand.New(rand.NewPCG(r.seed, uint64(slot)))
	current := filepath.Join(r.world.target.Dir, fmt.Sprintf("current-%d.json", slot))
	for !r.stop.Load() {
		spec := r.world.spec(childSpec{
			Role: "lease-worker", Slot: slot, Spawn: int(r.spawned.Add(1)), Seed: r.seed,
			TTLMs: r.ttlMs, Current: current,
		})
		aimed := rng.IntN(2) == 0
		if aimed {
			step := randomSteps[rng.IntN(len(randomSteps))]
			spec.KillOp, spec.KillStep = step[0], step[1]
		}
		child, err := startChild(spec)
		if err != nil {
			r.fail("spawn: %v", err)
			return
		}
		if aimed {
			r.awaitStep(child, spec)
		} else {
			r.killAtRandom(child, rng)
		}
		child.sigkill()
		code, signaled := child.exitCode()
		switch {
		case signaled:
			r.killed.Add(1)
		case code != exitDone:
			r.fail("worker exited %d: %s", code, child.stderr.String())
		}
		if r.killed.Load() >= int64(r.kills) {
			r.stop.Store(true)
		}
	}
}

// awaitStep gives a child time to reach its step and end itself.
func (r *randomRun) awaitStep(child *proc, spec childSpec) {
	select {
	case <-child.exited:
		r.atStep.Add(1)
		r.mu.Lock()
		r.stepsKilled[spec.KillOp+"/"+spec.KillStep]++
		r.mu.Unlock()
	case <-time.After(1500 * time.Millisecond):
	}
}

// killAtRandom waits a random delay, and sometimes stops the child past its lease
// first, because a paused process keeps believing it holds one.
func (r *randomRun) killAtRandom(child *proc, rng *rand.Rand) {
	delay := time.Duration(rng.Int64N(int64(150*time.Millisecond))) + 40*time.Millisecond
	select {
	case <-time.After(delay):
	case <-child.exited:
		return
	}
	if rng.IntN(3) != 0 {
		return
	}
	if child.cmd.Process.Signal(syscall.SIGSTOP) != nil {
		return
	}
	r.paused.Add(1)
	time.Sleep(time.Duration(r.ttlMs) * time.Millisecond * 3 / 2)
	_ = child.cmd.Process.Signal(syscall.SIGCONT)
	time.Sleep(60 * time.Millisecond)
}

func (r *randomRun) execute() error {
	var wg sync.WaitGroup
	for slot := range r.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.slot(slot)
		}()
	}
	wg.Wait()
	r.mu.Lock()
	failures := slices.Clone(r.failures)
	r.mu.Unlock()
	if len(failures) > 0 {
		return fmt.Errorf("harness: %v", failures)
	}
	if err := r.drain(); err != nil {
		return err
	}
	return r.world.verify()
}

// drain finishes with no kills: every lease a dead worker left must expire and
// resolve, so a stuck work order shows.
func (r *randomRun) drain() error {
	w := r.world
	ops := w.rig.NewOps(w.store)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := ops.Sweep(w.rig.Ctx, w.rig.Scope, w.rig.Actor); err != nil {
			return err
		}
		rows, err := w.rows()
		if err != nil {
			return err
		}
		settled := true
		for _, row := range rows {
			if row.State == workorders.Ready {
				continue
			}
			settled = false
			if row.State == workorders.Lapsed {
				if err := ops.Reassign(w.rig.Ctx, w.rig.Scope, row.ID, "drain", w.rig.Actor); err != nil {
					return err
				}
			}
		}
		if settled {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return contract.Violatedf("work orders never settled after the workers stopped")
}

// TestCrashLeases is the random run: SIGKILL at exact steps and at random delays,
// and SIGSTOP past the lease, on four real worker processes.
func TestCrashLeases(t *testing.T) {
	w := newWorld(t, "")
	run := newRandomRun(w, crashKills(60))
	if err := run.seedWork(3); err != nil {
		t.Fatal(err)
	}
	t.Logf("seed=%d kills=%d", run.seed, run.kills)
	if err := run.execute(); err != nil {
		t.Fatalf("seed %d: %v", run.seed, err)
	}
	t.Logf("kills=%d at_step=%d distinct_steps=%d pauses=%d spawned=%d claims=%d writes=%d fenced=%d salvaged=%d records=%d",
		run.killed.Load(), run.atStep.Load(), len(run.stepsKilled), run.paused.Load(), run.spawned.Load(),
		w.stats.claims, w.stats.writes, w.stats.fencedWrites, w.stats.salvaged, w.stats.records)
	if w.stats.fencedWrites+w.stats.salvaged == 0 {
		t.Errorf("seed %d: no holder was fenced, so the run did not exercise fencing", run.seed)
	}
}

const pausedRounds = 8

// checkPausedWorker is the case fencing exists for. A real process stops between
// two writes, believing it holds the lease. Its lease expires, a member reassigns
// the work order, a rival claims it, and the process continues and writes.
func checkPausedWorker(w *world) error {
	id, err := w.rig.Ready(w.store)
	if err != nil {
		return err
	}
	ops := w.rig.NewOps(w.store)
	for round := range pausedRounds {
		gate := filepath.Join(w.target.Dir, fmt.Sprintf("gate-%d", round))
		spec := w.spec(childSpec{
			Role: "zombie-writer", WorkOrder: id, Spawn: round + 1, Seed: crashSeed(), TTLMs: 40, Gate: gate,
		})
		child, err := startChild(spec)
		if err != nil {
			return err
		}
		select {
		case <-child.at:
		case <-child.exited:
			return fmt.Errorf("round %d: the writer exited early: %s", round, child.stderr.String())
		case <-time.After(30 * time.Second):
			child.sigkill()
			return fmt.Errorf("round %d: the writer never reached its pause", round)
		}
		if err := child.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
			return err
		}
		rival, err := handOver(w, id)
		if err != nil {
			_ = child.cmd.Process.Signal(syscall.SIGCONT)
			child.sigkill()
			return err
		}
		if err := os.WriteFile(gate, nil, 0o600); err != nil {
			return err
		}
		if err := child.cmd.Process.Signal(syscall.SIGCONT); err != nil {
			return err
		}
		child.wait()
		if code, signaled := child.exitCode(); signaled || code != exitDone {
			return fmt.Errorf("round %d: writer exit %d: %s", round, code, child.stderr.String())
		}
		time.Sleep(rivalLease)
		if _, err := ops.Sweep(w.rig.Ctx, w.rig.Scope, w.rig.Actor); err != nil {
			return err
		}
		if err := ops.Reassign(w.rig.Ctx, w.rig.Scope, id, "next round", w.rig.Actor); err != nil {
			return fmt.Errorf("round %d: %w (rival %+v)", round, err, rival)
		}
	}
	if err := w.verify(); err != nil {
		return err
	}
	if w.stats.fencedWrites != pausedRounds {
		return contract.Violatedf("%d of %d paused writers were fenced", w.stats.fencedWrites, pausedRounds)
	}
	return nil
}

// rivalLease is short, so each round can end by reassigning the rival.
const rivalLease = 30 * time.Millisecond

// handOver waits for the paused holder's lease to expire in real time, then runs
// the reassignment and a new claim.
func handOver(w *world, id string) (workorders.Grant, error) {
	ops := w.rig.NewOps(w.store)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := ops.Sweep(w.rig.Ctx, w.rig.Scope, w.rig.Actor); err != nil {
			return workorders.Grant{}, err
		}
		row, err := w.rig.WorkOrder(w.store, id)
		if err != nil {
			return workorders.Grant{}, err
		}
		if row.State == workorders.Lapsed {
			break
		}
		if time.Now().After(deadline) {
			return workorders.Grant{}, fmt.Errorf("the lease never lapsed: %s", row.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := ops.Reassign(w.rig.Ctx, w.rig.Scope, id, "paused holder", w.rig.Actor); err != nil {
		return workorders.Grant{}, err
	}
	request := workorders.ClaimRequest{
		WorkOrder: id, ClaimID: w.rig.ID("rival"), Worker: "rival", TTLMs: rivalLease.Milliseconds(),
	}
	grant, err := ops.Claim(w.rig.Ctx, w.rig.Scope, request, w.rig.Actor)
	if err != nil {
		return workorders.Grant{}, err
	}
	log, err := OpenEffectLog(w.logPath)
	if err != nil {
		return workorders.Grant{}, err
	}
	defer log.Close()
	if err := log.Append(Record{Kind: Attempted, Op: "claim", WorkOrder: id, Key: request.ClaimID, Actor: "parent"}); err != nil {
		return workorders.Grant{}, err
	}
	err = log.Append(Record{Kind: Completed, Op: "claim", WorkOrder: id, Key: request.ClaimID, Fence: grant.Fence, Rev: grant.Rev, Actor: "parent"})
	if err != nil {
		return workorders.Grant{}, err
	}
	_, err = ops.StartLane(w.rig.Ctx, w.rig.Scope, grant.Lease, "lane-"+request.ClaimID, w.rig.Actor)
	return grant, err
}

func TestPausedWorkerIsFenced(t *testing.T) {
	w := newWorld(t, "")
	if err := checkPausedWorker(w); err != nil {
		t.Fatal(err)
	}
	t.Logf("paused workers fenced: %d of %d", w.stats.fencedWrites, pausedRounds)
}

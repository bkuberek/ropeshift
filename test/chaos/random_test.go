//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
)

const defaultSeed = uint64(20261031)

const (
	// holderRounds is how many holders a run stops on purpose. A pause at a random
	// moment finds a holder less than half the time and leaves it 60 ms to act, so a
	// schedule alone can end with no holder fenced and prove nothing about fencing.
	holderRounds = 3
	// holderLimit bounds each wait of one round.
	holderLimit = 30 * time.Second
	// randomDrainLimit bounds the drain, which waits for every lease a dead worker left to resolve.
	randomDrainLimit = 20 * time.Second
)

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
	// holders is how many holders this run stops on purpose, one after another.
	holders int

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
	{"claim", "begun"}, {"claim", "eligible"}, {"claim", "granted"}, {"claim", "packed"}, {"claim", "recorded"},
	{"claim", "before-commit"}, {"claim", "committed"},
	{"lane.start", "started"}, {"lane.start", "committed"},
	{"fenced_write", "guarded"}, {"fenced_write", "before-commit"}, {"fenced_write", "committed"},
	{"lapse", "lapsed"}, {"lapse", "committed"},
	{"reassign", "revoked"}, {"reassign", "committed"},
	{"release", "released"}, {"release", "committed"},
	{"return", "restored"}, {"return", "committed"},
	{"lease.renew", "before-commit"}, {"lease.renew", "committed"},
	{"lane.end", "ended"}, {"lane.end", "committed"},
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

// randomAttempts keeps a failed lane's work order coming back for the whole run.
const randomAttempts = 1 << 40

func (r *randomRun) seedWork(workOrders int) error {
	r.world.rig.MaxAttempts = randomAttempts
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
	var refused error
	wg.Go(func() {
		for round := range r.holders {
			if refused = r.pauseHolder(round); refused != nil {
				r.stop.Store(true)
				return
			}
		}
	})
	wg.Wait()
	if refused != nil {
		return r.judged(refused)
	}
	r.mu.Lock()
	failures := slices.Clone(r.failures)
	r.mu.Unlock()
	if len(failures) > 0 {
		return r.judged(fmt.Errorf("harness: %v", failures))
	}
	if err := r.world.within("drain", randomDrainLimit+storeSlack, r.drain); err != nil {
		return r.judged(err)
	}
	return r.verify()
}

// verify checks the ledger once the workers are gone, bounded by the wait for the plane to
// settle and the slack for the reads after it.
func (r *randomRun) verify() error {
	return r.world.within("verify", settleBound+storeSlack, r.world.verify)
}

// judged decides a run that stopped short, once its workers are gone. A defect can show
// first as an error a worker has no rule for, and whether it does depends on the schedule,
// so the ledger and the witness are read before a stop counts as the harness's own. A stop
// they do not explain stays what it was, so an unrelated error cannot pass as a catch.
func (r *randomRun) judged(stopped error) error {
	var violation *contract.Violation
	if errors.As(stopped, &violation) {
		return stopped
	}
	if found := r.verify(); errors.As(found, &violation) {
		return fmt.Errorf("%w, and the run stopped on: %v", found, stopped)
	}
	return stopped
}

// pauseHolder is the pause no schedule can miss. A worker stops itself just after a
// committed write, the parent stops the process, and the lease is taken from it in real
// time while the other workers run. Then the worker continues, still believing, and the
// witness must show it refused.
func (r *randomRun) pauseHolder(round int) error {
	w := r.world
	slot := r.workers + round
	spec := w.spec(childSpec{
		Role: "lease-worker", Slot: slot, Spawn: int(r.spawned.Add(1)), Seed: r.seed, TTLMs: r.ttlMs,
		Current: filepath.Join(w.target.Dir, fmt.Sprintf("current-%d.json", slot)),
		Gate:    filepath.Join(w.target.Dir, fmt.Sprintf("holder-gate-%d", slot)),
	})
	child, err := startChild(spec)
	if err != nil {
		return err
	}
	defer child.sigkill()
	var workOrder string
	var fence int64
	select {
	case at := <-child.at:
		if _, err := fmt.Sscanf(at, "holding %s %d", &workOrder, &fence); err != nil {
			return fmt.Errorf("round %d: %q: %w", round, at, err)
		}
	case <-child.exited:
		return fmt.Errorf("round %d: the holder exited early: %s", round, child.stderr.String())
	case <-time.After(holderLimit):
		return fmt.Errorf("round %d: no holder reached its first write", round)
	}
	if err := child.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		return err
	}
	r.paused.Add(1)
	take := func() error { return r.takeLease(workOrder, fence) }
	if err := w.within(fmt.Sprintf("round %d: take the lease of %s", round, workOrder), holderLimit+storeSlack, take); err != nil {
		return err
	}
	actor := fmt.Sprintf("worker-%d", slot)
	_, wrote, err := r.witness(actor, fence)
	if err != nil {
		return err
	}
	if err := os.WriteFile(spec.Gate, nil, 0o600); err != nil {
		return err
	}
	if err := child.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		return err
	}
	return r.awaitRefusal(actor, fence, wrote)
}

// takeLease waits for the stopped holder's lease to end in real time, and moves the
// fence. A rival may do either first, which is the contention this run is for.
func (r *randomRun) takeLease(workOrder string, fence int64) error {
	w := r.world
	ops := w.rig.NewOps(w.store)
	deadline := time.Now().Add(holderLimit)
	for time.Now().Before(deadline) {
		if _, err := ops.Sweep(w.rig.Ctx, w.rig.Scope, w.rig.Actor); err != nil {
			return fmt.Errorf("sweep: %w", err)
		}
		row, err := w.rig.WorkOrder(w.store, workOrder)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if row.Fence > fence {
			return nil
		}
		if row.State == workorders.Lapsed {
			err := ops.Reassign(w.rig.Ctx, w.rig.Scope, workOrder, "stopped holder", w.rig.Actor)
			if err != nil && !errors.Is(err, workorders.ErrNotLapsed) {
				return fmt.Errorf("reassign: %w", err)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("the fence of %s never moved past %d", workOrder, fence)
}

// witness counts what one holder saw at one fence, from the log it cannot rewrite.
func (r *randomRun) witness(actor string, fence int64) (refused, wrote int, err error) {
	records, err := ReadEffectLog(r.world.logPath)
	if err != nil {
		return 0, 0, contract.Violatedf("%v", err)
	}
	for _, record := range records {
		if record.Actor != actor || record.Fence != fence {
			continue
		}
		switch {
		case record.Kind == Rejected && record.Op == "write":
			refused++
		case record.Kind == Completed && record.Op == "write":
			wrote++
		}
	}
	return refused, wrote, nil
}

// awaitRefusal ends at the holder's next act. Its fence already moved, so one more write
// is a violation, and a refusal is the only pass.
func (r *randomRun) awaitRefusal(actor string, fence int64, wrote int) error {
	deadline := time.Now().Add(holderLimit)
	for time.Now().Before(deadline) {
		refused, now, err := r.witness(actor, fence)
		if err != nil {
			return err
		}
		if now > wrote {
			return contract.Violatedf("%s wrote at fence %d after the fence moved", actor, fence)
		}
		if refused > 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("%s neither wrote nor was refused at fence %d", actor, fence)
}

// drain finishes with no kills: every lease a dead worker left must expire and
// resolve, and failed work is retried, so a stuck work order shows, and the failure names it.
func (r *randomRun) drain() error {
	w := r.world
	ops := w.rig.NewOps(w.store)
	deadline := time.Now().Add(randomDrainLimit)
	var unsettled []string
	for time.Now().Before(deadline) {
		if _, err := ops.Sweep(w.rig.Ctx, w.rig.Scope, w.rig.Actor); err != nil {
			return fmt.Errorf("sweep: %w", err)
		}
		rows, err := w.rows()
		if err != nil {
			return fmt.Errorf("list the work orders: %w", err)
		}
		unsettled = unsettled[:0]
		for _, row := range rows {
			if row.State == workorders.Ready {
				continue
			}
			unsettled = append(unsettled, fmt.Sprintf("%s %s at fence %d", row.ID, row.State, row.Fence))
			switch row.State {
			case workorders.Lapsed:
				if err := ops.Reassign(w.rig.Ctx, w.rig.Scope, row.ID, "drain", w.rig.Actor); err != nil {
					return fmt.Errorf("reassign %s: %w", row.ID, err)
				}
			case workorders.Failed:
				// Failed work stays failed until a member retries it (ADR-0014), as the children's maintain does.
				_, err := control.NewRecoveries(w.store).Retry(w.rig.Ctx, w.rig.Scope, row.ID, "drain", w.rig.Actor)
				if err != nil && !errors.Is(err, workorders.ErrIllegalMove) {
					return fmt.Errorf("retry %s: %w", row.ID, err)
				}
			}
		}
		if len(unsettled) == 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return contract.Violatedf("work orders never settled after the workers stopped: %s", strings.Join(unsettled, ", "))
}

// TestCrashLeases is the random run: SIGKILL at exact steps and at random delays,
// and SIGSTOP past the lease at random and on purpose, on four real worker processes.
func TestCrashLeases(t *testing.T) {
	w := newWorld(t, "")
	run := newRandomRun(w, crashKills(60))
	run.holders = holderRounds
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
	if w.stats.fencedWrites < holderRounds {
		t.Errorf("seed %d: %d writes fenced for the %d holders stopped on purpose", run.seed, w.stats.fencedWrites, holderRounds)
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

// repeatFence walks the defect of a fence kept in process memory to where it first shows.
// Two holders share a fence, and the one whose lane never started asks to start it while
// the other's runs. It returns the error that worker exits on, which no healthy ledger
// gives, because a healthy fence names one holder.
func repeatFence(t *testing.T, w *world) error {
	t.Helper()
	memoryFence.Store(0)
	id, err := w.rig.Ready(w.store)
	if err != nil {
		t.Fatal(err)
	}
	ops := w.rig.NewOps(w.store)
	first, err := ops.Claim(w.rig.Ctx, w.rig.Scope, workorders.ClaimRequest{
		WorkOrder: id, ClaimID: w.rig.ID("first"), Worker: "first", TTLMs: 1,
	}, w.rig.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handOver(w, id); err != nil {
		t.Fatal(err)
	}
	time.Sleep(rivalLease)
	if _, err := ops.Sweep(w.rig.Ctx, w.rig.Scope, w.rig.Actor); err != nil {
		t.Fatal(err)
	}
	if err := ops.Reassign(w.rig.Ctx, w.rig.Scope, id, "next", w.rig.Actor); err != nil {
		t.Fatal(err)
	}
	memoryFence.Store(0)
	second, err := ops.Claim(w.rig.Ctx, w.rig.Scope, workorders.ClaimRequest{
		WorkOrder: id, ClaimID: w.rig.ID("second"), Worker: "second", TTLMs: 60_000,
	}, w.rig.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fence != first.Fence {
		t.Fatalf("fences %d and %d, and this walk needs one fence twice", first.Fence, second.Fence)
	}
	if _, err := ops.StartLane(w.rig.Ctx, w.rig.Scope, second.Lease, "lane-second", w.rig.Actor); err != nil {
		t.Fatal(err)
	}
	_, err = ops.StartLane(w.rig.Ctx, w.rig.Scope, first.Lease, "lane-first", w.rig.Actor)
	return err
}

// TestAStoppedRunIsJudgedByTheLedger holds the verdict of a run a worker's error ended. A
// defect can show first as an error a worker has no rule for, and that depends on the
// schedule, so a broken variant would be caught in one run and a harness error in the next.
// Only what the ledger and the witness show is a catch: a stop they do not explain stays a
// harness error, so an unrelated failure still cannot pass.
func TestAStoppedRunIsJudgedByTheLedger(t *testing.T) {
	t.Run("the ledger explains the stop", func(t *testing.T) {
		sqlstore.Sabotaged(sqlstore.Sabotage{Rewrite: fenceInMemory})
		defer sqlstore.Sabotaged(sqlstore.Sabotage{})
		run := newRandomRun(newWorld(t, ""), 0)
		stopped := repeatFence(t, run.world)
		if !errors.Is(stopped, ports.ErrNotFound) {
			t.Fatalf("the worker's error: %v", stopped)
		}
		run.fail("worker exited %d: %v", exitBroken, stopped)
		err := run.execute()
		var violation *contract.Violation
		if !errors.As(err, &violation) {
			t.Errorf("a run on a repeated fence ended in: %v", err)
		}
	})
	t.Run("the ledger does not explain the stop", func(t *testing.T) {
		run := newRandomRun(newWorld(t, ""), 0)
		if err := run.seedWork(1); err != nil {
			t.Fatal(err)
		}
		run.fail("worker exited %d: a full disk", exitBroken)
		err := run.execute()
		var violation *contract.Violation
		if err == nil || errors.As(err, &violation) {
			t.Errorf("a stop on a sound ledger ended in: %v", err)
		}
	})
}

// TestALongRunKeepsABudgetForItsChecks holds the parent's store work to bounds of its own. The
// rig's context is one budget for the whole test, and the kills last as long as KILLS asks: 1500
// kills on a four-core runner outlast it, and the drain then failed on a context the kills had
// spent, on a sound ledger. A spent rig context stands for that long run here, and the stopped
// holder, the drain, and the checks must still each run on their own bound.
func TestALongRunKeepsABudgetForItsChecks(t *testing.T) {
	w := newWorld(t, "")
	run := newRandomRun(w, 0)
	run.holders = 1
	if err := run.seedWork(3); err != nil {
		t.Fatal(err)
	}
	spent, cancel := context.WithDeadline(context.Background(), time.Now())
	defer cancel()
	w.rig.Ctx = spent
	if err := run.execute(); err != nil {
		t.Fatalf("a run that outlasted the rig's context: %v", err)
	}
}

// TestAPhaseStillStopsAStoreCallThatBlocks holds the other half of a phase's bound: a store call
// that blocks still ends at it, well before the store's own busy timeout, and the error names
// the phase and the call. A write transaction held open on another connection stands for the
// hang, and an expired lease makes the drain's sweep write behind it.
func TestAPhaseStillStopsAStoreCallThatBlocks(t *testing.T) {
	w := newWorld(t, "")
	run := newRandomRun(w, 0)
	id, err := w.rig.Ready(w.store)
	if err != nil {
		t.Fatal(err)
	}
	request := workorders.ClaimRequest{WorkOrder: id, ClaimID: w.rig.ID("claim"), Worker: "gone", TTLMs: 1}
	if _, err := w.rig.NewOps(w.store).Claim(w.rig.Ctx, w.rig.Scope, request, w.rig.Actor); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	db, err := sqlite.OpenDB(w.target.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	holding, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holding.Rollback() }()
	const limit = 200 * time.Millisecond
	began := time.Now()
	err = w.within("drain", limit, run.drain)
	took := time.Since(began)
	if err == nil || !strings.Contains(err.Error(), "drain, bounded at 200ms: sweep: ") {
		t.Fatalf("a drain behind a held lock ended in: %v", err)
	}
	if took > storeSlack/2 {
		t.Fatalf("the drain ended after %s, so the store's busy timeout stopped it and not its bound", took)
	}
}

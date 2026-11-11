//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
)

// sabotageDropsWake is the broken variant of a wake, which the oracle below must catch.
const sabotageDropsWake = "a restart reads what is due as already told"

// witnessWake records one wake reaching a session, which nothing a worker wrote can vouch for.
const witnessWake = "wake"

// wakeProfile batches at a test's scale, and a kill must not look like a crash to restart from.
const wakeProfile = "[lifecycle]\nwake_debounce = \"5ms\"\nwake_min_interval = \"10ms\"\nask_wait = \"5m\"\nmax_restarts = 0\n"

// wakeBound is how long a child looks for its wake. A variant that lost it must still end, so the
// oracle reads that, because a child that hangs is a harness error and no catch.
const wakeBound = 3 * time.Second

func applyWakeVariant(variant string) {
	if variant == sabotageDropsWake {
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageDropsWake)
	}
}

// wakeDriver is an orchestrator's session, which outlives its worker and rests between wakes.
type wakeDriver struct {
	*lastingDriver
	question string
	told     atomic.Bool
}

func (d *wakeDriver) rests() fake.DriverScript {
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{
		{{Bound: true}, {Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.01}}},
	}}
}

func (d *wakeDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	d.Play(d.rests())
	return d.lastingDriver.Start(ctx, spec)
}

func (d *wakeDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	d.Play(fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Hold: true}}}})
	d.Leave(laneOf(h.ID), generationOf(h.ID), lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *wakeDriver) Send(ctx context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	if strings.Contains(in.Text, d.question) {
		if err := d.witness(witnessWake, d.question); err != nil {
			return ports.SendReceipt{}, err
		}
		d.told.Store(true)
	}
	return d.AgentDriver.Send(ctx, h, in)
}

// roleWorkerWake runs one orchestrator to its second wake, the first that reaches a live session.
// Everything it does is safe to repeat, and a retry goes on from what the journal holds.
func roleWorkerWake(c *child) error {
	applyWakeVariant(c.spec.Variant)
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
	driver := &wakeDriver{lastingDriver: &lastingDriver{AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal}, question: c.spec.Question}
	profiles := fake.NewProfileSource()
	profiles.PutUser(c.spec.Profile)
	subject := worker.New(workerlink.New(client.Client), journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver:      driver,
			Provisioner: worker.Provisioner{Profiles: profiles, Worktrees: lastingWorktrees{dir: dir, log: c.log}},
			Secrets:     fake.SecretResolver{},
			Repository:  func(context.Context, string) (string, error) { return "/repository", nil },
			StateDir:    dir,
		},
	})
	if err := subject.Start(c.ctx); err != nil {
		return err
	}
	if _, err := apitest.SignIn(c.ctx, client); err != nil {
		return err
	}
	ctx, stop := context.WithCancel(c.ctx)
	defer stop()
	defer driver.StopEverySession()
	row, err := client.SetOrchestrator(ctx, subject.Auth().WorkerID, api.SetOrchestrator{Desired: ledger.OrchestratorRunning})
	if err != nil {
		return err
	}
	deadline := time.Now().Add(wakeBound)
	for time.Now().Before(deadline) && !driver.told.Load() {
		if err := subject.Sync(ctx); err != nil {
			return err
		}
		if _, err := subject.Reconcile(ctx); err != nil {
			return err
		}
		record, err := journal.Lane(ctx, row.Lane())
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		if err == nil && record.Actual == lanes.Idle {
			_, err := client.RecordQuestion(ctx, api.RecordQuestion{ID: c.spec.Question, Text: "A second question to judge"})
			var refused *api.Error
			if err != nil && !errors.As(err, &refused) {
				return err
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func orchestratorWakeSpec(w *world) (childSpec, error) {
	w.serveOnce()
	if _, err := w.rig.Asked(w.store); err != nil {
		return childSpec{}, err
	}
	spec, err := w.workerSpec("worker-wake")
	spec.Question, spec.Profile = w.rig.ID("q"), wakeProfile
	return spec, err
}

// wokenOut is the oracle of a wake: whichever side of the send a kill landed on, the session heard
// what was due. A second telling costs one context, and a lost one leaves a question nobody reads.
func wokenOut(w *world, spec childSpec) error {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	for _, record := range records {
		if record.Op == witnessWake && record.Key == spec.Question {
			return nil
		}
	}
	return contract.Violatedf("no wake told a session of question %s", spec.Question)
}

func orchestratorWakeCase(name string, steps ...string) crashCase {
	return crashCase{name: name, op: "worker.wake", steps: steps, prepare: orchestratorWakeSpec, settled: wokenOut}
}

func orchestratorCases() []crashCase {
	return []crashCase{orchestratorWakeCase("an orchestrator's wake", "handing", "handed")}
}

func orchestratorVariants() []variant {
	return []variant{{
		name: sabotageDropsWake, check: "crash",
		crash: func() (crashCase, string) { return orchestratorWakeCase("a lost wake", "handing"), "handing" },
	}, {
		name: "a hold that ended and kept its time", control: control.Sabotage{KeepsHoldTime: true}, check: "Q26",
	}}
}

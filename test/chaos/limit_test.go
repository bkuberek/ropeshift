//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
)

const (
	// sabotageSplitsSignal clears a limit in one journal write and says so in another, so a kill
	// between them leaves lanes waiting on a word no worker will send.
	sabotageSplitsSignal = "a limit's end and its signal in two journal writes"
	// sabotageForgetsLimit holds a limit in memory, so a kill leaves lanes nobody parks.
	sabotageForgetsLimit = "a limit no journal holds"
)

// limitChaosBackoff is a probe's first wait, short because a crash run keeps real time.
const limitChaosBackoff = 150 * time.Millisecond

// limitChaosProfile asks little of a session that cannot answer, and keeps a probe's wait short.
var limitChaosProfile = fmt.Sprintf("[lifecycle]\nhandoff_timeout = \"300ms\"\nmax_restarts = 0\nusage_backoff = %q\n", limitChaosBackoff.String())

func applyLimitVariant(variant string) {
	switch variant {
	case sabotageSplitsSignal:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsSignal)
	case sabotageForgetsLimit:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageForgetsLimit)
	}
}

// limitDriver is two sessions on one sign-in. The first reaches a limit nobody gave a time for,
// once the second is at work, and each goes on from the session it left.
type limitDriver struct {
	*resumeDriver
	first, second string
}

func (d *limitDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if spec.Generation > 1 {
		d.Play(d.goesOn())
	} else {
		d.Play(d.atWork(spec))
	}
	return d.lastingDriver.Start(ctx, spec)
}

func (d *limitDriver) atWork(spec ports.SessionSpec) fake.DriverScript {
	steps := []fake.DriverStep{{Bound: true}}
	if spec.Lane == d.first {
		wait := fake.DriverStep{Do: func(ports.SessionSpec) {
			for d.ctx.Err() == nil {
				if _, err := os.Stat(d.marker(d.second + "/1")); err == nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}}
		steps = append(steps, wait, fake.DriverStep{Limit: &lanes.UsageReading{Status: lanes.UsageRejected}})
	}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{append(steps, fake.DriverStep{Hold: true})}}
}

func roleWorkerLimit(c *child) error {
	applyLimitVariant(c.spec.Variant)
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
	lasting := &lastingDriver{AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal}
	driver := &limitDriver{
		resumeDriver: &resumeDriver{parkDriver: &parkDriver{lastingDriver: lasting, ctx: c.ctx}},
		first:        c.spec.Lane, second: c.spec.OtherLane,
	}
	profiles := fake.NewProfileSource()
	profiles.PutUser(c.spec.Profile)
	subject := worker.New(witnessLink{WorkerLink: workerlink.New(client.Client), log: c.log}, journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver:      driver,
			Provisioner: worker.Provisioner{Profiles: profiles, Worktrees: lastingWorktrees{dir: dir, log: c.log}},
			Secrets:     fake.SecretResolver{},
			Repository:  func(context.Context, string, string) (string, error) { return "/repository", nil },
		},
	})
	driver.subject = subject
	if err := subject.Start(c.ctx); err != nil {
		return err
	}
	if _, err := apitest.SignIn(c.ctx, client); err != nil {
		return err
	}
	for _, lane := range []struct{ order, claim, id string }{
		{c.spec.WorkOrder, c.spec.ClaimID, c.spec.Lane}, {c.spec.OtherOrder, c.spec.OtherClaim, c.spec.OtherLane},
	} {
		_, err := client.Claim(c.ctx, lane.order, api.ClaimRequest{ClaimID: lane.claim, WorkerID: subject.Auth().WorkerID, TTLMs: longTTL})
		var refused *api.Error
		if err != nil && !errors.As(err, &refused) {
			return err
		}
		if _, err := client.StartLane(c.ctx, lane.order, api.StartLane{LaneID: lane.id}); err != nil && !errors.As(err, &refused) {
			return err
		}
	}
	return bothEnded(c, subject, journal)
}

// bothEnded passes as a daemon's ticks do, until both lanes have an end on record.
func bothEnded(c *child, subject *worker.Worker, journal *sqlite.Journal) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.ops.WakeTick(c.ctx, c.scope, ledger.Actor{Kind: ledger.ActorSystem, ID: "wake-tick"}); err != nil {
			return err
		}
		if err := subject.Sync(c.ctx); err != nil {
			return err
		}
		if _, err := subject.Reconcile(c.ctx); err != nil {
			return err
		}
		if err := subject.Upload(c.ctx); err != nil {
			return err
		}
		ended := 0
		for _, lane := range []string{c.spec.Lane, c.spec.OtherLane} {
			record, err := journal.Lane(c.ctx, lane)
			if err != nil && !errors.Is(err, ports.ErrNotFound) {
				return err
			}
			if err == nil && record.State == lanes.StateEnded {
				ended++
			}
		}
		if ended == 2 {
			return subject.Upload(c.ctx)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The oracle says what this means, because a child that gave up tells it nothing.
	return subject.Upload(c.ctx)
}

func limitSpec(w *world) (childSpec, error) {
	w.serveOnce()
	first, err := w.rig.Ready(w.store)
	if err != nil {
		return childSpec{}, err
	}
	second, err := w.rig.Ready(w.store)
	if err != nil {
		return childSpec{}, err
	}
	spec, err := w.workerSpec("worker-limit")
	spec.WorkOrder, spec.ClaimID, spec.Lane, spec.Profile = first, w.rig.ID("claim"), w.rig.ID("lane"), limitChaosProfile
	spec.OtherOrder, spec.OtherClaim, spec.OtherLane = second, w.rig.ID("claim"), w.rig.ID("lane")
	return spec, err
}

// limitedOut is the oracle of a limit. Each lane parked once and came back once, in a session of
// its own, and one signal did it: the second lane's wake names no time, so nothing else could have.
func limitedOut(w *world, spec childSpec) error {
	for _, lane := range []struct{ order, id string }{{spec.WorkOrder, spec.Lane}, {spec.OtherOrder, spec.OtherLane}} {
		for _, trigger := range []workorders.Trigger{workorders.TriggerLaneParked, workorders.TriggerWakeFired, workorders.TriggerLaneCompleted} {
			if err := countEvents(w, workorders.EventType(trigger), lane.order, 1); err != nil {
				return err
			}
		}
		view, err := w.laneView(lane.id)
		if err != nil {
			return err
		}
		if len(view.Generations) != 2 || view.ActualState != ledger.StateEnded {
			return contract.Violatedf("lane %s reads %+v", lane.id, view)
		}
	}
	events, err := w.rig.Events(w.store, w.rig.Scope)
	if err != nil {
		return err
	}
	signals := 0
	for _, event := range events {
		if event.Type == parking.EventSignal {
			signals++
		}
	}
	if signals != 1 {
		return contract.Violatedf("%d signals, want one", signals)
	}
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	spawned := map[string]int{}
	for _, record := range records {
		if record.Op == witnessSpawn {
			spawned[record.Key]++
		}
	}
	for _, session := range []string{spec.Lane + "/1", spec.Lane + "/2", spec.OtherLane + "/1", spec.OtherLane + "/2"} {
		if spawned[session] != 1 {
			return contract.Violatedf("sessions %v", spawned)
		}
	}
	return nil
}

func limitCase(name string, steps ...string) crashCase {
	return crashCase{name: name, op: "worker.limit", steps: steps, prepare: limitSpec, settled: limitedOut}
}

// probeCameDue holds the retry back until the probe's time came, as a slow restart does. The lane
// that found the limit was never parked, so nothing sent it to find out, and it still has to park.
func probeCameDue(*world, childSpec) error {
	time.Sleep(2 * limitChaosBackoff)
	return nil
}

func limitCases() []crashCase {
	late := limitCase("a limit whose probe came due in a restart", "noted")
	late.between = probeCameDue
	return []crashCase{limitCase("a limit on two lanes", "noted", "signaled"), late}
}

func limitVariants() []variant {
	crash := func(name, step string) func() (crashCase, string) {
		return func() (crashCase, string) { return limitCase(name, step), step }
	}
	return []variant{
		{name: sabotageSplitsSignal, check: "crash", crash: crash("a split signal", "split")},
		{name: sabotageForgetsLimit, check: "crash", crash: crash("a forgotten limit", "noted")},
		{name: "a worker that speaks for any sign-in", control: control.Sabotage{SignalsAnyKey: true}, check: "LM2"},
		{name: "a worker that speaks for a person", control: control.Sabotage{SignalsAnyKind: true}, check: "LM3"},
	}
}

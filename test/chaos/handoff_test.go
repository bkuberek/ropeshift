//go:build chaos

package chaos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
)

// The broken variants of a handoff, each of which the oracle below must catch.
const (
	sabotageStoresAgain = "a restart stores a handoff again"
	sabotageForgets     = "a restart ends a lane whose handoff lost its session"
	sabotageResends     = "a restart sends a handoff's instruction again"
	sabotageSplits      = "a handoff and its upload in two journal writes"
)

// handoffProfile gives a session that will not answer a short time to say so.
const handoffProfile = "[lifecycle]\nhandoff_timeout = \"300ms\"\nmax_restarts = 0\n"

// witnessBrief records what a generation was told, and witnessEnded that a session stopped,
// so the order of two sessions can be read from outside the worker.
const (
	witnessBrief = "brief"
	witnessEnded = "ended"
)

func applyHandoffVariant(variant string) {
	switch variant {
	case sabotageStoresAgain:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageStoresAgain)
	case sabotageForgets:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageForgetsHandoff)
	case sabotageResends:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageResendsHandoff)
	case sabotageSplits:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsHandoff)
	}
}

// handoffDriver is a session that outlives its worker, or goes with it, which a test chooses.
// Its first generation is asked for a handoff and answers, and its second finishes the work.
type handoffDriver struct {
	*lastingDriver
	subject *worker.Worker
	lost    bool
	ctx     context.Context
}

func generationOf(handle string) int64 {
	_, text, _ := strings.Cut(handle, "/")
	generation, _ := strconv.ParseInt(text, 10, 64)
	return generation
}

func (d *handoffDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if spec.Generation == 1 {
		d.Play(d.asked())
	} else {
		d.Play(d.goesOn())
	}
	if err := d.log.Append(Record{Kind: Completed, Op: witnessBrief, Key: handleOf(spec), Note: spec.Brief, Actor: "session"}); err != nil {
		return ports.SessionHandle{}, err
	}
	return d.lastingDriver.Start(ctx, spec)
}

// Find reports a session that went with its worker as one nothing can read.
func (d *handoffDriver) Find(ctx context.Context, spec ports.SessionSpec) (ports.Located, error) {
	located, err := d.lastingDriver.Find(ctx, spec)
	if err != nil || !located.Found || !d.lost || spec.Generation != 1 {
		return located, err
	}
	located.Liveness, located.Readable = lanes.LivenessDead, false
	return located, nil
}

// Adopt plays a session that took its instruction, or never will, and waits to be stopped.
func (d *handoffDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	generation := generationOf(h.ID)
	lane, _, _ := strings.Cut(h.ID, "/")
	if generation == 1 {
		d.Play(fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, {Hold: true}}}})
	} else {
		d.Play(d.goesOn())
	}
	d.Leave(lane, generation, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *handoffDriver) Stop(ctx context.Context, h ports.SessionHandle, how ports.StopMode) error {
	if err := d.witness(witnessEnded, h.ID); err != nil {
		return err
	}
	return d.lastingDriver.Stop(ctx, h, how)
}

// asked is a session at work when its worker asks for a handoff, and one that answers.
func (d *handoffDriver) asked() fake.DriverScript {
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.01}}
	ask := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		for d.ctx.Err() == nil {
			err := d.subject.HandOff(d.ctx, spec.Lane, lanes.HandoffContextThreshold)
			if err == nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}}
	answer := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		line, err := json.Marshal(lanes.Request{
			Version: lanes.SocketProtocol, Token: tokenOf(spec), Handoff: json.RawMessage(`{"next_steps":["go on"],"carries_next_step":true}`),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "session:", err)
			return
		}
		if reply := d.subject.AnswerLane(d.ctx, line); !reply.OK {
			fmt.Fprintln(os.Stderr, "session:", reply.Message)
		}
	}}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, ask, turn}, {answer, turn}}}
}

func tokenOf(spec ports.SessionSpec) string {
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			return value
		}
	}
	return ""
}

// goesOn is the generation that follows, which ends the lane.
func (d *handoffDriver) goesOn() fake.DriverScript {
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.02}}
	report := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		records, err := d.journal.Lanes(context.Background())
		if err != nil || len(records) != 1 {
			fmt.Fprintln(os.Stderr, "lanes:", len(records), err)
			return
		}
		d.report(ports.SessionSpec{}, records[0].ID)
	}}
	return fake.DriverScript{SessionID: "session-2", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, report, turn}}}
}

// roleWorkerHandoff runs one lane through a handoff on a real worker. The claim and the
// dispatch are safe to repeat, and a retry goes on from what the journal holds.
func roleWorkerHandoff(c *child) error {
	return runHandoffWorker(c, func(d *handoffDriver) ports.AgentDriver { return d })
}

// runHandoffWorker runs a worker on whatever session a role builds over the shared driver.
func runHandoffWorker(c *child, session func(*handoffDriver) ports.AgentDriver) error {
	applyHandoffVariant(c.spec.Variant)
	applyContextVariant(c.spec.Variant)
	applyClearVariant(c.spec.Variant)
	applyApprovalVariant(c.spec.Variant)
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
	driver := &handoffDriver{
		lastingDriver: &lastingDriver{AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal},
		lost:          c.spec.Script == laneDied, ctx: c.ctx,
	}
	profiles := fake.NewProfileSource()
	profiles.PutUser(c.spec.Profile)
	subject := worker.New(workerlink.New(client), journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver:      session(driver),
			Provisioner: worker.Provisioner{Profiles: profiles, Worktrees: lastingWorktrees{dir: dir, log: c.log}},
			Secrets:     fake.SecretResolver{},
			Repository:  func(context.Context, string) (string, error) { return "/repository", nil },
		},
	})
	driver.subject = subject
	if err := subject.Start(c.ctx); err != nil {
		return err
	}
	if _, err := apitest.SignIn(c.ctx, client); err != nil {
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

func handoffSpec(w *world, script string) (childSpec, error) {
	w.serveOnce()
	id, err := w.rig.Ready(w.store)
	if err != nil {
		return childSpec{}, err
	}
	spec, err := w.workerSpec("worker-handoff")
	spec.WorkOrder, spec.ClaimID, spec.Lane, spec.Script, spec.Profile = id, w.rig.ID("claim"), w.rig.ID("lane"), script, handoffProfile
	return spec, err
}

// handedOff is the oracle of a handoff. The ledger holds one document, each generation says
// why it began, and the log no process can rewrite holds one session per generation, the
// second after the first, and an instruction sent at most once.
func handedOff(w *world, spec childSpec) error {
	if err := countEvents(w, "work_order."+ledger.UploadHandoffStored, spec.WorkOrder, 1); err != nil {
		return err
	}
	if err := countEvents(w, workorders.EventLaneHandoff, spec.WorkOrder, 1); err != nil {
		return err
	}
	if err := countEvents(w, workorders.EventType(workorders.TriggerLaneCompleted), spec.WorkOrder, 1); err != nil {
		return err
	}
	view, err := w.laneView(spec.Lane)
	if err != nil {
		return err
	}
	if len(view.Generations) != 2 || view.Generations[1].StartReason != ledger.StartHandoff || view.Generations[1].HandoffIn == "" {
		return contract.Violatedf("generations %+v", view.Generations)
	}
	return handoffWitness(w, spec)
}

func handoffWitness(w *world, spec childSpec) error {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	first, second := spec.Lane+"/1", spec.Lane+"/2"
	spawned, inputs, order := map[string]int{}, 0, []string{}
	brief, held := "", false
	for _, record := range records {
		switch {
		case record.Op == witnessSpawn:
			spawned[record.Key]++
			order = append(order, "spawn "+record.Key)
		case record.Op == witnessEnded:
			order = append(order, "ended "+record.Key)
		case record.Op == witnessHold && record.Key == first:
			held = true
		case record.Op == witnessInput && record.Key == first:
			if !held {
				return contract.Violatedf("a running turn was told with nothing held")
			}
			inputs++
		case record.Op == witnessBrief && record.Key == second:
			brief = record.Note
		}
	}
	if spawned[first] != 1 || spawned[second] != 1 {
		return contract.Violatedf("sessions %v", spawned)
	}
	if inputs > 1 {
		return contract.Violatedf("the instruction went %d times", inputs)
	}
	if !strings.Contains(brief, "which handed off") {
		return contract.Violatedf("the next generation was not told: %q", brief)
	}
	if spec.Script == laneDied {
		return nil
	}
	ended, started := -1, -1
	for i, entry := range order {
		if entry == "ended "+first && ended < 0 {
			ended = i
		}
		if entry == "spawn "+second {
			started = i
		}
	}
	if ended < 0 || started < ended {
		return contract.Violatedf("two sessions of one lane overlapped: %v", order)
	}
	return nil
}

func handoffCase(name, op, script string, steps ...string) crashCase {
	return crashCase{
		name: name, op: op, steps: steps, settled: handedOff,
		prepare: func(w *world) (childSpec, error) { return handoffSpec(w, script) },
	}
}

// handoffCases kills a worker at each step of the sequence, with a session that outlives it
// and with one that goes with it, because the two recover by different paths.
func handoffCases() []crashCase {
	const begun, requested, awaited, guarded, stored = "begun", "requested", "awaited", "guarded", "stored"
	return append([]crashCase{
		handoffCase("a handoff", "worker.handoff", "", begun, requested, awaited, guarded, stored),
		handoffCase("a handoff with no session left", "worker.handoff", laneDied, begun, requested, awaited, guarded, stored),
		handoffCase("a handoff's instruction", "effect.input", "", "started", "performed", "finished"),
		handoffCase("a handoff's stop", "effect.stop", "", "started", "performed", "finished"),
		handoffCase("a handoff's next generation", "worker.restart", "", "next"),
	}, slices.Concat(contextCases(), clearCases(), approvalCases())...)
}

func handoffVariants() []variant {
	crash := func(name, op, script, step string) func() (crashCase, string) {
		return func() (crashCase, string) { return handoffCase(name, op, script, step), step }
	}
	return append([]variant{
		{name: sabotageStoresAgain, check: "crash", crash: crash("a second store", "worker.handoff", "", "stored")},
		{name: sabotageForgets, check: "crash", crash: crash("a forgotten handoff", "worker.handoff", laneDied, "requested")},
		{name: sabotageResends, check: "crash", crash: crash("a second instruction", "effect.input", "", "performed")},
		{name: sabotageSplits, check: "crash", crash: crash("a split store", "worker.handoff", "", "split")},
	}, slices.Concat(contextVariants(), clearVariants(), approvalVariants())...)
}

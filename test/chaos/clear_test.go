//go:build chaos

package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// The broken variants of a clear, each of which the oracle below must catch.
const (
	sabotageSplitsClear  = "a clear and its upload in two journal writes"
	sabotageResendsBrief = "a restart tells a cleared session its work again"
)

const (
	clearedTo      = "session-2"
	witnessCleared = "told-its-work"
	// clearTick is how often the worker's own pass comes round for a lane no turn will end.
	clearTick = 5 * time.Millisecond
)

// clearDriver is a session a person clears in its first turn. The process goes on, so a worker that
// takes it over reads the rest of it, and it finishes on the brief it is owed.
type clearDriver struct {
	*handoffDriver
	ticks sync.Once
}

// tick stands for the worker's reconcile loop, which a child of this suite does not run.
func (d *clearDriver) tick() {
	d.ticks.Do(func() {
		go func() {
			for d.ctx.Err() == nil {
				if _, err := d.subject.Reconcile(d.ctx); err != nil && d.ctx.Err() == nil {
					fmt.Fprintln(os.Stderr, "tick:", err)
				}
				time.Sleep(clearTick)
			}
		}()
	})
}

func (d *clearDriver) clearedBy(spec ports.SessionSpec) {
	for _, event := range []lanes.HookEvent{
		{Event: "SessionEnd", Subject: "session-1", SessionID: "session-1", Detail: lanes.SourceClear},
		{Event: "SessionStart", Subject: clearedTo, SessionID: clearedTo, Detail: lanes.SourceClear},
	} {
		event.AtMs = time.Now().UnixMilli()
		line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: tokenOf(spec), Hook: &event})
		if err != nil {
			fmt.Fprintln(os.Stderr, "session:", err)
			return
		}
		if reply := d.subject.AnswerLane(d.ctx, line); !reply.OK {
			fmt.Fprintln(os.Stderr, "session:", reply.Message)
		}
	}
}

func clearedTurn(usd float64) fake.DriverStep {
	return fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: usd}}
}

func (d *clearDriver) finish() fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) {
		records, err := d.journal.Lanes(context.Background())
		if err != nil || len(records) != 1 {
			fmt.Fprintln(os.Stderr, "lanes:", len(records), err)
			return
		}
		d.report(ports.SessionSpec{}, records[0].ID)
	}}
}

// atWork clears itself, ends the turn that held it, and answers the brief it is owed.
func (d *clearDriver) atWork() fake.DriverScript {
	clear := fake.DriverStep{Do: d.clearedBy}
	tick := fake.DriverStep{Do: func(ports.SessionSpec) { d.tick() }}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{
		{{Bound: true}, clear, tick, clearedTurn(0.01)},
		{d.finish(), clearedTurn(0.02)},
	}}
}

// readOn is what a worker that took over reads. A session that took its brief goes on with it, and
// one still waiting has a turn left to end.
func (d *clearDriver) readOn(lane string) fake.DriverScript {
	first := []fake.DriverStep{{Bound: true}, clearedTurn(0.01)}
	key := lanes.NewEffect(lanes.EffectInput, lane, 2, "", "cleared").Key
	if effect, err := d.journal.Effect(context.Background(), key); err == nil && effect.State != lanes.EffectFailed {
		first = []fake.DriverStep{{Bound: true}, d.finish(), clearedTurn(0.02)}
	}
	return fake.DriverScript{SessionID: clearedTo, Model: "fixture", Turns: [][]fake.DriverStep{first, {d.finish(), clearedTurn(0.02)}}}
}

func (d *clearDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	d.Play(d.atWork())
	return d.lastingDriver.Start(ctx, spec)
}

func (d *clearDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	lane, _, _ := strings.Cut(h.ID, "/")
	d.Play(d.readOn(lane))
	d.Leave(lane, generationOf(h.ID), lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	d.tick()
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *clearDriver) Send(ctx context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	if err := d.witness(witnessCleared, h.ID); err != nil {
		return ports.SendReceipt{}, err
	}
	return d.handoffDriver.Send(ctx, h, in)
}

// roleWorkerClear runs one lane through a clear on a real worker.
func roleWorkerClear(c *child) error {
	return runHandoffWorker(c, func(d *handoffDriver) ports.AgentDriver { return &clearDriver{handoffDriver: d} })
}

// clearedOnce is the oracle of a clear. Whichever step a kill landed on, the ledger holds one
// generation for it, on the new session, the lane ends once under it, and the log no process can
// rewrite holds one brief.
func clearedOnce(w *world, spec childSpec) error {
	if err := countEvents(w, workorders.EventType(workorders.TriggerLaneCompleted), spec.WorkOrder, 1); err != nil {
		return err
	}
	var generations []ledger.Generation
	err := w.store.Read(context.Background(), w.rig.Scope, func(tx ports.ReadTx) error {
		var err error
		generations, err = tx.Lanes().Generations(spec.Lane)
		return err
	})
	if err != nil {
		return err
	}
	if len(generations) != 2 || generations[1].StartReason != ledger.StartClear || generations[1].SessionID != clearedTo {
		return contract.Violatedf("generations %+v", generations)
	}
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	told := 0
	for _, record := range records {
		if record.Op == witnessCleared {
			told++
		}
	}
	if told > 1 {
		return contract.Violatedf("told its work %d times", told)
	}
	return nil
}

func clearCase(name, op string, steps ...string) crashCase {
	return crashCase{
		name: name, op: op, steps: steps, settled: clearedOnce,
		prepare: func(w *world) (childSpec, error) {
			spec, err := handoffSpec(w, "")
			spec.Role = "worker-clear"
			return spec, err
		},
	}
}

// clearCases kills a worker at each step of a clear.
func clearCases() []crashCase {
	return []crashCase{clearCase("a clear", "worker.clear", "cleared", "briefed")}
}

func clearVariants() []variant {
	crash := func(name, step string) func() (crashCase, string) {
		return func() (crashCase, string) { return clearCase(name, "worker.clear", step), step }
	}
	return []variant{
		{name: sabotageSplitsClear, check: "crash", crash: crash("a clear in two writes", "split")},
		{name: sabotageResendsBrief, check: "crash", crash: crash("a second brief", "briefed")},
	}
}

func applyClearVariant(variant string) {
	switch variant {
	case sabotageSplitsClear:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsClear)
	case sabotageResendsBrief:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageResendsBrief)
	}
}

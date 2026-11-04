//go:build chaos

package chaos

import (
	"context"
	"fmt"
	"os"
	"strings"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// The broken variants of the context policy, each of which an oracle must catch.
const (
	sabotageHoldsNoTools = "a running turn is told to hand off with nothing held"
	sabotageResendsAsk   = "a restart asks for a compaction again"
	sabotageResendsGoOn  = "a restart tells a compacted session to go on again"
)

// contextProfile puts a lane's thresholds where a script reaches them, and gives a session that
// will not answer a short time to say so.
const contextProfile = handoffProfile + `
[context]
min_headroom_tokens = 40

[context.roles.writer]
compact_tokens = 150
handoff_tokens = 180
`

// compactionRan says a session outlived its worker with a boundary still to read.
const compactionRan = "compaction-ran"

const (
	witnessCompact   = "compact"
	witnessContinued = "continued"
)

// contextDriver is a session a threshold reaches at rest.
type contextDriver struct {
	*handoffDriver
	script string
}

func contextUsage(tokens int64) fake.DriverStep {
	return fake.DriverStep{Context: &lanes.Usage{InputTokens: tokens}}
}

func contextTurn(usd float64) fake.DriverStep {
	return fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: usd}}
}

// responds is what a session does with what it was told last. Only a session this process started
// has a token, and one that took over leaves a handoff to its deadline.
func (d *contextDriver) responds() fake.DriverStep {
	return fake.DriverStep{Do: func(spec ports.SessionSpec) {
		sent := d.Sent()
		if len(sent) == 0 || !strings.Contains(sent[len(sent)-1].Text, lanes.ToolLaneHandoff) {
			d.finishes()
		}
	}}
}

func (d *contextDriver) finishes() {
	records, err := d.journal.Lanes(context.Background())
	if err != nil || len(records) != 1 {
		fmt.Fprintln(os.Stderr, "lanes:", len(records), err)
		return
	}
	d.report(ports.SessionSpec{}, records[0].ID)
}

// atWork crosses the compact threshold at rest, compacts, and finishes.
func (d *contextDriver) atWork() fake.DriverScript {
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{
		{{Bound: true}, contextUsage(30), contextUsage(160), contextTurn(0.01)},
		{{Compaction: true}, contextTurn(0.01)},
		{contextUsage(40), d.responds(), contextTurn(0.02)},
	}}
}

// readOn is what a worker that took over reads: the turn the last one did not finish with, and
// what that leads to.
func (d *contextDriver) readOn() fake.DriverScript {
	first := []fake.DriverStep{{Bound: true}, contextUsage(160)}
	if d.script == compactionRan {
		first = []fake.DriverStep{{Bound: true}, {Compaction: true}}
	}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{
		append(first, contextTurn(0.01)),
		{contextUsage(40), d.responds(), contextTurn(0.02)},
	}}
}

func (d *contextDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if spec.Generation == 1 {
		d.Play(d.atWork())
	} else {
		d.Play(d.goesOn())
	}
	return d.lastingDriver.Start(ctx, spec)
}

func (d *contextDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	generation := generationOf(h.ID)
	lane, _, _ := strings.Cut(h.ID, "/")
	if generation == 1 {
		d.Play(d.readOn())
	} else {
		d.Play(d.goesOn())
	}
	d.Leave(lane, generation, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *contextDriver) Compact(ctx context.Context, h ports.SessionHandle) error {
	if err := d.witness(witnessCompact, h.ID); err != nil {
		return err
	}
	return d.AgentDriver.Compact(ctx, h)
}

func (d *contextDriver) Send(ctx context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	if in.Text == lanes.AfterCompaction {
		if err := d.witness(witnessContinued, h.ID); err != nil {
			return ports.SendReceipt{}, err
		}
	}
	return d.handoffDriver.Send(ctx, h, in)
}

// roleWorkerContext runs one lane through the policy on a real worker.
func roleWorkerContext(c *child) error {
	return runHandoffWorker(c, func(d *handoffDriver) ports.AgentDriver {
		return &contextDriver{handoffDriver: d, script: c.spec.Script}
	})
}

// compacted is the oracle of a compaction. The lane ends once, whichever way a restart took it, and
// the log no process can rewrite holds no input sent twice: a compaction asked for again would cost
// a turn, and a session told twice to go on would act on it twice.
func compacted(w *world, spec childSpec) error {
	if err := countEvents(w, workorders.EventType(workorders.TriggerLaneCompleted), spec.WorkOrder, 1); err != nil {
		return err
	}
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	counts := map[string]int{}
	for _, record := range records {
		counts[record.Op+" "+record.Key]++
	}
	first := spec.Lane + "/1"
	if got := counts[witnessCompact+" "+first]; got > 1 {
		return contract.Violatedf("compacted %d times", got)
	}
	if got := counts[witnessContinued+" "+first]; got > 1 {
		return contract.Violatedf("told to go on %d times", got)
	}
	return nil
}

func contextCase(name, op, script string, steps ...string) crashCase {
	return crashCase{
		name: name, op: op, steps: steps, settled: compacted,
		prepare: func(w *world) (childSpec, error) {
			spec, err := handoffSpec(w, script)
			spec.Role, spec.Profile = "worker-context", contextProfile
			return spec, err
		},
	}
}

// contextCases kills a worker at each step of a compaction, and at the hold of a handoff. A
// compaction the journal cannot place reads as one that did not run, so the lane hands off instead.
func contextCases() []crashCase {
	return []crashCase{
		contextCase("a compaction's request", "worker.context", "", "compacting"),
		contextCase("a compaction's input", "effect.input", "", "started", "performed", "finished"),
		contextCase("a compaction's next turn", "worker.context", compactionRan, "continued"),
		handoffCase("a hold on a running turn", "worker.context", "", "held"),
	}
}

func contextVariants() []variant {
	crash := func(name, script, step string) func() (crashCase, string) {
		return func() (crashCase, string) { return contextCase(name, "worker.context", script, step), step }
	}
	held := func() (crashCase, string) {
		return handoffCase("an unheld instruction", "worker.handoff", "", "requested"), "requested"
	}
	return []variant{
		{name: sabotageHoldsNoTools, check: "crash", crash: held},
		{name: sabotageResendsAsk, check: "crash", crash: crash("a second compaction", "", "compacting")},
		{name: sabotageResendsGoOn, check: "crash", crash: crash("a second word to go on", compactionRan, "continued")},
	}
}

func applyContextVariant(variant string) {
	switch variant {
	case sabotageHoldsNoTools:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageHoldsNoTools)
	case sabotageResendsAsk, sabotageResendsGoOn:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageResendsCompaction)
	}
}

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
	"strings"
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
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
)

// The broken variants of parking, each of which the oracle below must catch.
const (
	sabotageParksFirst      = "a parked commit ahead of the stop"
	sabotageForgetsWake     = "a restart ends a lane that stored a wake"
	sabotageSplitsPark      = "a parked commit and its upload in two journal writes"
	sabotageStaysCompacting = "a restart parks a lane and leaves it in its compaction"
)

// witnessParked records that a parked commit left the worker, which is where a lease can go.
const witnessParked = "parked"

// laneCompacting has a session stand in a compaction when its worker parks it.
const laneCompacting = "compacting"

// parkProfile gives a session that will not answer a short time to say so.
const parkProfile = "[lifecycle]\nhandoff_timeout = \"300ms\"\nmax_restarts = 0\n"

func applyParkVariant(variant string) {
	switch variant {
	case sabotageParksFirst:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageParksFirst)
	case sabotageForgetsWake:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageForgetsWake)
	case sabotageSplitsPark:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsPark)
	case sabotageStaysCompacting:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageStaysCompacting)
	}
}

// parkDriver is a session that outlives its worker, or goes with it, which a test chooses. It
// asks to park, then answers the instruction that asks for its narrative.
type parkDriver struct {
	*lastingDriver
	subject  *worker.Worker
	ctx      context.Context
	lost     bool
	question string
	// compacting has the session start a compaction before it asks to park, which only a hold with
	// a bound of its own may take a lane out of.
	compacting bool
}

func (d *parkDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	d.Play(d.parks())
	return d.lastingDriver.Start(ctx, spec)
}

// Find reports a session that went with its worker as one nothing can read.
func (d *parkDriver) Find(ctx context.Context, spec ports.SessionSpec) (ports.Located, error) {
	located, err := d.lastingDriver.Find(ctx, spec)
	if err != nil || !located.Found || !d.lost {
		return located, err
	}
	located.Liveness, located.Readable = lanes.LivenessDead, false
	return located, nil
}

// Adopt plays a session that took its instruction, or never will, and waits to be stopped.
func (d *parkDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	lane, _, _ := strings.Cut(h.ID, "/")
	generation := generationOf(h.ID)
	d.Play(fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, {Hold: true}}}})
	d.Leave(lane, generation, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *parkDriver) Stop(ctx context.Context, h ports.SessionHandle, how ports.StopMode) error {
	if err := d.witness(witnessEnded, h.ID); err != nil {
		return err
	}
	return d.lastingDriver.Stop(ctx, h, how)
}

// parks is a session at work when its worker parks it, and one that writes its narrative.
func (d *parkDriver) parks() fake.DriverScript { return d.parksCarrying(true) }

// parksCarrying says whether that narrative holds what the next step needs.
func (d *parkDriver) parksCarrying(carries bool) fake.DriverScript {
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.01}}
	park := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		request := lanes.ParkRequest{Trigger: lanes.ParkBlockingQuestion, Expr: lanes.QuestionWake([]string{d.question}), PastCompaction: d.compacting}
		if d.compacting && !d.startCompaction(spec) {
			return
		}
		for d.ctx.Err() == nil {
			if d.subject.Park(d.ctx, spec.Lane, request) == nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}}
	answer := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		line, err := json.Marshal(lanes.Request{
			Version: lanes.SocketProtocol, Token: tokenOf(spec),
			Handoff: json.RawMessage(fmt.Sprintf(`{"next_steps":["go on"],"carries_next_step":%v}`, carries)),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "session:", err)
			return
		}
		if reply := d.subject.AnswerLane(d.ctx, line); !reply.OK {
			fmt.Fprintln(os.Stderr, "session:", reply.Message)
		}
	}}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, park, turn}, {answer, turn}}}
}

// startCompaction moves the lane into a compaction, and waits until its journal says so, because a
// compaction starts only from a state a session's first event has to reach first.
func (d *parkDriver) startCompaction(spec ports.SessionSpec) bool {
	line, err := json.Marshal(lanes.Request{
		Version: lanes.SocketProtocol, Token: tokenOf(spec),
		Hook: &lanes.HookEvent{Event: "PreCompact", Subject: "s", Detail: "auto", AtMs: time.Now().UnixMilli()},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "session:", err)
		return false
	}
	for d.ctx.Err() == nil {
		record, err := d.journal.Lane(d.ctx, spec.Lane)
		switch {
		case err != nil:
			fmt.Fprintln(os.Stderr, "session:", err)
			return false
		case record.Actual == lanes.Compacting:
			return true
		case record.Actual == lanes.Working:
			if reply := d.subject.AnswerLane(d.ctx, line); !reply.OK {
				fmt.Fprintln(os.Stderr, "session:", reply.Message)
				return false
			}
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// witnessLink tells the witness when a parked commit leaves, because nothing a worker wrote can
// vouch for the order of a stop and a lease.
type witnessLink struct {
	ports.WorkerLink
	log *EffectLog
}

func (l witnessLink) Upload(ctx context.Context, batch ledger.UploadBatch) (ledger.UploadReply, error) {
	for _, event := range batch.Events {
		if event.Type != ledger.UploadLaneParked {
			continue
		}
		if err := l.log.Append(Record{Kind: Completed, Op: witnessParked, Key: event.Lane, Actor: "driver"}); err != nil {
			return ledger.UploadReply{}, err
		}
	}
	return l.WorkerLink.Upload(ctx, batch)
}

// roleWorkerPark runs one lane to parked on a real worker. The claim and the dispatch are safe
// to repeat, and a retry goes on from what the journal holds.
func roleWorkerPark(c *child) error {
	applyParkVariant(c.spec.Variant)
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
	driver := &parkDriver{
		lastingDriver: &lastingDriver{AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal},
		lost:          c.spec.Script == laneDied, ctx: c.ctx, question: c.spec.Question, compacting: c.spec.Script == laneCompacting,
	}
	profiles := fake.NewProfileSource()
	profiles.PutUser(c.spec.Profile)
	subject := worker.New(witnessLink{WorkerLink: workerlink.New(client), log: c.log}, journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver:      driver,
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

func parkSpec(w *world, script string, first bool) (childSpec, error) {
	w.serveOnce()
	id, err := w.rig.Ready(w.store)
	if err != nil {
		return childSpec{}, err
	}
	question, err := w.rig.Asked(w.store)
	if err != nil {
		return childSpec{}, err
	}
	spec, err := w.workerSpec("worker-park")
	spec.WorkOrder, spec.ClaimID, spec.Lane, spec.Script, spec.Profile = id, w.rig.ID("claim"), w.rig.ID("lane"), script, parkProfile
	spec.Question, spec.Decision, spec.AnswerFirst = question, w.rig.ID("d"), first
	return spec, err
}

// answerStatement is what a parked lane must hear of its answer.
const answerStatement = "Twice."

func answerOf(decision string) questions.Answer {
	return questions.Answer{DecisionID: decision, Statement: answerStatement, Rationale: "It follows."}
}

// answerLate answers after the kill and before the retry, unless the child answered before it.
func answerLate(w *world, spec childSpec) error {
	if spec.AnswerFirst {
		return nil
	}
	_, err := w.rig.NewOps(w.store).Answer(w.rig.Ctx, w.rig.Scope, spec.Question, answerOf(spec.Decision), w.rig.Actor)
	return err
}

// parkedOut is the oracle of a parking. The wake fired once, the ledger holds one of everything,
// and the log no process can rewrite holds the stop ahead of the commit, so the lease cannot have
// gone with the process alive.
func parkedOut(w *world, spec childSpec) error {
	for _, eventType := range []string{workorders.EventLaneParking, workorders.EventType(workorders.TriggerLaneParked)} {
		if err := countEvents(w, eventType, spec.WorkOrder, 1); err != nil {
			return err
		}
	}
	if err := countEvents(w, "work_order."+ledger.UploadHandoffStored, spec.WorkOrder, 1); err != nil {
		return err
	}
	wake := lanes.WakeID(spec.Lane, 1)
	if err := w.woken(wake); err != nil {
		return err
	}
	view, err := w.laneView(spec.Lane)
	if err != nil {
		return err
	}
	if view.ActualState != ledger.StateParked {
		return contract.Violatedf("the lane reads %+v", view.LaneStatus)
	}
	if err := leftItsCompaction(w, spec); err != nil {
		return err
	}
	return stopBeforeLease(w, spec)
}

// leftItsCompaction reads the worker's own record. The ledger names a parked lane by itself, so only
// this shows one that parked without leaving a compaction, which reached `compacting` before the
// kill and has one way out.
func leftItsCompaction(w *world, spec childSpec) error {
	if spec.Script != laneCompacting {
		return nil
	}
	journal, err := sqlite.OpenJournal(spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	record, err := journal.Lane(w.rig.Ctx, spec.Lane)
	if err != nil {
		return err
	}
	if record.Actual != lanes.Parked {
		return contract.Violatedf("the worker's journal reads %s", record.Actual)
	}
	return nil
}

func stopBeforeLease(w *world, spec childSpec) error {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	session := spec.Lane + "/1"
	var order []string
	spawns := 0
	for _, record := range records {
		switch {
		case record.Op == witnessSpawn && record.Key == session:
			spawns++
		case record.Op == witnessEnded && record.Key == session:
			order = append(order, witnessEnded)
		case record.Op == witnessParked && record.Key == spec.Lane:
			order = append(order, witnessParked)
		}
	}
	if spawns != 1 {
		return contract.Violatedf("%d sessions of generation 1", spawns)
	}
	parked := slices.Index(order, witnessParked)
	if parked < 0 {
		return contract.Violatedf("no parked commit left the worker: %v", order)
	}
	if spec.Script == laneDied {
		return nil
	}
	if ended := slices.Index(order, witnessEnded); ended < 0 || ended > parked {
		return contract.Violatedf("the lease went with the process alive: %v", order)
	}
	return nil
}

func parkCase(name, op, script string, first bool, steps ...string) crashCase {
	return crashCase{
		name: name, op: op, steps: steps, settled: parkedOut,
		prepare: func(w *world) (childSpec, error) { return parkSpec(w, script, first) },
		between: answerLate,
	}
}

// The eight steps of the design's sequence, each with the named steps a kill must reach. The
// last two belong to the transaction, so parkedOrders kills that apart.
var parkSteps = []struct {
	name string
	op   string
	kill []string
}{
	{"the wake", "worker.park", []string{"begun", "stored"}},
	{"the narrative's request", "effect.input", []string{"started", "performed", "finished"}},
	{"the narrative's wait", "worker.park", []string{"requested", "awaited"}},
	{"the document and its push", "worker.handoff", []string{"guarded", "stored"}},
	{"the stop", "effect.stop", []string{"started", "performed", "finished"}},
	{"the confirmation", "worker.park", []string{"gone", "saved"}},
	{"the commit", "worker.park", []string{"committed"}},
}

// parkOrders kills a worker at each step in both orders of answer and crash, with a session that
// outlives it and with one that goes with it, because the two recover by different paths.
func parkOrders() []crashCase {
	var cases []crashCase
	for _, step := range parkSteps {
		for _, first := range []bool{true, false} {
			order := "answer after"
			if first {
				order = "answer first"
			}
			cases = append(cases, parkCase(step.name+", "+order, step.op, "", first, step.kill...))
			if step.op == "effect.stop" {
				continue
			}
			cases = append(cases, parkCase(step.name+" with no session left, "+order, step.op, laneDied, first, step.kill...))
		}
	}
	// The wake is kept before the lane moves, so a kill there leaves it on record in its compaction.
	for _, first := range []bool{true, false} {
		cases = append(cases, parkCase(fmt.Sprintf("the wake of a lane in a compaction, answered first %v", first), "worker.park", laneCompacting, first, "begun"))
	}
	return cases
}

// commitOrders are the last two of the design's steps, in the transaction that commits `parked`
// and evaluates the wake. A kill there leaves nothing, in either order of answer and crash.
func commitOrders() []crashCase {
	prepare := func(first bool) func(w *world) (childSpec, error) {
		return func(w *world) (childSpec, error) {
			question, err := w.rig.Asked(w.store)
			if err != nil {
				return childSpec{}, err
			}
			request, err := w.rig.Parking(w.store, askedExpr(question))
			if err != nil {
				return childSpec{}, err
			}
			spec := parkingSpec("commit-parked", request)
			spec.Question, spec.Decision, spec.AnswerFirst = question, w.rig.ID("d"), first
			return spec, answerFirst(w, spec)
		}
	}
	settled := func(w *world, spec childSpec) error { return w.woken(spec.Key) }
	var cases []crashCase
	for _, first := range []bool{true, false} {
		order := "an answer after the kill"
		if first {
			order = "an answer on record"
		}
		cases = append(cases,
			crashCase{name: "the parked commit, " + order, op: "lane.parked", steps: withSteps("parked", "recorded"), prepare: prepare(first), between: answerLate, settled: settled},
		)
	}
	return append(cases, crashCase{name: "the wake the commit fires", op: "wake", steps: []string{"fired"}, prepare: prepare(true), settled: settled})
}

func answerFirst(w *world, spec childSpec) error {
	if !spec.AnswerFirst {
		return nil
	}
	_, err := w.rig.NewOps(w.store).Answer(w.rig.Ctx, w.rig.Scope, spec.Question, answerOf(spec.Decision), w.rig.Actor)
	return err
}

func parkCases() []crashCase { return slices.Concat(parkOrders(), commitOrders()) }

func parkVariants() []variant {
	crash := func(name, op, script string, first bool, step string) func() (crashCase, string) {
		return func() (crashCase, string) { return parkCase(name, op, script, first, step), step }
	}
	return []variant{
		{
			name: "a commit that evaluates nothing", control: control.Sabotage{NoParkedEvaluation: true}, check: "plane-control",
			crash: crash("an answer the commit misses", "worker.park", "", true, "saved"),
		},
		{name: sabotageParksFirst, check: "crash", crash: crash("a commit ahead of its stop", "worker.park", "", false, "committed")},
		{name: sabotageForgetsWake, check: "crash", crash: crash("a forgotten wake", "worker.park", laneDied, false, "stored")},
		{name: sabotageSplitsPark, check: "crash", crash: crash("a split commit", "worker.park", "", false, "split")},
		{name: sabotageStaysCompacting, check: "crash", crash: crash("a lane left in its compaction", "worker.park", laneCompacting, false, "begun")},
	}
}

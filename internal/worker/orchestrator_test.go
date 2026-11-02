package worker_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
)

const orchestratorProfile = "[lifecycle]\nwake_debounce = \"30s\"\nwake_min_interval = \"2m\"\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\nmax_restarts = 2\nrestart_window = \"10m\"\nmax_generations_per_day = 8\nrestart_backoff = \"30s\"\n"

// orchestratorRig is a worker with one lane at work, which stays at work through everything here,
// and a member who may ask for an orchestrator. Each of its sessions plays the next script.
type orchestratorRig struct {
	*laneRig
	ctx     context.Context
	mu      sync.Mutex
	scripts []fake.DriverScript
}

func newOrchestratorRig(t *testing.T, scripts ...fake.DriverScript) *orchestratorRig {
	t.Helper()
	r := laneRigWith(t, "")
	r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(orchestratorProfile)
	ctx, cancel := context.WithCancel(t.Context())
	o := &orchestratorRig{laneRig: r, ctx: ctx, scripts: scripts}
	r.stopWorker = cancel
	r.config.Driver = &scriptedLanes{AgentDriver: r.driver, script: o.scriptFor}
	t.Cleanup(func() {
		cancel()
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	return o
}

func (o *orchestratorRig) scriptFor(spec ports.SessionSpec) fake.DriverScript {
	if spec.WorkOrder != "" {
		return atRest()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	next := o.scripts[0]
	if len(o.scripts) > 1 {
		o.scripts = o.scripts[1:]
	}
	return next
}

func (o *orchestratorRig) start(t *testing.T) ledger.Orchestrator {
	t.Helper()
	row, err := o.plane.SetOrchestrator(t.Context(), o.subject.Auth().WorkerID, ledger.OrchestratorRunning, member)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// tick is what a worker does on each pass, on the clock of the test.
func (o *orchestratorRig) tick(t *testing.T) {
	t.Helper()
	if err := o.subject.Sync(o.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := o.subject.Reconcile(o.ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * superviseStep)
}

func (o *orchestratorRig) asked(t *testing.T, id, text string) {
	t.Helper()
	_, err := control.NewQuestions(o.store, questions.BatchPolicy{}).Ask(t.Context(), scope, questions.Ask{ID: id, Text: text}, member)
	if err != nil {
		t.Fatal(err)
	}
}

// awaitNoted waits for a worker to read what was asked. A run reads nothing until it has its profile,
// which comes on a goroutine of its own, and a debounce starts where the worker reads an item, so a
// clock moved before that moves past nothing.
func (o *orchestratorRig) awaitNoted(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		o.tick(t)
		if o.subject.WakeWaiting() >= want {
			return
		}
	}
	t.Fatalf("%d items noted, want %d", o.subject.WakeWaiting(), want)
}

// specs lists the orchestrator's sessions, which is what a wake spends.
func (o *orchestratorRig) specs() []ports.SessionSpec {
	var out []ports.SessionSpec
	for _, spec := range o.driver.Specs() {
		if spec.WorkOrder == "" {
			out = append(out, spec)
		}
	}
	return out
}

func (o *orchestratorRig) awaitSessions(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		o.tick(t)
		if len(o.specs()) >= want {
			return
		}
	}
	t.Fatalf("%d sessions, want %d", len(o.specs()), want)
}

func aSession(next ...[]fake.DriverStep) fake.DriverScript {
	return script(append([][]fake.DriverStep{{{Bound: true}, completed(0.01)}}, next...)...)
}

// wokenOnce brings the orchestrator up on one question and waits for it to rest.
func (o *orchestratorRig) wokenOnce(t *testing.T) {
	t.Helper()
	o.start(t)
	o.tick(t)
	o.asked(t, "q1", "Which retry budget?")
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	o.awaitRest(t)
}

func (o *orchestratorRig) row(t *testing.T) ledger.Orchestrator {
	t.Helper()
	row, err := o.plane.Orchestrator(t.Context(), o.subject.Auth().WorkerID)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func (o *orchestratorRig) await(t *testing.T, what string, done func(ledger.Orchestrator) bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		o.tick(t)
		if err := o.subject.Publish(o.ctx); err != nil {
			t.Fatal(err)
		}
		if err := o.subject.Upload(o.ctx); err != nil {
			t.Fatal(err)
		}
		if done(o.row(t)) {
			return
		}
	}
	t.Fatalf("%s: row %+v", what, o.row(t))
}

func (o *orchestratorRig) awaitRest(t *testing.T) {
	t.Helper()
	o.await(t, "a rest", func(row ledger.Orchestrator) bool { return row.Actual == string(lanes.Idle) })
}

// The first passphrase needs no session, which is safe only because no agent runs until a member
// says so. A digest with work in it must not be enough.
func TestNoOrchestratorRunsUntilAMemberAsks(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.asked(t, "q1", "What is the retry budget?")
	for range 3 {
		o.clock.Advance(time.Hour)
		o.tick(t)
	}
	if got := len(o.specs()); got != 0 {
		t.Fatalf("%d sessions no member asked for", got)
	}
}

// An empty wake reads a whole context, and a session's first prompt is a wake, so an idle fleet has
// no session at all.
func TestAnIdleOrchestratorSpendsNothing(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.start(t)
	for range 10 {
		o.clock.Advance(10 * time.Minute)
		o.tick(t)
	}
	if got := len(o.specs()); got != 0 {
		t.Fatalf("%d sessions with nothing to judge", got)
	}
}

func TestThreeItemsMakeOneSession(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.start(t)
	o.tick(t)
	o.asked(t, "q1", "Which retry budget?")
	o.awaitNoted(t, 1)
	o.clock.Advance(10 * time.Second)
	o.asked(t, "q2", "Which log level?")
	o.asked(t, "q3", "Which queue?")
	o.tick(t)
	if got := len(o.specs()); got != 0 {
		t.Fatalf("a wake inside the debounce: %d sessions", got)
	}
	o.clock.Advance(21 * time.Second)
	o.awaitSessions(t, 1)
	brief := o.specs()[0].Brief
	for _, text := range []string{"Which retry budget?", "Which log level?", "Which queue?"} {
		if !strings.Contains(brief, text) {
			t.Errorf("the first wake leaves out %q: %s", text, brief)
		}
	}
	for range 3 {
		o.clock.Advance(10 * time.Minute)
		o.tick(t)
	}
	if got := len(o.driver.Sent()); got != 0 {
		t.Errorf("%d wakes for what the first carried", got)
	}
}

func TestASecondBatchIsOneWakeUnderTheFloor(t *testing.T) {
	o := newOrchestratorRig(t, aSession([]fake.DriverStep{completed(0.02)}))
	o.wokenOnce(t)
	o.asked(t, "q2", "Which log level?")
	o.tick(t)
	o.clock.Advance(89 * time.Second)
	o.tick(t)
	if got := len(o.driver.Sent()); got != 0 {
		t.Fatalf("a wake under the floor: %d", got)
	}
	o.asked(t, "q3", "Which queue?")
	o.tick(t)
	o.clock.Advance(40 * time.Second)
	o.tick(t)
	o.tick(t)
	sent := o.driver.Sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "Which log level?") || !strings.Contains(sent[0].Text, "Which queue?") {
		t.Fatalf("sent %+v", sent)
	}
}

// It has no work order and no outcome, so a turn that ends leaves it waiting: nothing nudges it,
// and nothing ends it.
func TestARestingOrchestratorIsNotALaneWithNoOutcome(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.wokenOnce(t)
	if sent := o.driver.Sent(); len(sent) != 0 {
		t.Errorf("it was told %+v", sent)
	}
	if row := o.row(t); row.Desired != ledger.OrchestratorRunning || row.Actual != string(lanes.Idle) {
		t.Errorf("row %+v", row)
	}
}

// Its token reaches its own tools and nothing of a lane's: it has no work to report on and no lane
// to ask for.
func TestAnOrchestratorsTokenReachesOnlyItsOwnTools(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.wokenOnce(t)
	spec := o.specs()[0]
	var token string
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			token = value
		}
	}
	ask := func(request lanes.Request) lanes.Reply {
		request.Version, request.Token = lanes.SocketProtocol, token
		line, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		return o.subject.AnswerLane(t.Context(), line)
	}
	if who := ask(lanes.Request{Who: true}); !who.OK || who.Owner != lanes.OwnerOrchestrator {
		t.Fatalf("who %+v", who)
	}
	report := workorders.Report{Outcome: workorders.OutcomeCompleted}
	for name, request := range map[string]lanes.Request{
		"a report": {Report: &report},
		"a wait":   {Wait: &lanes.WaitDeclaration{Minutes: 1}},
		"a reply":  {Reply: &lanes.ReplyRequest{ReplyTo: "m", Body: "b"}},
		"an ask":   {Ask: &lanes.AskRequest{Text: "Why?"}},
	} {
		if got := ask(request); got.OK || got.Code != lanes.CodeForbidden {
			t.Errorf("%s: %+v", name, got)
		}
	}
	tools := strings.Join(spec.Tools, " ")
	if !strings.Contains(tools, lanes.ToolApprovalDecide) || strings.Contains(tools, lanes.ToolLaneReport) {
		t.Errorf("its tools %v", spec.Tools)
	}
}

// A failed orchestrator stops nothing that is running (FR-ORCH-4), and it comes back under the
// breaker. The wake it died in told nobody, so the next generation starts on that.
func TestACrashedOrchestratorStopsNoLane(t *testing.T) {
	o := newOrchestratorRig(t, script([]fake.DriverStep{{Bound: true}, {Crash: true}}), aSession())
	o.start(t)
	o.tick(t)
	o.asked(t, "q1", "Which retry budget?")
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	o.await(t, "a backoff", func(row ledger.Orchestrator) bool { return row.Actual == string(lanes.Backoff) })
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 2)
	second := o.specs()[1]
	if second.Generation != 2 || !strings.Contains(second.Brief, "Which retry budget?") {
		t.Errorf("generation %d, brief %q", second.Generation, second.Brief)
	}
	if view := o.read(); view.ActualState != string(lanes.Working) {
		t.Errorf("the lane reads %s", view.ActualState)
	}
	if row := o.laneRig.row(); row.State != workorders.Running {
		t.Errorf("the work order reads %s", row.State)
	}
}

// A member's stop ends the session and leaves the lanes alone.
func TestAMembersStopEndsTheSession(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.wokenOnce(t)
	if _, err := o.plane.SetOrchestrator(t.Context(), o.subject.Auth().WorkerID, ledger.OrchestratorStopped, member); err != nil {
		t.Fatal(err)
	}
	o.await(t, "an end", func(row ledger.Orchestrator) bool { return row.Actual == "ended" })
	if view := o.read(); view.ActualState != string(lanes.Working) {
		t.Errorf("the lane reads %s", view.ActualState)
	}
}

// A usage limit is a wait for an orchestrator: it holds no work to park, and a wake before the reset
// would cost a turn that fails.
func TestAtALimitAnOrchestratorWaitsAndIsNotWoken(t *testing.T) {
	o := newOrchestratorRig(t)
	reset := o.clock.Now().Add(time.Hour).UnixMilli()
	o.scripts = []fake.DriverScript{script(
		[]fake.DriverStep{{Bound: true}, *rejected(reset), completed(0.01)},
		[]fake.DriverStep{completed(0.02)},
	)}
	o.wokenOnce(t)
	o.asked(t, "q2", "Which log level?")
	o.tick(t)
	o.clock.Advance(30 * time.Minute)
	o.tick(t)
	o.clock.Advance(time.Minute)
	o.tick(t)
	if got := len(o.driver.Sent()); got != 0 {
		t.Fatalf("a wake before the reset: %d", got)
	}
	record, err := o.journal.Lane(t.Context(), o.row(t).Lane())
	if err != nil {
		t.Fatal(err)
	}
	if record.Parking != nil {
		t.Errorf("an orchestrator parked: %+v", record.Parking)
	}
	o.clock.Advance(30 * time.Minute)
	o.tick(t)
	o.tick(t)
	o.tick(t)
	if got := len(o.driver.Sent()); got != 1 {
		t.Errorf("%d wakes after the reset", got)
	}
}

// It holds no lane's place, or it could keep the work it claimed from starting.
func TestAnOrchestratorTakesNoLanesPlace(t *testing.T) {
	o := newOrchestratorRig(t, aSession())
	o.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(orchestratorProfile + "[admission]\nmax_lanes = 2\nmax_lanes_per_sign_in = 2\n")
	o.tick(t)
	o.wokenOnce(t)
	o.secondLane("wo-2", "lane-2")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		o.tick(t)
		for _, spec := range o.driver.Specs() {
			if spec.Lane == "lane-2" {
				return
			}
		}
	}
	t.Error("a second lane under its cap never started")
}

// Its budget is the orchestrator role's, and a handoff asks nothing of it, because it has no tool to
// answer with. What it knows lives in the ledger, and the next generation starts on the next wake.
func TestAnOrchestratorHandsOffOnItsOwnBudget(t *testing.T) {
	o := newOrchestratorRig(t)
	o.scripts = []fake.DriverScript{aSession(), aSession()}
	o.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(orchestratorProfile + "[context]\nmin_headroom_tokens = 40\n[context.roles.orchestrator]\nwarn_tokens = 100\ncompact_tokens = 200\nhandoff_tokens = 300\n")
	o.scripts[0] = script([]fake.DriverStep{{Bound: true}, tokens(30), tokens(400), completed(0.01)})
	o.start(t)
	o.tick(t)
	o.asked(t, "q1", "Which retry budget?")
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	o.await(t, "a next generation", func(row ledger.Orchestrator) bool { return row.Generation == 2 })
	if sent := o.driver.Sent(); len(sent) != 0 {
		t.Errorf("it was asked for what it has no tool to give: %+v", sent)
	}
	if got := len(o.specs()); got != 1 {
		t.Fatalf("a next generation with nothing to judge: %d sessions", got)
	}
	o.asked(t, "q2", "Which log level?")
	o.tick(t)
	o.clock.Advance(3 * time.Minute)
	o.awaitSessions(t, 2)
	if spec := o.specs()[1]; spec.Generation != 2 || !strings.Contains(spec.Brief, "Which log level?") {
		t.Errorf("generation %d, brief %q", spec.Generation, spec.Brief)
	}
}

// A restart finds the session its last worker left, and goes on from what its journal holds.
func TestAnOrchestratorComesBackAfterARestart(t *testing.T) {
	o := newOrchestratorRig(t, script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	o.start(t)
	o.tick(t)
	o.asked(t, "q1", "Which retry budget?")
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	o.awaitSessions(t, 1)
	row := o.row(t)
	first := o.subject
	o.stopWorker()
	t.Cleanup(func() {
		o.driver.StopEverySession()
		first.Wait()
	})
	driver := fake.NewAgentDriver()
	driver.Leave(row.Lane(), 1, sessionAlive)
	driver.Play(script([]fake.DriverStep{{Hold: true}}))
	config := *o.config
	config.Driver = driver
	next := worker.New(o.link, o.journal, o.clock, worker.Config{Name: "test", Lanes: &config})
	t.Cleanup(next.Wait)
	t.Cleanup(driver.StopEverySession)
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := next.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	if _, err := next.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * superviseStep)
	record, err := o.journal.Lane(t.Context(), row.Lane())
	if err != nil || record.Owner != lanes.OwnerOrchestrator || record.State != lanes.StateRunning {
		t.Fatalf("record %+v: %v", record, err)
	}
	if got := len(driver.Specs()); got != 0 {
		t.Errorf("a restart started %d sessions", got)
	}
}

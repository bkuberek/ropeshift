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

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/adapters/workerlink"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/contract"
)

// The paths a resume can take, which a child's spec names.
const (
	pathNative = "native"
	pathFresh  = "fresh"
)

const (
	// sabotageSplitsResume keeps a resume and its upload in two journal writes, so a kill between
	// them leaves a lane on a generation the ledger never hears of.
	sabotageSplitsResume = "a resume and its upload in two journal writes"
	// sabotageLeavesParked journals a resume with its lane still reading `parked`, so a kill after
	// that write leaves a generation with no way to rest, park, or hand off.
	sabotageLeavesParked = "a resume that leaves its lane reading parked"
	// sabotageDropsFacts starts the next generation with nothing it inherited.
	sabotageDropsFacts = "a first prompt that states no answer"
	// witnessStranded records a session that found its lane still reading `parked`.
	witnessStranded = "stranded"
	// witnessTold records a session whose first prompt held the answer it parked on.
	witnessTold = "told"
)

// resumeDriver is a parked session's lane, and the generation that follows it.
type resumeDriver struct {
	*parkDriver
	carries bool
	// member is who answers, and decision what the answer records.
	member   *api.Client
	decision string
}

func (d *resumeDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if spec.Generation == 1 {
		d.Play(d.asksFirst(d.parksCarrying(d.carries)))
	} else {
		d.Play(d.goesOn())
		if err := d.noteTold(spec); err != nil {
			return ports.SessionHandle{}, err
		}
	}
	return d.lastingDriver.Start(ctx, spec)
}

// noteTold keeps what only a prompt can show: an answer that reached no prompt is lost for good,
// and nothing on the record would say so.
func (d *resumeDriver) noteTold(spec ports.SessionSpec) error {
	if !strings.Contains(spec.Brief, answerStatement) {
		return nil
	}
	return d.witness(witnessTold, handleOf(spec))
}

// asksFirst has the session attach to its question and hear it answered, before it parks. What
// that answer becomes depends on where a kill leaves the message, and every path ends in a prompt.
func (d *resumeDriver) asksFirst(script fake.DriverScript) fake.DriverScript {
	ask := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		if err := d.asks(spec); err != nil {
			fmt.Fprintln(os.Stderr, "session:", err)
		}
	}}
	script.Turns[0] = slices.Insert(script.Turns[0], 1, ask)
	return script
}

func (d *resumeDriver) asks(spec ports.SessionSpec) error {
	question, err := d.member.Question(d.ctx, d.question)
	if err != nil {
		return err
	}
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: tokenOf(spec), Ask: &lanes.AskRequest{Text: question.Text}})
	if err != nil {
		return err
	}
	if reply := d.subject.AnswerLane(d.ctx, line); !reply.OK {
		return errors.New(reply.Message)
	}
	_, err = d.member.AnswerQuestion(d.ctx, d.question, api.AnswerQuestion{ID: d.decision, Answer: answerStatement, Rationale: "It follows."})
	return err
}

// Resume stands for a new process on an earlier session, which the witness counts as a spawn.
func (d *resumeDriver) Resume(ctx context.Context, spec ports.SessionSpec, from ports.ResumeRef) (ports.SessionHandle, error) {
	d.Play(d.goesOn())
	if err := d.noteTold(spec); err != nil {
		return ports.SessionHandle{}, err
	}
	if err := d.witness(witnessSpawn, handleOf(spec)); err != nil {
		return ports.SessionHandle{}, err
	}
	if err := os.WriteFile(d.marker(handleOf(spec)), nil, 0o600); err != nil {
		return ports.SessionHandle{}, err
	}
	return d.AgentDriver.Resume(ctx, spec, from)
}

// Adopt plays the rest of whichever session it found.
func (d *resumeDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	if generationOf(h.ID) == 1 {
		return d.parkDriver.Adopt(ctx, h, from)
	}
	return d.lastingDriver.Adopt(ctx, h, from)
}

// goesOn is the generation that follows, which ends the lane. It first says where it found its
// lane, because a session that reports needs no state at all, and only a lane's state lets it
// rest, park, or hand off.
func (d *resumeDriver) goesOn() fake.DriverScript {
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.02}}
	report := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		record, err := d.journal.Lane(context.Background(), spec.Lane)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lane:", err)
		}
		if record.Actual == lanes.Parked {
			if err := d.witness(witnessStranded, handleOf(spec)); err != nil {
				fmt.Fprintln(os.Stderr, "witness:", err)
			}
		}
		d.report(spec, "")
	}}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{{Bound: true}, report, turn}}}
}

// roleWorkerResume runs one lane through parking and back, as passes of a daemon would. A wake
// that fires at the parked commit leaves a claim for the next pass, and a retry goes on from what
// the journal holds.
func roleWorkerResume(c *child) error {
	applyResumeVariant(c.spec.Variant)
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
	lasting := &lastingDriver{AgentDriver: fake.NewAgentDriver(), dir: dir, log: c.log, journal: journal}
	driver := &resumeDriver{
		parkDriver: &parkDriver{lastingDriver: lasting, ctx: c.ctx, question: c.spec.Question},
		carries:    c.spec.Script == pathFresh, member: client, decision: c.spec.Decision,
	}
	if c.spec.Script == pathFresh {
		driver.SetResumeCosts(lanes.ResumeCosts{})
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
	return convergeUntilEnded(c, subject, journal)
}

// convergeUntilEnded passes as a daemon's ticks do, until the lane has an end on record.
func convergeUntilEnded(c *child, subject *worker.Worker, journal *sqlite.Journal) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := subject.Converge(c.ctx); err != nil {
			return err
		}
		record, err := journal.Lane(c.ctx, c.spec.Lane)
		if err == nil && record.State == lanes.StateEnded {
			return nil
		}
		if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("lane %s has no end on record", c.spec.Lane)
}

func applyResumeVariant(variant string) {
	switch variant {
	case sabotageSplitsResume:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsResume)
	case sabotageLeavesParked:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageLeavesParked)
	case sabotageDropsFacts:
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageDropsFacts)
	}
}

// resumeSpec leaves the answer to the session's first turn, so the parked commit fires the wake,
// and the child goes on to the claim and the resume by itself.
func resumeSpec(w *world, path string) (childSpec, error) {
	spec, err := parkSpec(w, path, false)
	if err != nil {
		return childSpec{}, err
	}
	spec.Role = "worker-resume"
	return spec, nil
}

// resumedOut is the oracle of a resume. The ledger holds one parking and one wake, the lane ended
// in its second generation for the reason its path gives, and the log no process can rewrite shows
// two sessions of one work order that never overlap.
func resumedOut(w *world, spec childSpec) error {
	if err := countEvents(w, workorders.EventType(workorders.TriggerLaneParked), spec.WorkOrder, 1); err != nil {
		return err
	}
	if err := countEvents(w, workorders.EventType(workorders.TriggerWakeFired), spec.WorkOrder, 1); err != nil {
		return err
	}
	if err := countEvents(w, workorders.EventType(workorders.TriggerLaneStarted), spec.WorkOrder, 2); err != nil {
		return err
	}
	if err := countEvents(w, workorders.EventType(workorders.TriggerLaneCompleted), spec.WorkOrder, 1); err != nil {
		return err
	}
	view, err := w.laneView(spec.Lane)
	if err != nil {
		return err
	}
	want := ledger.StartResume
	if spec.Script == pathFresh {
		want = ledger.StartHandoff
	}
	if len(view.Generations) != 2 || view.Generations[1].StartReason != want {
		return contract.Violatedf("generations %+v, want %s", view.Generations, want)
	}
	if view.ActualState != ledger.StateEnded {
		return contract.Violatedf("the lane reads %+v", view.LaneStatus)
	}
	row, err := w.rig.WorkOrder(w.store, spec.WorkOrder)
	if err != nil {
		return err
	}
	if row.ResumeLaneID != "" {
		return contract.Violatedf("a resumed lane is still to resume: %+v", row)
	}
	return sessionsApart(w, spec)
}

// sessionsApart reads the independent log for what a crash must never do: a second session
// before the first has stopped, or a second one of either.
func sessionsApart(w *world, spec childSpec) error {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	first, second := spec.Lane+"/1", spec.Lane+"/2"
	spawned := map[string]int{}
	var order []string
	told := false
	for _, record := range records {
		switch {
		case record.Op == witnessSpawn:
			spawned[record.Key]++
			order = append(order, "spawn "+record.Key)
		case record.Op == witnessEnded:
			order = append(order, "ended "+record.Key)
		case record.Op == witnessStranded:
			return contract.Violatedf("session %s found its lane still reading parked", record.Key)
		case record.Op == witnessTold:
			told = told || record.Key == second
		}
	}
	if spawned[first] != 1 || spawned[second] != 1 {
		return contract.Violatedf("sessions %v", spawned)
	}
	if !told {
		return contract.Violatedf("session %s started with no word of its answer", second)
	}
	ended, started := slices.Index(order, "ended "+first), slices.Index(order, "spawn "+second)
	if ended < 0 || started < ended {
		return contract.Violatedf("two sessions of one work order overlapped: %v", order)
	}
	return nil
}

func resumeCase(path, op string, steps ...string) crashCase {
	return crashCase{
		name: "a resume, " + path + ", " + op, op: op, steps: steps, settled: resumedOut,
		prepare: func(w *world) (childSpec, error) { return resumeSpec(w, path) },
	}
}

// ledgerResumeCase kills the transaction that starts a woken lane's next generation.
func ledgerResumeCase(reason ledger.StartReason) crashCase {
	return crashCase{
		name: "the resume transaction, " + string(reason), op: "lane.resume",
		steps: withSteps("started", "recorded"),
		prepare: func(w *world) (childSpec, error) {
			if reason != ledger.StartResume {
				return childSpec{}, errors.New("this step belongs to a native resume")
			}
			request, err := w.rig.Parked(w.store, `{"human_resume":true}`)
			if err != nil {
				return childSpec{}, err
			}
			ops := w.rig.NewOps(w.store)
			if _, err := ops.ResumeLane(w.rig.Ctx, w.rig.Scope, request.Lane, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			grant, err := ops.Claim(w.rig.Ctx, w.rig.Scope, w.rig.Request(request.WorkOrder, "a"), w.rig.Actor)
			if err != nil {
				return childSpec{}, err
			}
			holder := workorders.Holder{Lease: grant.Lease, Lane: request.Lane, Generation: 1}
			return holderSpec("continue-lane", holder), nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, workorders.EventLaneResumed, spec.WorkOrder, 1); err != nil {
				return err
			}
			return hasState(w, spec.WorkOrder, workorders.Running)
		},
	}
}

func roleContinueLane(c *child) error {
	_, err := c.ops.ContinueLane(c.ctx, c.scope, c.holder(), ledger.StartResume, c.actor)
	return err
}

// resumeCases kills a worker at each step of a resume, on both paths. A kill at each step of the
// session's own effect runs through the same run, because a lane that resumed has an ordinary
// start from there on.
func resumeCases() []crashCase {
	steps := []string{"claimed", "charged", "begun", "uploaded", "launching", "launched"}
	return []crashCase{
		resumeCase(pathNative, "worker.resume", steps...),
		resumeCase(pathFresh, "worker.resume", steps...),
		ledgerResumeCase(ledger.StartResume),
	}
}

func resumeVariants() []variant {
	return []variant{
		{
			name: sabotageSplitsResume, check: "crash",
			crash: func() (crashCase, string) { return resumeCase(pathNative, "worker.resume", "split"), "split" },
		},
		{
			name: sabotageLeavesParked, check: "crash",
			crash: func() (crashCase, string) { return resumeCase(pathNative, "worker.resume", "begun"), "begun" },
		},
		{
			name: sabotageDropsFacts, check: "crash",
			crash: func() (crashCase, string) { return resumeCase(pathFresh, "worker.resume", "launched"), "launched" },
		},
		{
			name:     "a resume that leaves the lane to resume on the work order",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.start", ", resume_lane_id = NULL", "")}, check: "R4",
		},
		{
			name:     "a parked lane bound to nothing",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.mark_parked", "resume_affinity = :affinity", "resume_affinity = ''")}, check: "R6",
		},
		{
			name:     "a delay that never binds",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.ease", " AND resume_after <= :now", "")}, check: "R7",
		},
		{
			name:     "a preferred worker that holds on for good",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.easing", "resume_after <= :now", "resume_after < 0")}, check: "R7",
		},
		{
			name:     "a cache clock that never starts",
			sabotage: sqlstore.Sabotage{Rewrite: replace("generation.responded", "last_response_at = :now", "last_response_at = NULL")}, check: "R10",
		},
		{
			name:     "a claim that drops the lane's own layer",
			sabotage: sqlstore.Sabotage{Rewrite: replace("claim.record", ":lane, :overrides", ":lane, ''")}, check: "R2",
		},
		{
			name:     "a claim on woken work that runs no lane",
			sabotage: sqlstore.Sabotage{Rewrite: replace("claim.record", ":lane, :overrides", "'', :overrides")}, check: "R1",
		},
		{
			name:     "a claim that forgets the lane it woke",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.grant", "SET state = 'claimed',", "SET state = 'claimed', resume_lane_id = NULL,")}, check: "R1",
		},
		{
			name:     "work a claim took is still offered",
			sabotage: sqlstore.Sabotage{Rewrite: replace("work_order.woken", "AND state = 'ready'", "AND state IN ('ready', 'claimed')")}, check: "R1",
		},
		{
			name:     "a generation that says one reason for every start",
			sabotage: sqlstore.Sabotage{Rewrite: replace("generation.start", ":start_reason, :handoff_in", "'resume', :handoff_in")}, check: "R3",
		},
		{
			name:     "a resume that leaves its work order claimed",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.start", "SET state = 'running', resume_lane_id = NULL", "SET resume_lane_id = NULL")}, check: "R3",
		},
		{
			name:     "a resume that rewrites how the last generation ended",
			sabotage: sqlstore.Sabotage{Rewrite: replace("generation.end", " AND ended_at IS NULL", "")}, check: "R3",
		},
		{
			name:     "a released claim forgets the lane to resume",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.release", "SET state = 'ready',", "SET state = 'ready', resume_lane_id = NULL,")}, check: "R5",
		},
		{
			name:     "a checkpoint that forgets what was in progress",
			sabotage: sqlstore.Sabotage{Rewrite: replace("checkpoint.insert", ":handoff_artifact_id, :pending_instruction, :now", ":handoff_artifact_id, '', :now")}, check: "R9",
		},
		{
			name:     "a resume that names no worker",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.mark_parked", "resume_worker = :worker", "resume_worker = ''")}, check: "R9",
		},
		{
			name:     "a resume that reads no document",
			sabotage: sqlstore.Sabotage{Rewrite: replace("handoff.find", "AND generation = :generation", "AND generation = :generation AND 1 = 0")}, check: "R9",
		},
		{
			name:     "a resume with no cache clock",
			sabotage: sqlstore.Sabotage{Rewrite: replace("generation.responded", "last_response_at = :now", "last_response_at = NULL")}, check: "R9",
		},
		{
			name: "a resume that leaves the last generation writing",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lane.generation",
				"SELECT current_generation FROM lanes", "SELECT 1 FROM lanes")},
			check: "R11",
		},
		{
			name:     "a resume that leaves the last claim writing",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.guard", "\n   AND fence = :fence", "")}, check: "R11",
		},
		{
			name:     "a second parking an old word wakes",
			sabotage: sqlstore.Sabotage{Rewrite: replace("wake.signaled", " AND rev > :after", " AND rev > 0 * :after")}, check: "R12",
		},
		{name: "a resume under any claim", control: control.Sabotage{ResumesAnyClaim: true}, check: "R13"},
		{name: "a retried resume refused", control: control.Sabotage{RefusesResumeRetry: true}, check: "R15"},
		{name: "a repeated parked commit refused", control: control.Sabotage{RefusesParkedRepeat: true}, check: "P6"},
		{
			name: "a read that cannot go on from the last",
			sabotage: sqlstore.Sabotage{Rewrite: replace("question.list",
				"(CAST(:after AS TEXT) = '' OR q.id > CAST(:after AS TEXT))", "CAST(:after AS TEXT) = CAST(:after AS TEXT)")},
			check: "Q25",
		},
		{
			name:     "a parked commit that keeps the fence",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.park", "\n       fence = fence + 1,", "")}, check: "P9",
		},
		{
			name:     "a wake that names no lane on its work order",
			sabotage: sqlstore.Sabotage{Rewrite: replace("wake.move", ", resume_lane_id = :lane_id", "")}, check: "P9",
		},
		{
			name:     "an answer that reads no wake",
			sabotage: sqlstore.Sabotage{Rewrite: replace("wake.open", "AND fired_at IS NULL", "AND 1 = 0")}, check: "P9",
		},
	}
}

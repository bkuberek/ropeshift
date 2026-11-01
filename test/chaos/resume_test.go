//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/adapters/workerlink"
	"ropeshift.dev/ropeshift/internal/api"
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

// sabotageSplitsResume keeps a resume and its upload in two journal writes, so a kill between
// them leaves a lane on a generation the ledger never hears of.
const sabotageSplitsResume = "a resume and its upload in two journal writes"

// resumeDriver is a parked session's lane, and the generation that follows it.
type resumeDriver struct {
	*parkDriver
	carries bool
}

func (d *resumeDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if spec.Generation == 1 {
		d.Play(d.parksCarrying(d.carries))
	} else {
		d.Play(d.goesOn())
	}
	return d.lastingDriver.Start(ctx, spec)
}

// Resume stands for a new process on an earlier session, which the witness counts as a spawn.
func (d *resumeDriver) Resume(ctx context.Context, spec ports.SessionSpec, from ports.ResumeRef) (ports.SessionHandle, error) {
	d.Play(d.goesOn())
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

// goesOn is the generation that follows, which ends the lane.
func (d *resumeDriver) goesOn() fake.DriverScript {
	turn := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.02}}
	report := fake.DriverStep{Do: func(spec ports.SessionSpec) { d.report(spec, "") }}
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
	driver := &resumeDriver{parkDriver: &parkDriver{lastingDriver: lasting, ctx: c.ctx, question: c.spec.Question}, carries: c.spec.Script == pathFresh}
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
	if variant == sabotageSplitsResume {
		_ = os.Setenv(worker.EnvSabotage, worker.SabotageSplitsResume)
	}
}

func resumeSpec(w *world, path string) (childSpec, error) {
	spec, err := parkSpec(w, path, false)
	if err != nil {
		return childSpec{}, err
	}
	spec.Role = "worker-resume"
	return spec, answerLateFor(w, spec)
}

// answerLateFor answers before the child starts, so the parked commit fires the wake, and the
// child goes on to the claim and the resume by itself.
func answerLateFor(w *world, spec childSpec) error {
	_, err := w.rig.NewOps(w.store).Answer(w.rig.Ctx, w.rig.Scope, spec.Question, answerOf(spec.Decision), w.rig.Actor)
	return err
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
	for _, record := range records {
		switch {
		case record.Op == witnessSpawn:
			spawned[record.Key]++
			order = append(order, "spawn "+record.Key)
		case record.Op == witnessEnded:
			order = append(order, "ended "+record.Key)
		}
	}
	if spawned[first] != 1 || spawned[second] != 1 {
		return contract.Violatedf("sessions %v", spawned)
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
	}
}

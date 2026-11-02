//go:build chaos

package chaos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/clock"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/adapters/workerlink"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/api/server"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
)

const reportedEvent = "work_order.note"

// servePlane serves the real handler in this process on the world's ledger, so a
// worker child speaks the real protocol over a real socket.
func (w *world) servePlane(heartbeat, ttl time.Duration) string {
	w.t.Helper()
	socket := filepath.Join(w.target.Dir, api.ControlSocketName)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		w.t.Fatal(err)
	}
	httpServer := &http.Server{
		Handler: server.New(server.Config{
			Scope: w.rig.Scope, Store: w.store, LeaseTTL: ttl.Milliseconds(), HeartbeatMs: heartbeat.Milliseconds(),
			Identity: fake.NewIdentityProvider(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ConnState: func(_ net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				w.planeConns.Add(1)
			case http.StateClosed, http.StateHijacked:
				w.planeConns.Add(-1)
			}
		},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = httpServer.Serve(listener)
	}()
	w.t.Cleanup(func() {
		_ = httpServer.Close()
		<-done
	})
	return socket
}

func (w *world) plane() *control.Workers {
	return control.NewWorkers(w.store, w.rig.Scope, control.WorkerOptions{})
}

func (w *world) joinToken() (string, error) {
	return w.plane().CreateJoinToken(w.rig.Ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
}

func (w *world) workerSpec(role string) (childSpec, error) {
	token, err := w.joinToken()
	if err != nil {
		return childSpec{}, err
	}
	return childSpec{
		Role: role, Key: token, Socket: w.socket,
		Journal: filepath.Join(w.target.Dir, api.JournalFileName),
	}, nil
}

// newWorker builds a child's worker from what is on disk.
func (c *child) newWorker() (*worker.Worker, *sqlite.Journal, func(), error) {
	journal, err := sqlite.OpenJournal(c.spec.Journal)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := journal.Migrate(c.ctx); err != nil {
		return nil, nil, nil, err
	}
	client := api.NewUnixClient(c.spec.Socket)
	subject := worker.New(workerlink.New(client), journal, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Manifest:  contract.UsableManifest,
	})
	return subject, journal, func() {
		client.Close()
		_ = journal.Close()
	}, nil
}

// splitLaneEnd names the variant that stores a lane's end and its last event in two
// writes, so a kill between them leaves a lane that reads ended with no outcome.
const (
	splitLaneEnd  = "a lane's end and its last event in two writes"
	laneSplitStep = "split"
)

type splitJournal struct {
	ports.Journal
}

func (j splitJournal) EndLane(ctx context.Context, id string, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	record, err := j.Journal.Lane(ctx, id)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	record.State, record.TokenDigest = lanes.StateEnded, ""
	if err := j.Journal.SaveLane(ctx, record); err != nil {
		return ledger.UploadEvent{}, err
	}
	crashpoint.Hit("worker.lane", laneSplitStep)
	return j.Journal.Append(ctx, event)
}

// laneScript is one session that reports completed and ends its turn.
func laneScript(journal ports.Journal, ctx context.Context) fake.DriverScript {
	report := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		raw, err := json.Marshal(workorders.Report{Outcome: workorders.OutcomeCompleted})
		if err == nil {
			_, err = journal.SaveReport(ctx, spec.Lane, raw)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
		}
	}}
	return fake.DriverScript{SessionID: "session-1", Model: "fixture", Turns: [][]fake.DriverStep{{
		{Bound: true}, report,
		{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.01}},
	}}}
}

// roleWorkerLane runs one lane through a real worker on a fake driver. The claim and
// the dispatch are safe to repeat, so a retry reaches the same lane.
func roleWorkerLane(c *child) error {
	var opened ports.Journal
	journal, err := sqlite.OpenJournal(c.spec.Journal)
	if err != nil {
		return err
	}
	defer journal.Close()
	if err := journal.Migrate(c.ctx); err != nil {
		return err
	}
	opened = journal
	if c.spec.Variant == splitLaneEnd {
		opened = splitJournal{Journal: journal}
	}
	client := api.NewUnixClient(c.spec.Socket)
	defer client.Close()
	driver := fake.NewAgentDriver()
	driver.Play(laneScript(journal, c.ctx))
	worktrees := fake.NewWorktrees()
	subject := worker.New(workerlink.New(client), opened, clock.System{}, worker.Config{
		Name:      "child",
		JoinToken: func(context.Context) (string, error) { return c.spec.Key, nil },
		Report:    func(err error) { fmt.Fprintln(os.Stderr, "worker:", err) },
		Lanes: &worker.LaneRuntime{
			Driver:      driver,
			Provisioner: worker.Provisioner{Profiles: fake.NewProfileSource(), Worktrees: worktrees},
			Secrets:     fake.SecretResolver{},
			Repository:  func(context.Context, string) (string, error) { return "/repository", nil },
		},
	})
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

// roleWorkerUpload journals one event once and uploads it. A retry finds the event
// in the journal, because a restart holds nothing else.
func roleWorkerUpload(c *child) error {
	subject, journal, closeAll, err := c.newWorker()
	if err != nil {
		return err
	}
	defer closeAll()
	if err := subject.Start(c.ctx); err != nil {
		return err
	}
	through, err := journal.Through(c.ctx)
	if err != nil {
		return err
	}
	pending, err := journal.Pending(c.ctx, 1)
	if err != nil {
		return err
	}
	if through == 0 && len(pending) == 0 {
		_, err := subject.Record(c.ctx, ledger.UploadEvent{
			Type: "note", WorkOrder: c.spec.WorkOrder, ClaimID: c.spec.ClaimID, Fence: c.spec.Fence, Payload: []byte("once"),
		})
		if err != nil {
			return err
		}
	}
	return subject.Upload(c.ctx)
}

// roleWorkerDaemon runs a worker until something stops it.
func roleWorkerDaemon(c *child) error {
	subject, _, closeAll, err := c.newWorker()
	if err != nil {
		return err
	}
	defer closeAll()
	if err := subject.Start(c.ctx); err != nil {
		return err
	}
	fmt.Println("AT ready " + subject.Auth().WorkerID)
	return subject.Run(c.ctx)
}

// rolePlaneServer serves the API from a child, so a kill lands on the control plane
// and not on the worker.
func rolePlaneServer(c *child) error {
	socket := c.spec.Socket
	listener, err := server.Listen(socket)
	if err != nil {
		return err
	}
	handler := server.New(server.Config{
		Scope: c.scope, Store: c.store, LeaseTTL: longTTL, HeartbeatMs: 15_000, Identity: fake.NewIdentityProvider(),
	})
	fmt.Println("AT ready")
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	return httpServer.Serve(listener)
}

func workerStartCase() crashCase {
	return crashCase{
		name:  "worker start",
		op:    "worker.start",
		steps: []string{"identified", "exchanged"},
		prepare: func(w *world) (childSpec, error) {
			w.serveOnce()
			holder, err := w.running(longTTL)
			if err != nil {
				return childSpec{}, err
			}
			spec, err := w.workerSpec("worker-upload")
			spec.WorkOrder, spec.ClaimID, spec.Fence = holder.WorkOrder, holder.ClaimID, holder.Fence
			return spec, err
		},
		settled: func(w *world, spec childSpec) error {
			workers, err := w.plane().List(w.rig.Ctx)
			if err != nil {
				return err
			}
			if len(workers) != 1 {
				return contract.Violatedf("%d workers after one registration", len(workers))
			}
			return countEvents(w, reportedEvent, spec.WorkOrder, 1)
		},
	}
}

// serveOnce serves the plane the first time a case in this world asks.
func (w *world) serveOnce() {
	if w.socket == "" {
		w.socket = w.servePlane(time.Hour, time.Minute)
	}
}

func uploadCase(op string, steps []string) crashCase {
	return crashCase{
		name:  op,
		op:    op,
		steps: steps,
		prepare: func(w *world) (childSpec, error) {
			w.serveOnce()
			holder, err := w.running(longTTL)
			if err != nil {
				return childSpec{}, err
			}
			spec, err := w.workerSpec("worker-upload")
			spec.WorkOrder, spec.ClaimID, spec.Fence = holder.WorkOrder, holder.ClaimID, holder.Fence
			return spec, err
		},
		settled: func(w *world, spec childSpec) error {
			return countEvents(w, reportedEvent, spec.WorkOrder, 1)
		},
	}
}

func planeOutage(t *testing.T, w *world, step string) error {
	t.Helper()
	socket := filepath.Join(w.target.Dir, api.ControlSocketName)
	holder, err := w.running(longTTL)
	if err != nil {
		return err
	}
	token, err := w.joinToken()
	if err != nil {
		return err
	}
	journal, err := fake.NewJournal()
	if err != nil {
		return err
	}
	defer journal.Close()
	ctx := w.rig.Ctx
	if err := journal.Migrate(ctx); err != nil {
		return err
	}
	client := api.NewUnixClient(socket)
	defer client.Close()
	subject := worker.New(workerlink.New(client), journal, clock.System{}, worker.Config{
		Name:      "parent",
		JoinToken: func(context.Context) (string, error) { return token, nil },
	})
	serve := func(killStep string) (*proc, error) {
		spec := w.spec(childSpec{Role: "plane-server", Socket: socket})
		if killStep != "" {
			spec.KillOp, spec.KillStep = "plane.upload", killStep
		}
		p, err := startChild(spec)
		if err != nil {
			return nil, err
		}
		select {
		case <-p.at:
		case <-p.exited:
			return nil, fmt.Errorf("the plane exited: %s", p.stderr.String())
		case <-time.After(30 * time.Second):
			return nil, errors.New("the plane never served")
		}
		return p, nil
	}
	first, err := serve(step)
	if err != nil {
		return err
	}
	defer first.sigkill()
	if err := subject.Start(ctx); err != nil {
		return err
	}
	if _, err := subject.Record(ctx, ledger.UploadEvent{
		Type: "note", WorkOrder: holder.WorkOrder, ClaimID: holder.ClaimID, Fence: holder.Fence, Payload: []byte("once"),
	}); err != nil {
		return err
	}
	if err := subject.Upload(ctx); err == nil {
		return fmt.Errorf("plane.upload/%s was never reached", step)
	}
	first.wait()
	if _, signaled := first.exitCode(); !signaled {
		return fmt.Errorf("the plane survived %s: %s", step, first.stderr.String())
	}
	second, err := serve("")
	if err != nil {
		return err
	}
	defer second.sigkill()
	if err := subject.Upload(ctx); err != nil {
		return err
	}
	if err := countEvents(w, reportedEvent, holder.WorkOrder, 1); err != nil {
		return err
	}
	return w.verify()
}

// TestKilledPlaneDuplicatesNothing kills the control plane at every step of storing
// one event, so the answer is lost after the commit as well as before it.
func TestKilledPlaneDuplicatesNothing(t *testing.T) {
	for _, step := range []string{"begun", "opened", fencedStep, "before-commit", "committed"} {
		t.Run(step, func(t *testing.T) {
			if err := planeOutage(t, newWorld(t, ""), step); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// sleepTiming is short enough for a test and long enough that a beat lands inside
// it on a loaded machine. Neither value is a budget: a late beat lapses the lease
// earlier and the test still expects the return.
const (
	sleepHeartbeat = 40 * time.Millisecond
	sleepLease     = 500 * time.Millisecond
	sleepWait      = 30 * time.Second
)

// checkSleep stops a real worker process past its lease, while the control plane
// keeps running, and expects the lease back under the same fence. A laptop that
// sleeps reaches this path, and the lapse is certain here, because nothing can renew
// a lease while its worker is stopped.
func checkSleep(w *world, started bool) error {
	w.socket = w.servePlane(sleepHeartbeat, sleepLease)
	spec, err := w.workerSpec("worker-daemon")
	if err != nil {
		return err
	}
	child, err := startChild(w.spec(spec))
	if err != nil {
		return err
	}
	defer child.sigkill()
	var workerID string
	select {
	case at := <-child.at:
		workerID = strings.TrimPrefix(at, "ready ")
	case <-child.exited:
		return fmt.Errorf("the worker exited: %s", child.stderr.String())
	case <-time.After(sleepWait):
		return errors.New("the worker never started")
	}
	ops := w.rig.NewOps(w.store)
	id, err := w.rig.Ready(w.store)
	if err != nil {
		return err
	}
	grant, err := ops.Claim(w.rig.Ctx, w.rig.Scope, workorders.ClaimRequest{
		WorkOrder: id, ClaimID: w.rig.ID("sleeper"), Worker: workerID, TTLMs: sleepLease.Milliseconds(),
	}, w.rig.Actor)
	if err != nil {
		return err
	}
	want, event := workorders.Claimed, workorders.TriggerReturnedUnstarted
	if started {
		if _, err := ops.StartLane(w.rig.Ctx, w.rig.Scope, grant.Lease, w.rig.ID("lane"), w.rig.Actor); err != nil {
			return err
		}
		want, event = workorders.Running, workorders.TriggerReturnedStarted
	}
	first, err := w.rig.WorkOrder(w.store, id)
	if err != nil {
		return err
	}
	if err := until("the worker's first renewal", func() (bool, error) {
		row, err := w.rig.WorkOrder(w.store, id)
		return row.LeaseUntil > first.LeaseUntil, err
	}); err != nil {
		return err
	}
	if err := child.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		return err
	}
	if err := until("the lapse", func() (bool, error) {
		if _, err := ops.Sweep(w.rig.Ctx, w.rig.Scope, w.rig.Actor); err != nil {
			return false, err
		}
		row, err := w.rig.WorkOrder(w.store, id)
		return row.State == workorders.Lapsed, err
	}); err != nil {
		return err
	}
	if err := child.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		return err
	}
	returned := workorders.EventType(event)
	err = until("the return", func() (bool, error) {
		seen, err := countOf(w, returned, id)
		return seen >= 1, err
	})
	if err != nil {
		return contract.Violatedf("a worker with the current fence did not return: %v", err)
	}
	row, err := w.rig.WorkOrder(w.store, id)
	if err != nil {
		return err
	}
	if row.Fence != grant.Fence || row.ClaimID != grant.ClaimID || row.State != want {
		return contract.Violatedf("after the return %+v", row)
	}
	child.sigkill()
	return w.verify()
}

func countOf(w *world, eventType, subject string) (int, error) {
	events, err := w.rig.Events(w.store, w.rig.Scope)
	if err != nil {
		return 0, err
	}
	seen := 0
	for _, e := range events {
		if e.Type == eventType && e.Subject.ID == subject {
			seen++
		}
	}
	return seen, nil
}

func until(what string, done func() (bool, error)) error {
	deadline := time.Now().Add(sleepWait)
	for time.Now().Before(deadline) {
		ok, err := done()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("%s did not happen", what)
}

func TestSleepReturnsFromLapsed(t *testing.T) {
	for name, started := range map[string]bool{"before its lane started": false, "with its lane running": true} {
		t.Run(name, func(t *testing.T) {
			if err := checkSleep(newWorld(t, ""), started); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// checkSoloSleep stops the whole of a real ropeshiftd past its lease, as a closed
// laptop does. The lease tick and the heartbeat resume together, so the sweep is held
// back and the heartbeat comes first, which is the renewal the deadline refuses. A
// lapse goes on record, and the same holder returns under the same fence. A late beat
// on a loaded machine can add a lapse and its return, and changes nothing else.
func checkSoloSleep(w *world, sabotage ...string) error {
	binary := filepath.Join(w.target.Dir, "ropeshiftd")
	build := exec.Command("go", "build", "-tags", "chaos", "-o", binary, "ropeshift.dev/ropeshift/cmd/ropeshiftd")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build: %v\n%s", err, out)
	}
	paths := api.Paths{StateDir: filepath.Join(w.target.Dir, "state"), RunDir: filepath.Join(w.target.Dir, "run")}
	daemon := exec.Command(binary, "solo", "--heartbeat", "40ms", "--lease-ttl", "500ms", "--lease-tick", "40ms")
	daemon.Env = append([]string{
		"HOME=" + w.target.Dir, "PATH=" + doubles.InstallFakeClaude(w.t, "no-session"),
		api.EnvStateDir + "=" + paths.StateDir, api.EnvRunDir + "=" + paths.RunDir, api.EnvWeb + "=" + api.WebOff,
		crashpoint.EnvPause + "=sweep/candidates-read=150ms",
	}, sabotage...)
	if err := daemon.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- daemon.Wait() }()
	defer func() {
		_ = daemon.Process.Signal(syscall.SIGCONT)
		_ = daemon.Process.Kill()
		<-exited
	}()
	client := api.NewUnixClient(paths.ControlSocket())
	defer client.Close()
	ctx := w.rig.Ctx
	err := until("the worker's registration", func() (bool, error) {
		workers, err := client.Workers(ctx)
		return err == nil && len(workers) == 1 && workers[0].Epoch >= 1, nil
	})
	if err != nil {
		return err
	}
	workers, err := client.Workers(ctx)
	if err != nil {
		return err
	}
	if _, err := apitest.SignIn(ctx, client); err != nil {
		return err
	}
	if _, err := client.CreateWorkOrder(ctx, api.CreateWorkOrder{ID: "wo", Title: "T", Criteria: criterion()}); err != nil {
		return err
	}
	if _, err := client.Review(ctx, "wo"); err != nil {
		return err
	}
	grant, err := client.Claim(ctx, "wo", api.ClaimRequest{ClaimID: "c1", WorkerID: workers[0].ID})
	if err != nil {
		return err
	}
	first, err := client.WorkOrder(ctx, "wo")
	if err != nil {
		return err
	}
	err = until("the worker's first renewal", func() (bool, error) {
		row, err := client.WorkOrder(ctx, "wo")
		return err == nil && row.LeaseUntil > first.LeaseUntil, nil
	})
	if err != nil {
		return err
	}
	if err := daemon.Process.Signal(syscall.SIGSTOP); err != nil {
		return err
	}
	time.Sleep(time.Second)
	resumed := time.Now().UnixMilli()
	if err := daemon.Process.Signal(syscall.SIGCONT); err != nil {
		return err
	}
	err = until("a lease held after the wake", func() (bool, error) {
		row, err := client.WorkOrder(ctx, "wo")
		return err == nil && row.State == workorders.Claimed && row.LeaseUntil > resumed, nil
	})
	if err != nil {
		return err
	}
	row, err := client.WorkOrder(ctx, "wo")
	if err != nil {
		return err
	}
	if row.Fence != grant.Fence || row.ClaimID != grant.ClaimID {
		return contract.Violatedf("after the sleep %+v", row)
	}
	_ = daemon.Process.Kill()
	<-exited
	exited <- nil
	return checkLapsesReturned(ctx, paths)
}

// checkLapsesReturned reads the ledger of a stopped daemon. Lapses and returns must
// alternate from a lapse, and at least one of each must be on record. The kill can
// land between a late lapse and its return.
func checkLapsesReturned(ctx context.Context, paths api.Paths) error {
	store, err := sqlite.Open(filepath.Join(paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		return err
	}
	defer store.Close()
	lapses, returns := 0, 0
	err = store.Read(ctx, "default", func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, e := range events {
			switch e.Type {
			case workorders.EventType(workorders.TriggerLeaseExpired):
				if lapses != returns {
					return contract.Violatedf("a second lapse with no return at revision %d", e.Rev)
				}
				lapses++
			case workorders.EventType(workorders.TriggerReturnedUnstarted):
				if returns+1 != lapses {
					return contract.Violatedf("a return with no lapse at revision %d", e.Rev)
				}
				returns++
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	if returns == 0 {
		return contract.Violatedf("a sleep past the lease left %d lapses and %d returns on record", lapses, returns)
	}
	return nil
}

func TestSoloSleepLapsesAndReturns(t *testing.T) {
	if err := checkSoloSleep(newWorld(t, "")); err != nil {
		t.Fatal(err)
	}
}

func criterion() []planning.Criterion {
	return []planning.Criterion{{ID: "c1", Text: "It works"}}
}

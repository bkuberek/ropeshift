package worker_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// heldWork is what the ledger and the journal hold of the work a daemon finds as it starts: every work order, how many
// events the ledger holds of anything but the worker itself, each journaled lane with its generations and its charges,
// the events the journal has not sent, each conversation a member asked to run, and how many sessions the driver
// started, resumed, or adopted. A worker's own events, such as the opening of its epoch, are no work.
type heldWork struct {
	orders        []workorders.WorkOrder
	events        int
	lanes         []lanes.Record
	generations   map[string][]ledger.Generation
	charges       map[string][]lanes.Restart
	unsent        []ledger.UploadEvent
	conversations []conversations.Conversation
	sessions      int
}

func (r *laneRig) heldWork(t *testing.T) heldWork {
	t.Helper()
	ctx := t.Context()
	held := heldWork{generations: map[string][]ledger.Generation{}, charges: map[string][]lanes.Restart{}}
	var err error
	if held.lanes, err = r.journal.Lanes(ctx); err != nil {
		t.Fatal(err)
	}
	if held.unsent, err = r.journal.Pending(ctx, 10_000); err != nil {
		t.Fatal(err)
	}
	for _, lane := range held.lanes {
		if held.charges[lane.ID], err = r.journal.Restarts(ctx, lane.ID); err != nil {
			t.Fatal(err)
		}
	}
	err = r.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		if held.orders, err = tx.WorkOrders().List(); err != nil {
			return err
		}
		events, err := control.ReadAllEvents(tx.Events)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Subject.ID != r.subject.Auth().WorkerID {
				held.events++
			}
		}
		if held.conversations, err = tx.Conversations().Running(); err != nil {
			return err
		}
		for _, lane := range held.lanes {
			if held.generations[lane.ID], err = tx.Lanes().Generations(lane.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	held.sessions = len(r.driver.Specs()) + len(r.driver.Resumed()) + len(r.driver.Adopted())
	return held
}

// sameAs fails the test for each part of the held work that differs from what was held before.
func (h heldWork) sameAs(t *testing.T, before heldWork) {
	t.Helper()
	for _, part := range []struct {
		name          string
		before, after any
	}{
		{"work orders", before.orders, h.orders},
		{"ledger events", before.events, h.events},
		{"journaled lanes", before.lanes, h.lanes},
		{"generations", before.generations, h.generations},
		{"charges", before.charges, h.charges},
		{"unsent events", len(before.unsent), len(h.unsent)},
		{"conversations", before.conversations, h.conversations},
		{"sessions", before.sessions, h.sessions},
	} {
		if !reflect.DeepEqual(part.before, part.after) {
			t.Errorf("the %s changed:\nbefore %+v\nafter  %+v", part.name, part.before, part.after)
		}
	}
}

// daemonOn is the worker of a new process of a daemon on this rig's journal, control plane, and driver, as a restart
// of the installation's own daemon makes one, or as the daemon `ropeshift init` starts makes one when initDaemon is set.
func (r *laneRig) daemonOn(t *testing.T, initDaemon bool) *worker.Worker {
	t.Helper()
	config := *r.config
	config.NoProbe = initDaemon
	return worker.New(r.link, r.journal, r.clock, worker.Config{
		Name:      "test",
		JoinToken: func(ctx context.Context) (string, error) { return r.plane.CreateJoinToken(ctx, member) },
		Lanes:     &config,
		Spool:     filepath.Join(r.dir, "spool"),
		Telemetry: r.telemetry,
		Report:    r.noteProblem,
		Notice:    r.noteNotice,
	})
}

// initDaemonRun runs the worker of the daemon `ropeshift init` starts until its epoch is open, and then for as many
// heartbeats as a worker that takes work needs to take what the journal and the ledger hold, and stops it.
func (r *laneRig) initDaemonRun(t *testing.T) {
	t.Helper()
	before := r.subject.Session().Epoch
	subject := r.daemonOn(t, true)
	r.runUntil(t, subject, false, func() bool { return subject.Session().Epoch > before })
	if got := subject.Session().Epoch; got <= before {
		t.Fatalf("the init daemon's worker opened epoch %d, after %d", got, before)
	}
}

// daemonRun runs the worker of the installation's own daemon until done says so, and stops it.
func (r *laneRig) daemonRun(t *testing.T, done func() bool) {
	t.Helper()
	r.runUntil(t, r.daemonOn(t, false), true, done)
}

// runUntil runs a worker's loop until done says so, and for the time of 25 heartbeats more, and then stops it as a
// daemon's stop does. stopSessions is false for the init daemon, which starts no session: the driver is shared with the
// installation's own daemon that runs next, and a stopped driver stops that daemon's sessions as they open.
func (r *laneRig) runUntil(t *testing.T, subject *worker.Worker, stopSessions bool, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ran := make(chan error, 1)
	go func() { ran <- subject.Run(ctx) }()
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("a daemon's worker never took the work up: the work order reads %s, %d sessions started, problems %v",
				r.row().State, len(r.driver.Specs()), r.problems())
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(25 * time.Duration(subject.Session().HeartbeatMs) * time.Millisecond)
	cancel()
	if err := <-ran; !errors.Is(err, context.Canceled) {
		t.Errorf("the worker's loop ended with %v", err)
	}
	if stopSessions {
		r.driver.StopEverySession()
	}
	waited := make(chan struct{})
	go func() {
		subject.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatalf("the worker still runs %v", subject.Unfinished())
	}
}

// startedOnce says the driver started one session, the first generation of the rig's lane.
func startedOnce(r *laneRig) bool {
	specs := r.driver.Specs()
	return len(specs) == 1 && specs[0].Lane == "lane-1" && specs[0].Generation == 1
}

// A later `ropeshift init`, run while the installation's own daemon is stopped, starts a daemon of its own on the same
// journal and ledger. That daemon qualifies no agent CLI and starts no session, so a worker of it that acted on the
// work it found would end each piece of it as refused: blocked, which only a member or the orchestrator undoes, or a
// next generation charged and never run. It takes none: every claim, woken lane, lane whose session is gone, and
// conversation stays as it was, and the installation's own daemon takes each of them up as it always does.
func TestTheDaemonInitStartsTakesNoWork(t *testing.T) {
	for _, tt := range []struct {
		name string
		// seed lays the work out as the installation's own daemon left it when it was stopped.
		seed func(t *testing.T) *laneRig
		// takenUp says the installation's own daemon took the work up.
		takenUp func(r *laneRig) bool
	}{
		{
			name: "a claim not yet started",
			seed: func(t *testing.T) *laneRig {
				r := newLaneRig(t)
				r.driver.Play(atRest())
				r.subject.Wait()
				return r
			},
			takenUp: func(r *laneRig) bool { return startedOnce(r) && r.row().State == workorders.Running },
		},
		{
			name:    "a parked lane whose wake fired",
			seed:    func(t *testing.T) *laneRig { return parkedAndWoken(t, notCarried, nil).laneRig },
			takenUp: func(r *laneRig) bool { return len(r.driver.Resumed()) == 1 && len(r.generations(r.t)) == 2 },
		},
		{
			name: "a running lane whose session is gone",
			seed: func(t *testing.T) *laneRig {
				r := laneRigWith(t, handoffProfile)
				r.driver.Play(atRest())
				crashBeforeSession(t, r, lanes.Pending)
				r.subject.Wait()
				return r
			},
			takenUp: func(r *laneRig) bool { return startedOnce(r) && r.row().State == workorders.Running },
		},
		{
			name: "an open assistant conversation",
			seed: func(t *testing.T) *laneRig {
				a := newIdleAssistantRig(t, newRig(t), aSession())
				if _, err := a.convs.Create(t.Context(), scope, control.NewConversation{ID: "conv-1"}, member); err != nil {
					t.Fatal(err)
				}
				a.subject.Wait()
				return a.laneRig
			},
			takenUp: func(r *laneRig) bool {
				for _, spec := range r.driver.Specs() {
					if spec.Assistant {
						return true
					}
				}
				return false
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.seed(t)
			before := r.heldWork(t)
			r.initDaemonRun(t)
			r.heldWork(t).sameAs(t, before)
			if n := r.refusals(""); n != 0 {
				t.Errorf("the init daemon's worker refused %d lanes, from %v", n, r.problems())
			}
			r.daemonRun(t, func() bool { return tt.takenUp(r) })
		})
	}
}

// closeOnlyListener is a listener that records its close and takes no connection until then.
type closeOnlyListener struct {
	net.Listener
	once     sync.Once
	closed   chan struct{}
	accepted chan struct{}
}

func newCloseOnlyListener() *closeOnlyListener {
	return &closeOnlyListener{closed: make(chan struct{}), accepted: make(chan struct{}, 1)}
}

func (l *closeOnlyListener) Accept() (net.Conn, error) {
	select {
	case l.accepted <- struct{}{}:
	default:
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *closeOnlyListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

// The worker of the daemon `ropeshift init` starts answers no lane on its socket. A session the installation's own
// daemon left running reaches its worker there, and an answer would carry its reports, its tools, and its
// consultations into a worker that takes no work. With the socket closed, a hook spools what it carries, as it does
// while no daemon runs, for the installation's own daemon to read.
func TestTheDaemonInitStartsAnswersNoLane(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	subject := r.daemonOn(t, true)
	listener := newCloseOnlyListener()
	served := make(chan error, 1)
	go func() { served <- subject.ServeLanes(t.Context(), listener) }()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("the socket ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = listener.Close()
		t.Fatal("the init daemon's worker serves its socket")
	}
	select {
	case <-listener.closed:
	default:
		t.Error("the init daemon's worker left its socket open")
	}
	select {
	case <-listener.accepted:
		t.Error("the init daemon's worker took a connection on its socket")
	default:
	}
}

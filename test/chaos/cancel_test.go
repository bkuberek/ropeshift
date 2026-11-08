//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

const (
	// laneCanceled has a member cancel the work while its session runs, and no beat tells the worker.
	laneCanceled = "canceled"
	// sabotageKeepsLost stops a lane only when a beat says so.
	sabotageKeepsLost = "a lane no desired state names runs on"
	// canceledBound is how long a child passes before it says a lane ran on. It sits under the bound
	// a parent gives a child, so a lane that ran on reads as that and not as a child that hung.
	canceledBound = 20 * time.Second
)

var canceledEvent = workorders.EventType(workorders.TriggerCanceled)

func roleCancel(c *child) error {
	return c.ops.Cancel(c.ctx, c.scope, c.spec.WorkOrder, "crash matrix", c.member())
}

// cancelsWork is a member's cancel landing where a lane's session has reached its work.
func (c *child) cancelsWork() func(ports.SessionSpec) {
	return func(ports.SessionSpec) {
		if err := control.NewLeases(c.store).Cancel(c.ctx, c.scope, c.spec.WorkOrder, "crash matrix", c.member()); err != nil {
			fmt.Fprintln(os.Stderr, "canceling the work:", err)
		}
	}
}

// passUntilEnded runs what a daemon's loop runs after a hint, with no beat, because a beat that names
// a lost lease is the one way a worker heard before. A kill on this path repeats it from the journal.
func (c *child) passUntilEnded(subject *worker.Worker, lane func(context.Context) (bool, error)) error {
	deadline := time.Now().Add(canceledBound)
	for time.Now().Before(deadline) {
		if err := subject.Sync(c.ctx); err != nil {
			return err
		}
		if _, err := subject.Reconcile(c.ctx); err != nil {
			return err
		}
		if err := subject.Upload(c.ctx); err != nil {
			return err
		}
		ended, err := lane(c.ctx)
		if err != nil {
			return err
		}
		if ended {
			subject.Wait()
			return subject.Upload(c.ctx)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("a lane of canceled work ran on")
}

func cancelSettled(w *world, spec childSpec) error {
	if err := countEvents(w, canceledEvent, spec.WorkOrder, 1); err != nil {
		return err
	}
	return hasState(w, spec.WorkOrder, workorders.Canceled)
}

func (w *world) claimEnded(id string) error {
	return w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		claim, err := tx.Claims().Find(id)
		if err != nil {
			return err
		}
		if !claim.Ended() || claim.EndReason != workorders.EndCanceled {
			return contract.Violatedf("the canceled claim is %+v", claim)
		}
		return nil
	})
}

// cancelHeldCase kills a cancel of work a lane holds. Its lane has a process, so it stays as it was
// reported, and nothing about its claim may survive.
func cancelHeldCase(name string, held func(*world) (workorders.Holder, error)) crashCase {
	return crashCase{
		name: name, op: "cancel", steps: withSteps("moved", "lanes-ended"),
		prepare: func(w *world) (childSpec, error) {
			holder, err := held(w)
			return holderSpec("cancel", holder), err
		},
		settled: func(w *world, spec childSpec) error {
			if err := cancelSettled(w, spec); err != nil {
				return err
			}
			return w.claimEnded(spec.ClaimID)
		},
	}
}

// cancelParkedCase kills a cancel of a parked lane, whose end the same transaction records.
func cancelParkedCase() crashCase {
	return crashCase{
		name: "cancel a parked lane", op: "cancel", steps: withSteps("moved", "lanes-ended"),
		prepare: func(w *world) (childSpec, error) {
			request, err := w.rig.Parked(w.store, `{"human_resume":true}`)
			spec := holderSpec("cancel", request.Holder)
			spec.Key = request.Wake
			return spec, err
		},
		settled: func(w *world, spec childSpec) error {
			if err := cancelSettled(w, spec); err != nil {
				return err
			}
			if err := countEvents(w, ledger.EventLaneEnded, spec.Lane, 1); err != nil {
				return err
			}
			view, err := w.laneView(spec.Lane)
			if err != nil {
				return err
			}
			if view.ActualState != ledger.StateEnded {
				return contract.Violatedf("a canceled parked lane reads %s", view.ActualState)
			}
			return w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
				wake, err := tx.Wakes().Get(spec.Key)
				if err != nil {
					return err
				}
				if wake.FiredBy != parking.RetiredCanceled {
					return contract.Violatedf("the wake reads %+v", wake)
				}
				return nil
			})
		},
	}
}

// canceledLaneCase kills a worker while it stops a lane a member canceled. What the session wrote
// goes to a salvage ref, and the lane's end reaches the ledger after however many restarts.
func canceledLaneCase(op string, steps ...string) crashCase {
	return crashCase{
		name: "a canceled lane at " + op, op: op, steps: steps,
		prepare: func(w *world) (childSpec, error) { return stopSpec(w, true, laneCanceled, "") },
		settled: canceledLaneSettled,
	}
}

func canceledLaneSettled(w *world, spec childSpec) error {
	if err := fencedSettled(w, spec); err != nil {
		return err
	}
	if err := cancelSettled(w, spec); err != nil {
		return err
	}
	view, err := w.laneView(spec.Lane)
	if err != nil {
		return err
	}
	if view.ActualState != ledger.StateEnded {
		return contract.Violatedf("a canceled lane reads %s", view.ActualState)
	}
	last := view.Generations[len(view.Generations)-1]
	if last.EndReason != workorders.ReasonCanceled {
		return contract.Violatedf("its last generation is %+v", last)
	}
	return nothingHeld(spec)
}

func cancelCases() []crashCase {
	const surveyed, pushed, snapshotted, snapshotPushed, saved = "surveyed", "pushed", "snapshotted", "snapshot-pushed", "saved"
	return []crashCase{
		cancelHeldCase("cancel running work", func(w *world) (workorders.Holder, error) { return w.running(longTTL) }),
		cancelHeldCase("cancel lapsed work", func(w *world) (workorders.Holder, error) { return w.lapsed() }),
		cancelParkedCase(),
		canceledLaneCase("worker.guard", surveyed, pushed, snapshotted, snapshotPushed, saved),
		canceledLaneCase("worker.lane", "ended"),
		canceledLaneCase("worker.upload", "read", "answered", "acknowledged"),
	}
}

// checkCanceledStops runs a canceled lane on a real repository with no kill, for the variant that
// would leave it running. A child that hit its bound says so, which is the violation.
func checkCanceledStops(w *world) error {
	spec, err := stopSpec(w, true, laneCanceled, "")
	if err != nil {
		return err
	}
	code, signaled, stderr, err := runToEnd(w.spec(spec))
	if err != nil {
		return err
	}
	if signaled || code != exitDone {
		return contract.Violatedf("a canceled lane ran on: exit %d: %s", code, stderr)
	}
	return canceledLaneSettled(w, spec)
}

func TestACanceledLaneStopsWithNoBeat(t *testing.T) {
	if err := checkCanceledStops(newWorld(t, "")); err != nil {
		t.Fatal(err)
	}
}

// splitCancel moves work to `canceled` in one transaction and says so in another, so a kill between
// them leaves work the log says still runs.
type splitCancel struct {
	contract.Ops
	store ports.Store
}

func (s splitCancel) Cancel(ctx context.Context, scope ledger.WorkspaceID, id, reason string, actor ledger.Actor) error {
	var fence int64
	err := s.store.Write(ctx, scope, "cancel", func(tx ports.WriteTx) error {
		current, err := tx.WorkOrders().Get(id)
		if err != nil || current.State == workorders.Canceled {
			return err
		}
		fence = current.Fence + 1
		if _, err := tx.Leases().Cancel(id, current.State, current.Fence, true); err != nil {
			return err
		}
		_, err = tx.Append(noteEvent(actor))
		return err
	})
	if err != nil {
		return err
	}
	crashpoint.Hit("cancel", "split")
	return s.store.Write(ctx, scope, "cancel", func(tx ports.WriteTx) error {
		event, err := workorders.ChangeEvent(canceledEvent, id, workorders.Change{Trigger: workorders.TriggerCanceled, Fence: fence, Reason: reason}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

// anyoneCancels takes the check off, so a lane or an orchestrator can end work.
type anyoneCancels struct{ contract.Ops }

func (a anyoneCancels) Cancel(ctx context.Context, scope ledger.WorkspaceID, id, reason string, _ ledger.Actor) error {
	return a.Ops.Cancel(ctx, scope, id, reason, ledger.Actor{Kind: ledger.ActorMember, ID: "anyone"})
}

func cancelVariants() []variant {
	return []variant{
		{
			name:     "a cancel leaves the fence where it was",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.cancel", "fence = fence + CAST(:moved AS BIGINT),", "fence = fence + 0 * CAST(:moved AS BIGINT),")},
			check:    "CN2",
		},
		{
			name:     "a cancel leaves the holder on the row",
			sabotage: sqlstore.Sabotage{Rewrite: replace("lease.cancel", "lease_worker = NULL,", "")},
			check:    "CN1",
		},
		{name: "a cancel leaves a parked lane parked", control: control.Sabotage{LeavesParkedLanes: true}, check: "CN7"},
		{name: "a cancel leaves a wake armed", control: control.Sabotage{KeepsWakesOpen: true}, check: "CN7"},
		{name: "a cancel leaves messages waiting", control: control.Sabotage{LeavesMessages: true}, check: "CN9"},
		{name: "any worker ends a canceled lane", control: control.Sabotage{EndsAnyCanceledLane: true}, check: "CN10"},
		{
			name:  "anyone cancels work",
			ops:   func(s ports.Store) contract.Ops { return anyoneCancels{contract.RealOps(s)} },
			check: "CN6",
		},
		{
			name:  "a cancel and its event in two transactions",
			ops:   func(s ports.Store) contract.Ops { return splitCancel{Ops: contract.RealOps(s), store: s} },
			check: "crash",
			crash: func() (crashCase, string) {
				return cancelHeldCase("cancel running work", func(w *world) (workorders.Holder, error) { return w.running(longTTL) }), "split"
			},
		},
		{name: sabotageKeepsLost, check: "canceled"},
	}
}

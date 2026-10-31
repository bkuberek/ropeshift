//go:build chaos

package chaos

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

var childRoles = map[string]func(*child) error{
	"note":              roleNote,
	"create-work-order": roleCreateWorkOrder,
	"claim":             roleClaim,
	"renew":             roleRenew,
	"sweep":             roleSweep,
	"return":            roleReturn,
	"release":           roleRelease,
	"reassign":          roleReassign,
	"lane-start":        roleLaneStart,
	"fenced-write":      roleFencedWrite,
	"handoff":           roleHandoff,
	"lease-worker":      roleLeaseWorker,
	"zombie-writer":     roleZombieWriter,
	"intake":            roleIntake,
	"review-pass":       roleReviewPass,
	"record-work":       roleRecordWork,
	"worker-upload":     roleWorkerUpload,
	"worker-lane":       roleWorkerLane,
	"dispatch":          roleDispatch,
	"end-lane":          roleEndLane,
	"worker-daemon":     roleWorkerDaemon,
	"plane-server":      rolePlaneServer,
	"worktree-create":   roleWorktreeCreate,
}

func roleRecordWork(c *child) error {
	_, err := c.ops.Record(c.ctx, c.scope, control.LedgerWork{
		Order: workorders.WorkOrder{ID: c.spec.WorkOrder, Kind: workorders.KindChange, Title: "from a child"},
	}, c.actor)
	return err
}

// roleIntake polls one contract file. The source is rebuilt on every start, because
// a restart holds nothing from an earlier process.
func roleIntake(c *child) error {
	source := fake.NewWorkSource()
	source.Write("contract.md", []byte(contract.ContractText(c.spec.Key, "Alpha", "One.")))
	_, err := c.ops.Poll(c.ctx, c.scope, source)
	return err
}

func roleReviewPass(c *child) error {
	_, err := c.ops.Pass(c.ctx, c.scope, c.spec.WorkOrder, c.member())
	return err
}

func roleNote(c *child) error {
	return c.store.Write(c.ctx, c.scope, "note", func(tx ports.WriteTx) error {
		_, err := tx.Append(ledger.EventDraft{
			Type:    "note.added",
			Actor:   c.actor,
			Subject: ledger.Subject{Kind: "note", ID: "note"},
		})
		return err
	})
}

func roleCreateWorkOrder(c *child) error {
	id := c.spec.WorkOrder
	if err := c.attempted("create", id, id, 0); err != nil {
		return err
	}
	_, err := c.ops.Create(c.ctx, c.scope, workorders.WorkOrder{ID: id, Kind: workorders.KindChange, Title: "from a child"}, c.actor)
	if err != nil {
		return err
	}
	return c.completed("create", id, id, 0, 0)
}

func roleClaim(c *child) error {
	request := workorders.ClaimRequest{
		WorkOrder: c.spec.WorkOrder, ClaimID: c.spec.ClaimID, Worker: c.spec.Worker, TTLMs: c.spec.TTLMs,
	}
	if err := c.attempted("claim", request.WorkOrder, request.ClaimID, 0); err != nil {
		return err
	}
	grant, err := c.ops.Claim(c.ctx, c.scope, request, c.actor)
	if err != nil {
		return err
	}
	return c.completed("claim", request.WorkOrder, request.ClaimID, grant.Fence, grant.Rev)
}

func roleRenew(c *child) error {
	return c.ops.Renew(c.ctx, c.scope, c.lease(), c.spec.TTLMs)
}

func roleSweep(c *child) error {
	_, err := c.ops.Sweep(c.ctx, c.scope, c.member())
	return err
}

func roleReturn(c *child) error {
	return c.ops.Return(c.ctx, c.scope, c.lease(), c.spec.TTLMs, c.actor)
}

// roleDispatch is safe to repeat, so a retry names the same lane.
func roleDispatch(c *child) error {
	_, err := c.ops.Dispatch(c.ctx, c.scope, c.spec.WorkOrder, c.spec.Lane, c.member())
	return err
}

// roleEndLane counts a retry that finds its lane already ended.
func roleEndLane(c *child) error {
	holder := workorders.Holder{
		Lease: c.lease(), Lane: c.spec.Lane, Generation: c.spec.Generation,
	}
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}}
	err := c.ops.EndLane(c.ctx, c.scope, holder, result, c.actor)
	var fenced *workorders.FencedError
	if errors.As(err, &fenced) && fenced.State == workorders.Acceptance {
		return nil
	}
	return err
}

func roleRelease(c *child) error {
	return c.ops.Release(c.ctx, c.scope, c.lease(), c.actor)
}

// roleReassign counts a retry that finds the work a first attempt finished.
func roleReassign(c *child) error {
	err := c.ops.Reassign(c.ctx, c.scope, c.spec.WorkOrder, "crash matrix", c.member())
	if errors.Is(err, workorders.ErrNotLapsed) {
		return nil
	}
	return err
}

func roleLaneStart(c *child) error {
	_, err := c.ops.StartLane(c.ctx, c.scope, c.lease(), c.spec.Lane, c.actor)
	return err
}

func roleFencedWrite(c *child) error {
	key := c.spec.Key
	if err := c.attempted("write", c.spec.WorkOrder, key, c.spec.Fence); err != nil {
		return err
	}
	event, err := c.ops.Write(c.ctx, c.scope, c.holder(), key, []byte(key), c.actor)
	if err != nil {
		return err
	}
	return c.completed("write", c.spec.WorkOrder, key, c.spec.Fence, event.Rev)
}

// roleHandoff counts a retry that finds the generation already moved.
func roleHandoff(c *child) error {
	_, err := c.ops.Handoff(c.ctx, c.scope, c.holder(), c.actor)
	var fenced *workorders.FencedError
	if !errors.As(err, &fenced) {
		return err
	}
	return c.store.Read(c.ctx, c.scope, func(tx ports.ReadTx) error {
		generation, err := tx.Lanes().Generation(c.spec.Lane)
		if err != nil {
			return err
		}
		if generation != c.spec.Generation+1 {
			return fmt.Errorf("lane generation %d after a handoff from %d", generation, c.spec.Generation)
		}
		return nil
	})
}

func (c *child) worker() string { return c.actor.ID }

func (c *child) sleep(limit time.Duration) {
	time.Sleep(time.Duration(c.rng.Int64N(int64(limit))))
}

func (c *child) newID() string {
	return fmt.Sprintf("child-%d-%d-%x", c.spec.Slot, c.spec.Spawn, c.rng.Uint64())
}

// currentClaim is what a worker remembers across a restart. A later unit keeps
// this in the journal.
type currentClaim struct {
	WorkOrder string `json:"work_order"`
	ClaimID   string `json:"claim_id"`
}

func (c *child) readCurrent() (currentClaim, bool) {
	raw, err := os.ReadFile(c.spec.Current)
	if err != nil {
		return currentClaim{}, false
	}
	var current currentClaim
	if err := json.Unmarshal(raw, &current); err != nil {
		return currentClaim{}, false
	}
	return current, true
}

func (c *child) writeCurrent(current currentClaim) error {
	raw, err := json.Marshal(current)
	if err != nil {
		return err
	}
	next := c.spec.Current + ".next"
	if err := os.WriteFile(next, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(next, c.spec.Current)
}

func (c *child) clearCurrent() error {
	err := os.Remove(c.spec.Current)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// roleLeaseWorker claims, writes under its fence, and walks away or releases. A
// restart either retries its claim with the same ID or abandons it, as a machine
// that never comes back would. It runs until a parent kills it.
func roleLeaseWorker(c *child) error {
	if current, ok := c.readCurrent(); ok {
		if c.rng.IntN(2) == 0 {
			if err := c.finishClaim(current); err != nil {
				return err
			}
		} else if err := c.clearCurrent(); err != nil {
			return err
		}
	}
	for {
		if err := c.maintain(); err != nil {
			return err
		}
		ready, err := c.readyWorkOrders()
		if err != nil {
			return err
		}
		if len(ready) == 0 {
			c.sleep(5 * time.Millisecond)
			continue
		}
		current := currentClaim{WorkOrder: ready[c.rng.IntN(len(ready))], ClaimID: c.newID()}
		if err := c.writeCurrent(current); err != nil {
			return err
		}
		if err := c.finishClaim(current); err != nil {
			return err
		}
	}
}

func (c *child) listWorkOrders() ([]workorders.WorkOrder, error) {
	var rows []workorders.WorkOrder
	err := c.store.Read(c.ctx, c.scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.WorkOrders().List()
		return err
	})
	return rows, err
}

func (c *child) readyWorkOrders() ([]string, error) {
	rows, err := c.listWorkOrders()
	if err != nil {
		return nil, err
	}
	var ready []string
	for _, row := range rows {
		if row.State == workorders.Ready {
			ready = append(ready, row.ID)
		}
	}
	return ready, nil
}

// maintain stands for the control plane's lease tick and a member resolving what
// lapsed.
func (c *child) maintain() error {
	if _, err := c.ops.Sweep(c.ctx, c.scope, c.member()); err != nil {
		return err
	}
	rows, err := c.listWorkOrders()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.State != workorders.Lapsed {
			continue
		}
		err := c.ops.Reassign(c.ctx, c.scope, row.ID, "maintain", c.member())
		if err != nil && !errors.Is(err, workorders.ErrNotLapsed) {
			return err
		}
	}
	return nil
}

func (c *child) finishClaim(current currentClaim) error {
	request := workorders.ClaimRequest{
		WorkOrder: current.WorkOrder, ClaimID: current.ClaimID, Worker: c.worker(), TTLMs: c.spec.TTLMs,
	}
	if err := c.attempted("claim", request.WorkOrder, request.ClaimID, 0); err != nil {
		return err
	}
	grant, err := c.ops.Claim(c.ctx, c.scope, request, c.actor)
	if errors.Is(err, workorders.ErrLost) || errors.Is(err, workorders.ErrIneligible) {
		if err := c.rejected("claim", request.WorkOrder, request.ClaimID, 0, "lost"); err != nil {
			return err
		}
		return c.clearCurrent()
	}
	if err != nil {
		return err
	}
	if err := c.completed("claim", request.WorkOrder, request.ClaimID, grant.Fence, grant.Rev); err != nil {
		return err
	}
	return c.work(grant)
}

func (c *child) work(grant workorders.Grant) error {
	lease := grant.Lease
	state, err := c.stateOf(lease.WorkOrder)
	if err != nil {
		return err
	}
	if state == workorders.Lapsed {
		if err := c.ops.Return(c.ctx, c.scope, lease, c.spec.TTLMs, c.actor); err != nil {
			return c.lostLease(lease, err)
		}
		if state, err = c.stateOf(lease.WorkOrder); err != nil {
			return err
		}
	}
	if state == workorders.Claimed && c.rng.IntN(4) == 0 {
		if err := c.ops.Release(c.ctx, c.scope, lease, c.actor); err != nil {
			return c.lostLease(lease, err)
		}
		return c.clearCurrent()
	}
	lane := "lane-" + lease.ClaimID
	generation, err := c.ops.StartLane(c.ctx, c.scope, lease, lane, c.actor)
	if err != nil {
		return c.lostLease(lease, err)
	}
	holder := workorders.Holder{Lease: lease, Lane: lane, Generation: generation}
	for range 2 + c.rng.IntN(6) {
		key := c.newID()
		if err := c.attempted("write", lease.WorkOrder, key, lease.Fence); err != nil {
			return err
		}
		event, err := c.ops.Write(c.ctx, c.scope, holder, key, []byte(key), c.actor)
		var fenced *workorders.FencedError
		if errors.As(err, &fenced) {
			if err := c.rejected("write", lease.WorkOrder, key, lease.Fence, "fenced"); err != nil {
				return err
			}
			return c.clearCurrent()
		}
		if err != nil {
			return err
		}
		if err := c.completed("write", lease.WorkOrder, key, lease.Fence, event.Rev); err != nil {
			return err
		}
		c.sleep(14 * time.Millisecond)
		if c.rng.IntN(3) == 0 {
			if err := c.ops.Renew(c.ctx, c.scope, lease, c.spec.TTLMs); err != nil {
				return c.lostLease(lease, err)
			}
		}
	}
	// A running work order has no release, so half the time this holder walks away,
	// and its lease expires and a member resolves it. The other half its lane fails,
	// which moves the fence and returns the work order to `ready`.
	if c.rng.IntN(2) == 0 {
		result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: workorders.ReasonCrashed}}
		if err := c.ops.EndLane(c.ctx, c.scope, holder, result, c.actor); err != nil {
			return c.lostLease(lease, err)
		}
	}
	return c.clearCurrent()
}

func (c *child) stateOf(id string) (workorders.State, error) {
	var state workorders.State
	err := c.store.Read(c.ctx, c.scope, func(tx ports.ReadTx) error {
		row, err := tx.WorkOrders().Get(id)
		state = row.State
		return err
	})
	return state, err
}

// lostLease is what FR-WO-5 will ask of a fenced worker: stop and report. Here
// that is one record in the witness.
func (c *child) lostLease(lease workorders.Lease, cause error) error {
	var fenced *workorders.FencedError
	expired := errors.Is(cause, workorders.ErrExpired) || errors.Is(cause, workorders.ErrLapsed)
	if !errors.As(cause, &fenced) && !expired {
		return cause
	}
	if err := c.rejected("lease", lease.WorkOrder, lease.ClaimID, lease.Fence, "salvage"); err != nil {
		return err
	}
	return c.clearCurrent()
}

// roleZombieWriter holds a lease, writes, tells the parent, and waits for a gate
// file. The parent stops it, hands the work order to a rival, opens the gate, and
// continues it, so its next write comes from a holder that still believes.
func roleZombieWriter(c *child) error {
	request := workorders.ClaimRequest{
		WorkOrder: c.spec.WorkOrder, ClaimID: c.newID(), Worker: c.worker(), TTLMs: c.spec.TTLMs,
	}
	if err := c.attempted("claim", request.WorkOrder, request.ClaimID, 0); err != nil {
		return err
	}
	grant, err := c.ops.Claim(c.ctx, c.scope, request, c.actor)
	if err != nil {
		return err
	}
	if err := c.completed("claim", request.WorkOrder, request.ClaimID, grant.Fence, grant.Rev); err != nil {
		return err
	}
	lane := "lane-" + request.ClaimID
	generation, err := c.ops.StartLane(c.ctx, c.scope, grant.Lease, lane, c.actor)
	if err != nil {
		return err
	}
	holder := workorders.Holder{Lease: grant.Lease, Lane: lane, Generation: generation}
	write := func() error {
		key := c.newID()
		if err := c.attempted("write", request.WorkOrder, key, grant.Fence); err != nil {
			return err
		}
		event, err := c.ops.Write(c.ctx, c.scope, holder, key, []byte(key), c.actor)
		var fenced *workorders.FencedError
		if errors.As(err, &fenced) {
			return c.rejected("write", request.WorkOrder, key, grant.Fence, "fenced")
		}
		if err != nil {
			return err
		}
		return c.completed("write", request.WorkOrder, key, grant.Fence, event.Rev)
	}
	for range 3 {
		if err := write(); err != nil {
			return err
		}
	}
	fmt.Println("AT pause")
	for {
		if _, err := os.Stat(c.spec.Gate); err == nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	return write()
}

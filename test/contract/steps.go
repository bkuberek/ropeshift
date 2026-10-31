package contract

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// The moments a check can stop at.
const (
	StepBefore   = "before"
	StepGuarded  = "guarded"
	StepEligible = "eligible"
)

// Stepper is a Store handle that can report a named moment. A broken variant uses
// it to stop where the real path stops.
type Stepper interface {
	Step(op, name string)
}

// Stepped wraps a Store so that step runs at each named moment. It proves a port
// is enough: no test switch has to live in the adapter.
func Stepped(store ports.Store, step func(op, name string)) ports.Store {
	return steppedStore{Store: store, step: step}
}

type steppedStore struct {
	ports.Store
	step func(op, name string)
}

func (s steppedStore) Step(op, name string) { s.step(op, name) }

func (s steppedStore) Write(ctx context.Context, scope ledger.WorkspaceID, op string, fn func(ports.WriteTx) error) error {
	s.step(op, StepBefore)
	return s.Store.Write(ctx, scope, op, func(tx ports.WriteTx) error {
		return fn(steppedTx{WriteTx: tx, op: op, step: s.step})
	})
}

type steppedTx struct {
	ports.WriteTx
	op   string
	step func(op, name string)
}

func (t steppedTx) Leases() ports.LeaseWriter {
	return steppedLeases{LeaseWriter: t.WriteTx.Leases(), op: t.op, step: t.step}
}

type steppedLeases struct {
	ports.LeaseWriter
	op   string
	step func(op, name string)
}

func (l steppedLeases) Guard(workOrder string, fence int64) (bool, error) {
	won, err := l.LeaseWriter.Guard(workOrder, fence)
	if err == nil {
		l.step(l.op, StepGuarded)
	}
	return won, err
}

func (l steppedLeases) WorkInProgress() (int64, error) {
	count, err := l.LeaseWriter.WorkInProgress()
	if err == nil {
		l.step(l.op, StepEligible)
	}
	return count, err
}

// barrier holds racers until all of them arrive or the wait ends, so they are all
// past a check before any of them acts.
type barrier struct {
	mu    sync.Mutex
	need  int
	count int
	open  chan struct{}
}

func newBarrier(need int) *barrier {
	return &barrier{need: need, open: make(chan struct{})}
}

func (b *barrier) wait(limit time.Duration) {
	b.mu.Lock()
	b.count++
	if b.count == b.need {
		close(b.open)
	}
	b.mu.Unlock()
	select {
	case <-b.open:
	case <-time.After(limit):
	}
}

// ErrStopped is what a stopped Store returns.
var ErrStopped = errors.New("stopped by a check")

type atomicCount struct {
	writes atomic.Int64
	off    atomic.Bool
}

func (c *atomicCount) disarm() { c.off.Store(true) }

// StopAfter wraps a Store so that writes of one operation fail after a count, which
// stands for a process that stopped between two tick items.
func StopAfter(store ports.Store, op string, allowed int64, count *atomicCount) ports.Store {
	return stoppingStore{Store: store, op: op, allowed: allowed, count: count}
}

type stoppingStore struct {
	ports.Store
	op      string
	allowed int64
	count   *atomicCount
}

func (s stoppingStore) Write(ctx context.Context, scope ledger.WorkspaceID, op string, fn func(ports.WriteTx) error) error {
	if op == s.op && !s.count.off.Load() && s.count.writes.Add(1) > s.allowed {
		return ErrStopped
	}
	return s.Store.Write(ctx, scope, op, fn)
}

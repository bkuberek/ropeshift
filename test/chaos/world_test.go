//go:build chaos

package chaos

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

// world is one crash run: its target, the parent's view of the ledger, and the
// witness.
type world struct {
	t       *testing.T
	target  Target
	rig     *contract.Rig
	store   ports.Store
	logPath string
	variant string
	stats   runStats
	// socket is where this world serves its control plane, once one is serving.
	socket string
	// planeConns counts the connections that plane holds. Only children connect to it.
	planeConns atomic.Int64
}

// settleBound is how long a plane may take over what a dead child asked of it.
const settleBound = 10 * time.Second

// settle waits for the plane in this process to finish what a dead child sent. It outlives the
// child, so a write can commit after the kill, and each check reads the log and what it projects in
// two transactions, which a commit between them would show as a ledger that disagrees with itself. A
// connection closes only after its last request ran.
func (w *world) settle() error {
	deadline := time.Now().Add(settleBound)
	for w.planeConns.Load() != 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("the plane still holds %d connections", w.planeConns.Load())
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

// storeSlack is how long the store calls a phase makes near its own bound may still take. It is
// the SQLite adapter's busy timeout, the longest a call waits on a lock before it fails by itself,
// so a phase's context ends only on a call that blocks past anything the store allows.
const storeSlack = 30 * time.Second

// within runs one phase of the parent's store work on a context of its own, bounded by limit, and
// names the phase in what it returns. The rig's context is one budget for the whole test, while the
// kills before a phase last as long as KILLS asks, so a long run would spend it before the phase
// began, and a phase would fail on its length and not on anything the ledger did. Its own bound
// still stops a store call that blocks. The rig's helpers read its context, so the phase holds it
// in their place and gives the old one back.
func (w *world) within(phase string, limit time.Duration, fn func() error) error {
	outer := w.rig.Ctx
	ctx, cancel := context.WithTimeout(w.t.Context(), limit)
	w.rig.Ctx = ctx
	defer func() {
		cancel()
		w.rig.Ctx = outer
	}()
	err := fn()
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return fmt.Errorf("%s, bounded at %s: %w", phase, limit, err)
	default:
		return fmt.Errorf("%s: %w", phase, err)
	}
}

// runStats counts what the witness shows.
type runStats struct {
	claims       int
	writes       int
	fencedWrites int
	salvaged     int
	records      int
}

// newWorld creates a disposable ledger after the guard agrees.
func newWorld(t *testing.T, variant string) *world {
	t.Helper()
	target, err := NewTarget()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(target.Dir) })
	if err := Verify(target, LiveInstallation()); err != nil {
		t.Fatal(err)
	}
	backend := contract.Backend{Name: "chaos", Fresh: func(testing.TB) contract.Database {
		return contract.Database{
			Restartable: true,
			ShiftClock:  func(int64) { t.Fatal("a crash run keeps real time") },
			Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
				store, err := sqlite.Open(target.Path, opts)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				return store
			},
		}
	}}
	rig := contract.NewRig(t, backend)
	return &world{
		t:       t,
		target:  target,
		rig:     rig,
		store:   rig.Store(),
		logPath: filepath.Join(target.Dir, "effects.log"),
		variant: variant,
	}
}

// spec fills in what every child of this world shares.
func (w *world) spec(s childSpec) childSpec {
	s.Target = w.target
	s.Scope = string(w.rig.Scope)
	s.LogPath = w.logPath
	s.Variant = w.variant
	return s
}

func (w *world) rows() ([]workorders.WorkOrder, error) {
	var rows []workorders.WorkOrder
	err := w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.WorkOrders().List()
		return err
	})
	return rows, err
}

// verify checks the ledger against itself and against the witness.
func (w *world) verify() error {
	if err := w.settle(); err != nil {
		return err
	}
	if err := w.rig.CheckRevisions(w.store, w.rig.Scope); err != nil {
		return err
	}
	if err := w.rig.CheckProjection(w.store); err != nil {
		return err
	}
	if err := w.rig.CheckPlanning(w.store); err != nil {
		return err
	}
	if err := w.rig.CheckQuestions(w.store); err != nil {
		return err
	}
	if err := w.rig.CheckSessions(w.store); err != nil {
		return err
	}
	if err := w.rig.CheckAutonomy(w.store); err != nil {
		return err
	}
	if err := w.rig.CheckWakes(w.store); err != nil {
		return err
	}
	if err := w.rig.CheckMessages(w.store); err != nil {
		return err
	}
	if err := checkClaimsPinned(w); err != nil {
		return err
	}
	if err := w.verifyClaims(); err != nil {
		return err
	}
	return w.verifyLog()
}

// verifyClaims proves every grant is whole: its event and its row commit together,
// a work order has one open claim, and fences rise.
func (w *world) verifyClaims() error {
	var claims []workorders.Claim
	err := w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		var err error
		claims, err = tx.Claims().List()
		return err
	})
	if err != nil {
		return err
	}
	events, err := w.rig.Events(w.store, w.rig.Scope)
	if err != nil {
		return err
	}
	grants := map[string]workorders.Change{}
	for _, e := range events {
		if e.Type != grantEventType() {
			continue
		}
		change, err := changeOf(e)
		if err != nil {
			return err
		}
		if _, repeated := grants[change.ClaimID]; repeated {
			return contract.Violatedf("claim %s was granted twice", change.ClaimID)
		}
		grants[change.ClaimID] = change
	}
	if len(grants) != len(claims) {
		return contract.Violatedf("%d grant events and %d claim rows", len(grants), len(claims))
	}
	open := map[string]int{}
	last := map[string]int64{}
	for _, c := range claims {
		grant, ok := grants[c.ID]
		if !ok || grant.Fence != c.Fence {
			return contract.Violatedf("claim %s has no matching grant event", c.ID)
		}
		if c.Fence <= last[c.WorkOrder] {
			return contract.Violatedf("%s: claim at fence %d after fence %d", c.WorkOrder, c.Fence, last[c.WorkOrder])
		}
		last[c.WorkOrder] = c.Fence
		if !c.Ended() {
			open[c.WorkOrder]++
		}
	}
	for workOrder, n := range open {
		if n > 1 {
			return contract.Violatedf("%s: %d open claims", workOrder, n)
		}
	}
	return nil
}

func grantEventType() string { return workorders.EventType(workorders.TriggerClaimGranted) }

func changeOf(e ledger.Event) (workorders.Change, error) {
	var change workorders.Change
	err := json.Unmarshal(e.Data, &change)
	return change, err
}

// verifyLog holds the database to what the children saw. A commit a child saw must
// be in the log with the same revision and fence. Everything in the log must have
// been attempted by a child. A write counts only if no later grant had been seen
// at its revision.
func (w *world) verifyLog() error {
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		return contract.Violatedf("%v", err)
	}
	events, err := w.rig.Events(w.store, w.rig.Scope)
	if err != nil {
		return err
	}
	byRev := map[int64]ledger.Event{}
	fences := map[string]int64{}
	keys := map[string]bool{}
	for _, e := range events {
		byRev[e.Rev] = e
		change, err := changeOf(e)
		if err != nil {
			return err
		}
		switch {
		case e.Type == grantEventType():
			keys[change.ClaimID] = true
		case e.Type == workorders.EventFencedWrite:
			keys[string(change.Payload)] = true
		}
	}
	attempted := map[string]bool{}
	type seen struct{ rev, fence int64 }
	grants := map[string][]seen{}
	var writes []Record
	w.stats = runStats{}
	for _, r := range records {
		switch {
		case r.Kind == Attempted:
			attempted[r.Key] = true
		case r.Kind == Rejected && r.Op == "write":
			w.stats.fencedWrites++
		case r.Kind == Rejected && r.Op == "lease":
			w.stats.salvaged++
		case r.Kind == Completed && r.Rev != 0:
			e, ok := byRev[r.Rev]
			if !ok {
				return contract.Violatedf("a child saw %s %s commit at rev %d, and the log has no such event", r.Op, r.Key, r.Rev)
			}
			change, err := changeOf(e)
			if err != nil {
				return err
			}
			if r.Fence != 0 && change.Fence != r.Fence {
				return contract.Violatedf("%s %s: the child saw fence %d, the log holds %d", r.Op, r.Key, r.Fence, change.Fence)
			}
			switch r.Op {
			case "claim":
				grants[r.WorkOrder] = append(grants[r.WorkOrder], seen{r.Rev, r.Fence})
				w.stats.claims++
				fences[r.WorkOrder] = max(fences[r.WorkOrder], r.Fence)
			case "write":
				writes = append(writes, r)
				w.stats.writes++
			}
		}
	}
	for key := range keys {
		if !attempted[key] && strings.HasPrefix(key, "child-") {
			return contract.Violatedf("%s is in the log with no attempt by a child", key)
		}
	}
	for workOrder, list := range grants {
		slices.SortFunc(list, func(a, b seen) int { return cmp.Compare(a.rev, b.rev) })
		for i := 1; i < len(list); i++ {
			if list[i].fence <= list[i-1].fence {
				return contract.Violatedf("%s: children saw fence %d after %d", workOrder, list[i].fence, list[i-1].fence)
			}
		}
	}
	for _, r := range writes {
		for _, g := range grants[r.WorkOrder] {
			if g.rev < r.Rev && g.fence > r.Fence {
				return contract.Violatedf("%s: a write with fence %d committed at rev %d, after fence %d was granted at rev %d",
					r.WorkOrder, r.Fence, r.Rev, g.fence, g.rev)
			}
		}
	}
	w.stats.records = len(records)
	return nil
}

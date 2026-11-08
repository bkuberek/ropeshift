package contract

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// StoreChecks are the ledger core's invariants.
var StoreChecks = []Check{
	{"S1", "revisions are gapless and commit-ordered", checkGaplessRevisions},
	{"S2", "each event takes its own revision", checkEventRevisions},
	{"S3", "a rollback leaves no gap", checkRollbackLeavesNoGap},
	{"S4", "a transaction with no event commits nothing", checkNoEventNoCommit},
	{"S5", "every call is scoped to one workspace", checkWorkspaceScope},
	{"S6", "every event names its actor", checkEventNamesActor},
	{"S7", "time comes from the database", checkDatabaseTime},
	{"S8", "event replay equals the projection", checkReplayEqualsProjection},
	{"S9", "a projection rebuilds from events", checkRebuild},
	{"S10", "the revision is the first statement of every write", checkRevisionFirst},
	{"S11", "migrations repeat as a no-op", checkMigrationsRepeat},
	{"S12", "creating a workspace is idempotent", checkCreateWorkspace},
	{"S13", "creating a work order is idempotent", checkCreateWorkOrderRetry},
}

// checkGaplessRevisions races writers against a reader that follows a cursor. A
// reader that ever skips a revision would lose that event for good, which is why
// a database sequence cannot be the cursor.
func checkGaplessRevisions(r *Rig) error {
	s := r.Store()
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	const writers, each = 8, 25
	stop := make(chan struct{})
	followed := make(chan error, 1)
	go func() { followed <- r.follow(s, base, stop) }()
	err = parallel(writers, func(int) error {
		for range each {
			if _, err := r.Note(s, r.Scope, r.ID("note")); err != nil {
				return err
			}
		}
		return nil
	})
	close(stop)
	if ferr := <-followed; ferr != nil {
		return ferr
	}
	if err != nil {
		return err
	}
	rev, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if want := base + writers*each; rev != want {
		return Violatedf("rev %d after %d writes from %d", rev, writers*each, base)
	}
	return r.CheckRevisions(s, r.Scope)
}

func (r *Rig) follow(s ports.Store, cursor int64, stop <-chan struct{}) error {
	for {
		var page []ledger.Event
		err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			var err error
			page, err = tx.Events(cursor, 50)
			return err
		})
		if err != nil {
			return err
		}
		for _, e := range page {
			if e.Rev != cursor+1 {
				return Violatedf("a reader at rev %d next saw rev %d", cursor, e.Rev)
			}
			cursor = e.Rev
		}
		select {
		case <-stop:
			if len(page) == 0 {
				return nil
			}
		default:
		}
	}
}

func checkEventRevisions(r *Rig) error {
	s := r.Store()
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	var first, second ledger.Event
	err = s.Write(r.Ctx, r.Scope, "two-events", func(tx ports.WriteTx) error {
		var err error
		if first, err = tx.Append(r.noteDraft("one")); err != nil {
			return err
		}
		second, err = tx.Append(r.noteDraft("two"))
		return err
	})
	if err != nil {
		return err
	}
	if first.Rev != base+1 || second.Rev != base+2 {
		return Violatedf("two events took revisions %d and %d after %d", first.Rev, second.Rev, base)
	}
	return r.CheckRevisions(s, r.Scope)
}

func checkRollbackLeavesNoGap(r *Rig) error {
	s := r.Store()
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	failure := errors.New("the decision refused this")
	err = s.Write(r.Ctx, r.Scope, "fails", func(tx ports.WriteTx) error {
		if _, err := tx.Append(r.noteDraft("rolled-back")); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		return Violatedf("a failed write returned %v", err)
	}
	if err := r.panicInWrite(s); err != nil {
		return err
	}
	rev, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if rev != base {
		return Violatedf("a rollback moved the revision from %d to %d", base, rev)
	}
	next, err := r.Note(s, r.Scope, "next")
	if err != nil {
		return err
	}
	if next.Rev != base+1 {
		return Violatedf("the next event took rev %d after %d", next.Rev, base)
	}
	return r.CheckRevisions(s, r.Scope)
}

func (r *Rig) panicInWrite(s ports.Store) (err error) {
	defer func() {
		if recover() == nil {
			err = Violatedf("a panic inside a write did not reach the caller")
		}
	}()
	return s.Write(r.Ctx, r.Scope, "panics", func(tx ports.WriteTx) error {
		if _, err := tx.Append(r.noteDraft("panicked")); err != nil {
			return err
		}
		panic("stop")
	})
}

func checkNoEventNoCommit(r *Rig) error {
	s := r.Store()
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	err = s.Write(r.Ctx, r.Scope, "reads-only", func(tx ports.WriteTx) error {
		_, err := tx.Workspace()
		return err
	})
	if err != nil {
		return err
	}
	id := r.ID("wo")
	err = s.Write(r.Ctx, r.Scope, "untraced", func(tx ports.WriteTx) error {
		return tx.WorkOrders().Insert(workorders.WorkOrder{ID: id, Kind: workorders.KindChange, State: workorders.Intake})
	})
	if !errors.Is(err, ports.ErrNoEvent) {
		return Violatedf("a change with no event returned %v", err)
	}
	rev, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if rev != base {
		return Violatedf("a transaction with no event moved the revision from %d to %d", base, rev)
	}
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		_, err := tx.WorkOrders().Get(id)
		return err
	})
	if !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a change with no event stayed in the ledger: %v", err)
	}
	return nil
}

func checkWorkspaceScope(r *Rig) error {
	s := r.Store()
	other := ledger.WorkspaceID("workspace-b")
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{ID: other, Name: "B"}, r.Actor); err != nil {
		return err
	}
	if _, err := s.CreateWorkspace(r.Ctx, ledger.Workspace{Name: "none"}, r.Actor); !errors.Is(err, ledger.ErrNoWorkspace) {
		return Violatedf("a workspace with no ID returned %v", err)
	}
	noop := func(ports.WriteTx) error { return nil }
	read := func(ports.ReadTx) error { return nil }
	if err := s.Write(r.Ctx, "", "scope", noop); !errors.Is(err, ledger.ErrNoWorkspace) {
		return Violatedf("a write with no scope returned %v", err)
	}
	if err := s.Read(r.Ctx, "", read); !errors.Is(err, ledger.ErrNoWorkspace) {
		return Violatedf("a read with no scope returned %v", err)
	}
	if err := s.Write(r.Ctx, "missing", "scope", noop); !errors.Is(err, ledger.ErrUnknownWorkspace) {
		return Violatedf("a write to an unknown workspace returned %v", err)
	}
	if err := s.Read(r.Ctx, "missing", read); !errors.Is(err, ledger.ErrUnknownWorkspace) {
		return Violatedf("a read of an unknown workspace returned %v", err)
	}

	order, err := r.NewWorkOrder(s)
	if err != nil {
		return err
	}
	baseOther, err := r.Revision(s, other)
	if err != nil {
		return err
	}
	if _, err := r.Note(s, r.Scope, "only-in-a"); err != nil {
		return err
	}
	revOther, err := r.Revision(s, other)
	if err != nil {
		return err
	}
	if revOther != baseOther {
		return Violatedf("a write in one workspace moved another's revision")
	}
	seen, err := r.Events(s, other)
	if err != nil {
		return err
	}
	for _, e := range seen {
		if e.Subject.ID == "only-in-a" || e.Subject.ID == order.ID {
			return Violatedf("workspace b read an event of workspace a: %s", e.Subject.ID)
		}
	}
	return s.Read(r.Ctx, other, func(tx ports.ReadTx) error {
		if _, err := tx.WorkOrders().Get(order.ID); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("workspace b read a work order of workspace a: %v", err)
		}
		rows, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return Violatedf("workspace b listed %d work orders of workspace a", len(rows))
		}
		return nil
	})
}

func checkEventNamesActor(r *Rig) error {
	s := r.Store()
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	err = s.Write(r.Ctx, r.Scope, "anonymous", func(tx ports.WriteTx) error {
		draft := r.noteDraft("anonymous")
		draft.Actor = ledger.Actor{}
		_, err := tx.Append(draft)
		return err
	})
	if !errors.Is(err, ledger.ErrInvalidEvent) {
		return Violatedf("an event with no actor returned %v", err)
	}
	if rev, err := r.Revision(s, r.Scope); err != nil {
		return err
	} else if rev != base {
		return Violatedf("a refused event moved the revision")
	}

	draft := r.noteDraft("full")
	draft.ID = r.ID("event")
	draft.OccurredAt = 12345
	draft.Traceparent = "00-trace-span-01"
	var written ledger.Event
	err = s.Write(r.Ctx, r.Scope, "full", func(tx ports.WriteTx) error {
		var err error
		written, err = tx.Append(draft)
		return err
	})
	if err != nil {
		return err
	}
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	read := events[len(events)-1]
	if read.ID != draft.ID || read.Type != draft.Type || read.Actor != draft.Actor ||
		read.Subject != draft.Subject || string(read.Data) != string(draft.Data) ||
		read.OccurredAt != draft.OccurredAt || read.Traceparent != draft.Traceparent ||
		read.RecordedAt != written.RecordedAt {
		return Violatedf("the log changed an event: wrote %+v, read %+v", written, read)
	}
	generated, err := r.Note(s, r.Scope, "generated-id")
	if err != nil {
		return err
	}
	if len(generated.ID) != 26 {
		return Violatedf("a generated event ID %q is not a ULID", generated.ID)
	}
	return nil
}

// checkDatabaseTime moves only the database's clock, so a time from anywhere else
// shows.
func checkDatabaseTime(r *Rig) error {
	s := r.Store()
	const hour = int64(time.Hour / time.Millisecond)
	r.DB.ShiftClock(hour)
	before := time.Now().UnixMilli() + hour
	var now int64
	var event ledger.Event
	err := s.Write(r.Ctx, r.Scope, "clock", func(tx ports.WriteTx) error {
		now = tx.Now()
		var err error
		event, err = tx.Append(r.noteDraft("clocked"))
		return err
	})
	if err != nil {
		return err
	}
	after := time.Now().UnixMilli() + hour
	const slack = 2000
	if now < before-slack || now > after+slack {
		return Violatedf("transaction time %d is outside database time [%d, %d]", now, before, after)
	}
	if event.RecordedAt != now {
		return Violatedf("event recorded at %d in a transaction at %d", event.RecordedAt, now)
	}
	return nil
}

func checkReplayEqualsProjection(r *Rig) error {
	s := r.Store()
	for range 5 {
		if _, err := r.NewWorkOrder(s); err != nil {
			return err
		}
		if _, err := r.Note(s, r.Scope, r.ID("note")); err != nil {
			return err
		}
	}
	return r.CheckProjection(s)
}

func checkRebuild(r *Rig) error {
	s := r.Store()
	kept, err := r.NewWorkOrder(s)
	if err != nil {
		return err
	}
	lost, err := r.NewWorkOrder(s)
	if err != nil {
		return err
	}
	if _, err := r.NewWorkOrder(s); err != nil {
		return err
	}
	if err := r.DB.Damage(r.T, fmt.Sprintf(`UPDATE work_orders SET title = 'damaged' WHERE id = '%s'`, kept.ID)); err != nil {
		return err
	}
	if err := r.DB.Damage(r.T, fmt.Sprintf(`DELETE FROM work_orders WHERE id = '%s'`, lost.ID)); err != nil {
		return err
	}
	if err := r.CheckProjection(s); err == nil {
		return errors.New("the comparison did not see the damage")
	}
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	rev, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if rev != base+1 {
		return Violatedf("a rebuild took %d revisions", rev-base)
	}
	return r.CheckRevisions(s, r.Scope)
}

// checkRevisionFirst runs every kind of write and reads the order of the
// statements, which no result can show on SQLite (D7).
func checkRevisionFirst(r *Rig) error {
	s := r.Store()
	if _, err := r.NewWorkOrder(s); err != nil {
		return err
	}
	if _, err := r.Note(s, r.Scope, "traced"); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	err := s.Write(r.Ctx, r.Scope, "fails", func(ports.WriteTx) error { return errors.New("no") })
	if err == nil {
		return errors.New("a failing write returned nil")
	}
	entries := r.trace.Entries()
	count, err := CheckRevisionFirst(entries)
	if err != nil {
		return err
	}
	if count < 4 {
		return fmt.Errorf("the trace held only %d write transactions", count)
	}
	return nil
}

func checkMigrationsRepeat(r *Rig) error {
	s := r.Store()
	order, err := r.NewWorkOrder(s)
	if err != nil {
		return err
	}
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	for range 2 {
		if err := s.Migrate(r.Ctx); err != nil {
			return Violatedf("a repeated migration failed: %v", err)
		}
	}
	rev, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if rev != base {
		return Violatedf("a repeated migration moved the revision")
	}
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		_, err := tx.WorkOrders().Get(order.ID)
		return err
	})
}

func checkCreateWorkspace(r *Rig) error {
	s := r.Store()
	id := ledger.WorkspaceID("workspace-new")
	workspace := ledger.Workspace{ID: id, Name: "New"}
	var mu sync.Mutex
	created := 0
	err := parallel(6, func(int) error {
		fresh, err := s.CreateWorkspace(r.Ctx, workspace, r.Actor)
		if err != nil {
			return err
		}
		if fresh {
			mu.Lock()
			created++
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if created != 1 {
		return Violatedf("%d of 6 racing creates reported a new workspace", created)
	}
	events, err := r.Events(s, id)
	if err != nil {
		return err
	}
	if len(events) != 1 || !strings.HasPrefix(events[0].Type, "workspace.") {
		return Violatedf("a new workspace has events %+v", events)
	}
	if events[0].Actor != r.Actor {
		return Violatedf("workspace creation names actor %+v", events[0].Actor)
	}
	return r.CheckRevisions(s, id)
}

func checkCreateWorkOrderRetry(r *Rig) error {
	s := r.Store()
	draft := workorders.WorkOrder{ID: r.ID("wo"), Kind: workorders.KindChange, Title: "first"}
	first, err := r.NewOps(s).Create(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	base, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	draft.Title = "retry"
	second, err := r.NewOps(s).Create(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	if second.Title != first.Title {
		return Violatedf("a retry returned %q, the first write was %q", second.Title, first.Title)
	}
	rev, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if rev != base {
		return Violatedf("a retry moved the revision from %d to %d", base, rev)
	}
	return nil
}

// parallel runs n functions at once and returns the first error.
func parallel(n int, fn func(i int) error) error {
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fn(i)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

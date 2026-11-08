//go:build chaos

package chaos

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
)

const (
	// timerAheadMs puts a timer past a parked commit, so the commit leaves it false.
	timerAheadMs = int64(120)
	// timerTries bounds how often a slow commit doubles that.
	timerTries = 6
	// killLimit bounds the killing, which KILLS=1500 stretches.
	killLimit = 20 * time.Minute
	// drainLimit bounds a drain, and a lost wake is a work order that never gets there.
	drainLimit = 90 * time.Second
)

func askedExpr(id string) string { return fmt.Sprintf(`{"%s":%q}`, parking.QuestionAnswered, id) }

func timerExpr(ms int64) string { return fmt.Sprintf(`{"at":%d}`, ms) }

func parkingSpec(role string, request contract.ParkRequest) childSpec {
	spec := holderSpec(role, request.Holder)
	spec.Key = request.Wake
	return spec
}

func roleBeginParking(c *child) error {
	return c.ops.BeginParking(c.ctx, c.scope, c.holder(), parking.Draft{ID: c.spec.Key, Expr: c.spec.Script}, c.actor)
}

func roleCommitParked(c *child) error {
	_, err := c.ops.CommitParked(c.ctx, c.scope, c.holder(), contract.PushedCommit(c.spec.Key), c.actor)
	return err
}

func roleWakeTick(c *child) error {
	fired, err := c.ops.WakeTick(c.ctx, c.scope, c.member())
	for _, id := range fired {
		if err := c.completed("wake", "", id, 0, 0); err != nil {
			return err
		}
	}
	return err
}

func roleSignal(c *child) error {
	_, err := c.ops.Signal(c.ctx, c.scope, parking.Signal{Kind: parking.Kind(c.spec.Script), Key: c.spec.Key}, c.actor)
	return err
}

func (w *world) woken(id string) error {
	wake, err := w.readWake(id)
	if err != nil {
		return err
	}
	var row ledger.LaneRow
	if err := w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Lanes().Row(wake.Lane)
		return err
	}); err != nil {
		return err
	}
	holder := workorders.Holder{Lease: workorders.Lease{WorkOrder: row.WorkOrder}, Lane: wake.Lane}
	return w.rig.ExpectWoken(w.store, contract.ParkRequest{Holder: holder, Wake: id})
}

func (w *world) readWake(id string) (parking.Wake, error) {
	var wake parking.Wake
	err := w.store.Read(w.rig.Ctx, w.rig.Scope, func(tx ports.ReadTx) error {
		var err error
		wake, err = tx.Wakes().Get(id)
		return err
	})
	return wake, err
}

// beginParkingCase stores a wake once, whatever a kill does to the first attempt.
func beginParkingCase() crashCase {
	return crashCase{
		name: "store a wake", op: "lane.parking", steps: withSteps(fencedStep, "recorded"),
		prepare: func(w *world) (childSpec, error) {
			holder, err := w.running(longTTL)
			if err != nil {
				return childSpec{}, err
			}
			spec := holderSpec("begin-parking", holder)
			spec.Key, spec.Script = w.rig.ID("wake"), `{"human_resume":true}`
			return spec, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := countEvents(w, workorders.EventLaneParking, spec.WorkOrder, 1); err != nil {
				return err
			}
			if _, err := w.readWake(spec.Key); err != nil {
				return contract.Violatedf("the wake is not stored: %v", err)
			}
			return hasState(w, spec.WorkOrder, workorders.Running)
		},
	}
}

// parkedCase has an answer on record before the commit, so the commit itself must fire. A
// kill on either side leaves the work order running with its wake, or woken.
func parkedCase() crashCase {
	return crashCase{
		name: "commit parked", op: "lane.parked", steps: withSteps("parked", "recorded"),
		prepare: func(w *world) (childSpec, error) {
			question, err := w.rig.Asked(w.store)
			if err != nil {
				return childSpec{}, err
			}
			request, err := w.rig.Parking(w.store, askedExpr(question))
			if err != nil {
				return childSpec{}, err
			}
			answer := questions.Answer{DecisionID: w.rig.ID("d"), Statement: "Twice.", Rationale: "It follows."}
			if _, err := w.rig.NewOps(w.store).Answer(w.rig.Ctx, w.rig.Scope, question, answer, w.rig.Actor); err != nil {
				return childSpec{}, err
			}
			return parkingSpec("commit-parked", request), nil
		},
		settled: func(w *world, spec childSpec) error { return w.woken(spec.Key) },
	}
}

// parkedOnTimer parks a lane on a timer still ahead of its commit. A crash run keeps real time,
// so a slow commit can find the timer passed and fire it, which is correct and of no use to a
// test that needs a due wake, so it tries again with more room.
func (w *world) parkedOnTimer(expr func(at int64) string) (contract.ParkRequest, int64, error) {
	ahead := timerAheadMs
	for range timerTries {
		now, err := w.rig.Clock(w.store)
		if err != nil {
			return contract.ParkRequest{}, 0, err
		}
		request, err := w.rig.Parked(w.store, expr(now+ahead))
		if !errors.Is(err, contract.ErrFiredAtCommit) {
			return request, now + ahead, err
		}
		ahead *= 2
	}
	return contract.ParkRequest{}, 0, errors.New("every commit outran its timer")
}

// awaitClock waits for database time to pass a moment.
func (w *world) awaitClock(at int64) error {
	for {
		now, err := w.rig.Clock(w.store)
		if err != nil || now > at {
			return err
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// dueTimer leaves a parked work order with a timer that has passed and that nothing fired.
func dueTimer(w *world, expr func(at int64) string) (childSpec, error) {
	request, at, err := w.parkedOnTimer(expr)
	if err != nil {
		return childSpec{}, err
	}
	return parkingSpec("wake-tick", request), w.awaitClock(at)
}

// tickCase kills a tick at one step of a firing, and the next tick must fire it once.
func tickCase(name, op string, steps ...string) crashCase {
	return crashCase{
		name: name, op: op, steps: steps,
		prepare: func(w *world) (childSpec, error) { return dueTimer(w, timerExpr) },
		settled: func(w *world, spec childSpec) error { return w.woken(spec.Key) },
	}
}

// nextEvalCase kills a tick around the statement that moves a false wake on, which repeats
// safely, because it takes no revision and the next tick computes the same time.
func nextEvalCase() crashCase {
	return crashCase{
		name: "move a wake on", op: sqlstore.OpWakeNextEval, steps: []string{"before-commit"},
		prepare: func(w *world) (childSpec, error) {
			question, err := w.rig.Asked(w.store)
			if err != nil {
				return childSpec{}, err
			}
			return dueTimer(w, func(at int64) string {
				return fmt.Sprintf(`{"all_of":[%s,%s]}`, timerExpr(at), askedExpr(question))
			})
		},
		settled: func(w *world, spec childSpec) error {
			wake, err := w.readWake(spec.Key)
			if err != nil {
				return err
			}
			if wake.NextEvalAt != 0 || !wake.Open() {
				return contract.Violatedf("a wake after its retry reads %+v", wake)
			}
			return nil
		},
	}
}

func signalCase() crashCase {
	return crashCase{
		name: "signal", op: "wake.signal", steps: withSteps("recorded"),
		prepare: func(w *world) (childSpec, error) {
			key := w.rig.ID("sign-in")
			request, err := w.rig.Parked(w.store, fmt.Sprintf(`{%q:%q}`, parking.UsageReset, key))
			if err != nil {
				return childSpec{}, err
			}
			spec := parkingSpec("signal", request)
			spec.Script, spec.ClaimID, spec.Lane = string(parking.UsageReset), request.Wake, key
			spec.Key = key
			return spec, nil
		},
		settled: func(w *world, spec childSpec) error { return w.woken(spec.ClaimID) },
	}
}

// answerFiresCase kills an answer inside the transaction that fires what waits on it.
func answerFiresCase() crashCase {
	return crashCase{
		name: "an answer that fires a wake", op: "question.answer", steps: withSteps("decided"),
		prepare: func(w *world) (childSpec, error) {
			question, err := w.rig.Asked(w.store)
			if err != nil {
				return childSpec{}, err
			}
			request, err := w.rig.Parked(w.store, askedExpr(question))
			if err != nil {
				return childSpec{}, err
			}
			return childSpec{Role: "answer", Key: question, ClaimID: w.rig.ID("d"), Current: request.Wake}, nil
		},
		settled: func(w *world, spec childSpec) error { return w.woken(spec.Current) },
	}
}

// answerTellsCase kills an answer on its way to the lanes that wait on it: one parked, whose next
// generation must hear of it, and one still running, which holds a message. All of it lands, or
// none of it.
func answerTellsCase(name string, steps ...string) crashCase {
	return crashCase{
		name: name, op: "question.answer", steps: steps,
		prepare: func(w *world) (childSpec, error) {
			question, err := w.rig.Asked(w.store)
			if err != nil {
				return childSpec{}, err
			}
			parked, err := w.rig.Parked(w.store, askedExpr(question))
			if err != nil {
				return childSpec{}, err
			}
			running, err := w.rig.Parking(w.store, `{"human_resume":true}`)
			if err != nil {
				return childSpec{}, err
			}
			for _, lane := range []string{parked.Lane, running.Lane} {
				if err := w.rig.Attached(w.store, question, lane); err != nil {
					return childSpec{}, err
				}
			}
			spec := childSpec{Role: "answer", Key: question, ClaimID: w.rig.ID("d"), Current: parked.Wake}
			spec.Lane, spec.Script = parked.Lane, running.Lane
			return spec, nil
		},
		settled: func(w *world, spec childSpec) error {
			if err := w.woken(spec.Current); err != nil {
				return err
			}
			if err := w.answerReads(spec.Lane, spec.Key, messages.Expired, messages.ReasonLedgerFact); err != nil {
				return err
			}
			return w.answerReads(spec.Script, spec.Key, messages.Queued, "")
		},
	}
}

// answerReads holds a lane to one answer, in one state.
func (w *world) answerReads(lane, question string, state messages.State, reason string) error {
	rows, err := w.rig.NewOps(w.store).LaneMessages(w.rig.Ctx, w.rig.Scope, lane)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].ReplyTo != question || rows[0].State != state || rows[0].Reason != reason {
		return contract.Violatedf("lane %s holds %+v, want one answer reading %s", lane, rows, state)
	}
	return nil
}

func wakeCases() []crashCase {
	return []crashCase{
		answerTellsCase("an answer on its way to its lanes", withSteps("decided", "told")...),
		beginParkingCase(),
		parkedCase(),
		tickCase("fire a wake", "wake.fire", withSteps()...),
		tickCase("a firing's guards", "wake", "fired"),
		tickCase("a tick's list", "wake.tick", "listed"),
		nextEvalCase(),
		signalCase(),
		answerFiresCase(),
	}
}

// splitFire fires in one transaction and moves in another, so a kill between them spends the
// wake with nothing woken.
type splitFire struct {
	contract.Ops
	store ports.Store
}

func (s splitFire) FireDue(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (bool, error) {
	var lane, order string
	won := false
	err := s.store.Write(ctx, scope, "wake.fire", func(tx ports.WriteTx) error {
		wake, err := tx.Wakes().Get(id)
		if err != nil {
			return err
		}
		row, err := tx.Lanes().Row(wake.Lane)
		if err != nil {
			return err
		}
		lane, order = wake.Lane, row.WorkOrder
		if won, err = tx.Wakes().Fire(id, parking.CauseTick); err != nil || !won {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "note.added", Actor: actor, Subject: ledger.Subject{Kind: "note", ID: id}})
		return err
	})
	if err != nil || !won {
		return false, err
	}
	crashpoint.Hit("wake.fire", "split")
	return true, s.store.Write(ctx, scope, "wake.fire", func(tx ports.WriteTx) error {
		moved, err := tx.Wakes().MoveToReady(order, lane)
		if err != nil || !moved {
			return err
		}
		event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerWakeFired), order, workorders.Change{
			Trigger: workorders.TriggerWakeFired, Lane: lane, Reason: parking.CauseTick, Payload: []byte(id),
		}, actor)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
}

func (s splitFire) WakeTick(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	due, err := s.DueWakes(ctx, scope)
	if err != nil {
		return nil, err
	}
	var fired []string
	for _, id := range due {
		won, err := s.FireDue(ctx, scope, id, actor)
		if err != nil {
			return fired, err
		}
		if won {
			fired = append(fired, id)
		}
	}
	return fired, nil
}

// splitParked commits `parked` and evaluates in a second transaction, so a kill between them
// leaves an answer nothing will ever fire on. A retry finds the commit done, as a real one does.
type splitParked struct {
	contract.Ops
	store ports.Store
}

func (s splitParked) CommitParked(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, commit ledger.ParkedCommit, actor ledger.Actor) (string, error) {
	var before workorders.State
	err := s.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		row, err := tx.WorkOrders().Get(holder.WorkOrder)
		before = row.State
		return err
	})
	if err != nil {
		return "", err
	}
	if _, err := s.Ops.CommitParked(ctx, scope, holder, commit, actor); err != nil {
		return "", err
	}
	if before == workorders.Parked {
		return "", nil
	}
	crashpoint.Hit("lane.parked", "split")
	fired, err := s.FireDue(ctx, scope, commit.Wake, actor)
	if !fired {
		return "", err
	}
	return commit.Wake, err
}

// wakeVariants are the broken builds of a wake. Each must fail the check it names, or that
// check proves nothing.
func wakeVariants() []variant {
	noFiredGuard := replace("wake.fire", "\n   AND fired_at IS NULL", "")
	noStateGuard := replace("wake.move", "\n   AND state = 'parked'", "")
	spent := control.Sabotage{FiresBeforeParked: true}
	return []variant{
		{name: "a firing with no fired_at guard", sabotage: sqlstore.Sabotage{Rewrite: noFiredGuard}, check: "W1"},
		{name: "a move with no state guard", sabotage: sqlstore.Sabotage{Rewrite: noStateGuard}, check: "W1"},
		{name: "a firing with no guard at all", sabotage: sqlstore.Sabotage{Rewrite: chain(noFiredGuard, noStateGuard)}, control: spent, check: "W1"},
		{name: "a timer evaluated early", control: control.Sabotage{EarlyMs: 60_000}, check: "W2"},
		{name: "time read on the client", sabotage: sqlstore.Sabotage{ClientClock: true}, check: "W2"},
		{
			name:     "a tick that ignores what it missed",
			sabotage: sqlstore.Sabotage{Rewrite: replace("wake.due", "AND next_eval_at <= :now", "AND next_eval_at <= :now\n   AND next_eval_at >= CAST(:now AS BIGINT) - 60000")},
			check:    "W3",
		},
		{name: "a parked commit that does not evaluate", control: control.Sabotage{NoParkedEvaluation: true}, check: "W4"},
		{name: "a parked commit that does not evaluate a race", control: control.Sabotage{NoParkedEvaluation: true}, check: "W5"},
		{name: "a parked commit that does not evaluate a timer", control: control.Sabotage{NoParkedEvaluation: true}, check: "W6"},
		{name: "an answer spends a wake during parking", control: spent, check: "W4"},
		{name: "an answer moves a running work order", sabotage: sqlstore.Sabotage{Rewrite: noStateGuard}, control: spent, check: "W4"},
		{name: "a tick fires during parking", sabotage: sqlstore.Sabotage{Rewrite: noStateGuard}, control: spent, check: "W6"},
		{name: "no statement moves next_eval_at", control: control.Sabotage{KeepsNextEval: true}, check: "W7"},
		{name: "a wake that reads one page of a scope", control: control.Sabotage{ReadsOnePage: true}, check: "W25"},
		{name: "a reassignment leaves a wake armed", control: control.Sabotage{KeepsWakesOpen: true}, check: "W8"},
		{name: "an ended lane keeps its wake", control: control.Sabotage{KeepsWakesOpen: true}, check: "W9"},
		{name: "an unreadable wake stops the tick", control: control.Sabotage{UnreadableStops: true}, check: "W10"},
		{name: "a signal wakes only the first lane", control: control.Sabotage{FirstOnly: true}, check: "W11"},
		{
			name:     "a signal from an earlier park counts",
			sabotage: sqlstore.Sabotage{Rewrite: replace("wake.signaled", " AND rev > :after", " AND rev > 0 * :after")},
			check:    "W16",
		},
		{
			name:     "a tick that finds nothing",
			sabotage: sqlstore.Sabotage{Rewrite: replace("wake.due", "AND next_eval_at <= :now", "AND next_eval_at <= :now\n   AND 1 = 0")},
			check:    "lifecycle",
		},
		{
			name:  "a firing split from its move",
			ops:   func(s ports.Store) contract.Ops { return splitFire{Ops: contract.RealOps(s), store: s} },
			check: "crash",
			crash: func() (crashCase, string) { return tickCase("a split firing", "wake.fire", "split"), "split" },
		},
		{
			name:    "an evaluation split from the parked commit",
			control: control.Sabotage{NoParkedEvaluation: true},
			ops:     func(s ports.Store) contract.Ops { return splitParked{Ops: contract.RealOps(s), store: s} },
			check:   "crash",
			crash: func() (crashCase, string) {
				c := parkedCase()
				c.steps = []string{"split"}
				return c, "split"
			},
		},
	}
}

// TestTickProcessesFireEachWakeOnce gives several real processes the same due wakes, which is
// the one way to show that two tickers in two processes cannot both fire.
func TestTickProcessesFireEachWakeOnce(t *testing.T) {
	w := newWorld(t, "")
	const wakes, processes = 40, 4
	var last int64
	ids := make([]string, 0, wakes)
	for range wakes {
		request, at, err := w.parkedOnTimer(timerExpr)
		if err != nil {
			t.Fatal(err)
		}
		ids, last = append(ids, request.Wake), max(last, at)
	}
	if err := w.awaitClock(last); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for slot := range processes {
		wg.Go(func() {
			code, signaled, stderr, err := runToEnd(w.spec(childSpec{Role: "wake-tick", Slot: slot}))
			if err != nil || signaled || code != exitDone {
				t.Errorf("a tick process: exit %d, %v: %s", code, err, stderr)
			}
		})
	}
	wg.Wait()
	for _, id := range ids {
		if err := w.woken(id); err != nil {
			t.Error(err)
		}
	}
	if err := w.verify(); err != nil {
		t.Fatal(err)
	}
	records, err := ReadEffectLog(w.logPath)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, record := range records {
		if record.Op == "wake" && record.Kind == Completed {
			seen[record.Key]++
		}
	}
	for id, times := range seen {
		if times != 1 {
			t.Errorf("two processes fired %s: %d", id, times)
		}
	}
	if len(seen) != wakes {
		t.Errorf("%d of %d wakes fired by a process that lived", len(seen), wakes)
	}
}

// lifecycleSteps are the steps a kill aims at, beside the kills at random delays.
var lifecycleSteps = [][2]string{
	{"lane.parking", "guarded"}, {"lane.parking", "recorded"}, {"lane.parking", "committed"},
	{"lane.parked", "parked"}, {"lane.parked", "recorded"}, {"lane.parked", "before-commit"}, {"lane.parked", "committed"},
	{"wake.fire", "before-commit"}, {"wake.fire", "committed"}, {"wake", "fired"}, {"wake.tick", "listed"},
	{"question.answer", "decided"}, {"question.answer", "before-commit"}, {"question.answer", "committed"},
}

// doneFile says a role's work is over, and drainFile that the killing stopped, so each lane
// finishes at its next chance.
func doneFile(dir string) string { return filepath.Join(dir, "lifecycle-done") }

func drainFile(dir string) string { return filepath.Join(dir, "lifecycle-drain") }

// roleWakeCycler takes one work order round and round: claim, start, store a wake, park, and
// wait to be woken. Everything it needs it reads from the ledger, so a process killed at any
// step leaves the next one nothing to work out. Each step repeats safely.
func roleWakeCycler(c *child) error {
	for !c.overNow() {
		row, err := c.workOrderRow()
		if err != nil {
			return err
		}
		done, err := c.cycle(row)
		if err != nil || done {
			return err
		}
	}
	return nil
}

func (c *child) workOrderRow() (workorders.WorkOrder, error) {
	var row workorders.WorkOrder
	err := c.store.Read(c.ctx, c.scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.WorkOrders().Get(c.spec.WorkOrder)
		return err
	})
	return row, err
}

func (c *child) cycle(row workorders.WorkOrder) (bool, error) {
	switch row.State {
	case workorders.Ready:
		claim := fmt.Sprintf("%s-c%d", row.ID, row.Fence+1)
		_, err := c.ops.Claim(c.ctx, c.scope, workorders.ClaimRequest{WorkOrder: row.ID, ClaimID: claim, Worker: c.actor.ID, TTLMs: longTTL}, c.actor)
		return false, err
	case workorders.Claimed:
		_, err := c.ops.StartLane(c.ctx, c.scope, workorders.Lease{WorkOrder: row.ID, ClaimID: row.ClaimID, Fence: row.Fence}, laneAt(row), c.actor)
		return false, err
	case workorders.Running:
		return false, c.runningRound(row)
	case workorders.Parked:
		time.Sleep(5 * time.Millisecond)
		return false, nil
	case workorders.Acceptance:
		return true, nil
	}
	return false, fmt.Errorf("work order %s is %s", row.ID, row.State)
}

func laneAt(row workorders.WorkOrder) string { return fmt.Sprintf("%s-f%d", row.ID, row.Fence) }

// runningRound parks, or ends the work once the killing has stopped.
func (c *child) runningRound(row workorders.WorkOrder) error {
	holder := workorders.Holder{Lease: workorders.Lease{WorkOrder: row.ID, ClaimID: row.ClaimID, Fence: row.Fence}, Lane: laneAt(row), Generation: 1}
	if _, err := os.Stat(drainFile(filepath.Dir(c.spec.LogPath))); err == nil {
		result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}}
		return c.ops.EndLane(c.ctx, c.scope, holder, result, c.actor)
	}
	expr, err := c.roundExpr(row)
	if err != nil {
		return err
	}
	wake := holder.Lane + "-wake"
	if err := c.ops.BeginParking(c.ctx, c.scope, holder, parking.Draft{ID: wake, Expr: expr}, c.actor); err != nil {
		return err
	}
	_, err = c.ops.CommitParked(c.ctx, c.scope, holder, contract.PushedCommit(wake), c.actor)
	return err
}

// roundExpr picks one of four shapes by the fence, so a restart picks the same one. A round
// moves the fence twice, once for its claim and once for its park, so it is half the fence
// that counts rounds. A wake already stored keeps the time the first attempt wrote.
func (c *child) roundExpr(row workorders.WorkOrder) (string, error) {
	var now int64
	err := c.store.Read(c.ctx, c.scope, func(tx ports.ReadTx) error {
		var err error
		now, err = tx.Now()
		return err
	})
	if err != nil {
		return "", err
	}
	timer := timerExpr(now + int64(c.rng.IntN(20)) + 5)
	question := laneAt(row) + "-q"
	draft := questions.Question{ID: question, Text: "Round " + question}
	if _, err := c.ops.RecordQuestion(c.ctx, c.scope, draft, c.member()); err != nil {
		return "", err
	}
	switch (row.Fence / 2) % 4 {
	case 0:
		return timer, nil
	case 1:
		return askedExpr(question), nil
	case 2:
		return fmt.Sprintf(`{"any_of":[%s,%s]}`, timer, askedExpr(question)), nil
	}
	return fmt.Sprintf(`{"all_of":[%s,%s]}`, timer, askedExpr(question)), nil
}

// roleWakeTicker ticks until the work is over.
func roleWakeTicker(c *child) error {
	for !c.overNow() {
		if err := roleWakeTick(c); err != nil {
			return err
		}
		time.Sleep(4 * time.Millisecond)
	}
	return nil
}

// roleWakeAnswerer answers what is open until the work is over. A client's ID makes a repeat
// the same answer.
func roleWakeAnswerer(c *child) error {
	for !c.overNow() {
		var open []questions.Question
		err := c.store.Read(c.ctx, c.scope, func(tx ports.ReadTx) error {
			var err error
			open, err = tx.Questions().Questions(ports.RecordFilter{Status: string(questions.StatusOpen), Limit: ports.MaxRecords})
			return err
		})
		if err != nil {
			return err
		}
		for _, question := range open {
			answer := questions.Answer{DecisionID: "d-" + question.ID, Statement: "Go on.", Rationale: "It follows."}
			if _, err := c.ops.Answer(c.ctx, c.scope, question.ID, answer, c.member()); err != nil {
				return err
			}
		}
		time.Sleep(6 * time.Millisecond)
	}
	return nil
}

func (c *child) overNow() bool {
	_, err := os.Stat(doneFile(filepath.Dir(c.spec.LogPath)))
	return err == nil
}

// lifecycleRun kills cyclers, tickers, and answerers at random and at named steps, then stops
// killing and lets every lane finish. A lost wake is a work order that never gets there.
type lifecycleRun struct {
	world  *world
	kills  atomic.Int64
	limit  int64
	drain  time.Duration
	orders []string
	mu     sync.Mutex
	broken []string
	// steps counts the kills that landed on a named step.
	steps map[string]int
}

func (r *lifecycleRun) fail(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.broken = append(r.broken, fmt.Sprintf(format, args...))
}

// keepRunning restarts one role until the work is over, killing it while kills are left.
func (r *lifecycleRun) keepRunning(role, workOrder string, slot int) {
	rng := rand.New(rand.NewPCG(crashSeed(), uint64(slot)))
	spawn := 0
	for !r.over() {
		spawn++
		spec := r.world.spec(childSpec{Role: role, WorkOrder: workOrder, Slot: slot, Spawn: spawn, Seed: crashSeed()})
		killing := r.kills.Load() < r.limit
		aimed := killing && rng.IntN(2) == 0
		if aimed {
			step := lifecycleSteps[rng.IntN(len(lifecycleSteps))]
			spec.KillOp, spec.KillStep = step[0], step[1]
		}
		child, err := startChild(spec)
		if err != nil {
			r.fail("spawn: %v", err)
			return
		}
		atStep := r.awaitChild(child, killing, aimed, rng)
		code, signaled := child.exitCode()
		switch {
		case !signaled && code != exitDone:
			r.fail("%s exited %d: %s", role, code, child.stderr.String())
			return
		case signaled:
			r.kills.Add(1)
		}
		if signaled && atStep {
			r.mu.Lock()
			r.steps[spec.KillOp+"/"+spec.KillStep]++
			r.mu.Unlock()
		}
	}
}

// awaitChild reports whether a child ended itself at its step. Only a SIGKILL counts as a kill,
// because a lane that has finished exits at once, and a count of those would prove nothing.
func (r *lifecycleRun) awaitChild(child *proc, killing, aimed bool, rng *rand.Rand) bool {
	switch {
	case aimed:
		select {
		case <-child.exited:
			return true
		case <-time.After(400 * time.Millisecond):
			child.sigkill()
		}
	case killing:
		select {
		case <-child.exited:
		case <-time.After(time.Duration(rng.Int64N(int64(80*time.Millisecond))) + 10*time.Millisecond):
			child.sigkill()
		}
	default:
		// With no kills left, a child runs until the work is over.
		child.wait()
	}
	return false
}

func (r *lifecycleRun) over() bool {
	_, err := os.Stat(doneFile(r.world.target.Dir))
	return err == nil
}

// finished says every work order got there, which is what a drain waits for.
func (r *lifecycleRun) finished() (bool, error) {
	rows, err := r.world.rows()
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.State != workorders.Acceptance {
			return false, nil
		}
	}
	return true, nil
}

// watch lets the kills run out, then gives every lane a bounded time to finish.
func (r *lifecycleRun) watch() error {
	deadline := time.Now().Add(killLimit)
	draining := false
	for time.Now().Before(deadline) {
		r.mu.Lock()
		failed := len(r.broken) != 0
		r.mu.Unlock()
		if failed {
			break
		}
		if !draining && r.kills.Load() >= r.limit {
			if err := os.WriteFile(drainFile(r.world.target.Dir), nil, 0o600); err != nil {
				return err
			}
			draining, deadline = true, time.Now().Add(r.drain)
		}
		if draining {
			done, err := r.finished()
			if err != nil {
				return err
			}
			if done {
				return os.WriteFile(doneFile(r.world.target.Dir), nil, 0o600)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(doneFile(r.world.target.Dir), nil, 0o600); err != nil {
		return err
	}
	rows, err := r.world.rows()
	if err != nil {
		return err
	}
	stuck := []string{}
	for _, row := range rows {
		if row.State != workorders.Acceptance {
			stuck = append(stuck, row.ID+"="+string(row.State))
		}
	}
	return contract.Violatedf("the drain did not finish, so a wake was lost: %s", strings.Join(stuck, " "))
}

func (r *lifecycleRun) execute() (int, error) {
	var wg sync.WaitGroup
	for i, id := range r.orders {
		wg.Go(func() { r.keepRunning("wake-cycler", id, i) })
	}
	wg.Go(func() { r.keepRunning("wake-ticker", "", len(r.orders)) })
	wg.Go(func() { r.keepRunning("wake-ticker", "", len(r.orders)+1) })
	wg.Go(func() { r.keepRunning("wake-answerer", "", len(r.orders)+2) })
	err := r.watch()
	wg.Wait()
	if err != nil {
		return 0, err
	}
	if len(r.broken) != 0 {
		return 0, errors.New(strings.Join(r.broken, "\n"))
	}
	return r.check()
}

// check reads the ledger and the log. Every park was answered by exactly one wake, because a
// lane that finished had to get out of each one, and no process recorded firing what another
// fired.
func (r *lifecycleRun) check() (int, error) {
	if err := r.world.verify(); err != nil {
		return 0, err
	}
	parks := 0
	for _, id := range r.orders {
		parked, err := countOf(r.world, workorders.EventType(workorders.TriggerLaneParked), id)
		if err != nil {
			return 0, err
		}
		woke, err := countOf(r.world, workorders.EventType(workorders.TriggerWakeFired), id)
		if err != nil {
			return 0, err
		}
		if parked != woke || parked == 0 {
			return 0, contract.Violatedf("%s parked %d times and woke %d", id, parked, woke)
		}
		parks += parked
	}
	records, err := ReadEffectLog(r.world.logPath)
	if err != nil {
		return 0, contract.Violatedf("%v", err)
	}
	fired := map[string]bool{}
	for _, record := range records {
		if record.Op == "wake" && record.Kind == Completed {
			if fired[record.Key] {
				return 0, contract.Violatedf("two ticks fired %s", record.Key)
			}
			fired[record.Key] = true
		}
	}
	return parks, nil
}

// newLifecycle seeds a run of four lanes.
func newLifecycle(w *world, kills int64, drain time.Duration) (*lifecycleRun, error) {
	w.rig.MaxAttempts = randomAttempts
	run := &lifecycleRun{world: w, limit: kills, drain: drain, steps: map[string]int{}}
	for range 4 {
		id, err := w.rig.Ready(w.store)
		if err != nil {
			return nil, err
		}
		run.orders = append(run.orders, id)
	}
	return run, nil
}

// checkLostWake runs the lifecycle on a build whose tick finds nothing, which loses every wake
// that waits on a timer alone. It proves the drain can fail.
func checkLostWake(w *world) error {
	run, err := newLifecycle(w, 8, 3*time.Second)
	if err != nil {
		return err
	}
	_, err = run.execute()
	return err
}

// TestWakesSurviveRandomKills runs the whole cycle across cyclers, tickers, and an answerer,
// and kills any of them at any step. Random kills alone do not catch every window, so the
// crash matrix is the check for those. This is the check that nothing is lost when kills land
// where nobody thought to aim them.
func TestWakesSurviveRandomKills(t *testing.T) {
	run, err := newLifecycle(newWorld(t, ""), int64(crashKills(60)), drainLimit)
	if err != nil {
		t.Fatal(err)
	}
	parks, err := run.execute()
	if err != nil {
		t.Fatal(err)
	}
	if run.kills.Load() < run.limit {
		t.Errorf("%d kills, want %d", run.kills.Load(), run.limit)
	}
	aimed := 0
	for _, kills := range run.steps {
		aimed += kills
	}
	t.Logf("%d kills, %d at a named step across %d steps, and %d parks each answered by one wake", run.kills.Load(), aimed, len(run.steps), parks)
}

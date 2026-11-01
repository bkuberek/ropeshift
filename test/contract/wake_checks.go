package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// WakeChecks are the invariants of the wake conditions spike, numbered as it numbers them.
// The checks after W12 cover what phase 1 adds to them. W12 guards a Postgres deadlock, so it
// holds on SQLite, and it earns its place in phase 3 (U28).
var WakeChecks = []Check{
	{"W1", "a wake fires once, under racing firers and a stale ticker", checkFireOnce},
	{"W2", "a timer never fires before its time", checkTimerNeverEarly},
	{"W3", "a timer fires after any downtime", checkTimerAfterDowntime},
	{"W4", "an answer is not lost, whatever its order against parking", checkAnswerOrders},
	{"W5", "an answer racing the parked commit is not lost", checkAnswerParkRace},
	{"W6", "a timer due during parking fires at the parked commit", checkTimerDuringParking},
	{"W7", "composite wakes evaluate, and next_eval_at moves on", checkComposite},
	{"W8", "a wake from a lane taken over cannot wake the new owner", checkStaleWake},
	{"W9", "a wake cannot revive a terminal work order", checkTerminalNotWoken},
	{"W10", "an expression from a newer build stops nothing", checkUnknownTerm},
	{"W11", "one signal wakes every lane on a sign-in", checkFanOut},
	{"W12", "a tick and an answer do not deadlock", checkTickAnswerDeadlock},
	{"W13", "a false evaluation appends nothing and takes no revision", checkFalseEvaluation},
	{"W14", "a step of parking repeats safely", checkParkingRepeats},
	{"W15", "each kind of event fires the wake that names it", checkEachEventFires},
	{"W16", "a signal counts only after its wake was stored", checkSignalAfterWake},
	{"W17", "a wake is stored only under the lease, and only if it can be read", checkWakeStored},
	{"W18", "only a wake takes a work order out of parked", checkOnlyAWakeLeavesParked},
	{"W19", "a parked lane reads as the ledger's own fact", checkParkedReads},
	{"W20", "a woken work order replays, and names the lane to resume", checkWokenReplays},
	{"W21", "a question counts as answered only while its answer stands", checkAnswerStands},
	{"W22", "only a member resumes a lane", checkResumeIsAMembers},
	{"W23", "a question merged into another wakes with it", checkMergedQuestion},
	{"W24", "a lane holds one open wake", checkOneOpenWake},
}

// atMs is a timer term.
func atMs(ms int64) string { return fmt.Sprintf(`{"at":%d}`, ms) }

// askedTerm is a term that waits on one question.
func askedTerm(id string) string { return fmt.Sprintf(`{"%s":%q}`, parking.QuestionAnswered, id) }

// ParkRequest is one lane on its way to parked.
type ParkRequest struct {
	workorders.Holder
	Wake string
}

// Parking claims a work order, starts its lane, and stores a wake, so its lease is still held.
func (r *Rig) Parking(s ports.Store, expr string) (ParkRequest, error) {
	id, err := r.Ready(s)
	if err != nil {
		return ParkRequest{}, err
	}
	return r.parkingOn(s, id, expr)
}

func (r *Rig) parkingOn(s ports.Store, id, expr string) (ParkRequest, error) {
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return ParkRequest{}, err
	}
	request := ParkRequest{Holder: holder, Wake: r.ID("wake")}
	return request, r.NewOps(s).BeginParking(r.Ctx, r.Scope, holder, parking.Draft{ID: request.Wake, Expr: expr}, r.worker("a"))
}

// Parked runs the whole sequence, with a wake that stays false.
func (r *Rig) Parked(s ports.Store, expr string) (ParkRequest, error) {
	request, err := r.Parking(s, expr)
	if err != nil {
		return request, err
	}
	fired, err := r.NewOps(s).CommitParked(r.Ctx, r.Scope, request.Holder, request.Wake, r.worker("a"))
	if err != nil {
		return request, err
	}
	if fired != "" {
		return request, fmt.Errorf("%s fired at the parked commit", request.Wake)
	}
	return request, nil
}

func (r *Rig) readWake(s ports.Store, id string) (parking.Wake, error) {
	var wake parking.Wake
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		wake, err = tx.Wakes().Get(id)
		return err
	})
	return wake, err
}

func (r *Rig) firings(s ports.Store, wake string) (int, error) {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return 0, err
	}
	seen := 0
	for _, e := range events {
		if e.Type != workorders.EventType(workorders.TriggerWakeFired) {
			continue
		}
		var change workorders.Change
		if err := json.Unmarshal(e.Data, &change); err != nil {
			return 0, err
		}
		if string(change.Payload) == wake {
			seen++
		}
	}
	return seen, nil
}

// ExpectWoken checks all a firing leaves: one event, a fired row, and a ready work order that
// resumes the parked lane.
func (r *Rig) ExpectWoken(s ports.Store, request ParkRequest) error {
	n, err := r.firings(s, request.Wake)
	if err != nil {
		return err
	}
	if n != 1 {
		return Violatedf("wake %s fired %d times", request.Wake, n)
	}
	wake, err := r.readWake(s, request.Wake)
	if err != nil {
		return err
	}
	if wake.Open() {
		return Violatedf("wake %s has a firing event and no fired_at", request.Wake)
	}
	order, err := r.WorkOrder(s, request.WorkOrder)
	if err != nil {
		return err
	}
	if order.State != workorders.Ready || order.ResumeLaneID != request.Lane {
		return Violatedf("after waking, state %s resume %q, want ready and %q", order.State, order.ResumeLaneID, request.Lane)
	}
	return nil
}

func (r *Rig) ExpectParked(s ports.Store, request ParkRequest) error {
	order, err := r.WorkOrder(s, request.WorkOrder)
	if err != nil {
		return err
	}
	if order.State != workorders.Parked {
		return Violatedf("%s is %s, want parked", request.WorkOrder, order.State)
	}
	return nil
}

// Asked records an open question and returns its ID.
func (r *Rig) Asked(s ports.Store) (string, error) {
	draft := questions.Question{ID: r.ID("q"), Text: "Question " + r.ID("text"), AskedBy: "lane"}
	record, err := r.NewOps(s).RecordQuestion(r.Ctx, r.Scope, draft, r.Actor)
	return record.ID, err
}

func (r *Rig) wakeTick(s ports.Store) ([]string, error) {
	return r.NewOps(s).WakeTick(r.Ctx, r.Scope, r.Actor)
}

func parallelly(n int, run func(i int) error) error {
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = run(i)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// checkFireOnce has three parts, because two guards protect a firing. The state guard stops
// firers racing on one parked work order, and a race alone does not show the other. The
// `fired_at` guard stops a stale ticker after the work order has parked again, so the check
// reaches that case, and then calls each guarded statement a second time directly.
func checkFireOnce(r *Rig) error {
	s := r.Store()
	const rounds, firers = 6, 8
	for round := range rounds {
		now, err := r.Clock(s)
		if err != nil {
			return err
		}
		request, err := r.Parked(s, atMs(now+1000))
		if err != nil {
			return err
		}
		r.DB.ShiftClock(1001)
		var wins atomic.Int64
		err = parallelly(firers, func(int) error {
			fired, err := r.NewOps(s).FireDue(r.Ctx, r.Scope, request.Wake, r.Actor)
			if fired {
				wins.Add(1)
			}
			return err
		})
		if err != nil {
			return err
		}
		if n := wins.Load(); n != 1 {
			return Violatedf("round %d: %d firers won one wake", round, n)
		}
		if err := r.ExpectWoken(s, request); err != nil {
			return err
		}
	}
	return r.staleTicker(s)
}

func (r *Rig) staleTicker(s ports.Store) error {
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	first, err := r.Parked(s, atMs(now+1000))
	if err != nil {
		return err
	}
	r.DB.ShiftClock(1001)
	ops := r.NewOps(s)
	due, err := ops.DueWakes(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if !slices.Contains(due, first.Wake) {
		return Violatedf("wake %s was not due", first.Wake)
	}
	if fired, err := ops.FireDue(r.Ctx, r.Scope, first.Wake, r.Actor); err != nil {
		return err
	} else if !fired {
		return Violatedf("the first firing did not happen")
	}
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	second, err := r.parkingOn(s, first.WorkOrder, askedTerm(question))
	if err != nil {
		return err
	}
	if _, err := ops.CommitParked(r.Ctx, r.Scope, second.Holder, second.Wake, r.worker("a")); err != nil {
		return err
	}
	if _, err := ops.FireDue(r.Ctx, r.Scope, first.Wake, r.Actor); err != nil {
		return err
	}
	if n, err := r.firings(s, first.Wake); err != nil {
		return err
	} else if n != 1 {
		return Violatedf("a stale ticker fired wake %s again, %d firings", first.Wake, n)
	}
	if err := r.ExpectParked(s, second); err != nil {
		return err
	}
	return r.guardsHold(s, first, second)
}

// guardsHold calls each guarded statement again, because a racing test does not reliably catch
// a removed compare-and-set: a spent wake stays spent, and only a parked work order moves.
func (r *Rig) guardsHold(s ports.Store, spent, open ParkRequest) error {
	return s.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
		won, err := tx.Wakes().Fire(spent.Wake, "again")
		if err != nil {
			return err
		}
		if won {
			return Violatedf("the guarded firing matched a wake that had fired")
		}
		ready, err := r.readyOrderIn(tx)
		if err != nil {
			return err
		}
		moved, err := tx.Wakes().MoveToReady(ready, spent.Lane)
		if err != nil {
			return err
		}
		if moved {
			return Violatedf("the guarded move matched a work order that was not parked")
		}
		_, err = tx.Append(r.noteDraft(open.Wake))
		return err
	})
}

// readyOrderIn finds a work order in `ready`, which every run of W1 leaves behind.
func (r *Rig) readyOrderIn(tx ports.WriteTx) (string, error) {
	rows, err := tx.WorkOrders().List()
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.State == workorders.Ready {
			return row.ID, nil
		}
	}
	return "", errors.New("no work order is ready")
}

func checkTimerNeverEarly(r *Rig) error {
	s := r.Store()
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	due := now + 60_000
	request, err := r.Parked(s, atMs(due))
	if err != nil {
		return err
	}
	r.DB.ShiftClock(55_000)
	if _, err := r.wakeTick(s); err != nil {
		return err
	}
	wake, err := r.readWake(s, request.Wake)
	if err != nil {
		return err
	}
	if !wake.Open() {
		return Violatedf("a timer due at %d fired at %d", due, wake.FiredAt)
	}
	r.DB.ShiftClock(5_001)
	if _, err := r.wakeTick(s); err != nil {
		return err
	}
	if wake, err = r.readWake(s, request.Wake); err != nil {
		return err
	}
	if wake.Open() {
		return Violatedf("a timer did not fire at its time on database time")
	}
	if wake.FiredAt < due {
		return Violatedf("a timer due at %d fired at %d", due, wake.FiredAt)
	}
	if err := r.ExpectWoken(s, request); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

func checkTimerAfterDowntime(r *Rig) error {
	s := r.Store()
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	request, err := r.Parked(s, atMs(now+1000))
	if err != nil {
		return err
	}
	r.DB.ShiftClock(86_400_000)
	if r.DB.Restartable {
		if err := s.Close(); err != nil {
			return err
		}
		s = r.Store()
	}
	fired, err := r.wakeTick(s)
	if err != nil {
		return err
	}
	if len(fired) != 1 {
		return Violatedf("%d wakes fired on the first tick after downtime", len(fired))
	}
	if again, err := r.wakeTick(s); err != nil {
		return err
	} else if len(again) != 0 {
		return Violatedf("a second tick fired %d wakes", len(again))
	}
	if err := r.ExpectWoken(s, request); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

func checkAnswerOrders(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	commit := func(request ParkRequest) error {
		_, err := ops.CommitParked(r.Ctx, r.Scope, request.Holder, request.Wake, r.worker("a"))
		return err
	}
	// The answer arrives before the wake condition is stored.
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	if _, err := r.answer(s, question, "Twice."); err != nil {
		return err
	}
	early, err := r.Parking(s, askedTerm(question))
	if err != nil {
		return err
	}
	if err := commit(early); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, early); err != nil {
		return err
	}
	// The answer arrives during parking. The process may be alive and the lease is held, so
	// nothing may fire until the transaction that commits parked.
	if question, err = r.Asked(s); err != nil {
		return err
	}
	during, err := r.Parking(s, askedTerm(question))
	if err != nil {
		return err
	}
	if _, err := r.answer(s, question, "Twice."); err != nil {
		return err
	}
	if n, err := r.firings(s, during.Wake); err != nil {
		return err
	} else if n != 0 {
		return Violatedf("a wake fired during parking, with the process possibly alive")
	}
	order, err := r.WorkOrder(s, during.WorkOrder)
	if err != nil {
		return err
	}
	if order.State != workorders.Running {
		return Violatedf("a work order is %s during parking, want running with its lease", order.State)
	}
	if _, err := ops.Claim(r.Ctx, r.Scope, r.Request(during.WorkOrder, "rival"), r.worker("rival")); !errors.Is(err, workorders.ErrLost) {
		return Violatedf("a rival claimed during parking: %v", err)
	}
	if err := commit(during); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, during); err != nil {
		return err
	}
	// The answer arrives after parked.
	if question, err = r.Asked(s); err != nil {
		return err
	}
	after, err := r.Parked(s, askedTerm(question))
	if err != nil {
		return err
	}
	if err := r.ExpectParked(s, after); err != nil {
		return err
	}
	if _, err := r.answer(s, question, "Twice."); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, after); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

// checkAnswerParkRace runs the race at full speed, and then with a barrier that holds each
// transaction after its write and before its evaluation, which is the exact interleaving
// that loses a wake when nothing serializes them.
func checkAnswerParkRace(r *Rig) error {
	var current atomic.Pointer[barrier]
	hook := func(op, name string) {
		if name == StepBeforeEval && (op == opParked || op == opAnswer) {
			if b := current.Load(); b != nil {
				b.wait(120 * time.Millisecond)
			}
		}
	}
	parker, answerer := r.StoreWith(hook), r.StoreWith(hook)
	race := func() error {
		question, err := r.Asked(parker)
		if err != nil {
			return err
		}
		request, err := r.Parking(parker, askedTerm(question))
		if err != nil {
			return err
		}
		err = parallelly(2, func(i int) error {
			if i == 0 {
				_, err := r.NewOps(parker).CommitParked(r.Ctx, r.Scope, request.Holder, request.Wake, r.worker("a"))
				return err
			}
			_, err := r.answer(answerer, question, "Twice.")
			return err
		})
		if err != nil {
			return err
		}
		return r.ExpectWoken(parker, request)
	}
	for range 20 {
		if err := race(); err != nil {
			return err
		}
	}
	for range 4 {
		current.Store(newBarrier(2))
		if err := race(); err != nil {
			return err
		}
	}
	return r.CheckWakes(parker)
}

func checkTimerDuringParking(r *Rig) error {
	s := r.Store()
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	request, err := r.Parking(s, atMs(now+1000))
	if err != nil {
		return err
	}
	r.DB.ShiftClock(1001)
	fired, err := r.wakeTick(s)
	if err != nil {
		return err
	}
	if len(fired) != 0 {
		return Violatedf("a tick fired during parking, with the process possibly alive")
	}
	if due, err := r.NewOps(s).DueWakes(r.Ctx, r.Scope); err != nil {
		return err
	} else if !slices.Contains(due, request.Wake) {
		return Violatedf("a tick during parking moved a wake's time on")
	}
	if _, err := r.NewOps(s).CommitParked(r.Ctx, r.Scope, request.Holder, request.Wake, r.worker("a")); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, request); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

func checkComposite(r *Rig) error {
	s := r.Store()
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	both, err := r.Parked(s, fmt.Sprintf(`{"all_of":[%s,%s]}`, atMs(now+1000), askedTerm(question)))
	if err != nil {
		return err
	}
	r.DB.ShiftClock(1001)
	if fired, err := r.wakeTick(s); err != nil {
		return err
	} else if len(fired) != 0 {
		return Violatedf("all_of fired with one term false")
	}
	due, err := r.NewOps(s).DueWakes(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if slices.Contains(due, both.Wake) {
		return Violatedf("a wake stays due after a false evaluation, so each tick reads it forever")
	}
	if _, err := r.answer(s, question, "Twice."); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, both); err != nil {
		return err
	}
	return r.printedExpression(s)
}

// printedExpression is the expression the architecture prints, by each way it can come true.
func (r *Rig) printedExpression(s ports.Store) error {
	a, err := r.Asked(s)
	if err != nil {
		return err
	}
	b, err := r.Asked(s)
	if err != nil {
		return err
	}
	printed := fmt.Sprintf(`{"any_of":[{"all_of":[%s,%s]},{"human_resume":true}]}`, askedTerm(a), askedTerm(b))
	byAnswers, err := r.Parked(s, printed)
	if err != nil {
		return err
	}
	if _, err := r.answer(s, a, "Yes."); err != nil {
		return err
	}
	if err := r.ExpectParked(s, byAnswers); err != nil {
		return err
	}
	if _, err := r.answer(s, b, "No."); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, byAnswers); err != nil {
		return err
	}
	if a, err = r.Asked(s); err != nil {
		return err
	}
	if b, err = r.Asked(s); err != nil {
		return err
	}
	byMember, err := r.Parked(s, fmt.Sprintf(`{"any_of":[{"all_of":[%s,%s]},{"human_resume":true}]}`, askedTerm(a), askedTerm(b)))
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).ResumeLane(r.Ctx, r.Scope, byMember.Lane, r.Actor); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, byMember); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

// checkStaleWake is a crash during parking followed by a reassignment. The old lane's wake must
// not fire on the new owner's parked work order.
func checkStaleWake(r *Rig) error {
	s := r.Store()
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	old, err := r.Asked(s)
	if err != nil {
		return err
	}
	if _, err := r.parkingOn(s, id, askedTerm(old)); err != nil {
		return err
	}
	ops := r.NewOps(s)
	r.DB.ShiftClock(TTLMs + 1)
	if _, err := ops.Sweep(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if err := ops.Reassign(r.Ctx, r.Scope, id, "the worker is gone", r.Actor); err != nil {
		return err
	}
	current, err := r.Asked(s)
	if err != nil {
		return err
	}
	request, err := r.parkingOn(s, id, askedTerm(current))
	if err != nil {
		return err
	}
	if _, err := ops.CommitParked(r.Ctx, r.Scope, request.Holder, request.Wake, r.worker("a")); err != nil {
		return err
	}
	if _, err := r.answer(s, old, "Twice."); err != nil {
		return err
	}
	if err := r.ExpectParked(s, request); err != nil {
		return Violatedf("the old lane's wake moved the new owner's work: %v", err)
	}
	if _, err := r.answer(s, current, "Twice."); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, request); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

func checkTerminalNotWoken(r *Rig) error {
	s := r.Store()
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	request, err := r.Parked(s, atMs(now+1000))
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	if err := ops.Move(r.Ctx, r.Scope, request.WorkOrder, workorders.TriggerCanceled, r.Actor); err != nil {
		return err
	}
	r.DB.ShiftClock(1001)
	if _, err := r.wakeTick(s); err != nil {
		return err
	}
	order, err := r.WorkOrder(s, request.WorkOrder)
	if err != nil {
		return err
	}
	if order.State != workorders.Canceled {
		return Violatedf("a wake revived a canceled work order: state %s", order.State)
	}
	due, err := ops.DueWakes(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if slices.Contains(due, request.Wake) {
		return Violatedf("a wake on a canceled work order stays due forever")
	}
	wake, err := r.readWake(s, request.Wake)
	if err != nil {
		return err
	}
	if wake.FiredBy != parking.RetiredCanceled {
		return Violatedf("a canceled work order's wake reads %+v", wake)
	}
	// A lane that finishes with a stored wake leaves nothing armed.
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	finished, err := r.Parking(s, askedTerm(question))
	if err != nil {
		return err
	}
	if err := ops.EndLane(r.Ctx, r.Scope, finished.Holder, laneResult(workorders.OutcomeCompleted, ""), r.worker("a")); err != nil {
		return err
	}
	if wake, err = r.readWake(s, finished.Wake); err != nil {
		return err
	}
	if wake.Open() || wake.FiredBy != parking.RetiredFinished {
		return Violatedf("a wake on a finished lane reads %+v", wake)
	}
	if n, err := r.firings(s, finished.Wake); err != nil {
		return err
	} else if n != 0 {
		return Violatedf("a retirement counted as a firing")
	}
	return r.CheckWakes(s)
}

// futureExpr is what a newer build could store.
const futureExpr = `{"moon_phase":"full"}`

// storeUnreadable writes a wake this build cannot read onto a running lane, as a newer build
// would have. It goes around the use case, which refuses to store one.
func (r *Rig) storeUnreadable(s ports.Store, holder workorders.Holder, nextEval int64) (string, error) {
	id := r.ID("future")
	err := s.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
		event, err := tx.Append(r.noteDraft(id))
		if err != nil {
			return err
		}
		_, err = tx.Wakes().Insert(parking.Wake{ID: id, Lane: holder.Lane, Expr: futureExpr, NextEvalAt: nextEval, StoredRev: event.Rev})
		return err
	})
	return id, err
}

func checkUnknownTerm(r *Rig) error {
	s := r.Store()
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	future, err := r.storeUnreadable(s, holder, now-2000)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	if fired, err := ops.CommitParked(r.Ctx, r.Scope, holder, future, r.worker("a")); err != nil {
		return Violatedf("an unreadable wake stopped a parked commit: %v", err)
	} else if fired != "" {
		return Violatedf("an unreadable wake fired at its commit")
	}
	plain, err := r.Parked(s, atMs(now+1000))
	if err != nil {
		return err
	}
	r.DB.ShiftClock(1001)
	fired, err := r.wakeTick(s)
	if err != nil {
		return Violatedf("one unreadable wake stopped the tick: %v", err)
	}
	if !slices.Equal(fired, []string{plain.Wake}) {
		return Violatedf("a tick fired %v, want only the readable wake", fired)
	}
	wake, err := r.readWake(s, future)
	if err != nil {
		return err
	}
	if !wake.Open() || wake.Expr != futureExpr {
		return Violatedf("an unreadable wake changed: %+v", wake)
	}
	due, err := ops.DueWakes(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if !slices.Contains(due, future) {
		return Violatedf("an unreadable wake left the due list, so a later build could not fire it")
	}
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	if _, err := r.answer(s, question, "Twice."); err != nil {
		return Violatedf("an unreadable wake stopped an answer: %v", err)
	}
	return r.CheckWakes(s)
}

// checkFanOut holds one row per lane with a shared term, which is what the schema allows.
func checkFanOut(r *Rig) error {
	s := r.Store()
	const lanes = 6
	const key = "worker-1:claude-code"
	requests := make([]ParkRequest, lanes)
	for i := range requests {
		request, err := r.Parked(s, fmt.Sprintf(`{%q:%q}`, parking.UsageReset, key))
		if err != nil {
			return err
		}
		requests[i] = request
	}
	fired, err := r.NewOps(s).Signal(r.Ctx, r.Scope, parking.Signal{Kind: parking.UsageReset, Key: key}, r.Actor)
	if err != nil {
		return err
	}
	if len(fired) != lanes {
		return Violatedf("%d of %d lanes woke on one sign-in", len(fired), lanes)
	}
	for _, request := range requests {
		if err := r.ExpectWoken(s, request); err != nil {
			return err
		}
	}
	return r.CheckWakes(s)
}

// checkTickAnswerDeadlock runs a tick against an answer on one wake, both held after their
// first locks. The firing has no row lock to take before the revision, so neither can wait
// on the other.
func checkTickAnswerDeadlock(r *Rig) error {
	var current atomic.Pointer[barrier]
	hook := func(op, name string) {
		if name == StepBeforeEval && (op == opFire || op == opAnswer) {
			if b := current.Load(); b != nil {
				b.wait(150 * time.Millisecond)
			}
		}
	}
	ticker, answerer := r.StoreWith(hook), r.StoreWith(hook)
	for round := range 3 {
		now, err := r.Clock(ticker)
		if err != nil {
			return err
		}
		question, err := r.Asked(ticker)
		if err != nil {
			return err
		}
		request, err := r.Parked(ticker, fmt.Sprintf(`{"any_of":[%s,%s]}`, atMs(now+1000), askedTerm(question)))
		if err != nil {
			return err
		}
		r.DB.ShiftClock(1001)
		current.Store(newBarrier(2))
		err = parallelly(2, func(i int) error {
			if i == 0 {
				_, err := r.wakeTick(ticker)
				return err
			}
			_, err := r.answer(answerer, question, "Twice.")
			return err
		})
		current.Store(nil)
		if err != nil {
			if strings.Contains(err.Error(), "deadlock") {
				return Violatedf("round %d: a tick and an answer deadlocked", round)
			}
			return err
		}
		if err := r.ExpectWoken(ticker, request); err != nil {
			return err
		}
	}
	return r.CheckWakes(ticker)
}

func checkFalseEvaluation(r *Rig) error {
	s := r.Store()
	now, err := r.Clock(s)
	if err != nil {
		return err
	}
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	request, err := r.Parked(s, fmt.Sprintf(`{"all_of":[{"any_of":[%s,%s]},%s]}`, atMs(now+1000), atMs(now+5000), askedTerm(question)))
	if err != nil {
		return err
	}
	r.DB.ShiftClock(1001)
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if _, err := r.wakeTick(s); err != nil {
		return err
	}
	after, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if after != before {
		return Violatedf("a false evaluation took %d revisions", after-before)
	}
	wake, err := r.readWake(s, request.Wake)
	if err != nil {
		return err
	}
	if wake.NextEvalAt != now+5000 {
		return Violatedf("next_eval_at %d, want the next time in the expression, %d", wake.NextEvalAt, now+5000)
	}
	r.DB.ShiftClock(4000)
	if _, err := r.wakeTick(s); err != nil {
		return err
	}
	if wake, err = r.readWake(s, request.Wake); err != nil {
		return err
	}
	if wake.NextEvalAt != 0 {
		return Violatedf("next_eval_at %d with no time left in the expression", wake.NextEvalAt)
	}
	return r.ExpectParked(s, request)
}

func checkParkingRepeats(r *Rig) error {
	s := r.Store()
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	request, err := r.Parking(s, askedTerm(question))
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	draft := parking.Draft{ID: request.Wake, Expr: askedTerm(question)}
	if err := ops.BeginParking(r.Ctx, r.Scope, request.Holder, draft, r.worker("a")); err != nil {
		return err
	}
	if after, err := r.Revision(s, r.Scope); err != nil {
		return err
	} else if after != before {
		return Violatedf("storing a wake twice took a revision")
	}
	for range 2 {
		if _, err := ops.CommitParked(r.Ctx, r.Scope, request.Holder, request.Wake, r.worker("a")); err != nil {
			return Violatedf("a parked commit whose answer was lost must read as done: %v", err)
		}
	}
	// A retry of the first step finds the wake it stored, which decides what a restart does.
	if err := ops.BeginParking(r.Ctx, r.Scope, request.Holder, draft, r.worker("a")); err != nil {
		return Violatedf("storing a wake again after parked: %v", err)
	}
	if err := r.ExpectParked(s, request); err != nil {
		return err
	}
	return r.CheckProjection(s)
}

func checkEachEventFires(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	done, err := r.Completed(s, false)
	if err != nil {
		return err
	}
	onDone, err := r.Parked(s, fmt.Sprintf(`{%q:%q}`, parking.WorkOrderDone, done))
	if err != nil {
		return err
	}
	if _, err := ops.Accept(r.Ctx, r.Scope, done, r.Actor); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, onDone); err != nil {
		return err
	}
	const scope = "payments"
	onDecision, err := r.Parked(s, fmt.Sprintf(`{%q:%q}`, parking.DecisionRecorded, scope))
	if err != nil {
		return err
	}
	draft := questions.Decision{ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Stripe.", Rationale: "It follows.", Scope: scope}
	if _, err := ops.RecordDecision(r.Ctx, r.Scope, draft, r.Actor); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, onDecision); err != nil {
		return err
	}
	for _, kind := range []parking.Kind{parking.SigninRestored, parking.ExternalCheck} {
		request, err := r.Parked(s, fmt.Sprintf(`{%q:"key"}`, kind))
		if err != nil {
			return err
		}
		if _, err := ops.Signal(r.Ctx, r.Scope, parking.Signal{Kind: kind, Key: "key"}, r.Actor); err != nil {
			return err
		}
		if err := r.ExpectWoken(s, request); err != nil {
			return err
		}
	}
	return r.CheckWakes(s)
}

func checkSignalAfterWake(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	const key = "worker-1:claude-code"
	signal := parking.Signal{Kind: parking.UsageReset, Key: key}
	if _, err := ops.Signal(r.Ctx, r.Scope, signal, r.Actor); err != nil {
		return err
	}
	term := fmt.Sprintf(`{%q:%q}`, parking.UsageReset, key)
	request, err := r.Parked(s, term)
	if err != nil {
		return Violatedf("a reset from before this park counted: %v", err)
	}
	if err := r.ExpectParked(s, request); err != nil {
		return err
	}
	if _, err := ops.Signal(r.Ctx, r.Scope, signal, r.Actor); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, request); err != nil {
		return err
	}
	// A signal between storing a wake and the parked commit fires at that commit.
	during, err := r.Parking(s, term)
	if err != nil {
		return err
	}
	if _, err := ops.Signal(r.Ctx, r.Scope, signal, r.Actor); err != nil {
		return err
	}
	if fired, err := ops.CommitParked(r.Ctx, r.Scope, during.Holder, during.Wake, r.worker("a")); err != nil {
		return err
	} else if fired != during.Wake {
		return Violatedf("a signal during parking fired %q at the commit", fired)
	}
	if err := r.ExpectWoken(s, during); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

func checkWakeStored(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	id, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, id, "a")
	if err != nil {
		return err
	}
	bad := parking.Draft{ID: r.ID("wake"), Expr: futureExpr}
	if err := ops.BeginParking(r.Ctx, r.Scope, holder, bad, r.worker("a")); !errors.Is(err, parking.ErrUnreadable) {
		return Violatedf("a wake no build can fire was stored: %v", err)
	}
	stale := holder
	stale.Fence++
	draft := parking.Draft{ID: r.ID("wake"), Expr: `{"human_resume":true}`}
	var fenced *workorders.FencedError
	if err := ops.BeginParking(r.Ctx, r.Scope, stale, draft, r.worker("a")); !errors.As(err, &fenced) {
		return Violatedf("a stale holder stored a wake: %v", err)
	}
	old := holder
	old.Generation++
	if err := ops.BeginParking(r.Ctx, r.Scope, old, draft, r.worker("a")); !errors.As(err, &fenced) {
		return Violatedf("another generation stored a wake: %v", err)
	}
	if err := ops.BeginParking(r.Ctx, r.Scope, holder, draft, r.worker("a")); err != nil {
		return err
	}
	if _, err := ops.CommitParked(r.Ctx, r.Scope, stale, draft.ID, r.worker("a")); !errors.As(err, &fenced) {
		return Violatedf("a stale holder parked a lane: %v", err)
	}
	if err := ops.Renew(r.Ctx, r.Scope, holder.Lease, TTLMs); err != nil {
		return Violatedf("a lane that is parking must keep its lease: %v", err)
	}
	return nil
}

func checkOnlyAWakeLeavesParked(r *Rig) error {
	s := r.Store()
	request, err := r.Parked(s, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	err = r.NewOps(s).Move(r.Ctx, r.Scope, request.WorkOrder, workorders.TriggerWakeFired, r.Actor)
	if !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a move took the wake's edge: %v", err)
	}
	return r.ExpectParked(s, request)
}

func checkParkedReads(r *Rig) error {
	s := r.Store()
	request, err := r.Parked(s, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	var row ledger.LaneRow
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.Lanes().Row(request.Lane)
		return err
	})
	if err != nil {
		return err
	}
	if row.Status.ActualState != ledger.StateParked || row.Status.Liveness != "dead" {
		return Violatedf("a parked lane reads %+v", row.Status)
	}
	order, err := r.WorkOrder(s, request.WorkOrder)
	if err != nil {
		return err
	}
	if order.Fence <= request.Fence || order.ClaimID != "" || order.LeaseWorker != "" {
		return Violatedf("a parked work order holds %+v", order)
	}
	return nil
}

func checkWokenReplays(r *Rig) error {
	s := r.Store()
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	request, err := r.Parked(s, askedTerm(question))
	if err != nil {
		return err
	}
	if _, err := r.answer(s, question, "Twice."); err != nil {
		return err
	}
	if err := r.CheckProjection(s); err != nil {
		return err
	}
	if err := r.NewOps(s).Rebuild(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	order, err := r.WorkOrder(s, request.WorkOrder)
	if err != nil {
		return err
	}
	if order.ResumeLaneID != request.Lane {
		return Violatedf("a rebuild lost the lane to resume: %+v", order)
	}
	grant, err := r.NewOps(s).Claim(r.Ctx, r.Scope, r.Request(request.WorkOrder, "a"), r.worker("a"))
	if err != nil {
		return err
	}
	if grant.ResumeLaneID != request.Lane {
		return Violatedf("a claim on woken work names no lane to resume: %+v", grant)
	}
	return r.CheckWakes(s)
}

// checkAnswerStands reads a question through its decision. A later decision that replaced the
// answer leaves the question with none, and an overturn gives it the answer a person gave.
func checkAnswerStands(r *Rig) error {
	s := r.Store()
	question, err := r.Asked(s)
	if err != nil {
		return err
	}
	decision, err := r.answer(s, question, "Twice.")
	if err != nil {
		return err
	}
	replacing := questions.Decision{
		ID: r.ID("d"), Status: questions.DecisionDecided, Statement: "Three times.", Rationale: "It follows.", Supersedes: decision.ID,
	}
	if _, err := r.NewOps(s).RecordDecision(r.Ctx, r.Scope, replacing, r.Actor); err != nil {
		return err
	}
	request, err := r.Parked(s, askedTerm(question))
	if err != nil {
		return Violatedf("a replaced decision answered a question: %v", err)
	}
	if err := r.ExpectParked(s, request); err != nil {
		return err
	}
	if question, err = r.Asked(s); err != nil {
		return err
	}
	if decision, err = r.answer(s, question, "Twice."); err != nil {
		return err
	}
	if _, err := r.overturn(s, decision.ID); err != nil {
		return err
	}
	overturned, err := r.Parking(s, askedTerm(question))
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).CommitParked(r.Ctx, r.Scope, overturned.Holder, overturned.Wake, r.worker("a")); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, overturned); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

func checkResumeIsAMembers(r *Rig) error {
	s := r.Store()
	request, err := r.Parked(s, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	ops := r.NewOps(s)
	for _, actor := range []ledger.Actor{r.worker("a"), {Kind: ledger.ActorOrchestrator, ID: "o"}, {Kind: ledger.ActorLane, ID: request.Lane}} {
		if _, err := ops.ResumeLane(r.Ctx, r.Scope, request.Lane, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s resumed a lane: %v", actor.Kind, err)
		}
	}
	if _, err := ops.ResumeLane(r.Ctx, r.Scope, "no-such-lane", r.Actor); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a resume of nothing: %v", err)
	}
	if _, err := ops.ResumeLane(r.Ctx, r.Scope, request.Lane, r.Actor); err != nil {
		return err
	}
	return r.ExpectWoken(s, request)
}

func checkMergedQuestion(r *Rig) error {
	s := r.Store()
	ops := r.NewOps(s)
	merged, err := r.Asked(s)
	if err != nil {
		return err
	}
	stays, err := r.Asked(s)
	if err != nil {
		return err
	}
	request, err := r.Parked(s, askedTerm(merged))
	if err != nil {
		return err
	}
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "o"}
	if _, err := ops.Take(r.Ctx, r.Scope, merged, TTLMs, orchestrator); err != nil {
		return err
	}
	if err := ops.Merge(r.Ctx, r.Scope, merged, stays, orchestrator); err != nil {
		return err
	}
	if err := r.ExpectParked(s, request); err != nil {
		return err
	}
	if _, err := r.answer(s, stays, "Twice."); err != nil {
		return err
	}
	if err := r.ExpectWoken(s, request); err != nil {
		return err
	}
	return r.CheckWakes(s)
}

func checkOneOpenWake(r *Rig) error {
	s := r.Store()
	request, err := r.Parking(s, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	second := parking.Draft{ID: r.ID("wake"), Expr: `{"human_resume":true}`}
	err = r.NewOps(s).BeginParking(r.Ctx, r.Scope, request.Holder, second, r.worker("a"))
	if err == nil {
		return Violatedf("a lane stored a second open wake")
	}
	if _, err := r.readWake(s, second.ID); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a second wake is on record: %v", err)
	}
	return nil
}

// factBook is what the log says had happened at one revision, read with nothing but events,
// so a firing is checked against the facts recorded by then and not the facts of today.
type factBook struct {
	questions map[string]questions.Question
	decisions map[string]questions.Decision
	done      map[string]bool
	signals   map[parking.Fact][]int64
}

func newFactBook() *factBook {
	return &factBook{
		questions: map[string]questions.Question{}, decisions: map[string]questions.Decision{},
		done: map[string]bool{}, signals: map[parking.Fact][]int64{},
	}
}

func (b *factBook) take(e ledger.Event) error {
	switch e.Type {
	case questions.EventQuestionChanged:
		var record questions.Question
		if err := json.Unmarshal(e.Data, &record); err != nil {
			return err
		}
		b.questions[record.ID] = record
	case questions.EventDecisionRecorded, questions.EventDecisionEnded:
		var record questions.Decision
		if err := json.Unmarshal(e.Data, &record); err != nil {
			return err
		}
		b.decisions[record.ID] = record
	case workorders.EventType(workorders.TriggerAcceptancePassed):
		b.done[e.Subject.ID] = true
	case parking.EventSignal:
		var signal parking.Signal
		if err := json.Unmarshal(e.Data, &signal); err != nil {
			return err
		}
		fact := parking.Fact(signal)
		b.signals[fact] = append(b.signals[fact], e.Rev)
	}
	return nil
}

func (b *factBook) answered(id string) bool {
	for range len(b.questions) + 1 {
		record, ok := b.questions[id]
		if !ok {
			return false
		}
		into, merged := record.MergedInto()
		if !merged {
			decision, ok := b.decisions[record.DecisionID]
			return record.Status == questions.StatusAnswered && ok && decision.Status.InForce()
		}
		id = into
	}
	return false
}

func (b *factBook) holds(fact parking.Fact, stored int64) bool {
	switch fact.Kind {
	case parking.QuestionAnswered:
		return b.answered(fact.Key)
	case parking.WorkOrderDone:
		return b.done[fact.Key]
	case parking.DecisionRecorded:
		for _, decision := range b.decisions {
			if decision.Scope == fact.Key && decision.Status.InForce() {
				return true
			}
		}
		return false
	}
	return slices.ContainsFunc(b.signals[fact], func(rev int64) bool { return rev > stored })
}

// CheckWakes replays every wake at its firing time, against the facts recorded by then. A wake
// that was false at that moment fired too early, and one with two firings fired twice.
func (r *Rig) CheckWakes(s ports.Store) error {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	byID := map[string]parking.Wake{}
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		wakes, err := tx.Wakes().All()
		for _, wake := range wakes {
			byID[wake.ID] = wake
		}
		return err
	})
	if err != nil {
		return err
	}
	book, fired := newFactBook(), map[string]int{}
	for _, e := range events {
		if e.Type != workorders.EventType(workorders.TriggerWakeFired) {
			if err := book.take(e); err != nil {
				return err
			}
			continue
		}
		var change workorders.Change
		if err := json.Unmarshal(e.Data, &change); err != nil {
			return err
		}
		id := string(change.Payload)
		if fired[id]++; fired[id] > 1 {
			return Violatedf("wake %s fired %d times", id, fired[id])
		}
		if err := replayFiring(book, byID, id, e); err != nil {
			return err
		}
	}
	for id, wake := range byID {
		retired := strings.HasPrefix(wake.FiredBy, "retired:")
		if !wake.Open() && !retired && fired[id] != 1 {
			return Violatedf("wake %s closed by %q with %d firings", id, wake.FiredBy, fired[id])
		}
		if (wake.Open() || retired) && fired[id] != 0 {
			return Violatedf("wake %s has a firing and reads %+v", id, wake)
		}
	}
	return nil
}

func replayFiring(book *factBook, byID map[string]parking.Wake, id string, e ledger.Event) error {
	wake, ok := byID[id]
	if !ok {
		return Violatedf("a firing names no wake: %s", id)
	}
	expr, err := parking.Parse(wake.Expr, wake.Lane)
	if err != nil {
		return Violatedf("wake %s fired on an expression this build cannot read: %v", id, err)
	}
	facts := parking.Facts{}
	for _, fact := range expr.Facts() {
		facts[fact] = book.holds(fact, wake.StoredRev)
	}
	if !expr.Holds(facts, e.RecordedAt) {
		return Violatedf("wake %s fired at rev %d with its expression false", id, e.Rev)
	}
	if wake.FiredAt != e.RecordedAt {
		return Violatedf("wake %s fired at %d and its event says %s", id, wake.FiredAt, strconv.FormatInt(e.RecordedAt, 10))
	}
	return nil
}

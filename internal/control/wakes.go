package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// The operations of parking and waking.
const (
	opLaneParking = "lane.parking"
	opLaneParked  = "lane.parked"
	opWakeFire    = "wake.fire"
	opWakeSignal  = "wake.signal"
)

// maxMerges bounds how far a question's answer is followed through merges.
const maxMerges = 16

// Wakes holds the use cases of durable parking. A wake fires only for a work order that is
// `parked`, under two guards in one transaction, and every path that fires one takes the
// workspace revision first, so an answer and a parked commit cannot miss each other.
type Wakes struct {
	store ports.Store
}

// NewWakes returns the use cases over a Store.
func NewWakes(store ports.Store) *Wakes { return &Wakes{store: store} }

// BeginParking stores a lane's wake while its lease is held, so an answer during parking is
// not lost. The work order stays `running`, because renewal must cover its lease until the
// process is gone. A wake already stored decides what a restarted worker does, so a retry is
// done whatever has happened since.
func (w *Wakes) BeginParking(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, draft parking.Draft, actor ledger.Actor) error {
	if _, err := parking.Parse(draft.Expr, holder.Lane); err != nil {
		return err
	}
	return w.store.Write(ctx, scope, opLaneParking, func(tx ports.WriteTx) error {
		return beginParking(tx, holder, draft, actor, noStamp)
	})
}

// noStamp leaves an event the identity the ledger gives it.
func noStamp(*ledger.EventDraft) {}

// beginParking runs inside a write transaction, because a worker's upload stores a wake in the
// same transaction that stores its event. An expression this build cannot read is kept, because
// it may come from a newer worker, and it stays out of evaluation.
func beginParking(tx ports.WriteTx, holder workorders.Holder, draft parking.Draft, actor ledger.Actor, stamp func(*ledger.EventDraft)) error {
	_, err := tx.Wakes().Get(draft.ID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	if err := guardHolder(tx, opLaneParking, holder); err != nil {
		return err
	}
	current, err := tx.WorkOrders().Get(holder.WorkOrder)
	if err != nil {
		return err
	}
	if current.State != workorders.Running {
		return workorders.Reject(current, holder.Lease)
	}
	event, err := workorders.ChangeEvent(workorders.EventLaneParking, holder.WorkOrder, workorders.Change{
		Guarded: true, Fence: holder.Fence, ClaimID: holder.ClaimID, Lane: holder.Lane, Generation: holder.Generation,
		Reason: draft.ID, Payload: []byte(draft.Expr),
	}, actor)
	if err != nil {
		return err
	}
	stamp(&event)
	appended, err := tx.Append(event)
	if err != nil {
		return err
	}
	crashpoint.Hit("lane.parking", "recorded")
	wake := parking.Wake{ID: draft.ID, Lane: holder.Lane, Expr: draft.Expr, StoredRev: appended.Rev}
	if expr, err := parking.Parse(draft.Expr, holder.Lane); err == nil {
		wake.NextEvalAt, _ = expr.FirstTime()
	} else if !errors.Is(err, parking.ErrUnreadable) {
		return err
	}
	if _, err := tx.Wakes().Insert(wake); err != nil {
		return err
	}
	return tx.Lanes().MarkParking(holder.Lane)
}

// CommitParked records that a lane's process is gone: the work order parks, the lease is
// released, and the fence moves. The wake is evaluated in this same transaction, so a fact
// that arrived during parking fires it here, and nothing can land between the two. It returns
// the wake that fired, or nothing.
func (w *Wakes) CommitParked(ctx context.Context, scope ledger.WorkspaceID, holder workorders.Holder, commit ledger.ParkedCommit, actor ledger.Actor) (string, error) {
	fired := ""
	err := w.store.Write(ctx, scope, opLaneParked, func(tx ports.WriteTx) error {
		var err error
		fired, err = commitParked(tx, holder, commit, actor, noStamp)
		return err
	})
	if err != nil {
		return "", err
	}
	return fired, nil
}

// commitParked runs inside a write transaction, because a worker's upload commits `parked` in
// the same transaction that stores its event.
func commitParked(tx ports.WriteTx, holder workorders.Holder, commit ledger.ParkedCommit, actor ledger.Actor, stamp func(*ledger.EventDraft)) (string, error) {
	generation, err := tx.Lanes().Generation(holder.Lane)
	if err != nil {
		return "", err
	}
	if generation != holder.Generation {
		return "", parkedOrFenced(tx, holder)
	}
	won, err := tx.Leases().Park(holder.Lease)
	if err != nil {
		return "", err
	}
	if !won {
		return "", parkedOrFenced(tx, holder)
	}
	crashpoint.Hit("lane.parked", "parked")
	if err := recordParked(tx, holder, commit.Checkpoint, actor, stamp); err != nil {
		return "", err
	}
	crashpoint.Hit("lane.parked", "recorded")
	if skipsParkedEvaluation() {
		return "", nil
	}
	wake, err := tx.Wakes().Get(commit.Wake)
	if err != nil {
		return "", err
	}
	expr, err := parking.Parse(wake.Expr, wake.Lane)
	if errors.Is(err, parking.ErrUnreadable) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	outcome, err := fire(tx, wake, expr, parking.CauseParked, actor)
	if outcome.fired {
		return wake.ID, err
	}
	return "", err
}

// uploadedHolder reads who an event says it comes from, and refuses a worker that never held
// that claim.
func uploadedHolder(tx ports.WriteTx, worker string, e ledger.UploadEvent) (workorders.Holder, error) {
	if err := ranLane(tx, worker, e); err != nil {
		return workorders.Holder{}, err
	}
	return workorders.Holder{
		Lease: workorders.Lease{WorkOrder: e.WorkOrder, ClaimID: e.ClaimID, Fence: e.Fence},
		Lane:  e.Lane, Generation: e.Generation,
	}, nil
}

// parkingUpload stores what a worker's lane waits on, as the lane begins to park.
func parkingUpload(tx ports.WriteTx, worker string, e ledger.UploadEvent, stamp func(*ledger.EventDraft)) error {
	var start ledger.ParkingStart
	if err := json.Unmarshal(e.Payload, &start); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	holder, err := uploadedHolder(tx, worker, e)
	if err != nil {
		return err
	}
	actor := ledger.Actor{Kind: ledger.ActorWorker, ID: worker}
	return beginParking(tx, holder, parking.Draft{ID: start.Wake, Expr: start.Expr}, actor, stamp)
}

// parkedUpload commits `parked` with what a worker's lane left.
func parkedUpload(tx ports.WriteTx, worker string, e ledger.UploadEvent, stamp func(*ledger.EventDraft)) error {
	var commit ledger.ParkedCommit
	if err := json.Unmarshal(e.Payload, &commit); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	holder, err := uploadedHolder(tx, worker, e)
	if err != nil {
		return err
	}
	_, err = commitParked(tx, holder, commit, ledger.Actor{Kind: ledger.ActorWorker, ID: worker}, stamp)
	return err
}

// parkedOrFenced answers a commit that matched nothing. A parked commit whose answer was lost
// shows as a claim that ended parked, which is done.
func parkedOrFenced(tx ports.WriteTx, holder workorders.Holder) error {
	claim, err := tx.Claims().Find(holder.ClaimID)
	if err != nil {
		return err
	}
	if claim.Ended() && claim.EndReason == workorders.EndParked {
		return nil
	}
	current, err := tx.WorkOrders().Get(holder.WorkOrder)
	if err != nil {
		return err
	}
	return workorders.Reject(current, holder.Lease)
}

func recordParked(tx ports.WriteTx, holder workorders.Holder, checkpoint ledger.Checkpoint, actor ledger.Actor, stamp func(*ledger.EventDraft)) error {
	claim, err := tx.Claims().Find(holder.ClaimID)
	if err != nil {
		return err
	}
	if err := tx.Claims().End(holder.ClaimID, workorders.EndParked); err != nil {
		return err
	}
	if err := tx.Lanes().MarkParked(holder.Lane, ledger.HoldOf(checkpoint, claim.Worker)); err != nil {
		return err
	}
	if err := tx.Lanes().EndGeneration(holder.Lane, holder.Generation, workorders.EndParked); err != nil {
		return err
	}
	checkpoint.Lane, checkpoint.Generation = holder.Lane, holder.Generation
	left, err := tx.Lanes().Handoff(holder.Lane, holder.Generation)
	switch {
	case err == nil:
		checkpoint.HandoffArtifact = left.ArtifactID
	case !errors.Is(err, ports.ErrNotFound):
		return err
	}
	if err := tx.Lanes().SaveCheckpoint(checkpoint); err != nil {
		return err
	}
	event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerLaneParked), holder.WorkOrder, workorders.Change{
		Trigger: workorders.TriggerLaneParked, Fence: holder.Fence + 1, ClaimID: holder.ClaimID, Lane: holder.Lane, Generation: holder.Generation,
	}, actor)
	if err != nil {
		return err
	}
	stamp(&event)
	_, err = tx.Append(event)
	return err
}

// firing says how one evaluation came out.
type firing struct {
	// fired says this transaction fired the wake.
	fired bool
	// parked says its work order was parked, so a false evaluation was a real answer.
	parked bool
}

// fire evaluates one wake inside a transaction that holds the revision, and fires it under
// both guards. A wake fires only for a work order that is `parked`: before that the process
// may be alive and the lease held, and the parked commit evaluates it. The first statement
// stops a stale ticker, and the second stops racing firers. A row count of 0 from the second
// is a defect, because the state was read after the revision.
func fire(tx ports.WriteTx, wake parking.Wake, expr parking.Expr, cause string, actor ledger.Actor) (firing, error) {
	row, err := tx.Lanes().Row(wake.Lane)
	if err != nil {
		return firing{}, err
	}
	order, err := tx.WorkOrders().Get(row.WorkOrder)
	if err != nil {
		return firing{}, err
	}
	parked := order.State == workorders.Parked
	if !parked && !firesBeforeParked() {
		return firing{}, nil
	}
	facts, err := factsOf(tx, wake, expr)
	if err != nil {
		return firing{}, err
	}
	outcome := firing{parked: parked}
	if !expr.Holds(facts, evaluatedAt(tx.Now())) {
		return outcome, nil
	}
	won, err := tx.Wakes().Fire(wake.ID, cause)
	if err != nil || !won {
		return outcome, err
	}
	crashpoint.Hit("wake", "fired")
	moved, err := tx.Wakes().MoveToReady(order.ID, wake.Lane)
	if err != nil {
		return outcome, err
	}
	if !moved {
		if firesBeforeParked() {
			return outcome, nil
		}
		return outcome, fmt.Errorf("wake %s fired and %s did not move from %s", wake.ID, order.ID, order.State)
	}
	event, err := workorders.ChangeEvent(workorders.EventType(workorders.TriggerWakeFired), order.ID, workorders.Change{
		Trigger: workorders.TriggerWakeFired, Lane: wake.Lane, Reason: cause, Payload: []byte(wake.ID),
	}, actor)
	if err != nil {
		return outcome, err
	}
	_, err = tx.Append(event)
	outcome.fired = err == nil
	return outcome, err
}

// fireWakes evaluates every open wake that names one of these kinds of fact, in the
// transaction of the event that touched it. An expression is opaque text and nothing maps a
// record to its wakes, so an event reads them all (ADR-0008). One this build cannot read stays
// out of evaluation and stops no other.
func fireWakes(tx ports.WriteTx, cause string, actor ledger.Actor, kinds ...parking.Kind) ([]string, error) {
	open, err := tx.Wakes().Open()
	if err != nil {
		return nil, err
	}
	var fired []string
	for _, wake := range open {
		expr, err := parking.Parse(wake.Expr, wake.Lane)
		if errors.Is(err, parking.ErrUnreadable) {
			continue
		}
		if err != nil {
			return fired, err
		}
		if !slices.ContainsFunc(kinds, expr.Names) {
			continue
		}
		outcome, err := fire(tx, wake, expr, cause, actor)
		if err != nil {
			return fired, err
		}
		if outcome.fired {
			fired = append(fired, wake.ID)
			if firesFirstOnly() {
				break
			}
		}
	}
	return fired, nil
}

// factsOf reads what an expression asks about, from the ledger as this transaction sees it.
func factsOf(tx ports.WriteTx, wake parking.Wake, expr parking.Expr) (parking.Facts, error) {
	facts := parking.Facts{}
	for _, fact := range expr.Facts() {
		holds, err := factHolds(tx, wake, fact)
		if err != nil {
			return nil, err
		}
		facts[fact] = holds
	}
	return facts, nil
}

func factHolds(tx ports.WriteTx, wake parking.Wake, fact parking.Fact) (bool, error) {
	switch fact.Kind {
	case parking.QuestionAnswered:
		return questionAnswered(tx.Questions(), fact.Key)
	case parking.DecisionRecorded:
		return decisionRecorded(tx.Questions(), fact.Key)
	case parking.WorkOrderDone:
		order, err := tx.WorkOrders().Get(fact.Key)
		if errors.Is(err, ports.ErrNotFound) {
			return false, nil
		}
		return order.State == workorders.Done, err
	case parking.ApprovalDecided:
		// Approvals have no record yet, so nothing can make this term true.
		return false, nil
	}
	return tx.Wakes().Signaled(fact.Kind, fact.Key, wake.StoredRev)
}

// questionAnswered is true when a question has an accepted decision. An overturned one answers
// nothing, and a question merged into another waits on that one's answer.
func questionAnswered(reader ports.QuestionReader, id string) (bool, error) {
	for range maxMerges {
		question, err := reader.Question(id)
		if errors.Is(err, ports.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		into, merged := question.MergedInto()
		if merged {
			id = into
			continue
		}
		if question.Status != questions.StatusAnswered {
			return false, nil
		}
		decision, err := reader.Decision(question.DecisionID)
		if err != nil {
			return false, err
		}
		return decision.Status.InForce(), nil
	}
	return false, nil
}

// decisionRecorded is true when a decision in force names exactly this scope.
func decisionRecorded(reader ports.QuestionReader, scope string) (bool, error) {
	decisions, err := reader.Decisions(ports.RecordFilter{Scope: scope, Limit: ports.MaxRecords})
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(decisions, func(decision questions.Decision) bool {
		return decision.Scope == scope && decision.Status.InForce()
	}), nil
}

// DueWakes lists the open wakes whose time has come. It takes no lock, so a stale list is
// harmless: the guarded statements decide each one. It names no window, so a timer a long
// downtime passed still comes due.
func (w *Wakes) DueWakes(ctx context.Context, scope ledger.WorkspaceID) ([]string, error) {
	var due []string
	err := w.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		due, err = tx.Wakes().Due(evaluatedAt(now))
		return err
	})
	return due, err
}

// FireDue evaluates one due wake in a short transaction of its own, so one wake stops no
// other. After a false evaluation of a parked lane, `next_eval_at` moves on in a statement of
// its own, so the tick does not read the same wake forever. One this build cannot read stays
// due, and so does one whose lane is still parking, which the parked commit evaluates.
func (w *Wakes) FireDue(ctx context.Context, scope ledger.WorkspaceID, id string, actor ledger.Actor) (bool, error) {
	var outcome firing
	var next int64
	err := w.store.Write(ctx, scope, opWakeFire, func(tx ports.WriteTx) error {
		wake, err := tx.Wakes().Get(id)
		if err != nil {
			return err
		}
		expr, err := parking.Parse(wake.Expr, wake.Lane)
		if err != nil {
			if errors.Is(err, parking.ErrUnreadable) && !stopsOnUnreadable() {
				return nil
			}
			return err
		}
		if outcome, err = fire(tx, wake, expr, parking.CauseTick, actor); err != nil {
			return err
		}
		next, _ = expr.NextTime(evaluatedAt(tx.Now()))
		return nil
	})
	if err != nil {
		return false, err
	}
	if !outcome.parked || outcome.fired || keepsNextEval() {
		return outcome.fired, nil
	}
	return false, w.store.ScheduleWake(ctx, scope, id, next)
}

// WakeTick fires what is due, each wake in its own transaction, and it holds no state of its
// own, so a timer fires on the first tick after any downtime.
func (w *Wakes) WakeTick(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	due, err := w.DueWakes(ctx, scope)
	if err != nil {
		return nil, err
	}
	crashpoint.Hit("wake.tick", "listed")
	var fired []string
	for _, id := range due {
		won, err := w.FireDue(ctx, scope, id, actor)
		if err != nil {
			return fired, err
		}
		if won {
			fired = append(fired, id)
		}
	}
	return fired, nil
}

// Signal records a fact that has no record of its own and fires the wakes that wait on it, in
// one transaction. One signal reaches every lane that names it. A person's resume has a use
// case of its own, because only a member can say that.
func (w *Wakes) Signal(ctx context.Context, scope ledger.WorkspaceID, signal parking.Signal, actor ledger.Actor) ([]string, error) {
	if signal.Kind == parking.HumanResume {
		return nil, workorders.ErrForbidden
	}
	var fired []string
	err := w.store.Write(ctx, scope, opWakeSignal, func(tx ports.WriteTx) error {
		var err error
		fired, err = signalIn(tx, signal, actor)
		return err
	})
	return fired, err
}

// ResumeLane is a member waking a parked lane, which a `human_resume` term waits on.
func (w *Wakes) ResumeLane(ctx context.Context, scope ledger.WorkspaceID, lane string, actor ledger.Actor) ([]string, error) {
	if !authority.Allowed(actor, authority.ActionResumeLane) {
		return nil, workorders.ErrForbidden
	}
	var fired []string
	err := w.store.Write(ctx, scope, opWakeSignal, func(tx ports.WriteTx) error {
		if _, err := tx.Lanes().Row(lane); err != nil {
			return err
		}
		var err error
		fired, err = signalIn(tx, parking.Signal{Kind: parking.HumanResume, Key: lane}, actor)
		return err
	})
	return fired, err
}

func signalIn(tx ports.WriteTx, signal parking.Signal, actor ledger.Actor) ([]string, error) {
	data, err := json.Marshal(signal)
	if err != nil {
		return nil, err
	}
	event, err := tx.Append(ledger.EventDraft{
		Type: parking.EventSignal, Actor: actor, Data: data,
		Subject: ledger.Subject{Kind: parking.SubjectSignal, ID: string(signal.Kind) + ":" + signal.Key},
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Wakes().Signal(signal, event.Rev); err != nil {
		return nil, err
	}
	crashpoint.Hit("wake.signal", "recorded")
	return fireWakes(tx, string(signal.Kind), actor, signal.Kind)
}

// retireWakes closes what a work order's lanes left open, in the transaction that ends them.
// Otherwise an answer to an old lane's question could wake a new owner's work order with the
// wrong lane, and an event-only wake would stay in every later evaluation.
func retireWakes(tx ports.WriteTx, workOrder, cause string) error {
	if keepsWakesOpen() {
		return nil
	}
	return tx.Wakes().Retire(workOrder, cause)
}

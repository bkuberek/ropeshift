package contract

import (
	"encoding/json"
	"sync/atomic"
	"time"

	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// ParkChecks are what the ledger promises a worker that parks a lane by upload (FR-PARK-1,
// FR-PARK-2). A worker with no connection runs the same sequence and uploads it on return, so
// the upload is the production path of everything the wake checks hold.
var ParkChecks = []Check{
	{"P1", "an upload stores a lane's wake once, and the work order stays running", checkParkingUpload},
	{"P2", "an upload commits parked, releases the lease, and moves the fence", checkParkedUpload},
	{"P3", "a parked commit fires what already holds, in its own transaction", checkParkedFires},
	{"P4", "a parked commit keeps its checkpoint, and work no remote holds binds the lane", checkParkedCheckpoint},
	{"P5", "a commit after a lapse waits for the worker to return", checkParkedAfterLapse},
	{"P6", "a commit whose answer was lost stores nothing twice", checkParkedRepeat},
	{"P7", "a worker that does not hold the claim cannot park it", checkParkingOthers},
	{"P8", "an expression this build cannot read stays stored", checkParkingUnreadable},
	{"P9", "parking replays", checkParkingReplays},
	{"P10", "an upload and an answer held at the moment that loses a wake still fire it", checkUploadAnswerRace},
}

func (s *statusRig) parkingUpload(wake, expr string) (ledger.UploadReply, error) {
	raw, err := json.Marshal(ledger.ParkingStart{Wake: wake, Expr: expr})
	if err != nil {
		return ledger.UploadReply{}, err
	}
	return s.uploadAt(1, ledger.UploadLaneParking, raw)
}

func (s *statusRig) parkedUpload(wake string, pushed bool) (ledger.UploadReply, error) {
	raw, err := json.Marshal(ledger.ParkedCommit{Wake: wake, Checkpoint: ledger.Checkpoint{
		Lane: s.lane, Generation: 1, Branch: "lane/x", Head: "abc", SnapshotRef: "refs/ropeshift/checkpoints/x/1", Pushed: pushed,
		PendingInstruction: "Finish the retry",
	}})
	if err != nil {
		return ledger.UploadReply{}, err
	}
	return s.uploadAt(1, ledger.UploadLaneParked, raw)
}

func (s *statusRig) wake(id string) (parking.Wake, error) {
	var out parking.Wake
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Wakes().Get(id)
		return err
	})
	return out, err
}

func refusedOnly(reply ledger.UploadReply, err error, status string) error {
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != status {
		return Violatedf("want one %s refusal, got %+v", status, reply.Rejected)
	}
	return nil
}

func accepted(reply ledger.UploadReply, err error) error {
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 0 {
		return Violatedf("refused: %+v", reply.Rejected)
	}
	return nil
}

// parkingOn is a rig whose lane has stored a wake on a human's word, which nothing else fires.
func parkingOn(r *Rig, expr string) (*statusRig, string, error) {
	s, err := newStatusRig(r)
	if err != nil {
		return nil, "", err
	}
	wake := r.ID("wake")
	return s, wake, accepted(s.parkingUpload(wake, expr))
}

func checkParkingUpload(r *Rig) error {
	s, wake, err := parkingOn(r, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	if err := accepted(s.parkingUpload(wake, `{"human_resume":true}`)); err != nil {
		return err
	}
	stored, err := s.wake(wake)
	if err != nil {
		return Violatedf("the wake is not stored: %v", err)
	}
	if stored.Lane != s.lane || !stored.Open() {
		return Violatedf("wake %+v", stored)
	}
	row, err := r.WorkOrder(s.store, s.grant.WorkOrder)
	if err != nil {
		return err
	}
	if row.State != workorders.Running {
		return Violatedf("a lane storing its wake moved its work order to %s", row.State)
	}
	return nil
}

func checkParkedUpload(r *Rig) error {
	s, wake, err := parkingOn(r, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	row, err := r.WorkOrder(s.store, s.grant.WorkOrder)
	if err != nil {
		return err
	}
	if row.State != workorders.Parked || row.ClaimID != "" || row.LeaseWorker != "" || row.Fence != s.grant.Fence+1 {
		return Violatedf("after parking: %+v", row)
	}
	view, err := s.plane.Lane(r.Ctx, s.lane)
	if err != nil {
		return err
	}
	if view.ActualState != ledger.StateParked || view.Freshness != ledger.FreshnessObserved {
		return Violatedf("the lane reads %+v", view)
	}
	stored, err := s.wake(wake)
	if err != nil {
		return err
	}
	if !stored.Open() {
		return Violatedf("a wake that holds nothing fired: %+v", stored)
	}
	return nil
}

func checkParkedFires(r *Rig) error {
	question, err := r.Asked(r.Store())
	if err != nil {
		return err
	}
	s, wake, err := parkingOn(r, askedTerm(question))
	if err != nil {
		return err
	}
	if _, err := r.answer(s.store, question, "Twice."); err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	return r.ExpectWoken(s.store, ParkRequest{Holder: workorders.Holder{Lease: s.grant.Lease, Lane: s.lane}, Wake: wake})
}

func checkParkedCheckpoint(r *Rig) error {
	for _, pushed := range []bool{true, false} {
		s, wake, err := parkingOn(r, `{"human_resume":true}`)
		if err != nil {
			return err
		}
		if err := accepted(s.parkedUpload(wake, pushed)); err != nil {
			return err
		}
		var checkpoint ledger.Checkpoint
		var row ledger.LaneRow
		err = s.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			var err error
			if checkpoint, err = tx.Lanes().Checkpoint(s.lane, 1); err != nil {
				return err
			}
			row, err = tx.Lanes().Row(s.lane)
			return err
		})
		if err != nil {
			return Violatedf("no checkpoint: %v", err)
		}
		if checkpoint.PendingInstruction != "Finish the retry" || checkpoint.Pushed != pushed || checkpoint.CreatedAt == 0 {
			return Violatedf("checkpoint %+v", checkpoint)
		}
		want := ledger.AffinityRequired
		if pushed {
			want = ledger.AffinityPreferred
		}
		if row.Hold.Affinity != want || row.Hold.Worker != s.auth.WorkerID {
			return Violatedf("pushed %v holds the lane as %+v", pushed, row.Hold)
		}
	}
	return nil
}

func checkParkedAfterLapse(r *Rig) error {
	s, wake, err := parkingOn(r, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	if err := r.Expire(s.store); err != nil {
		return err
	}
	reply, err := s.parkedUpload(wake, true)
	if err != nil {
		return err
	}
	if reply.HighWater != 0 || len(reply.Rejected) != 0 {
		return Violatedf("a commit on a lapsed lease answered %+v, and it must wait for the return", reply)
	}
	row, err := r.WorkOrder(s.store, s.grant.WorkOrder)
	if err != nil {
		return err
	}
	if row.State != workorders.Lapsed {
		return Violatedf("a lapsed lease read %s", row.State)
	}
	if err := r.NewOps(s.store).Return(r.Ctx, r.Scope, s.grant.Lease, TTLMs, r.Actor); err != nil {
		return err
	}
	s.seq--
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	return r.ExpectParked(s.store, ParkRequest{Holder: workorders.Holder{Lease: s.grant.Lease}})
}

func checkParkedRepeat(r *Rig) error {
	s, wake, err := parkingOn(r, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return err
	}
	s.seq--
	if err := accepted(s.parkedUpload(wake, true)); err != nil {
		return Violatedf("the same event again: %v", err)
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	parked := 0
	for _, e := range events {
		if e.Type == workorders.EventType(workorders.TriggerLaneParked) {
			parked++
		}
	}
	if parked != 1 {
		return Violatedf("%d parked events", parked)
	}
	return nil
}

func checkParkingOthers(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	stranger, err := newStatusRig(r)
	if err != nil {
		return err
	}
	stranger.lane, stranger.grant = s.lane, s.grant
	reply, err := stranger.parkingUpload(r.ID("wake"), `{"human_resume":true}`)
	return refusedOnly(reply, err, ledger.UploadInvalid)
}

func checkParkingUnreadable(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	wake := r.ID("wake")
	const newer = `{"from_a_later_build":true}`
	if err := accepted(s.parkingUpload(wake, newer)); err != nil {
		return err
	}
	stored, err := s.wake(wake)
	if err != nil {
		return Violatedf("an expression from a later build was lost: %v", err)
	}
	if stored.Expr != newer {
		return Violatedf("wake %+v", stored)
	}
	return accepted(s.parkedUpload(wake, true))
}

// checkUploadAnswerRace holds an uploaded commit and an answer after their writes and before
// their evaluations, which is the interleaving that loses a wake when nothing serializes them,
// and it runs the production path of a commit, which is an upload.
func checkUploadAnswerRace(r *Rig) error {
	var current atomic.Pointer[barrier]
	hook := func(op, name string) {
		if name == StepBeforeEval && (op == opUpload || op == opAnswer) {
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
		s, err := newStatusRig(r)
		if err != nil {
			return err
		}
		s.store = parker
		s.plane = control.NewWorkers(parker, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
		wake := r.ID("wake")
		if err := accepted(s.parkingUpload(wake, askedTerm(question))); err != nil {
			return err
		}
		err = parallelly(2, func(i int) error {
			if i == 0 {
				return accepted(s.parkedUpload(wake, true))
			}
			_, err := r.answer(answerer, question, "Twice.")
			return err
		})
		if err != nil {
			return err
		}
		return r.ExpectWoken(parker, ParkRequest{Holder: workorders.Holder{Lease: s.grant.Lease, Lane: s.lane}, Wake: wake})
	}
	for range 8 {
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

func checkParkingReplays(r *Rig) error {
	question, err := r.Asked(r.Store())
	if err != nil {
		return err
	}
	s, wake, err := parkingOn(r, askedTerm(question))
	if err != nil {
		return err
	}
	if err := accepted(s.parkedUpload(wake, false)); err != nil {
		return err
	}
	answer := questions.Answer{DecisionID: r.ID("d"), Statement: "Twice.", Rationale: "It follows."}
	if _, err := r.NewOps(s.store).Answer(r.Ctx, r.Scope, question, answer, r.Actor); err != nil {
		return err
	}
	if err := r.CheckProjection(s.store); err != nil {
		return err
	}
	return r.CheckWakes(s.store)
}

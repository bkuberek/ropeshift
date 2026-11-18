package contract

import (
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ScheduledStartChecks hold a start a member scheduled for a sign-in's usage reset: it starts the orchestrator once the
// worker says the limit is gone after the reset, waits for the next reported reset when the limit still holds, does
// nothing once canceled or for an orchestrator that already runs, and survives a restart.
var ScheduledStartChecks = []Check{
	{"SS4", "a start scheduled at a usage limit starts the orchestrator at the reset", checkScheduledStartFires},
	{"SS5", "a start whose reset was stale waits for the next reported reset, and says so", checkScheduledStartWaitsForNext},
	{"SS6", "a canceled start starts nothing", checkScheduledStartCanceled},
	{"SS7", "a start for an orchestrator that already runs does nothing", checkScheduledStartAlreadyRuns},
	{"SS8", "a scheduled start fires after a restart", checkScheduledStartFiresAfterRestart},
	{"SS9", "only a member schedules a start, and only while the sign-in is at its limit", checkScheduledStartRules},
}

const limitHour = int64(60 * 60 * 1000)

// limitWorld is one worker whose reports a check writes, and the use cases that schedule its orchestrator.
type limitWorld struct {
	r      *Rig
	store  ports.Store
	plane  *control.Workers
	auth   ledger.WorkerAuth
	epoch  int64
	beats  int
	member ledger.Actor
}

func newLimitWorld(r *Rig, store ports.Store) (*limitWorld, error) {
	w := &limitWorld{r: r, store: store, member: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}}
	w.plane = control.NewWorkers(store, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
	token, err := w.plane.CreateJoinToken(r.Ctx, w.member)
	if err != nil {
		return nil, err
	}
	worker := r.ID("worker")
	credential, err := w.plane.Register(r.Ctx, ledger.Registration{JoinToken: token, WorkerID: worker, Name: "w"})
	if err != nil {
		return nil, err
	}
	w.auth = ledger.WorkerAuth{WorkerID: worker, Credential: credential.Credential}
	return w, w.open()
}

func (w *limitWorld) open() error {
	session, err := w.plane.OpenSession(w.r.Ctx, ledger.SessionRequest{Auth: w.auth, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest})
	w.epoch = session.Epoch
	return err
}

// LimitedWorker registers a worker whose sign-in reports a usage limit that resets an hour from now, for a check of
// another package that schedules its orchestrator's start.
func LimitedWorker(r *Rig, store ports.Store) (string, error) {
	w, err := newLimitWorld(r, store)
	if err != nil {
		return "", err
	}
	now, err := w.now()
	if err != nil {
		return "", err
	}
	return w.auth.WorkerID, w.report(ledger.SignInUsage{Limited: true, Window: ledger.UsageWindowSession, ResetsAtMs: now + limitHour, Source: ledger.UsageFromSession})
}

func (w *limitWorld) now() (int64, error) {
	var now int64
	err := w.store.Installation(w.r.Scope).Read(w.r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		now, err = tx.Now()
		return err
	})
	return now, err
}

// report is a heartbeat whose resource report says this of the sign-in.
func (w *limitWorld) report(usage ledger.SignInUsage) error {
	w.beats++
	report := ledger.ResourceReport{IntervalMs: ledger.MinReportIntervalMs, Sessions: []ledger.SessionResources{}, SignIn: &usage}
	reply, err := w.plane.Heartbeat(w.r.Ctx, ledger.Heartbeat{Auth: w.auth, Epoch: w.epoch, Protocol: ledger.ProtocolVersion, Nonce: fmt.Sprintf("n%d", w.beats), Report: &report})
	if err == nil && reply.ReportError != "" {
		err = fmt.Errorf("the report was not kept: %s", reply.ReportError)
	}
	return err
}

// limitedFor reports a limit that resets this far from now, and returns the reset.
func (w *limitWorld) limitedFor(ms int64) (int64, error) {
	now, err := w.now()
	if err != nil {
		return 0, err
	}
	return now + ms, w.report(ledger.SignInUsage{Limited: true, Window: ledger.UsageWindowSession, ResetsAtMs: now + ms, Source: ledger.UsageFromSession})
}

func (w *limitWorld) free() error {
	return w.report(ledger.SignInUsage{Window: ledger.UsageWindowSession})
}

func (w *limitWorld) step() ([]string, error) {
	return w.plane.StartScheduled(w.r.Ctx, ledger.Actor{Kind: ledger.ActorSystem, ID: "start-tick"})
}

func (w *limitWorld) scheduled() (ledger.ScheduledStart, error) {
	var start ledger.ScheduledStart
	err := w.store.Installation(w.r.Scope).Read(w.r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		start, err = tx.Workers().ScheduledStart(w.auth.WorkerID)
		return err
	})
	return start, err
}

func (w *limitWorld) orchestrator() (ledger.Orchestrator, error) {
	var row ledger.Orchestrator
	err := w.store.Installation(w.r.Scope).Read(w.r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		row, err = tx.Workers().Orchestrator(w.auth.WorkerID)
		return err
	})
	return row, err
}

// startedOnce says the orchestrator was started once by the scheduled start, and the start is gone.
func (w *limitWorld) startedOnce(started []string) error {
	if len(started) != 1 || started[0] != w.auth.WorkerID {
		return Violatedf("the tick started %v, want %s", started, w.auth.WorkerID)
	}
	row, err := w.orchestrator()
	if err != nil {
		return err
	}
	if row.Desired != ledger.OrchestratorRunning || row.Run != 1 {
		return Violatedf("the orchestrator reads %+v, want one run asked for", row)
	}
	if _, err := w.scheduled(); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("the scheduled start is still there: %v", err)
	}
	return nil
}

func checkScheduledStartFires(r *Rig) error {
	w, err := newLimitWorld(r, r.Store())
	if err != nil {
		return err
	}
	reset, err := w.limitedFor(limitHour)
	if err != nil {
		return err
	}
	start, err := w.plane.ScheduleOrchestrator(r.Ctx, w.auth.WorkerID, w.member)
	if err != nil {
		return err
	}
	if start.StartsAt != reset || start.ScheduledBy != w.member.ID {
		return Violatedf("the start reads %+v, want it at the reset %d", start, reset)
	}
	if started, err := w.step(); err != nil || len(started) != 0 {
		return Violatedf("before the reset the tick started %v: %v", started, err)
	}
	r.DB.ShiftClock(limitHour + 1)
	if started, err := w.step(); err != nil || len(started) != 0 {
		return Violatedf("with no word from the worker since the reset the tick started %v: %v", started, err)
	}
	if err := w.free(); err != nil {
		return err
	}
	started, err := w.step()
	if err != nil {
		return err
	}
	return w.startedOnce(started)
}

func checkScheduledStartWaitsForNext(r *Rig) error {
	w, err := newLimitWorld(r, r.Store())
	if err != nil {
		return err
	}
	if _, err := w.limitedFor(limitHour); err != nil {
		return err
	}
	if _, err := w.plane.ScheduleOrchestrator(r.Ctx, w.auth.WorkerID, w.member); err != nil {
		return err
	}
	r.DB.ShiftClock(limitHour + 1)
	if err := w.report(ledger.SignInUsage{Limited: true, Window: ledger.UsageWindowSession}); err != nil {
		return err
	}
	if started, err := w.step(); err != nil || len(started) != 0 {
		return Violatedf("a stale reset started %v: %v", started, err)
	}
	waiting, err := w.scheduled()
	if err != nil {
		return err
	}
	if waiting.StartsAt != 0 || waiting.Missed != 1 {
		return Violatedf("a start whose reset was stale reads %+v, want it waiting for the next reset", waiting)
	}
	next, err := w.limitedFor(2 * limitHour)
	if err != nil {
		return err
	}
	if _, err := w.step(); err != nil {
		return err
	}
	if moved, err := w.scheduled(); err != nil || moved.StartsAt != next || moved.Missed != 1 {
		return Violatedf("after the next reset was reported the start reads %+v, %v", moved, err)
	}
	r.DB.ShiftClock(2*limitHour + 1)
	if err := w.free(); err != nil {
		return err
	}
	started, err := w.step()
	if err != nil {
		return err
	}
	return w.startedOnce(started)
}

func checkScheduledStartCanceled(r *Rig) error {
	w, err := newLimitWorld(r, r.Store())
	if err != nil {
		return err
	}
	if _, err := w.limitedFor(limitHour); err != nil {
		return err
	}
	if _, err := w.plane.ScheduleOrchestrator(r.Ctx, w.auth.WorkerID, w.member); err != nil {
		return err
	}
	if err := w.plane.CancelScheduledStart(r.Ctx, w.auth.WorkerID, w.member); err != nil {
		return err
	}
	if err := w.plane.CancelScheduledStart(r.Ctx, w.auth.WorkerID, w.member); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a second cancel: %v", err)
	}
	r.DB.ShiftClock(limitHour + 1)
	if err := w.free(); err != nil {
		return err
	}
	if started, err := w.step(); err != nil || len(started) != 0 {
		return Violatedf("a canceled start started %v: %v", started, err)
	}
	if _, err := w.orchestrator(); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a canceled start asked for an orchestrator: %v", err)
	}
	return nil
}

func checkScheduledStartAlreadyRuns(r *Rig) error {
	w, err := newLimitWorld(r, r.Store())
	if err != nil {
		return err
	}
	if _, err := w.limitedFor(limitHour); err != nil {
		return err
	}
	if _, err := w.plane.ScheduleOrchestrator(r.Ctx, w.auth.WorkerID, w.member); err != nil {
		return err
	}
	if _, err := w.plane.SetOrchestrator(r.Ctx, w.auth.WorkerID, ledger.OrchestratorRunning, w.member); err != nil {
		return err
	}
	r.DB.ShiftClock(limitHour + 1)
	if err := w.free(); err != nil {
		return err
	}
	if started, err := w.step(); err != nil || len(started) != 0 {
		return Violatedf("a start for an orchestrator that runs started %v: %v", started, err)
	}
	row, err := w.orchestrator()
	if err != nil {
		return err
	}
	if row.Run != 1 {
		return Violatedf("the orchestrator that ran was started again: run %d", row.Run)
	}
	if _, err := w.scheduled(); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("the start for an orchestrator that runs is still there: %v", err)
	}
	return nil
}

func checkScheduledStartFiresAfterRestart(r *Rig) error {
	if !r.DB.Restartable {
		return nil
	}
	first := r.Store()
	w, err := newLimitWorld(r, first)
	if err != nil {
		return err
	}
	if _, err := w.limitedFor(limitHour); err != nil {
		return err
	}
	if _, err := w.plane.ScheduleOrchestrator(r.Ctx, w.auth.WorkerID, w.member); err != nil {
		return err
	}
	if err := first.Close(); err != nil {
		return err
	}
	w.store = r.Store()
	w.plane = control.NewWorkers(w.store, r.Scope, control.WorkerOptions{HeartbeatMs: statusHeartbeatMs, LeaseTTLMs: TTLMs})
	if err := w.open(); err != nil {
		return err
	}
	r.DB.ShiftClock(limitHour + 1)
	if err := w.free(); err != nil {
		return err
	}
	started, err := w.step()
	if err != nil {
		return err
	}
	return w.startedOnce(started)
}

func checkScheduledStartRules(r *Rig) error {
	w, err := newLimitWorld(r, r.Store())
	if err != nil {
		return err
	}
	if err := w.free(); err != nil {
		return err
	}
	if _, err := w.plane.ScheduleOrchestrator(r.Ctx, w.auth.WorkerID, w.member); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a start scheduled with no limit: %v", err)
	}
	if _, err := w.limitedFor(limitHour); err != nil {
		return err
	}
	for _, actor := range []ledger.Actor{{Kind: ledger.ActorOrchestrator, ID: w.auth.WorkerID}, {Kind: ledger.ActorLane, ID: "lane-1"}, {Kind: ledger.ActorSystem, ID: "tick"}} {
		if _, err := w.plane.ScheduleOrchestrator(r.Ctx, w.auth.WorkerID, actor); !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s scheduled a start: %v", actor.Kind, err)
		}
	}
	if _, err := w.plane.ScheduleOrchestrator(r.Ctx, "worker-nobody", w.member); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a start for a worker nobody registered: %v", err)
	}
	return nil
}

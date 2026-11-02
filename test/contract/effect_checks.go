package contract

import (
	"encoding/json"
	"errors"

	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// EffectChecks are what the ledger promises about an effect nothing can observe: a person sees it, only a
// person decides it, and the worker that holds it hears the decision until it says it acted.
var EffectChecks = []Check{
	{"EF1", "a worker's word puts an effect in front of a person", checkEffectRaised},
	{"EF2", "only a member decides an effect", checkOnlyMemberDecidesEffect},
	{"EF3", "the store refuses a decision with no member behind it", checkStoreRefusesEffectDecider},
	{"EF4", "a decision reaches the worker that holds the effect and no other", checkDecisionReachesItsWorker},
	{"EF5", "a worker's settlement stops the offer", checkSettlementStopsTheOffer},
	{"EF6", "a decision sent twice is one decision, and a different one is refused", checkEffectDecidedOnce},
	{"EF7", "an effect of an ended lane cannot run again", checkEndedLaneCannotRepeat},
	{"EF8", "a worker settles only what it holds", checkWorkerSettlesItsOwn},
	{"EF9", "every effect replays from the log", checkEffectsReplay},
	{"EF10", "a retry a crash cut short asks again", checkInterruptedRetryAsksAgain},
	{"EF11", "a decision wakes the worker's next read of desired state", checkDecisionMovesTheRevision},
}

const (
	aHookKey   = "lane-1/1/hook/lane_created"
	anInputKey = "lane-1/2/input/nudge"
)

// raiseEffect raises an effect of this rig's lane, as a worker's upload does, and returns what the
// ledger holds.
func (s *statusRig) raiseEffect(key string, attempt int64) (ledger.UnknownEffect, error) {
	raw, err := json.Marshal(ledger.EffectReport{Key: key, Kind: "input", Target: "the session", Attempt: attempt})
	if err != nil {
		return ledger.UnknownEffect{}, err
	}
	reply, err := s.upload(ledger.UploadEffectUnknown, raw)
	if err != nil {
		return ledger.UnknownEffect{}, err
	}
	if len(reply.Rejected) != 0 {
		return ledger.UnknownEffect{}, Violatedf("the report was refused: %+v", reply.Rejected)
	}
	rows, err := control.NewEffects(s.store).List(s.r.Ctx, s.r.Scope)
	if err != nil {
		return ledger.UnknownEffect{}, err
	}
	for _, row := range rows {
		if row.Key == key && row.Attempt == attempt {
			return row, nil
		}
	}
	return ledger.UnknownEffect{}, Violatedf("no effect %s attempt %d among %d", key, attempt, len(rows))
}

func (s *statusRig) decideEffect(id string, outcome ledger.EffectOutcome) (ledger.UnknownEffect, error) {
	return control.NewEffects(s.store).Decide(s.r.Ctx, s.r.Scope, ledger.EffectDecision{ID: id, Outcome: outcome}, theMember)
}

func (s *statusRig) settleEffect(id string) (ledger.UploadReply, error) {
	raw, err := json.Marshal(ledger.EffectSettlement{ID: id})
	if err != nil {
		return ledger.UploadReply{}, err
	}
	return s.upload(ledger.UploadEffectSettled, raw)
}

func (s *statusRig) desiredEffects() ([]ledger.DesiredEffect, error) {
	state, err := s.plane.Desired(s.r.Ctx, ledger.DesiredRequest{Auth: s.auth})
	return state.Effects, err
}

func checkEffectRaised(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	switch {
	case !row.Waiting():
		return Violatedf("a new effect is not waiting: %+v", row)
	case row.Lane != s.lane || row.WorkOrder != s.grant.WorkOrder || row.WorkerID != s.auth.WorkerID:
		return Violatedf("the effect names the wrong place: %+v", row)
	case row.Generation != 1 || row.Target != "the session" || row.Kind != "input":
		return Violatedf("the effect lost what the worker said: %+v", row)
	case row.RaisedAt == 0:
		return Violatedf("nothing says how long it has waited: %+v", row)
	}
	return nil
}

func checkOnlyMemberDecidesEffect(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: s.auth.WorkerID},
		{Kind: ledger.ActorWorker, ID: s.auth.WorkerID},
		{Kind: ledger.ActorLane, ID: s.lane},
		{Kind: ledger.ActorSystem, ID: "tick"},
	} {
		_, err := control.NewEffects(s.store).Decide(r.Ctx, r.Scope, ledger.EffectDecision{ID: row.ID, Outcome: ledger.EffectHappened}, actor)
		if !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s decided an effect: %v", actor.Kind, err)
		}
	}
	if _, err := s.decideEffect(row.ID, ledger.EffectHappened); err != nil {
		return err
	}
	return nil
}

func checkStoreRefusesEffectDecider(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	decided := ledger.EffectDecided{Outcome: ledger.EffectHappened, DecidedBy: "worker-1", DecidedAt: 1}
	err = s.store.Write(r.Ctx, r.Scope, "effect.forged", func(tx ports.WriteTx) error {
		_, err := tx.Effects().Decide(row.ID, decided, string(ledger.ActorOrchestrator))
		return err
	})
	if err == nil {
		return Violatedf("the store kept a decision no member stands behind")
	}
	return nil
}

func checkDecisionReachesItsWorker(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	if _, err := s.decideEffect(row.ID, ledger.EffectRetry); err != nil {
		return err
	}
	offered, err := s.desiredEffects()
	if err != nil {
		return err
	}
	want := ledger.DesiredEffect{ID: row.ID, Key: anInputKey, Attempt: 1, Outcome: ledger.EffectRetry}
	if len(offered) != 1 || offered[0] != want {
		return Violatedf("offered %+v, want %+v", offered, want)
	}
	other, err := newStatusRigOn(r, s.store, r.Ready)
	if err != nil {
		return err
	}
	theirs, err := other.desiredEffects()
	if err != nil {
		return err
	}
	if len(theirs) != 0 {
		return Violatedf("another worker heard of it: %+v", theirs)
	}
	return nil
}

func checkSettlementStopsTheOffer(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	if _, err := s.decideEffect(row.ID, ledger.EffectHappened); err != nil {
		return err
	}
	if reply, err := s.settleEffect(row.ID); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("the settlement was refused: %+v, %v", reply.Rejected, err)
	}
	offered, err := s.desiredEffects()
	if err != nil {
		return err
	}
	if len(offered) != 0 {
		return Violatedf("still offered: %+v", offered)
	}
	got, err := control.NewEffects(s.store).Get(r.Ctx, r.Scope, row.ID)
	if err != nil {
		return err
	}
	if got.SettledAt == 0 {
		return Violatedf("nothing says the worker acted: %+v", got)
	}
	return nil
}

func checkEffectDecidedOnce(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	first, err := s.decideEffect(row.ID, ledger.EffectHappened)
	if err != nil {
		return err
	}
	again, err := s.decideEffect(row.ID, ledger.EffectHappened)
	if err != nil {
		return Violatedf("a retry was refused: %v", err)
	}
	if again.DecidedAt != first.DecidedAt {
		return Violatedf("a retry decided again: %+v, then %+v", first, again)
	}
	if _, err := s.decideEffect(row.ID, ledger.EffectRetry); !errors.Is(err, ledger.ErrEffectDecided) {
		return Violatedf("a second word replaced the first: %v", err)
	}
	return nil
}

func checkEndedLaneCannotRepeat(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	result, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "crashed"}})
	if err != nil {
		return err
	}
	if reply, err := s.upload(ledger.UploadLaneEnded, result); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("the lane did not end: %+v, %v", reply.Rejected, err)
	}
	if _, err := s.decideEffect(row.ID, ledger.EffectRetry); !errors.Is(err, ledger.ErrEffectCannotRepeat) {
		return Violatedf("an effect of an ended lane was set to run again: %v", err)
	}
	if _, err := s.decideEffect(row.ID, ledger.EffectNotHappened); err != nil {
		return err
	}
	return nil
}

func checkWorkerSettlesItsOwn(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	if _, err := s.decideEffect(row.ID, ledger.EffectHappened); err != nil {
		return err
	}
	other, err := newStatusRigOn(r, s.store, r.Ready)
	if err != nil {
		return err
	}
	reply, err := other.settleEffect(row.ID)
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
		return Violatedf("another worker settled it: %+v", reply)
	}
	offered, err := s.desiredEffects()
	if err != nil {
		return err
	}
	if len(offered) != 1 {
		return Violatedf("the offer stopped: %+v", offered)
	}
	return nil
}

func checkEffectsReplay(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	settled, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	if _, err := s.decideEffect(settled.ID, ledger.EffectRetry); err != nil {
		return err
	}
	if reply, err := s.settleEffect(settled.ID); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("the settlement was refused: %+v, %v", reply.Rejected, err)
	}
	decided, err := s.raiseEffect(aHookKey, 1)
	if err != nil {
		return err
	}
	if _, err := s.decideEffect(decided.ID, ledger.EffectNotHappened); err != nil {
		return err
	}
	if _, err := s.raiseEffect("lane-1/2/input/brief", 1); err != nil {
		return err
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	book, err := ledger.ReplayEffects(events)
	if err != nil {
		return err
	}
	rows, err := control.NewEffects(s.store).List(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	if len(rows) != len(book) {
		return Violatedf("%d effects, the log replays to %d", len(rows), len(book))
	}
	for _, row := range rows {
		if book[row.ID] != row {
			return Violatedf("effect %s reads %+v, and replays to %+v", row.ID, row, book[row.ID])
		}
	}
	return nil
}

func checkInterruptedRetryAsksAgain(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	first, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	if _, err := s.decideEffect(first.ID, ledger.EffectRetry); err != nil {
		return err
	}
	if reply, err := s.settleEffect(first.ID); err != nil || len(reply.Rejected) != 0 {
		return Violatedf("the settlement was refused: %+v, %v", reply.Rejected, err)
	}
	second, err := s.raiseEffect(anInputKey, 2)
	if err != nil {
		return err
	}
	if second.ID == first.ID || !second.Waiting() {
		return Violatedf("the second attempt did not ask again: %+v", second)
	}
	return nil
}

func checkDecisionMovesTheRevision(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	row, err := s.raiseEffect(anInputKey, 1)
	if err != nil {
		return err
	}
	before, err := r.Revision(s.store, r.Scope)
	if err != nil {
		return err
	}
	if _, err := s.decideEffect(row.ID, ledger.EffectHappened); err != nil {
		return err
	}
	after, err := r.Revision(s.store, r.Scope)
	if err != nil {
		return err
	}
	if after <= before {
		return Violatedf("a decision left the revision at %d, so no worker would read it", before)
	}
	return nil
}

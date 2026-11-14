package contract

import (
	"errors"
	"reflect"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// DeliveryChecks hold where a work order's time went and its delivery record, read from the records about it
// and not from the whole log (ADR-0026).
var DeliveryChecks = []Check{
	{"DV1", "a finished change's record sums its lanes and its review's, with its keys and its change", checkDeliveryRecord},
	{"DV2", "an approval a member decided is the person's time, and work under way has no record", checkDeliveryWaitsForAPerson},
	{"DV3", "the fold keeps finished work's timeline and record from its cursor, backfills what ended before it ran, and refolds on a new version", checkDeliveryFold},
}

// deliveryOf reads one work order's timeline and record as the store holds them now.
func (r *Rig) deliveryOf(s ports.Store, id string) (delivery.Timeline, *delivery.Record, error) {
	var timeline delivery.Timeline
	var record *delivery.Record
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		timeline, record, err = control.DeliveryOf(tx, id, control.DeliveryOptions{Now: now})
		return err
	})
	return timeline, record, err
}

// covers says a timeline's segments run from its start to its end with no gap and no overlap.
func covers(t delivery.Timeline) bool {
	at := t.Start
	for _, s := range t.Segments {
		if s.From != at || s.To <= s.From {
			return false
		}
		at = s.To
	}
	return at == t.End
}

// laneTimeOf is how long a timeline's lanes held a place under the cap.
func laneTimeOf(t delivery.Timeline) int64 {
	var out int64
	for _, s := range t.Segments {
		if s.State == workorders.Claimed || s.State == workorders.Running {
			out += s.To - s.From
		}
	}
	return out
}

func checkDeliveryRecord(r *Rig) error {
	s := r.Store()
	change := ledger.ChangeSize{Files: 2, Added: 10, Removed: 3}
	result := LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true))
	result.Change, result.CostUSD, result.InputTokens, result.OutputTokens = &change, 0.5, 1000, 200
	id, err := r.Awaiting(s, []string{criterionOne}, result)
	if err != nil {
		return err
	}
	if _, err := r.NewOps(s).Accept(r.Ctx, r.Scope, id, r.Actor); err != nil {
		return err
	}
	timeline, record, err := r.deliveryOf(s, id)
	if err != nil {
		return err
	}
	if record == nil || timeline.Terminal != workorders.Done || !covers(timeline) {
		return Violatedf("a finished change derived %+v with record %+v", timeline, record)
	}
	if last := timeline.Segments[len(timeline.Segments)-1]; last.Ended == nil || last.Ended.Actor != r.Actor {
		return Violatedf("the acceptance ended by %+v", last.Ended)
	}
	if record.Kind != string(workorders.KindChange) || record.Repository != AcceptanceRepository || record.Criteria != 1 || record.MergeAuthority == "" ||
		record.Agent == "" || record.Outcome != workorders.Done || record.Rounds != 1 || record.Version != delivery.Version {
		return Violatedf("the record's keys %+v", record)
	}
	if record.Usage.CostUSD != 0.5 || record.Usage.InputTokens != 1000 || record.Usage.OutputTokens != 200 || record.Change == nil || *record.Change != change {
		return Violatedf("the record's usage %+v and change %+v", record.Usage, record.Change)
	}
	reviews, err := r.reviewsOf(s, id)
	if err != nil {
		return err
	}
	if len(reviews) != 1 {
		return Violatedf("%d reviews", len(reviews))
	}
	review, reviewRecord, err := r.deliveryOf(s, reviews[0].ID)
	if err != nil {
		return err
	}
	if reviewRecord != nil || !covers(review) {
		return Violatedf("a review derived %+v with record %+v", review, reviewRecord)
	}
	if record.ElapsedMs != timeline.End-timeline.Start || record.DeliveryMs != record.ElapsedMs-record.ByKind[delivery.Queued] ||
		record.LaneMs != laneTimeOf(timeline)+laneTimeOf(review) {
		return Violatedf("elapsed %d, delivery %d, lane %d, of %+v and its review %+v", record.ElapsedMs, record.DeliveryMs, record.LaneMs, timeline, review)
	}
	return nil
}

func checkDeliveryWaitsForAPerson(r *Rig) error {
	st, err := newStatusRig(r)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(1_000)
	request, err := st.raised(`{"command":"git push"}`)
	if err != nil {
		return err
	}
	r.DB.ShiftClock(5_000)
	if _, err := st.decide(request, true, r.Actor); err != nil {
		return err
	}
	r.DB.ShiftClock(1_000)
	timeline, record, err := r.deliveryOf(st.store, st.grant.WorkOrder)
	if err != nil {
		return err
	}
	if record != nil || timeline.Terminal != "" || !covers(timeline) {
		return Violatedf("work under way derived %+v with record %+v", timeline, record)
	}
	for _, s := range timeline.Segments {
		if s.Kind == delivery.Person && s.State == workorders.Running && s.Ended != nil && s.Ended.Record == request.ID && s.Ended.Actor == r.Actor && s.To-s.From >= 5_000 {
			return nil
		}
	}
	return Violatedf("no wait for a person on %s: %+v", request.ID, timeline.Segments)
}

// stored reads what the fold keeps of one work order, and its own state.
func (r *Rig) stored(s ports.Store, id string) (delivery.FoldState, *delivery.Record, bool, error) {
	var state delivery.FoldState
	var record *delivery.Record
	var timeline bool
	err := s.Fold(r.Ctx, r.Scope, func(tx ports.FoldTx) error {
		rows := tx.Delivery()
		var err error
		if state, err = rows.FoldState(); err != nil {
			return err
		}
		if _, err := rows.Timeline(id); err == nil {
			timeline = true
		} else if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		found, err := rows.Record(id)
		switch {
		case err == nil:
			record = &found
		case !errors.Is(err, ports.ErrNotFound):
			return err
		}
		return nil
	})
	return state, record, timeline, err
}

// finished is a change a member accepted, done.
func (r *Rig) finished(s ports.Store) (string, error) {
	id, err := r.Awaiting(s, []string{criterionOne}, LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true)))
	if err != nil {
		return "", err
	}
	_, err = r.NewOps(s).Accept(r.Ctx, r.Scope, id, r.Actor)
	return id, err
}

func checkDeliveryFold(r *Rig) error {
	s := r.Store()
	before, err := r.finished(s)
	if err != nil {
		return err
	}
	fold := control.NewDeliveryFold(s)
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	state, record, timeline, err := r.stored(s, before)
	if err != nil {
		return err
	}
	head, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	if state.Version != delivery.Version || state.Rev != head || state.FirstRunAt == 0 || record == nil || !timeline {
		return Violatedf("the first fold kept %+v, record %+v, timeline %v, at revision %d", state, record, timeline, head)
	}
	_, read, err := r.deliveryOf(s, before)
	if err != nil {
		return err
	}
	if record.LaneCapKnown || !reflect.DeepEqual(*record, *read) {
		return Violatedf("work that ended before the first fold was kept as\n%+v\nand reads as\n%+v", *record, *read)
	}
	if again, err := fold.Fold(r.Ctx, r.Scope); err != nil || again != 0 {
		return Violatedf("a fold with nothing new read %d events: %v", again, err)
	}
	r.DB.ShiftClock(1_000)
	after, err := r.finished(s)
	if err != nil {
		return err
	}
	running, err := r.Ready(s)
	if err != nil {
		return err
	}
	if _, err := r.Running(s, running, "a"); err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	if _, record, _, err = r.stored(s, after); err != nil || record == nil || !record.LaneCapKnown {
		return Violatedf("work that ended after the first fold kept %+v: %v", record, err)
	}
	if _, record, timeline, err = r.stored(s, running); err != nil || record != nil || timeline {
		return Violatedf("work under way kept a record %+v and a timeline %v: %v", record, timeline, err)
	}
	err = s.Fold(r.Ctx, r.Scope, func(tx ports.FoldTx) error {
		rows := tx.Delivery()
		state, err := rows.FoldState()
		if err != nil {
			return err
		}
		state.Version--
		return rows.SaveFoldState(state)
	})
	if err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	for _, id := range []string{before, after} {
		state, record, timeline, err := r.stored(s, id)
		if err != nil || state.Version != delivery.Version || record == nil || !timeline {
			return Violatedf("after a new version, %s kept %+v with %+v and timeline %v: %v", id, state, record, timeline, err)
		}
	}
	return nil
}

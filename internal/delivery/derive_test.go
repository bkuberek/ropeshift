package delivery_test

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

const workOrder = "wo-1"

var (
	member       = ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	orchestrator = ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orch-1"}
	worker       = ledger.Actor{Kind: ledger.ActorWorker, ID: "worker-1"}
	lane         = ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"}
	system       = ledger.Actor{Kind: ledger.ActorSystem, ID: "tick"}
)

// book writes one work order's events as the control plane would record them, in revision order.
type book struct {
	t      *testing.T
	events []ledger.Event
}

func newBook(t *testing.T) *book { return &book{t: t} }

// record appends one event recorded at at, and, when occurred is not zero, reported by its actor at occurred.
func (b *book) record(at, occurred int64, actor ledger.Actor, eventType string, data any) *book {
	b.t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		b.t.Fatal(err)
	}
	rev := int64(len(b.events) + 1)
	b.events = append(b.events, ledger.Event{
		Rev: rev, ID: "ev-" + strconv.FormatInt(rev, 10), Type: eventType, Actor: actor,
		Subject: ledger.Subject{Kind: workorders.SubjectKind, ID: workOrder}, Data: raw, OccurredAt: occurred, RecordedAt: at,
	})
	return b
}

func (b *book) created(at int64, state workorders.State) *book {
	return b.record(at, 0, member, workorders.EventCreated, workorders.WorkOrder{ID: workOrder, Kind: workorders.KindChange, State: state})
}

func (b *book) move(at int64, actor ledger.Actor, trigger workorders.Trigger) *book {
	return b.moveWith(at, actor, workorders.Change{Trigger: trigger})
}

func (b *book) moveWith(at int64, actor ledger.Actor, change workorders.Change) *book {
	return b.record(at, 0, actor, workorders.EventType(change.Trigger), change)
}

func (b *book) startHeld(at int64, reason workorders.HoldReason) *book {
	return b.record(at, 0, system, workorders.EventStartHeld, workorders.Change{Hold: &workorders.StartHold{Reason: reason}})
}

func (b *book) startResolved(at int64) *book {
	return b.record(at, 0, system, workorders.EventStartResolved, workorders.Change{})
}

func (b *book) admissionHeld(at int64, reason string) *book {
	payload, err := json.Marshal(map[string]string{"lane": "lane-1", "reason": reason})
	if err != nil {
		b.t.Fatal(err)
	}
	return b.record(at, at, worker, workorders.EventAdmissionHeld, workorders.Change{Guarded: true, Lane: "lane-1", Payload: payload})
}

// running takes a ready work order to running on lane-1: claimed at claimed, started at started.
func (b *book) running(claimed, started int64) *book {
	b.moveWith(claimed, orchestrator, workorders.Change{Trigger: workorders.TriggerClaimGranted, Worker: "worker-1", ClaimID: "c1"})
	return b.moveWith(started, worker, workorders.Change{Trigger: workorders.TriggerLaneStarted, Lane: "lane-1"})
}

func (b *book) derive(in delivery.Input) delivery.Timeline {
	b.t.Helper()
	in.WorkOrder, in.Events = workOrder, b.events
	timeline, err := delivery.Derive(in)
	if err != nil {
		b.t.Fatal(err)
	}
	return timeline
}

// segment is a segment's shape without how it ended.
type segment struct {
	Kind  delivery.Kind
	Cause string
	Round int64
	From  int64
	To    int64
}

func shapes(timeline delivery.Timeline) []segment {
	out := make([]segment, 0, len(timeline.Segments))
	for _, s := range timeline.Segments {
		out = append(out, segment{Kind: s.Kind, Cause: s.Cause, Round: s.Round, From: s.From, To: s.To})
	}
	return out
}

func expect(t *testing.T, timeline delivery.Timeline, want []segment) {
	t.Helper()
	if got := shapes(timeline); !reflect.DeepEqual(got, want) {
		t.Errorf("segments\n got %+v\nwant %+v", got, want)
	}
}

// TestATimelineCoversFirstReadyToItsEnd holds FR-EST-1: segments run from the first `ready` to the terminal
// state with no gap, each with one kind, and what happened before the first `ready` is not delivery.
func TestATimelineCoversFirstReadyToItsEnd(t *testing.T) {
	b := newBook(t).created(10, workorders.Intake).
		move(20, member, workorders.TriggerAccepted).
		move(30, lane, workorders.TriggerContractSubmitted).
		move(100, member, workorders.TriggerReadyPassed).
		running(150, 200).
		move(500, worker, workorders.TriggerLaneCompleted).
		move(600, member, workorders.TriggerAcceptancePassed)
	timeline := b.derive(delivery.Input{})
	if timeline.Start != 100 || timeline.End != 600 || timeline.Terminal != workorders.Done || timeline.Version != delivery.Version {
		t.Errorf("timeline %d to %d, %s, version %d", timeline.Start, timeline.End, timeline.Terminal, timeline.Version)
	}
	expect(t, timeline, []segment{
		{delivery.Queued, delivery.CauseDispatch, 1, 100, 150},
		{delivery.Queued, delivery.CauseDispatch, 1, 150, 200},
		{delivery.Working, "", 1, 200, 500},
		{delivery.Person, "", 1, 500, 600},
	})
	last := timeline.Segments[3]
	if last.Ended == nil || last.Ended.Event != "ev-8" || last.Ended.Actor != member {
		t.Errorf("acceptance ended by %+v", last.Ended)
	}
	if timeline.Segments[2].Lane != "lane-1" {
		t.Errorf("the lane %q", timeline.Segments[2].Lane)
	}
}

// TestAWorkOrderCreatedReadyStartsAtItsCreation is a rework's timeline, which begins where it was created.
func TestAWorkOrderCreatedReadyStartsAtItsCreation(t *testing.T) {
	b := newBook(t).created(40, workorders.Ready).move(90, member, workorders.TriggerCanceled)
	timeline := b.derive(delivery.Input{})
	if timeline.Start != 40 || timeline.End != 90 || timeline.Terminal != workorders.Canceled {
		t.Errorf("timeline %d to %d, %s", timeline.Start, timeline.End, timeline.Terminal)
	}
	expect(t, timeline, []segment{{delivery.Queued, delivery.CauseDispatch, 1, 40, 90}})
}

// TestAStartHoldIsHeldWithItsReason holds FR-EST-1's `held`: a ready work order a start hold keeps back.
func TestAStartHoldIsHeldWithItsReason(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).
		startHeld(20, workorders.HoldWaitingForMerge).
		startResolved(50).
		running(70, 80).
		move(90, member, workorders.TriggerCanceled)
	expect(t, b.derive(delivery.Input{}), []segment{
		{delivery.Queued, delivery.CauseDispatch, 1, 0, 20},
		{delivery.Held, string(workorders.HoldWaitingForMerge), 1, 20, 50},
		{delivery.Queued, delivery.CauseDispatch, 1, 50, 70},
		{delivery.Queued, delivery.CauseDispatch, 1, 70, 80},
		{delivery.Working, "", 1, 80, 90},
	})
}

// TestAnAdmissionHoldIsReadByItsReason holds the claimed rows of the derivation's table.
func TestAnAdmissionHoldIsReadByItsReason(t *testing.T) {
	for reason, want := range map[string]segment{
		lanes.HeldWorkerFull: {delivery.Queued, delivery.CauseLaneCap, 1, 30, 60},
		lanes.HeldSignInFull: {delivery.Queued, delivery.CauseLaneCap, 1, 30, 60},
		lanes.HeldUsageLimit: {delivery.UsageLimit, "", 1, 30, 60},
		lanes.HeldNearLimit:  {delivery.UsageLimit, "", 1, 30, 60},
		lanes.HeldSignedOut:  {delivery.Person, "", 1, 30, 60},
		lanes.HeldMemory:     {delivery.Held, lanes.HeldMemory, 1, 30, 60},
	} {
		t.Run(reason, func(t *testing.T) {
			b := newBook(t).created(0, workorders.Ready).
				moveWith(10, orchestrator, workorders.Change{Trigger: workorders.TriggerClaimGranted}).
				admissionHeld(30, reason).
				moveWith(60, worker, workorders.Change{Trigger: workorders.TriggerLaneStarted, Lane: "lane-1"}).
				move(70, member, workorders.TriggerCanceled)
			expect(t, b.derive(delivery.Input{}), []segment{
				{delivery.Queued, delivery.CauseDispatch, 1, 0, 10},
				{delivery.Queued, delivery.CauseDispatch, 1, 10, 30},
				want,
				{delivery.Working, "", 1, 60, 70},
			})
		})
	}
}

// TestQueuedNamesTheLaneCapWhileLanesWereCapped splits a wait for a lane at the spans in which every worker
// serving the project stood at a cap or the project's work in progress was full.
func TestQueuedNamesTheLaneCapWhileLanesWereCapped(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).running(100, 120).move(130, member, workorders.TriggerCanceled)
	timeline := b.derive(delivery.Input{LaneCapped: []delivery.Span{{From: 20, To: 40}, {From: 90, To: 110}}})
	expect(t, timeline, []segment{
		{delivery.Queued, delivery.CauseDispatch, 1, 0, 20},
		{delivery.Queued, delivery.CauseLaneCap, 1, 20, 40},
		{delivery.Queued, delivery.CauseDispatch, 1, 40, 90},
		{delivery.Queued, delivery.CauseLaneCap, 1, 90, 100},
		{delivery.Queued, delivery.CauseLaneCap, 1, 100, 110},
		{delivery.Queued, delivery.CauseDispatch, 1, 110, 120},
		{delivery.Working, "", 1, 120, 130},
	})
}

// TestAWaitBelongsToWhoeverEndedIt holds FR-EST-2 on a running lane: a blocking question or approval a
// member closed is waiting for a person, one an orchestrator closed is the ladder, and neither is agent time.
func TestAWaitBelongsToWhoeverEndedIt(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).running(10, 20).move(200, worker, workorders.TriggerLaneCompleted).move(210, system, workorders.TriggerAcceptancePassed)
	timeline := b.derive(delivery.Input{Waits: []delivery.Wait{
		{Lane: "lane-1", Record: "q1", Opened: 30, Closed: 60, ClosedBy: member},
		{Lane: "lane-1", Record: "a1", Opened: 100, Closed: 120, ClosedBy: orchestrator},
		// Two that overlap: each stretch belongs to the wait that closes next.
		{Lane: "lane-1", Record: "q2", Opened: 140, Closed: 160, ClosedBy: orchestrator},
		{Lane: "lane-1", Record: "q3", Opened: 150, Closed: 180, ClosedBy: member},
		// Another lane's wait is not this lane's.
		{Lane: "lane-2", Record: "q4", Opened: 40, Closed: 190, ClosedBy: member},
	}})
	expect(t, timeline, []segment{
		{delivery.Queued, delivery.CauseDispatch, 1, 0, 10},
		{delivery.Queued, delivery.CauseDispatch, 1, 10, 20},
		{delivery.Working, "", 1, 20, 30},
		{delivery.Person, "", 1, 30, 60},
		{delivery.Working, "", 1, 60, 100},
		{delivery.Ladder, "", 1, 100, 120},
		{delivery.Working, "", 1, 120, 140},
		{delivery.Ladder, "", 1, 140, 160},
		{delivery.Person, "", 1, 160, 180},
		{delivery.Working, "", 1, 180, 200},
		{delivery.Integration, "", 1, 200, 210},
	})
	if ended := timeline.Segments[3].Ended; ended == nil || ended.Record != "q1" || ended.Actor != member {
		t.Errorf("the person's wait ended by %+v", ended)
	}
}

// TestAWaitBelongsToItsCloserEvenAfterTheLaneParked holds FR-EST-2 across a park: the stretch a lane ran
// with a blocking question open belongs to whoever closed the question, though the lane parked first.
func TestAWaitBelongsToItsCloserEvenAfterTheLaneParked(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).running(10, 20).
		moveWith(50, lane, workorders.Change{Trigger: workorders.TriggerLaneParked, Lane: "lane-1"}).
		moveWith(90, member, workorders.Change{Trigger: workorders.TriggerWakeFired, Lane: "lane-1", Reason: "answer:q1"}).
		move(95, member, workorders.TriggerCanceled)
	timeline := b.derive(delivery.Input{Waits: []delivery.Wait{{Lane: "lane-1", Record: "q1", Opened: 30, Closed: 90, ClosedBy: member}}})
	expect(t, timeline, []segment{
		{delivery.Queued, delivery.CauseDispatch, 1, 0, 10},
		{delivery.Queued, delivery.CauseDispatch, 1, 10, 20},
		{delivery.Working, "", 1, 20, 30},
		{delivery.Person, "", 1, 30, 50},
		{delivery.Person, "", 1, 50, 90},
		{delivery.Queued, delivery.CauseDispatch, 1, 90, 95},
	})
}

// TestADelegatesActIsNoPersons keeps an agent acting with a member's delegate credential out of the
// person's time: what it closed is the ladder's.
func TestADelegatesActIsNoPersons(t *testing.T) {
	delegate := ledger.Actor{Kind: ledger.ActorDelegate, ID: "cred-1"}
	b := newBook(t).created(0, workorders.Ready).running(10, 20).move(60, worker, workorders.TriggerLaneCompleted).move(70, delegate, workorders.TriggerAcceptancePassed)
	timeline := b.derive(delivery.Input{Waits: []delivery.Wait{{Lane: "lane-1", Record: "q1", Opened: 30, Closed: 40, ClosedBy: delegate}}})
	expect(t, timeline, []segment{
		{delivery.Queued, delivery.CauseDispatch, 1, 0, 10},
		{delivery.Queued, delivery.CauseDispatch, 1, 10, 20},
		{delivery.Working, "", 1, 20, 30},
		{delivery.Ladder, "", 1, 30, 40},
		{delivery.Working, "", 1, 40, 60},
		{delivery.Integration, "", 1, 60, 70},
	})
}

// TestAParkedLaneIsReadByWhatWokeIt holds the parked rows: the wake's cause and the party that fired it.
func TestAParkedLaneIsReadByWhatWokeIt(t *testing.T) {
	for name, tt := range map[string]struct {
		cause string
		by    ledger.Actor
		want  delivery.Kind
	}{
		"a member's answer":           {"answer:q1", member, delivery.Person},
		"an orchestrator's answer":    {"answer:q1", orchestrator, delivery.Ladder},
		"a member's approval":         {"approval:a1", member, delivery.Person},
		"an orchestrator's decision":  {"decision:d1", orchestrator, delivery.Ladder},
		"a usage reset":               {"usage_reset", worker, delivery.UsageLimit},
		"a sign-in restored":          {"signin_restored", worker, delivery.Person},
		"a person resuming":           {"human_resume", member, delivery.Person},
		"an external check":           {"external_check", worker, delivery.External},
		"a time":                      {"tick", system, delivery.External},
		"a dependency found finished": {"done:wo-0", system, delivery.External},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBook(t).created(0, workorders.Ready).running(10, 20).
				moveWith(50, lane, workorders.Change{Trigger: workorders.TriggerLaneParked, Lane: "lane-1"}).
				moveWith(90, tt.by, workorders.Change{Trigger: workorders.TriggerWakeFired, Lane: "lane-1", Reason: tt.cause}).
				move(95, member, workorders.TriggerCanceled)
			timeline := b.derive(delivery.Input{})
			if got := timeline.Segments[3]; got.Kind != tt.want || got.From != 50 || got.To != 90 || got.Ended == nil || got.Ended.Actor != tt.by {
				t.Errorf("parked %+v", got)
			}
		})
	}
}

// TestAcceptanceIsReviewWhileItsReviewIsOpen holds the acceptance rows: an open review is `review`, and the
// rest waits for a person when a member ended it and for integration when the control plane did.
func TestAcceptanceIsReviewWhileItsReviewIsOpen(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).running(10, 20).
		move(100, worker, workorders.TriggerLaneCompleted).
		move(150, system, workorders.TriggerIntegrating).
		move(180, system, workorders.TriggerMerged)
	timeline := b.derive(delivery.Input{Reviews: []delivery.ReviewSpan{{WorkOrder: "review-1", Opened: 100, Closed: 130, ClosedBy: system}}})
	expect(t, timeline, []segment{
		{delivery.Queued, delivery.CauseDispatch, 1, 0, 10},
		{delivery.Queued, delivery.CauseDispatch, 1, 10, 20},
		{delivery.Working, "", 1, 20, 100},
		{delivery.Review, "", 1, 100, 130},
		{delivery.Integration, "", 1, 130, 150},
		{delivery.Integration, "", 1, 150, 180},
	})
	if got := timeline.Segments[3]; got.Review != "review-1" || got.Ended == nil || got.Ended.Record != "review-1" {
		t.Errorf("the review segment %+v", got)
	}
}

// TestStuckWorkWaitsForWhoeverMovedIt holds the lapsed, blocked, and failed rows, and the planning rows
// after the first `ready`.
func TestStuckWorkWaitsForWhoeverMovedIt(t *testing.T) {
	lapsed := func(by ledger.Actor, trigger workorders.Trigger) delivery.Timeline {
		b := newBook(t).created(0, workorders.Ready).running(10, 20).
			move(40, system, workorders.TriggerLeaseExpired).
			move(70, by, trigger).
			move(90, member, workorders.TriggerCanceled)
		return b.derive(delivery.Input{})
	}
	if got := lapsed(member, workorders.TriggerTakeover).Segments[3]; got.Kind != delivery.Person || got.From != 40 || got.To != 70 {
		t.Errorf("a member's takeover after a lapse: %+v", got)
	}
	if got := lapsed(worker, workorders.TriggerReturnedStarted).Segments[3]; got.Kind != delivery.Stuck {
		t.Errorf("a worker's return after a lapse: %+v", got)
	}
	failed := newBook(t).created(0, workorders.Ready).running(10, 20).
		move(40, worker, workorders.TriggerLaneFailed).
		move(60, orchestrator, workorders.TriggerRetried).
		move(80, member, workorders.TriggerCanceled)
	if got := failed.derive(delivery.Input{}).Segments[3]; got.Kind != delivery.Stuck || got.From != 40 || got.To != 60 {
		t.Errorf("an orchestrator's retry of failed work: %+v", got)
	}
	replanned := newBook(t).created(0, workorders.Ready).
		move(10, member, workorders.TriggerBackToReview).
		move(30, orchestrator, workorders.TriggerReadyPassed).
		move(40, member, workorders.TriggerCanceled)
	expect(t, replanned.derive(delivery.Input{}), []segment{
		{delivery.Queued, delivery.CauseDispatch, 1, 0, 10},
		{delivery.Held, delivery.HeldContractReview, 1, 10, 30},
		{delivery.Queued, delivery.CauseDispatch, 1, 30, 40},
	})
}

// TestReworkStartsTheNextRound counts rounds from 1, one more after each `rework`, and a retry of failed work
// stays in its round.
func TestReworkStartsTheNextRound(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).running(10, 20).
		move(30, worker, workorders.TriggerLaneFailed).
		move(40, member, workorders.TriggerRetried).
		running(50, 60).
		move(70, worker, workorders.TriggerLaneCompleted).
		move(80, member, workorders.TriggerRework).
		running(90, 100).
		move(110, worker, workorders.TriggerLaneCompleted).
		move(120, member, workorders.TriggerAcceptancePassed)
	rounds := map[int64]int64{}
	for _, s := range b.derive(delivery.Input{}).Segments {
		rounds[s.From] = s.Round
	}
	for from, want := range map[int64]int64{0: 1, 30: 1, 40: 1, 70: 1, 80: 2, 100: 2, 110: 2} {
		if rounds[from] != want {
			t.Errorf("the segment from %d is round %d, want %d", from, rounds[from], want)
		}
	}
}

// TestWorkStillUnderWayEndsNow leaves the last segment open, with nothing that ended it.
func TestWorkStillUnderWayEndsNow(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).running(10, 20)
	timeline := b.derive(delivery.Input{Now: 75})
	if timeline.End != 75 || timeline.Terminal != "" {
		t.Errorf("timeline ends at %d in %q", timeline.End, timeline.Terminal)
	}
	if last := timeline.Segments[len(timeline.Segments)-1]; last.Kind != delivery.Working || last.To != 75 || last.Ended != nil {
		t.Errorf("the open segment %+v", last)
	}
}

// TestAWorkersEventIsPlacedWhereItReportedClamped holds FR-EST-3: a control plane event sits at its recorded
// time; a worker's sits at its reported time, never before the previous event about the work order and never
// after its own recorded time.
func TestAWorkersEventIsPlacedWhereItReportedClamped(t *testing.T) {
	events := newBook(t).created(0, workorders.Ready).
		record(100, 0, orchestrator, workorders.EventType(workorders.TriggerClaimGranted), workorders.Change{Trigger: workorders.TriggerClaimGranted}).
		record(200, 160, worker, workorders.EventType(workorders.TriggerLaneStarted), workorders.Change{Trigger: workorders.TriggerLaneStarted}).
		record(300, 50, worker, workorders.EventAdmissionHeld, workorders.Change{Guarded: true}).
		record(400, 900, lane, workorders.EventType(workorders.TriggerLaneCompleted), workorders.Change{Trigger: workorders.TriggerLaneCompleted}).
		events
	if got, want := delivery.Place(events), []int64{0, 100, 160, 200, 400}; !reflect.DeepEqual(got, want) {
		t.Errorf("placed at %v, want %v", got, want)
	}
}

// TestADerivationRefusesWhatTheMachineForbids never reads a timeline from a log the state machine cannot replay.
func TestADerivationRefusesWhatTheMachineForbids(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).move(10, worker, workorders.TriggerLaneCompleted)
	if _, err := delivery.Derive(delivery.Input{WorkOrder: workOrder, Events: b.events}); err == nil {
		t.Error("a lane completed from ready derived a timeline")
	}
}

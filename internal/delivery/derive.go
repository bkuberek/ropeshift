package delivery

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Wait is a blocking question or approval request of one of the work order's lanes, from when it opened to
// when it closed, and who closed it. Closed is zero while it stands.
type Wait struct {
	Lane     string       `json:"lane"`
	Record   string       `json:"record"`
	Opened   int64        `json:"opened"`
	Closed   int64        `json:"closed,omitempty"`
	ClosedBy ledger.Actor `json:"closed_by"`
}

// ReviewSpan is one of the work order's review work orders, from its creation to its end, and who ended it.
// Closed is zero while it is open.
type ReviewSpan struct {
	WorkOrder string       `json:"work_order"`
	Opened    int64        `json:"opened"`
	Closed    int64        `json:"closed,omitempty"`
	ClosedBy  ledger.Actor `json:"closed_by"`
}

// Input is what one work order's timeline is derived from. Events are the log's events in revision order,
// of which only those about the work order count. Waits, Reviews, and LaneCapped come from other records:
// its lanes' blocking questions and approval requests, its review work orders, and the spans in which every
// worker serving the project stood at a lane cap or the project's work in progress was full. Now ends the
// timeline of work that has not ended.
type Input struct {
	WorkOrder  string
	Events     []ledger.Event
	Waits      []Wait
	Reviews    []ReviewSpan
	LaneCapped []Span
	Now        int64
}

// The kinds of wake cause the control plane records when an answer, an approval, or a decision fires a wake,
// before the record's ID. A signal's cause is its kind, and a time's is parking.CauseTick.
const (
	causeAnswer   = "answer"
	causeApproval = "approval"
	causeDecision = "decision"
)

// capacity is the actor of a boundary no act made, where the lanes were capped or freed, and reviewer the
// control plane opening a review, which it does for every change it reviews.
var (
	capacity = ledger.Actor{Kind: ledger.ActorSystem, ID: "capacity"}
	reviewer = ledger.Actor{Kind: ledger.ActorSystem, ID: "review"}
)

// Derive folds one work order's events into its timeline (FR-EST-1). One rule attributes every wait: a
// waiting interval belongs to the party whose act ended it, so a wait a member's act ended is always a
// person segment and never agent time (FR-EST-2). Events sit where Place puts them (FR-EST-3). An event
// the state machine has no edge for is an error, as it is in a replay.
func Derive(in Input) (Timeline, error) {
	f := folder{in: in, round: 1, out: Timeline{WorkOrder: in.WorkOrder, Version: Version, Segments: []Segment{}}}
	var own []ledger.Event
	for _, e := range in.Events {
		if e.Subject.Kind == workorders.SubjectKind && e.Subject.ID == in.WorkOrder {
			own = append(own, e)
		}
	}
	placed := Place(own)
	for i, e := range own {
		if err := f.event(e, placed[i]); err != nil {
			return Timeline{}, err
		}
	}
	f.finish()
	waits := sortedWaits(in.Waits)
	for _, p := range f.phases {
		f.out.Segments = append(f.out.Segments, segmentsOf(p, waits, in)...)
	}
	return f.out, nil
}

// phase is a stretch in which nothing the derivation reads of the work order itself changed: its state, its
// start hold, and its admission hold. Ended is the event that ended it, absent while it stands.
type phase struct {
	state     workorders.State
	startHold string
	admission string
	lane      string
	round     int64
	from, to  int64
	ended     *Ending
	trigger   workorders.Trigger
	reason    string
}

type folder struct {
	in        Input
	state     workorders.State
	started   bool
	startHold string
	admission string
	lane      string
	round     int64
	current   phase
	phases    []phase
	out       Timeline
}

func (f *folder) event(e ledger.Event, at int64) error {
	if e.Type == workorders.EventCreated {
		var created workorders.WorkOrder
		if err := json.Unmarshal(e.Data, &created); err != nil {
			return fmt.Errorf("%s: %w", e.ID, err)
		}
		f.state = created.State
		if f.state == workorders.Ready {
			f.begin(at)
		}
		return nil
	}
	var change workorders.Change
	if json.Unmarshal(e.Data, &change) != nil {
		return nil
	}
	if change.Lane != "" {
		f.lane = change.Lane
		if f.current.lane == "" {
			f.current.lane = change.Lane
		}
	}
	switch e.Type {
	case workorders.EventStartHeld:
		reason := ""
		if change.Hold != nil {
			reason = string(change.Hold.Reason)
		}
		f.hold(e, at, workorders.Ready, &f.startHold, reason)
	case workorders.EventStartResolved:
		f.hold(e, at, workorders.Ready, &f.startHold, "")
	case workorders.EventAdmissionHeld:
		var held struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(change.Payload, &held) == nil {
			f.hold(e, at, workorders.Claimed, &f.admission, held.Reason)
		}
	}
	if change.Trigger == "" {
		return nil
	}
	edge, ok := workorders.Taken(f.state, change.Trigger)
	if !ok {
		return fmt.Errorf("%s: %w: %s from %s", e.ID, workorders.ErrInvalid, change.Trigger, f.state)
	}
	f.move(e, at, edge, change)
	return nil
}

// begin opens the timeline at the work order's first `ready`.
func (f *folder) begin(at int64) {
	f.started, f.out.Start = true, at
	f.open(at)
}

func (f *folder) open(at int64) {
	f.current = phase{state: f.state, startHold: f.startHold, admission: f.admission, lane: f.lane, round: f.round, from: at}
}

// close ends the phase that stands at an event.
func (f *folder) close(e ledger.Event, at int64, change workorders.Change) {
	p := f.current
	p.to, p.ended, p.trigger, p.reason = at, &Ending{Event: e.ID, Actor: e.Actor}, change.Trigger, change.Reason
	f.phases = append(f.phases, p)
}

// hold changes a hold, and starts a new phase when the hold matters in the state the work order is in.
func (f *folder) hold(e ledger.Event, at int64, matters workorders.State, hold *string, reason string) {
	if *hold == reason {
		return
	}
	if f.started && f.state == matters {
		f.close(e, at, workorders.Change{})
		*hold = reason
		f.open(at)
		return
	}
	*hold = reason
}

func (f *folder) move(e ledger.Event, at int64, edge workorders.Transition, change workorders.Change) {
	if f.started {
		f.close(e, at, change)
	}
	switch edge.Trigger {
	case workorders.TriggerClaimGranted:
		f.startHold, f.lane = "", change.Lane
	case workorders.TriggerRework:
		f.round++
	}
	if edge.From == workorders.Claimed {
		f.admission = ""
	}
	f.state = edge.To
	switch {
	case !f.started && f.state == workorders.Ready:
		f.begin(at)
	case f.started:
		f.open(at)
	}
}

// finish ends the timeline: at its terminal state, or now while it has not ended.
func (f *folder) finish() {
	if !f.started {
		return
	}
	if workorders.Terminal(f.current.state) {
		f.out.End, f.out.Terminal = f.current.from, f.current.state
		return
	}
	p := f.current
	p.to = max(f.in.Now, p.from)
	f.phases = append(f.phases, p)
	f.out.End = p.to
}

// piece is part of a phase, with its kind and what ended it.
type piece struct {
	kind     Kind
	cause    string
	review   string
	from, to int64
	ended    *Ending
}

// segmentsOf reads one phase by the derivation's table.
func segmentsOf(p phase, waits []Wait, in Input) []Segment {
	if p.to <= p.from {
		return nil
	}
	var pieces []piece
	whole := func(kind Kind, cause string) {
		pieces = []piece{{kind: kind, cause: cause, from: p.from, to: p.to, ended: p.ended}}
	}
	// waiting reads a wait the work order sat in by who ended it: a member's act is waiting for a person.
	waiting := func(kind Kind, cause string) {
		if byMember(p.ended) {
			whole(Person, "")
			return
		}
		whole(kind, cause)
	}
	switch p.state {
	case workorders.Ready:
		if p.startHold != "" {
			whole(Held, p.startHold)
		} else {
			pieces = queued(p, in.LaneCapped)
		}
	case workorders.Claimed:
		switch p.admission {
		case "":
			pieces = queued(p, in.LaneCapped)
		case lanes.HeldWorkerFull, lanes.HeldSignInFull:
			whole(Queued, CauseLaneCap)
		case lanes.HeldUsageLimit, lanes.HeldNearLimit:
			whole(UsageLimit, "")
		case lanes.HeldSignedOut:
			whole(Person, "")
		default:
			whole(Held, p.admission)
		}
	case workorders.Running:
		pieces = running(p, waits)
	case workorders.Parked:
		whole(parked(p), "")
	case workorders.Acceptance:
		pieces = accepting(p, in.Reviews)
	case workorders.Integrating:
		waiting(Integration, "")
	case workorders.Lapsed, workorders.Blocked, workorders.Failed:
		waiting(Stuck, "")
	case workorders.ContractReview:
		waiting(Held, HeldContractReview)
	default:
		waiting(Held, HeldReplanning)
	}
	held := p.state == workorders.Claimed || p.state == workorders.Running || p.state == workorders.Parked || p.state == workorders.Lapsed
	out := make([]Segment, 0, len(pieces))
	for _, c := range pieces {
		if c.to <= c.from {
			continue
		}
		s := Segment{Kind: c.kind, State: p.state, Cause: c.cause, Round: p.round, Review: c.review, From: c.from, To: c.to, Ended: c.ended}
		if held {
			s.Lane = p.lane
		}
		out = append(out, s)
	}
	return out
}

// byMember says a member's act ended a stretch. A delegate acting with a member's credential is not the
// member, so its act is no person's.
func byMember(e *Ending) bool { return e != nil && authority.PartyOf(e.Actor) == authority.PartyPerson }

// queued splits a wait for a lane at the spans in which the lanes were capped.
func queued(p phase, capped []Span) []piece {
	var out []piece
	at := p.from
	for _, s := range Intersect(Union(capped), []Span{{From: p.from, To: p.to}}) {
		if s.From > at {
			out = append(out, piece{kind: Queued, cause: CauseDispatch, from: at, to: s.From, ended: &Ending{Actor: capacity}})
		}
		out = append(out, piece{kind: Queued, cause: CauseLaneCap, from: s.From, to: s.To, ended: &Ending{Actor: capacity}})
		at = s.To
	}
	if at < p.to {
		out = append(out, piece{kind: Queued, cause: CauseDispatch, from: at, to: p.to})
	}
	out[len(out)-1].ended = p.ended
	return out
}

// clipped is a wait within one running phase. Person says a member closed its record, or, when it never
// closed, ended the phase. Ended is what ended it there: its own close, or, when it outlasted the phase,
// whatever ended the phase.
type clipped struct {
	record   string
	from, to int64
	person   bool
	ended    *Ending
}

// running splits a running lane at its blocking waits. A stretch with no wait open is agent time. A stretch
// with a wait open belongs to the wait that closes next, and is waiting for a person when a member closed
// its record and for the ladder otherwise, even when the lane parked before the record closed. A record
// that never closed belongs to whatever ended the phase.
func running(p phase, waits []Wait) []piece {
	var open []clipped
	cuts := []int64{p.from, p.to}
	for _, w := range waits {
		if w.Lane != "" && p.lane != "" && w.Lane != p.lane {
			continue
		}
		c := clipped{record: w.Record, from: max(w.Opened, p.from), to: w.Closed, ended: &Ending{Record: w.Record, Actor: w.ClosedBy}}
		c.person = authority.PartyOf(w.ClosedBy) == authority.PartyPerson
		if w.Closed == 0 {
			c.person = byMember(p.ended)
		}
		if w.Closed == 0 || w.Closed >= p.to {
			c.to, c.ended = p.to, p.ended
		}
		if c.to <= c.from {
			continue
		}
		open = append(open, c)
		cuts = append(cuts, c.from, c.to)
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)
	var out []piece
	var last *clipped
	for i := 0; i+1 < len(cuts); i++ {
		from, to := cuts[i], cuts[i+1]
		var next *clipped
		for j := range open {
			if c := &open[j]; c.from <= from && c.to >= to && (next == nil || c.to < next.to) {
				next = c
			}
		}
		if n := len(out); n > 0 && next == last {
			out[n-1].to = to
		} else {
			out = append(out, piece{kind: Working, from: from, to: to})
			if next != nil {
				out[len(out)-1].kind = Ladder
				if next.person {
					out[len(out)-1].kind = Person
				}
			}
		}
		last = next
		// What ended this stretch: the wait it belonged to closing, or another wait opening.
		switch {
		case next != nil && next.to == to:
			out[len(out)-1].ended = next.ended
		default:
			out[len(out)-1].ended = nil
			for j := range open {
				if open[j].from == to {
					out[len(out)-1].ended = &Ending{Record: open[j].record, Actor: ledger.Actor{Kind: ledger.ActorLane, ID: p.lane}}
					break
				}
			}
		}
	}
	out[len(out)-1].ended = p.ended
	return out
}

// sortedWaits puts waits in one order whatever order they came in, so the derivation does not depend on it.
func sortedWaits(waits []Wait) []Wait {
	out := slices.Clone(waits)
	slices.SortFunc(out, func(a, b Wait) int {
		return cmp.Or(cmp.Compare(a.Opened, b.Opened), cmp.Compare(a.Closed, b.Closed), cmp.Compare(a.Record, b.Record),
			cmp.Compare(a.Lane, b.Lane), cmp.Compare(a.ClosedBy.Kind, b.ClosedBy.Kind), cmp.Compare(a.ClosedBy.ID, b.ClosedBy.ID))
	})
	return out
}

// parked reads a parked lane by what woke it: an answer, an approval, or a decision belongs to whoever gave
// it, a usage reset is the usage limit, a sign-in restored or a person resuming is waiting for a person, and
// anything else the control plane fired, a check, a time, or a finished dependency, is external. Parked work
// another act ended belongs to its actor's party, and parked work that stands reads as external until its
// wake fires.
func parked(p phase) Kind {
	if p.trigger == workorders.TriggerWakeFired {
		kind, _, _ := strings.Cut(p.reason, ":")
		switch kind {
		case causeAnswer, causeApproval, causeDecision:
			if byMember(p.ended) {
				return Person
			}
			return Ladder
		case string(parking.UsageReset):
			return UsageLimit
		case string(parking.SigninRestored), string(parking.HumanResume):
			return Person
		}
		return External
	}
	switch {
	case byMember(p.ended):
		return Person
	case p.ended != nil && authority.PartyOf(p.ended.Actor) == authority.PartyOrchestrator:
		return Ladder
	}
	return External
}

// accepting splits acceptance at its open reviews. The rest waits for a person when a member's act ended it,
// and for integration otherwise.
func accepting(p phase, reviews []ReviewSpan) []piece {
	sorted := slices.Clone(reviews)
	slices.SortFunc(sorted, func(a, b ReviewSpan) int {
		return cmp.Or(cmp.Compare(a.Opened, b.Opened), cmp.Compare(a.Closed, b.Closed), cmp.Compare(a.WorkOrder, b.WorkOrder))
	})
	var out []piece
	at := p.from
	for _, r := range sorted {
		from, to, ended := max(r.Opened, at), r.Closed, &Ending{Record: r.WorkOrder, Actor: r.ClosedBy}
		if r.Closed == 0 || r.Closed >= p.to {
			to, ended = p.to, p.ended
		}
		if to <= from {
			continue
		}
		if from > at {
			out = append(out, piece{kind: Integration, from: at, to: from, ended: &Ending{Record: r.WorkOrder, Actor: reviewer}})
		}
		out = append(out, piece{kind: Review, review: r.WorkOrder, from: from, to: to, ended: ended})
		at = to
	}
	if at < p.to {
		kind := Integration
		if byMember(p.ended) {
			kind = Person
		}
		out = append(out, piece{kind: kind, from: at, to: p.to, ended: p.ended})
	}
	return out
}

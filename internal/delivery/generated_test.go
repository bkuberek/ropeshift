package delivery_test

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// legal lists the triggers each state takes, from the machine itself.
func legal() map[workorders.State][]workorders.Trigger {
	out := map[workorders.State][]workorders.Trigger{}
	for _, edge := range workorders.Transitions() {
		out[edge.From] = append(out[edge.From], edge.Trigger)
	}
	return out
}

// generated is one random work order's history: a legal walk through the machine from `ready`, with holds,
// waits, reviews, and capped spans scattered over it, by any of the parties that act.
func generated(t *testing.T, rng *rand.Rand, triggers map[workorders.State][]workorders.Trigger) delivery.Input {
	t.Helper()
	actors := []ledger.Actor{member, orchestrator, worker, lane, system}
	b := newBook(t).created(0, workorders.Ready)
	state, at := workorders.Ready, int64(0)
	for range 40 {
		if workorders.Terminal(state) && (state != workorders.Failed || rng.IntN(2) == 0) {
			break
		}
		at += int64(rng.IntN(50))
		actor := actors[rng.IntN(len(actors))]
		switch {
		case state == workorders.Ready && rng.IntN(4) == 0:
			b.startHeld(at, workorders.HoldWaitingForMerge)
			continue
		case state == workorders.Ready && rng.IntN(4) == 0:
			b.startResolved(at)
			continue
		case state == workorders.Claimed && rng.IntN(3) == 0:
			holds := []string{lanes.HeldWorkerFull, lanes.HeldUsageLimit, lanes.HeldSignedOut, lanes.HeldMemory}
			b.admissionHeld(at, holds[rng.IntN(len(holds))])
			continue
		}
		options := triggers[state]
		trigger := options[rng.IntN(len(options))]
		edge, _ := workorders.Next(state, trigger)
		change := workorders.Change{Trigger: trigger, Lane: "lane-1"}
		if trigger == workorders.TriggerWakeFired {
			causes := []string{"answer:q", "approval:a", "usage_reset", "signin_restored", "tick", "done:wo-0"}
			change.Reason = causes[rng.IntN(len(causes))]
		}
		reported := int64(0)
		if actor == worker || actor == lane {
			reported = at - int64(rng.IntN(80)) + 40
		}
		b.record(at, reported, actor, workorders.EventType(trigger), change)
		state = edge.To
	}
	in := delivery.Input{WorkOrder: workOrder, Events: b.events, Now: at + int64(rng.IntN(50))}
	for range rng.IntN(6) {
		opened := int64(rng.IntN(int(at + 1)))
		closed := opened + int64(rng.IntN(100))
		if rng.IntN(5) == 0 {
			closed = 0
		}
		in.Waits = append(in.Waits, delivery.Wait{Lane: "lane-1", Record: "q", Opened: opened, Closed: closed, ClosedBy: actors[rng.IntN(len(actors))]})
	}
	for range rng.IntN(3) {
		opened := int64(rng.IntN(int(at + 1)))
		in.Reviews = append(in.Reviews, delivery.ReviewSpan{WorkOrder: "review-1", Opened: opened, Closed: opened + int64(rng.IntN(100)), ClosedBy: system})
		opened = int64(rng.IntN(int(at + 1)))
		in.LaneCapped = append(in.LaneCapped, delivery.Span{From: opened, To: opened + int64(rng.IntN(100))})
	}
	return in
}

// TestGeneratedTimelines holds the derivation's promises on generated event sequences (FR-EST-1, FR-EST-2,
// FR-EST-3): every timeline covers its elapsed time exactly, no wait is ever agent time, every running stretch
// a member's close ended waits for a person, and a refold gives the same segments.
func TestGeneratedTimelines(t *testing.T) {
	triggers := legal()
	rng := rand.New(rand.NewPCG(20261114, 26))
	for run := range 3000 {
		in := generated(t, rng, triggers)
		timeline, err := delivery.Derive(in)
		if err != nil {
			t.Fatalf("run %d: a legal sequence did not derive: %v", run, err)
		}
		covered := timeline.Start
		var total int64
		for i, s := range timeline.Segments {
			if s.From != covered || s.To <= s.From {
				t.Fatalf("run %d: segment %d runs %d to %d after coverage reached %d: %+v", run, i, s.From, s.To, covered, timeline.Segments)
			}
			covered, total = s.To, total+s.To-s.From
			if s.Round < 1 || (s.Ended == nil && i != len(timeline.Segments)-1) {
				t.Fatalf("run %d: segment %d %+v", run, i, s)
			}
		}
		if covered != timeline.End || total != timeline.End-timeline.Start {
			t.Fatalf("run %d: segments cover to %d and sum to %d of %d to %d", run, covered, total, timeline.Start, timeline.End)
		}
		for _, s := range timeline.Segments {
			for _, w := range in.Waits {
				closed := w.Closed
				if closed == 0 {
					closed = timeline.End
				}
				// A wait that opened and closed at once held no time.
				overlaps := w.Opened < closed && s.From < closed && w.Opened < s.To
				if overlaps && s.Kind == delivery.Working {
					t.Fatalf("run %d: working %d to %d overlaps a wait %d to %d", run, s.From, s.To, w.Opened, closed)
				}
			}
		}
		for _, s := range timeline.Segments {
			if s.Ended != nil && s.Ended.Record == "q" && s.Ended.Actor == member && s.Kind != delivery.Person {
				t.Fatalf("run %d: a wait a member closed reads as %s: %+v", run, s.Kind, s)
			}
		}
		again, err := delivery.Derive(in)
		if err != nil || !reflect.DeepEqual(again, timeline) {
			t.Fatalf("run %d: a refold gave another timeline: %v", run, err)
		}
		shuffled := in
		shuffled.Waits = slices.Clone(in.Waits)
		rng.Shuffle(len(shuffled.Waits), func(i, j int) { shuffled.Waits[i], shuffled.Waits[j] = shuffled.Waits[j], shuffled.Waits[i] })
		if other, err := delivery.Derive(shuffled); err != nil || !reflect.DeepEqual(other, timeline) {
			t.Fatalf("run %d: the order the waits came in changed the timeline: %v", run, err)
		}
	}
}

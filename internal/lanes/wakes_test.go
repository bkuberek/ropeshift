package lanes_test

import (
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
)

func item(id string) ledger.WakeItem {
	return ledger.WakeItem{Kind: ledger.WakeQuestion, ID: id, Stamp: 1, Text: id}
}

func newBatch() *lanes.WakeBatch {
	return &lanes.WakeBatch{Debounce: 30 * time.Second, Floor: 2 * time.Minute}
}

var start = time.UnixMilli(1_000_000)

// An empty wake reads the whole context, so an idle fleet sends none, however long it waits.
func TestAnIdleFleetSendsNoWake(t *testing.T) {
	b := newBatch()
	for minutes := range 600 {
		now := start.Add(time.Duration(minutes) * time.Minute)
		b.Note(nil, now)
		if b.Due(now) {
			t.Fatalf("a wake with nothing to judge, after %d minutes", minutes)
		}
	}
}

func TestThreeItemsMakeOneWake(t *testing.T) {
	b := newBatch()
	b.Note([]ledger.WakeItem{item("a")}, start)
	if b.Due(start) {
		t.Fatal("a wake inside the debounce")
	}
	b.Note([]ledger.WakeItem{item("a"), item("b")}, start.Add(10*time.Second))
	b.Note([]ledger.WakeItem{item("a"), item("b"), item("c")}, start.Add(29*time.Second))
	if b.Due(start.Add(29 * time.Second)) {
		t.Fatal("a later item started no new wake, and the first's debounce has not passed")
	}
	now := start.Add(30 * time.Second)
	if !b.Due(now) {
		t.Fatal("no wake past the debounce")
	}
	if got := b.Batch(); len(got) != 3 {
		t.Fatalf("a wake holds %+v", got)
	}
	b.Sent(b.Batch(), now)
	b.Note([]ledger.WakeItem{item("a"), item("b"), item("c")}, now)
	if b.Due(now.Add(time.Hour)) {
		t.Error("a second wake for what the first carried")
	}
}

func TestTheFloorHolds(t *testing.T) {
	b := newBatch()
	b.Note([]ledger.WakeItem{item("a")}, start)
	first := start.Add(30 * time.Second)
	b.Sent(b.Batch(), first)
	b.Note([]ledger.WakeItem{item("a"), item("b")}, first.Add(time.Second))
	if b.Due(first.Add(2*time.Minute - time.Millisecond)) {
		t.Error("a wake under the floor")
	}
	if !b.Due(first.Add(2 * time.Minute)) {
		t.Error("no wake at the floor")
	}
	if got := b.Batch(); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("a later wake carries %+v", got)
	}
}

func TestAnItemThatChangedCountsAgain(t *testing.T) {
	b := newBatch()
	b.Note([]ledger.WakeItem{item("a")}, start)
	first := start.Add(30 * time.Second)
	b.Sent(b.Batch(), first)
	changed := item("a")
	changed.Stamp = 2
	b.Note([]ledger.WakeItem{changed}, first.Add(time.Hour))
	if !b.Due(first.Add(time.Hour + 30*time.Second)) {
		t.Error("a changed item woke nobody")
	}
}

func TestAnItemThatWentAndCameBackCountsAgain(t *testing.T) {
	b := newBatch()
	b.Note([]ledger.WakeItem{item("a")}, start)
	first := start.Add(30 * time.Second)
	b.Sent(b.Batch(), first)
	b.Note(nil, first.Add(time.Minute))
	b.Note([]ledger.WakeItem{item("a")}, first.Add(time.Hour))
	if !b.Due(first.Add(time.Hour + 30*time.Second)) {
		t.Error("an item that came back woke nobody")
	}
}

func TestWhatLeftBeforeAWakeCarriesNoWake(t *testing.T) {
	b := newBatch()
	b.Note([]ledger.WakeItem{item("a")}, start)
	b.Note(nil, start.Add(10*time.Second))
	if b.Due(start.Add(time.Hour)) {
		t.Error("a wake for what is gone")
	}
}

// A wake that did not leave told nobody, so it stays due.
func TestAWakeThatFailedStaysDue(t *testing.T) {
	b := newBatch()
	b.Note([]ledger.WakeItem{item("a")}, start)
	if !b.Due(start.Add(30 * time.Second)) {
		t.Fatal("no wake")
	}
	if !b.Due(start.Add(31 * time.Second)) {
		t.Error("a wake nobody recorded as sent is no longer due")
	}
}

// A hold keeps a wake back, and what came meanwhile is what the next wake carries.
func TestAHoldKeepsAWakeBackAndItsBatch(t *testing.T) {
	b := newBatch()
	b.Hold = true
	b.Note([]ledger.WakeItem{item("a")}, start)
	if b.Due(start.Add(time.Hour)) {
		t.Fatal("a wake under a hold")
	}
	b.Hold = false
	if !b.Due(start.Add(time.Hour)) {
		t.Error("the hold's end woke nobody")
	}
}

func TestBothBoundsStayUnderAskWait(t *testing.T) {
	tests := []struct {
		name          string
		lifecycle     lanes.LifecycleSettings
		debounce, min time.Duration
	}{
		{"the sample", lanes.LifecycleSettings{AskWait: "5m", WakeDebounce: "30s", WakeMinInterval: "2m"}, 30 * time.Second, 2 * time.Minute},
		{"a floor past the wait", lanes.LifecycleSettings{AskWait: "5m", WakeDebounce: "30s", WakeMinInterval: "10m"}, 30 * time.Second, 150 * time.Second},
		{"a debounce at the wait", lanes.LifecycleSettings{AskWait: "1m", WakeDebounce: "1m", WakeMinInterval: "30s"}, 30 * time.Second, 30 * time.Second},
		{"values that do not parse", lanes.LifecycleSettings{AskWait: "5m", WakeDebounce: "soon", WakeMinInterval: "later"}, 30 * time.Second, 2 * time.Minute},
		{"a short wait and the defaults", lanes.LifecycleSettings{AskWait: "10s"}, 5 * time.Second, 5 * time.Second},
		{"no wait and no settings", lanes.LifecycleSettings{}, 30 * time.Second, 2 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			debounce, floor := lanes.WakeBounds(tt.lifecycle)
			if debounce != tt.debounce || floor != tt.min {
				t.Errorf("debounce %s, floor %s", debounce, floor)
			}
		})
	}
}

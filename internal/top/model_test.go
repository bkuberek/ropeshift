package top_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/top"
	"ropeshift.com/ropeshift/internal/workorders"
)

var update = flag.Bool("update", false, "write the golden frame")

// golden reads what the API's own tests wrote, so the model reads the shapes a daemon serves.
func golden(t *testing.T) top.Snapshot {
	t.Helper()
	var snapshot top.Snapshot
	for name, into := range map[string]any{"needs-you": &snapshot.Needs, "shop-floor": &snapshot.Floor} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "web", "testdata", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func quiet(context.Context) (top.Snapshot, error) { return top.Snapshot{}, nil }

func loaded(t *testing.T, snapshot top.Snapshot) top.Model {
	t.Helper()
	m := top.New(top.Config{Read: quiet, Now: func() time.Time { return time.UnixMilli(snapshot.Needs.AsOf) }})
	next, _ := m.Update(top.Resized(120, 40))
	next, _ = next.Update(top.Loaded(snapshot))
	return next
}

func TestNeedsComeFirstInTheOrderTheServerChose(t *testing.T) {
	snapshot := golden(t)
	frame := loaded(t, snapshot).View()
	at := -1
	for _, item := range snapshot.Needs.Items {
		found := strings.Index(frame, item.ID)
		if found <= at {
			t.Fatalf("%s stands at %d, after %d:\n%s", item.ID, found, at, frame)
		}
		at = found
	}
	if lanes := strings.Index(frame, "LANES"); lanes < at {
		t.Errorf("the lanes come before what needs a person:\n%s", frame)
	}
}

func TestLanesThatNeedAPersonComeFirst(t *testing.T) {
	snapshot := golden(t)
	got := top.LaneOrder(snapshot)
	if len(got) != len(snapshot.Floor.Lanes) {
		t.Fatalf("%d lanes of %d", len(got), len(snapshot.Floor.Lanes))
	}
	waiting := map[string]bool{}
	for _, item := range snapshot.Needs.Items {
		if item.Work != nil && item.Work.Lane != nil {
			waiting[item.Work.Lane.ID] = true
		}
	}
	first := true
	for _, lane := range got {
		if waiting[lane.ID] && !first {
			t.Fatalf("lane %s needs a person, and stands after one that does not: %+v", lane.ID, got)
		}
		first = first && waiting[lane.ID]
	}
}

// A value read as last reported and shown as observed is the mistake FR-UI-5 exists to stop.
func TestALaneStatesWhetherItsValueWasObserved(t *testing.T) {
	snapshot := golden(t)
	snapshot.Floor.Lanes = []ledger.LaneView{
		{ID: "lane-seen", LaneStatus: ledger.LaneStatus{ActualState: "working"}, Freshness: ledger.FreshnessObserved, AsOf: snapshot.Needs.AsOf},
		{ID: "lane-old", LaneStatus: ledger.LaneStatus{ActualState: "working"}, Freshness: ledger.FreshnessLastReported, AsOf: snapshot.Needs.AsOf - 5*60*1000},
	}
	frame := loaded(t, snapshot).View()
	for _, want := range []string{"observed", "last reported 5m ago"} {
		if !strings.Contains(frame, want) {
			t.Errorf("no %q in:\n%s", want, frame)
		}
	}
}

// A parked lane says what would move it in the words the web UI uses, and never a reason's code.
func TestAParkedLaneSaysWhatItWaitsFor(t *testing.T) {
	frame := loaded(t, golden(t)).View()
	if !strings.Contains(frame, "wo-parked  parked: waits for question question-1 or a person  Wait for the check") || strings.Contains(frame, "human_resume") {
		t.Errorf("the parked lane does not say what it waits for:\n%s", frame)
	}
	snapshot := top.Snapshot{}
	snapshot.Needs.Items = []api.NeedItem{{Kind: api.NeedWork, ID: "wo-old", Work: &api.WorkNeed{
		Reason: api.ReasonLongParked, Wakes: []api.WakeView{{ID: "wake", Expression: `{"moon_phase":"full"}`}}, Actions: []string{},
	}}}
	if frame := loaded(t, snapshot).View(); !strings.Contains(frame, "wo-old  long parked: waits for something this build cannot read") {
		t.Errorf("a wake this build cannot read says nothing of itself:\n%s", frame)
	}
}

// Titles and lane reasons come from agents and repositories, and a terminal obeys what it is sent.
func TestNothingAReaderHoldsCanWriteToTheTerminal(t *testing.T) {
	hostile := "ok\x1b]52;c;c3RvbGVu\x07\x1b[2J\u009b31m\u202egone\r\nforged"
	order := workorders.WorkOrder{ID: "wo", Title: hostile}
	snapshot := top.Snapshot{}
	snapshot.Needs.Items = []api.NeedItem{{Kind: api.NeedWork, ID: "wo" + hostile, Work: &api.WorkNeed{Reason: api.ReasonBlocked, Detail: hostile, WorkOrder: &order, Actions: []string{}}}}
	snapshot.Floor.Lanes = []ledger.LaneView{{ID: "lane" + hostile, WorkOrder: hostile, LaneStatus: ledger.LaneStatus{ActualState: hostile}}}
	frame := loaded(t, snapshot).View()
	for _, r := range frame {
		if r == '\n' {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r < 0xa0) || r == '\u202e' {
			t.Fatalf("%q reached the terminal in:\n%q", r, frame)
		}
	}
	if !strings.Contains(frame, "ok") {
		t.Errorf("the title went too:\n%q", frame)
	}
}

func keyed(t *testing.T, m top.Model, key string) top.Model {
	t.Helper()
	next, _ := m.Update(top.Pressed(key))
	return next
}

func TestTheCursorStaysOnSomethingThatNeedsAPerson(t *testing.T) {
	snapshot := golden(t)
	m := loaded(t, snapshot)
	if m.Selected() != snapshot.Needs.Items[0].ID {
		t.Fatalf("selected %q", m.Selected())
	}
	m = keyed(t, m, "k")
	if m.Selected() != snapshot.Needs.Items[0].ID {
		t.Errorf("moved above the first: %q", m.Selected())
	}
	m = keyed(t, m, "j")
	if m.Selected() != snapshot.Needs.Items[1].ID {
		t.Errorf("selected %q", m.Selected())
	}
	for range snapshot.Needs.Items {
		m = keyed(t, m, "j")
	}
	last := snapshot.Needs.Items[len(snapshot.Needs.Items)-1].ID
	if m.Selected() != last {
		t.Errorf("selected %q, want the last, %q", m.Selected(), last)
	}
	// What the cursor stood on leaving the list, the cursor stays in the list.
	snapshot.Needs.Items = snapshot.Needs.Items[:2]
	next, _ := m.Update(top.Loaded(snapshot))
	if got := next.Selected(); got != snapshot.Needs.Items[1].ID {
		t.Errorf("selected %q after the list shrank", got)
	}
}

// One worker reads as one worker, and one lane as one lane.
func TestTheHeaderCountsInWords(t *testing.T) {
	snapshot := golden(t)
	snapshot.Floor.Lanes = snapshot.Floor.Lanes[:1]
	header, _, _ := strings.Cut(loaded(t, snapshot).View(), "\n")
	if !strings.HasPrefix(header, "ropeshift top   9 need you   1 lane   1 worker   ") {
		t.Errorf("header %q", header)
	}
	snapshot.Floor.Workers = append(snapshot.Floor.Workers, snapshot.Floor.Workers[0])
	snapshot.Floor.Lanes = nil
	header, _, _ = strings.Cut(loaded(t, snapshot).View(), "\n")
	if !strings.HasPrefix(header, "ropeshift top   9 need you   0 lanes   2 workers   ") {
		t.Errorf("header %q", header)
	}
}

func TestAnEmptyFleetSaysSo(t *testing.T) {
	frame := loaded(t, top.Snapshot{}).View()
	if !strings.Contains(frame, "Nothing needs you") || !strings.Contains(frame, "No lanes") {
		t.Errorf("frame:\n%s", frame)
	}
}

func TestAStreamEventAsksForARead(t *testing.T) {
	m := loaded(t, golden(t))
	reads := 0
	m = m.WithRead(func(context.Context) (top.Snapshot, error) {
		reads++
		return top.Snapshot{}, nil
	})
	next, cmd := m.Update(top.Moved(m.Revision() + 1))
	if cmd == nil {
		t.Fatal("a new revision asked for nothing")
	}
	cmd.Each(func(top.Msg) {})
	if reads != 1 {
		t.Errorf("reads %d", reads)
	}
	if _, cmd := next.Update(top.Moved(m.Revision())); cmd != nil {
		cmd.Each(func(top.Msg) {})
		if reads != 1 {
			t.Error("a revision already read asked for another read")
		}
	}
}

// A control plane that went away does not make what it last said untrue, only old, so the frame keeps
// it and says so.
func TestAFailedReadKeepsWhatItHadAndSaysHowOld(t *testing.T) {
	snapshot := golden(t)
	now := time.UnixMilli(snapshot.Needs.AsOf)
	m := top.New(top.Config{Read: quiet, Now: func() time.Time { return now }})
	next, _ := m.Update(top.Resized(120, 40))
	next, _ = next.Update(top.Loaded(snapshot))
	now = now.Add(90 * time.Second)
	next, _ = next.Update(top.Failed(errors.New("control plane unreachable: refused")))
	frame := next.View()
	if !strings.Contains(frame, snapshot.Needs.Items[0].ID) {
		t.Errorf("the frame dropped what it had:\n%s", frame)
	}
	for _, want := range []string{"control plane unreachable", "read 1m ago"} {
		if !strings.Contains(frame, want) {
			t.Errorf("no %q in:\n%s", want, frame)
		}
	}
}

func TestQuitting(t *testing.T) {
	m := loaded(t, top.Snapshot{})
	for _, key := range []string{"q", top.KeyCtrlC, top.KeyEsc} {
		_, cmd := m.Update(top.Pressed(key))
		if cmd == nil {
			t.Fatalf("%v does nothing", key)
		}
		if !top.IsQuit(cmd()) {
			t.Errorf("%v does not quit", key)
		}
	}
}

// An operator reads a command to run: the TUI changes nothing, and each thing it offers is real.
func TestEveryActionNamesACommand(t *testing.T) {
	snapshot := golden(t)
	m := loaded(t, snapshot)
	seen := map[string]bool{}
	for _, item := range snapshot.Needs.Items {
		frame := m.View()
		if item.Work != nil {
			for _, action := range item.Work.Actions {
				seen[action] = true
				command, ok := top.CommandFor(action, item)
				if !ok {
					t.Errorf("%s offers %s, which names no command", item.ID, action)
				}
				if !strings.Contains(frame, command) {
					t.Errorf("%q is not in the frame:\n%s", command, frame)
				}
			}
		}
		m = keyed(t, m, "j")
	}
	for _, action := range api.WorkActions {
		if !slices.Contains(top.Actions(), action) {
			t.Errorf("%s names no command", action)
		}
	}
	if len(seen) == 0 {
		t.Fatal("the golden view offers nothing")
	}
}

// TestALaneShowsItsContextTokensAndCost reads the worker's local telemetry beside the shop floor, so an
// operator sees each lane's context, tokens, and cost with no backend.
func TestALaneShowsItsContextTokensAndCost(t *testing.T) {
	snapshot := golden(t)
	snapshot.Telemetry = api.LaneTelemetryList{Available: true, Lanes: []api.LaneTelemetry{
		{Lane: "lane-wo-accept", ContextTokens: 42000, Tokens: map[string]int64{"input": 1000, "output": 200, "cache_read": 40000}, CostUSD: 0.1234},
	}}
	wide, _ := loaded(t, snapshot).Update(top.Resized(240, 40))
	frame := wide.View()
	var line string
	for candidate := range strings.Lines(frame) {
		if strings.Contains(candidate, "lane-wo-accept") && strings.Contains(candidate, "liveness") {
			line = candidate
		}
	}
	if !strings.Contains(line, "context 42000") || !strings.Contains(line, "tokens 41200") || !strings.Contains(line, "cost $0.1234") {
		t.Errorf("the lane line says nothing of its telemetry: %q", line)
	}
	for candidate := range strings.Lines(frame) {
		if strings.Contains(candidate, "lane-wo-approval") && strings.Contains(candidate, "context") {
			t.Errorf("a lane with no telemetry shows figures: %q", candidate)
		}
	}
}

func TestTheFrameIsWhatItWas(t *testing.T) {
	snapshot := golden(t)
	frame := loaded(t, snapshot).View()
	path := filepath.Join("testdata", "frame.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(frame), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: run this test with -update", err)
	}
	if string(want) != frame {
		t.Errorf("the frame changed: run this test with -update, and read the diff\ngot:\n%s", frame)
	}
}

// A short terminal keeps the cursor in view.
func TestAShortTerminalFollowsTheCursor(t *testing.T) {
	snapshot := golden(t)
	m := loaded(t, snapshot)
	m, _ = m.Update(top.Resized(60, 8))
	for range snapshot.Needs.Items {
		m = keyed(t, m, "j")
	}
	frame := m.View()
	if lines := strings.Count(frame, "\n") + 1; lines > 8 {
		t.Errorf("%d lines in 8:\n%s", lines, frame)
	}
	if !strings.Contains(frame, m.Selected()) {
		t.Errorf("the cursor left the frame:\n%s", frame)
	}
}

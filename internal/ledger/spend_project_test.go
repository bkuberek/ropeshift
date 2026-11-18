package ledger

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// wednesday is a midday, so the day and the week it falls in are both under way.
var wednesday = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC).UnixMilli()

func window(name string, spent, limit, warn float64, raised float64) ProjectWindowSpend {
	return ProjectLimits{Warn: warn}.Window(name, wednesday, spent, limit, raised)
}

// A window with no limit holds nothing back and warns of nothing, whatever was spent.
func TestAWindowWithNoLimitIsNeverPast(t *testing.T) {
	w := window(WindowDay, 1e6, 0, 0.8, 0)
	if w.Past() || w.PastSoft() || w.SoftLimit != 0 || w.HardLimit != 0 || w.Remaining != 0 {
		t.Errorf("no limit reads %+v", w)
	}
}

// The soft limit is the warn share of the hard limit, and a window is past each at the figure and over it.
func TestAWindowIsPastItsSoftAndHardLimitsAtTheFigure(t *testing.T) {
	cases := []struct {
		spent      float64
		soft, hard bool
	}{{0, false, false}, {7.99, false, false}, {8, true, false}, {9.99, true, false}, {10, true, true}, {12, true, true}}
	for _, tt := range cases {
		w := window(WindowDay, tt.spent, 10, 0.8, 0)
		if !near(w.SoftLimit, 8) || !near(w.HardLimit, 10) || w.PastSoft() != tt.soft || w.Past() != tt.hard {
			t.Errorf("spent %v reads %+v, want soft %v and hard %v", tt.spent, w, tt.soft, tt.hard)
		}
	}
}

// A member's raise lifts the limit of the window it was made in and never lowers one, and the room left counts from
// the limit in force.
func TestARaisedWindowLimitIsTheLargerOne(t *testing.T) {
	w := window(WindowDay, 12, 10, 0.8, 15)
	if !near(w.HardLimit, 15) || !near(w.RaisedTo, 15) || w.Past() || !near(w.Remaining, 3) || !near(w.SoftLimit, 12) {
		t.Errorf("a raise to 15 reads %+v", w)
	}
	if w = window(WindowDay, 12, 10, 0.8, 5); !near(w.HardLimit, 10) || w.RaisedTo != 0 || !w.Past() {
		t.Errorf("a raise to 5 lowered the limit: %+v", w)
	}
	// A limit the policy leaves unset has nothing to raise.
	if w = window(WindowDay, 12, 0, 0.8, 15); w.HardLimit != 0 || w.RaisedTo != 0 || w.Past() {
		t.Errorf("a raise of no limit set one: %+v", w)
	}
}

// The hold is the window that outlasts the others: a week that is past its limit holds work longer than a day that is.
func TestTheHoldIsTheWindowThatEndsLast(t *testing.T) {
	limits := ProjectLimits{Daily: 10, Weekly: 40, Warn: 0.8}
	now := wednesday
	for name, tt := range map[string]struct {
		day, week float64
		window    string
	}{
		"under both":     {5, 20, ""},
		"the day only":   {10, 20, WindowDay},
		"the week only":  {5, 40, WindowWeek},
		"both":           {10, 40, WindowWeek},
		"a weekly pause": {3, 45, WindowWeek},
	} {
		spend := limits.Spend(now, tt.day, tt.week, 0, 0)
		hold := spend.Hold()
		switch {
		case tt.window == "" && hold != nil:
			t.Errorf("%s: held %+v", name, hold)
		case tt.window != "" && (hold == nil || hold.Window != tt.window || hold.UntilMs != WindowEnd(tt.window, now)):
			t.Errorf("%s: hold %+v, want %s until %d", name, hold, tt.window, WindowEnd(tt.window, now))
		}
	}
}

// The limits a project runs on are read as raised for the window a raise was made in, so the next day is back to the policy.
func TestARaiseOnlyLiftsTheWindowItWasMadeIn(t *testing.T) {
	now := wednesday
	today := WindowStart(WindowDay, now)
	data := func(window string, start int64, after float64) []byte {
		raw, err := json.Marshal(ProjectLimitChange{Window: window, WindowStart: start, AfterUSD: after})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	events := []Event{
		{Type: EventSpendProjectLimitRaised, Data: data(WindowDay, today, 20)},
		{Type: EventSpendProjectLimitRaised, Data: data(WindowDay, today, 15)},
		{Type: EventSpendProjectLimitRaised, Data: data(WindowDay, today-dayMs, 99)},
		{Type: EventSpendProjectLimitRaised, Data: data(WindowWeek, WindowStart(WindowWeek, now), 70)},
		{Type: "spend.limit_raised", Data: data(WindowDay, today, 500)},
	}
	if got := RaisedProjectLimit(events, WindowDay, now); !near(got, 20) {
		t.Errorf("today's raise reads %v, want the highest of today's, 20", got)
	}
	if got := RaisedProjectLimit(events, WindowDay, now+dayMs); got != 0 {
		t.Errorf("tomorrow reads %v, and a raise ends with its day", got)
	}
	if got := RaisedProjectLimit(events, WindowWeek, now); !near(got, 70) {
		t.Errorf("this week's raise reads %v", got)
	}
	if got := RaisedProjectLimit(events, WindowWeek, now+7*dayMs); got != 0 {
		t.Errorf("next week reads %v", got)
	}
}

// A project with no limit set holds nobody, and a worker is told whether a limit exists so it knows to keep the ledger
// current.
func TestAProjectLimitedIsOneWithAnyLimitSet(t *testing.T) {
	now := wednesday
	if (ProjectLimits{Warn: 0.8}).Spend(now, 1e6, 1e6, 0, 0).Limited() {
		t.Error("a project with no limit is limited")
	}
	for _, limits := range []ProjectLimits{{Daily: 1}, {Weekly: 1}} {
		if !limits.Spend(now, 0, 0, 0, 0).Limited() {
			t.Errorf("%+v is not limited", limits)
		}
	}
}

// A refusal says which limit holds the project, how far it came, when work goes on by itself, and what a member can do.
func TestAProjectLimitRefusalNamesTheLimitAndTheWayOut(t *testing.T) {
	day := (&ProjectLimitError{Window: WindowDay, HardUSD: 10, SpentUSD: 10.5, UntilMs: wednesday}).Error()
	for _, want := range []string{"daily spend limit of $10.00", "$10.50 charged", "2026-11-18T12:00:00Z", "ropeshift spend raise --project day"} {
		if !strings.Contains(day, want) {
			t.Errorf("the refusal %q lacks %q", day, want)
		}
	}
	week := (&ProjectLimitError{Window: WindowWeek, HardUSD: 40, SpentUSD: 41, UntilMs: wednesday}).Error()
	if !strings.Contains(week, "weekly spend limit of $40.00") || !strings.Contains(week, "--project week") {
		t.Errorf("the weekly refusal reads %q", week)
	}
}

// The allowance a worker holds carries the hold, and a lane with no limit of its own is still held by it.
func TestAnAllowanceCarriesTheProjectHold(t *testing.T) {
	a := SpendAllowance{ProjectHold: &ProjectHold{Window: WindowDay, UntilMs: 5}}
	if !a.Held() || (SpendAllowance{}).Held() || a.Crossed(1e6) {
		t.Errorf("held %v, and a hold is not the work order's own limit", a.Held())
	}
}

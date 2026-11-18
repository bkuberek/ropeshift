package ledger

import (
	"testing"
	"time"
)

func instant(t *testing.T, text string) int64 {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.UnixMilli()
}

// A project's day is the UTC calendar day: it starts at midnight UTC, and the last millisecond before the next
// midnight still belongs to it.
func TestAProjectDayIsTheUTCCalendarDay(t *testing.T) {
	for name, tt := range map[string]struct{ now, start, end string }{
		"midday":        {"2026-11-18T12:30:00Z", "2026-11-18T00:00:00Z", "2026-11-19T00:00:00Z"},
		"the first ms":  {"2026-11-18T00:00:00Z", "2026-11-18T00:00:00Z", "2026-11-19T00:00:00Z"},
		"the last ms":   {"2026-11-18T23:59:59.999Z", "2026-11-18T00:00:00Z", "2026-11-19T00:00:00Z"},
		"a new year":    {"2026-12-31T23:00:00Z", "2026-12-31T00:00:00Z", "2027-01-01T00:00:00Z"},
		"a local night": {"2026-11-18T03:00:00-05:00", "2026-11-18T00:00:00Z", "2026-11-19T00:00:00Z"},
	} {
		now := instant(t, tt.now)
		if got := WindowStart(WindowDay, now); got != instant(t, tt.start) {
			t.Errorf("%s: the day starts at %d, want %d", name, got, instant(t, tt.start))
		}
		if got := WindowEnd(WindowDay, now); got != instant(t, tt.end) {
			t.Errorf("%s: the day ends at %d, want %d", name, got, instant(t, tt.end))
		}
	}
}

// A project's week is the ISO week: it starts on Monday at midnight UTC and runs seven days.
func TestAProjectWeekStartsOnMondayUTC(t *testing.T) {
	for name, tt := range map[string]struct{ now, start, end string }{
		"a wednesday": {"2026-11-18T12:00:00Z", "2026-11-16T00:00:00Z", "2026-11-23T00:00:00Z"},
		"the monday":  {"2026-11-16T00:00:00Z", "2026-11-16T00:00:00Z", "2026-11-23T00:00:00Z"},
		"the sunday":  {"2026-11-22T23:59:59.999Z", "2026-11-16T00:00:00Z", "2026-11-23T00:00:00Z"},
		"next monday": {"2026-11-23T00:00:00Z", "2026-11-23T00:00:00Z", "2026-11-30T00:00:00Z"},
		"the epoch":   {"1970-01-01T00:00:00Z", "1969-12-29T00:00:00Z", "1970-01-05T00:00:00Z"},
	} {
		now := instant(t, tt.now)
		if got := WindowStart(WindowWeek, now); got != instant(t, tt.start) {
			t.Errorf("%s: the week starts at %d, want %d", name, got, instant(t, tt.start))
		}
		if got := WindowEnd(WindowWeek, now); got != instant(t, tt.end) {
			t.Errorf("%s: the week ends at %d, want %d", name, got, instant(t, tt.end))
		}
	}
}

// A window the code does not know has no start, so a surface that mistypes one reads no limit and not a wrong one.
func TestAnUnknownWindowHasNoBounds(t *testing.T) {
	if WindowStart("month", 1) != 0 || WindowEnd("month", 1) != 0 || ValidWindow("month") || !ValidWindow(WindowDay) || !ValidWindow(WindowWeek) {
		t.Error("only the day and the week are windows")
	}
}

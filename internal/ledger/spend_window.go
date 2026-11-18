package ledger

// The windows of a project's spend limits (ADR-0037). A project's day is the UTC calendar day and its week is the ISO
// week, which starts on Monday at midnight UTC. The ledger's clock is the one clock a limit reads, so every worker and
// every surface agrees on when a day ends, whatever time zone a person or a machine is in.
const (
	WindowDay  = "day"
	WindowWeek = "week"
)

// dayMs is the span of a UTC day in milliseconds.
const dayMs int64 = 24 * 60 * 60 * 1000

// ValidWindow says a name is a window a project limit has.
func ValidWindow(window string) bool { return window == WindowDay || window == WindowWeek }

// floorDiv divides and rounds toward minus infinity, so a moment before the epoch still falls in its own day.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// WindowStart is the first millisecond of the window a moment falls in, and 0 for a window that is not one.
func WindowStart(window string, ms int64) int64 {
	days := floorDiv(ms, dayMs)
	switch window {
	case WindowDay:
		return days * dayMs
	case WindowWeek:
		// 1970-01-01 was a Thursday, so counting Monday as 0 the weekday of day number n is (n+3) mod 7.
		weekday := ((days+3)%7 + 7) % 7
		return (days - weekday) * dayMs
	}
	return 0
}

// WindowEnd is the first millisecond after the window a moment falls in, which is when a limit that held it back stops
// doing so. It is 0 for a window that is not one.
func WindowEnd(window string, ms int64) int64 {
	start := WindowStart(window, ms)
	switch window {
	case WindowDay:
		return start + dayMs
	case WindowWeek:
		return start + 7*dayMs
	}
	return 0
}

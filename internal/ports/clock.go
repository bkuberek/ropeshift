package ports

import "time"

// Clock gives wall and monotonic time for everything except lease and timer
// deadlines, which use database time.
type Clock interface {
	Now() time.Time
}

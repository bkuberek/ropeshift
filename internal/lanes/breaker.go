package lanes

import "time"

// oneDay is the span of the daily cap on generations.
const oneDay = 24 * time.Hour

// Restart is one charge against a lane's breaker. A key names the act it pays for, so an
// act is charged once however a crash falls.
type Restart struct {
	Key  string
	AtMs int64
	// NotBeforeMs is when the act may run, so a new worker waits out what is left of a
	// delay and does not start one again.
	NotBeforeMs int64
	// Planned pays for a generation nothing crashed to cause, such as a handoff. It counts
	// toward the day and not toward the window, because it is no crash loop.
	Planned bool
}

// Breaker counts crash restarts in a sliding window (FR-LANE-12). MaxPerDay caps a lane's
// generations separately, and zero leaves that cap off.
type Breaker struct {
	MaxRestarts int64
	Window      time.Duration
	MaxPerDay   int64
	// Backoff is the first delay, which doubles for each restart in the window.
	Backoff time.Duration
}

// BreakerVerdict says whether one more restart may run, and after how long.
type BreakerVerdict struct {
	Tripped bool
	Delay   time.Duration
}

// Judge reads what is on record. Time is the only thing that clears a charge short of a
// human reset, so a crash loop trips however many workers it outlives.
func (b Breaker) Judge(charges []Restart, now time.Time) BreakerVerdict {
	var inWindow, inDay int64
	for _, charge := range charges {
		age := now.Sub(time.UnixMilli(charge.AtMs))
		if age < b.Window && !charge.Planned {
			inWindow++
		}
		if age < oneDay {
			inDay++
		}
	}
	if inWindow >= b.MaxRestarts || (b.MaxPerDay > 0 && inDay+1 >= b.MaxPerDay) {
		return BreakerVerdict{Tripped: true}
	}
	delay := b.Backoff
	for range inWindow {
		delay *= 2
		if delay >= b.Window {
			break
		}
	}
	return BreakerVerdict{Delay: min(delay, b.Window)}
}

package lanes

import (
	"fmt"
	"testing"
	"time"
)

func chargesAt(now time.Time, ages ...time.Duration) []Restart {
	out := make([]Restart, 0, len(ages))
	for i, age := range ages {
		out = append(out, Restart{Key: fmt.Sprint(i), AtMs: now.Add(-age).UnixMilli()})
	}
	return out
}

func TestBreakerCountsASlidingWindow(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	breaker := Breaker{MaxRestarts: 3, Window: 10 * time.Minute, MaxPerDay: 8, Backoff: 30 * time.Second}
	tests := []struct {
		name    string
		ages    []time.Duration
		tripped bool
		delay   time.Duration
	}{
		{name: "the first restart waits the base delay", delay: 30 * time.Second},
		{name: "a second waits twice as long", ages: []time.Duration{time.Minute}, delay: time.Minute},
		{name: "a third waits four times", ages: []time.Duration{time.Minute, 2 * time.Minute}, delay: 2 * time.Minute},
		{name: "the limit trips", ages: []time.Duration{time.Minute, 2 * time.Minute, 3 * time.Minute}, tripped: true},
		{
			name: "an old restart leaves the window",
			ages: []time.Duration{time.Minute, 2 * time.Minute, 11 * time.Minute}, delay: 2 * time.Minute,
		},
		{name: "exactly at the window's edge is out of it", ages: []time.Duration{10 * time.Minute}, delay: 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := breaker.Judge(chargesAt(now, tt.ages...), now)
			if got.Tripped != tt.tripped || got.Delay != tt.delay {
				t.Errorf("verdict %+v", got)
			}
		})
	}
}

func TestBreakerHoldsADailyCap(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	breaker := Breaker{MaxRestarts: 5, Window: 10 * time.Minute, MaxPerDay: 4, Backoff: time.Second}
	spread := []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour}
	if got := breaker.Judge(chargesAt(now, spread...), now); !got.Tripped {
		t.Errorf("a first generation and 3 restarts is 4 of 4: %+v", got)
	}
	if got := breaker.Judge(chargesAt(now, spread[0], spread[1], 25*time.Hour), now); got.Tripped {
		t.Errorf("a day-old restart counted: %+v", got)
	}
}

func TestBreakerDelayStopsAtTheWindow(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	breaker := Breaker{MaxRestarts: 100, Window: time.Minute, Backoff: 30 * time.Second}
	ages := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second}
	if got := breaker.Judge(chargesAt(now, ages...), now); got.Delay != time.Minute {
		t.Errorf("delay %v", got.Delay)
	}
}

func TestNoRestartsAllowedTripsAtOnce(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	if got := (Breaker{Window: time.Minute}).Judge(nil, now); !got.Tripped {
		t.Errorf("verdict %+v", got)
	}
}

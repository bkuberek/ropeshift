package lanes

import (
	"fmt"
	"testing"
	"time"
)

// A bound can only tighten a daily cap, and a bound of zero says nothing, because a worker with no
// autonomy to hold it runs on its profile.
func TestABoundOnlyTightensTheDay(t *testing.T) {
	cases := []struct {
		name       string
		day, bound int64
		want       int64
	}{
		{"a tighter bound", 8, 2, 2},
		{"a looser bound", 2, 8, 2},
		{"no bound", 8, 0, 8},
		{"no daily cap", 0, 3, 3},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Breaker{MaxPerDay: tt.day}).Bounded(tt.bound).MaxPerDay; got != tt.want {
				t.Fatalf("MaxPerDay = %d, want %d", got, tt.want)
			}
		})
	}
}

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

// A handoff is a generation nothing crashed to cause, so it pays toward the day and adds
// nothing to a crash loop.
func TestAPlannedGenerationCountsTowardTheDayOnly(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	breaker := Breaker{MaxRestarts: 2, Window: 10 * time.Minute, MaxPerDay: 3, Backoff: time.Second}
	planned := []Restart{
		{Key: "g2", AtMs: now.Add(-time.Minute).UnixMilli(), Planned: true},
		{Key: "g3", AtMs: now.Add(-2 * time.Minute).UnixMilli(), Planned: true},
	}
	if got := breaker.Judge(planned, now); !got.Tripped {
		t.Errorf("two generations and this one reach a cap of 3: %+v", got)
	}
	breaker.MaxPerDay = 4
	if got := breaker.Judge(planned, now); got.Tripped || got.Delay != time.Second {
		t.Errorf("handoffs read as a crash loop: %+v", got)
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

package ledger

import (
	"testing"
	"time"
)

var scheduleStart = time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)

func running(startedAgo time.Duration, now time.Time) []CIRun {
	return []CIRun{{Required: true, Status: CIRunInProgress, StartedAt: now.Add(-startedAgo)}}
}

// FR-SPEND-20: reads follow the expected duration on a clock that runs only while a required run is in progress:
// every steady interval while every run is queued, once at the p50, at half the remaining time to the p85 each
// time and never closer than the minimum gap, then every steady interval.
func TestTheCIReadScheduleFollowsTheExpectedDuration(t *testing.T) {
	now := scheduleStart
	settings := CISettings{}.Effective()
	expected := settings.Expectation(nil)
	tests := []struct {
		name string
		runs []CIRun
		want time.Duration
	}{
		{"no run reported yet", nil, 5 * time.Minute},
		{"every required run queued", []CIRun{{Required: true, Status: CIRunQueued}, {Required: true, Status: CIRunQueued}}, 5 * time.Minute},
		{"a queued run does not start the clock", []CIRun{{Required: true, Status: CIRunQueued, StartedAt: now.Add(-time.Hour)}}, 5 * time.Minute},
		{"just started", running(0, now), 10 * time.Minute},
		{"two minutes in, before the p50", running(2*time.Minute, now), 8 * time.Minute},
		{"at the p50, half the remaining time to the p85", running(10*time.Minute, now), 5 * time.Minute},
		{"between the p50 and the p85", running(15*time.Minute, now), 150 * time.Second},
		{"near the p85, no closer than the minimum gap", running(19*time.Minute+50*time.Second, now), 30 * time.Second},
		{"at the p85, every steady interval", running(20*time.Minute, now), 5 * time.Minute},
		{"far past the p85", running(3*time.Hour, now), 5 * time.Minute},
		{"every run completed", []CIRun{{Required: true, Status: CIRunCompleted, StartedAt: now.Add(-time.Minute)}}, 5 * time.Minute},
		{"an unrequired run does not count", []CIRun{{Required: false, Status: CIRunInProgress, StartedAt: now.Add(-time.Minute)}}, 5 * time.Minute},
		{"the earliest start counts", []CIRun{
			{Required: true, Status: CIRunInProgress, StartedAt: now.Add(-time.Minute)}, {Required: true, Status: CIRunInProgress, StartedAt: now.Add(-9 * time.Minute)},
		}, 60 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := settings.NextRead(expected, tt.runs, now).Sub(now); got != tt.want {
				t.Errorf("the next read is in %s, want %s", got, tt.want)
			}
		})
	}
}

// With a history, the p50 and the p85 are the repository's own and the schedule says so.
func TestTheCIReadScheduleUsesAHistoryWhenThereIsOne(t *testing.T) {
	now := scheduleStart
	settings := CISettings{}.Effective()
	without := settings.Expectation(nil)
	with := settings.Expectation(&CIHistory{P50: 2 * time.Minute, P85: 4 * time.Minute})
	if without.History || !with.History || with.P50 != 2*time.Minute || with.P85 != 4*time.Minute || without.P50 != 10*time.Minute || without.P85 != 20*time.Minute {
		t.Fatalf("expectations %+v and %+v", without, with)
	}
	if got := settings.NextRead(with, running(time.Minute, now), now).Sub(now); got != time.Minute {
		t.Errorf("a run a minute into a 2 minute p50 is read in %s", got)
	}
	if got := settings.NextRead(with, running(5*time.Minute, now), now).Sub(now); got != 5*time.Minute {
		t.Errorf("a run past a 4 minute p85 is read in %s", got)
	}
}

// With a steady interval of 2 minutes, a run past its p85 is read every 2 minutes.
func TestASteadyIntervalOnTheRepositoryIsKept(t *testing.T) {
	settings, err := CISettings{}.Set("ci_steady_interval", "2m")
	if err != nil {
		t.Fatal(err)
	}
	now := scheduleStart
	if got := settings.NextRead(settings.Expectation(nil), running(25*time.Minute, now), now).Sub(now); got != 2*time.Minute {
		t.Errorf("a run past its p85 is read in %s", got)
	}
	if got := settings.NextRead(settings.Expectation(nil), nil, now).Sub(now); got != 2*time.Minute {
		t.Errorf("queued runs are read in %s", got)
	}
}

// A run that takes 3 hours is read about every steady interval once past the expected time, so its reads number
// in the dozens and never in the hundreds, and nothing in the schedule stops it.
func TestAThreeHourRunIsReadAboutEveryFiveMinutesPastTheExpectedTime(t *testing.T) {
	settings := CISettings{}.Effective()
	expected := settings.Expectation(nil)
	started := scheduleStart
	now, reads := started, 0
	for now.Sub(started) < 3*time.Hour {
		reads++
		now = settings.NextRead(expected, []CIRun{{Required: true, Status: CIRunInProgress, StartedAt: started}}, now)
		if reads > 200 {
			t.Fatalf("%d reads by %s", reads, now.Sub(started))
		}
	}
	// One at the p50, a few that close in on the p85, and every five minutes after it.
	if reads < 30 || reads > 45 {
		t.Errorf("a 3 hour run took %d reads", reads)
	}
}

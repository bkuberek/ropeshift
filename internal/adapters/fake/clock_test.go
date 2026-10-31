package fake

import (
	"testing"
	"time"
)

func TestClockMovesOnlyWhenAdvanced(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := NewClock(start)
	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("now %v, want %v", got, start)
	}
	clock.Advance(time.Minute)
	if got := clock.Now(); !got.Equal(start.Add(time.Minute)) {
		t.Fatalf("now %v after an advance", got)
	}
}

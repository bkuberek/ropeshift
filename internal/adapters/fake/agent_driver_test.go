package fake

import (
	"context"
	"sync/atomic"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// A stop waits for no step, so a session stopped inside a step is still running when the stop returns, and so is a
// session a later one of the same lane replaced. WaitForSessions waits for every one of them.
func TestWaitForSessionsWaitsForEverySessionInAStep(t *testing.T) {
	driver := NewAgentDriver()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var finished atomic.Int32
	driver.Play(DriverScript{Turns: [][]DriverStep{{{Do: func(ports.SessionSpec) {
		entered <- struct{}{}
		<-release
		finished.Add(1)
	}}}}})
	spec := ports.SessionSpec{Lane: "lane-1", Generation: 1}
	for range 2 {
		if _, err := driver.Start(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
	}
	<-entered
	<-entered
	driver.StopEverySession()

	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if err := driver.WaitForSessions(gone); err == nil {
		t.Fatal("the wait returned while both sessions were inside a step")
	}

	close(release)
	if err := driver.WaitForSessions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := finished.Load(); got != 2 {
		t.Errorf("%d of 2 steps had returned when the wait did", got)
	}
}

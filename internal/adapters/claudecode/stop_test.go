package claudecode

import (
	"context"
	"errors"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// TestAStopOfAPaneNobodyReadsReturns stops a session in a pane whose stream nobody reads, as a worker on
// its way out leaves its lanes' sessions. The session said more than the stream holds, so its end waits
// behind the stream and is never read while nobody reads. The stop returns within its signals' graces and
// the wait after the last one, with the end unconfirmed, and a reader that comes back gets every event,
// with the end last.
func TestAStopOfAPaneNobodyReadsReturns(t *testing.T) {
	const grace = 20 * time.Millisecond
	terminal := fake.NewTerminal()
	spec := policySpec(t)
	spec.RunMode = ports.RunModeInteractive
	name := paneName(spec)
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: name, Command: []string{"claude", "x"}}); err != nil {
		t.Fatal(err)
	}
	driver := New(nil, Options{Terminal: terminal, StopGrace: grace})
	handle := ports.SessionHandle{ID: sessionHandleID(spec)}
	s := driver.newPaneSession(spec, name, handle, lanes.CostMeter{})
	driver.keep(handle.ID, s, spec)
	said := 2 * eventBuffer
	s.mu.Lock()
	for range said {
		s.push(ports.DriverEvent{Kind: ports.EventUsage, ContextTokens: 1})
	}
	s.mu.Unlock()
	s.follow()
	eventually(t, "a full stream", func() bool { return len(s.events) == cap(s.events) })

	stopped := make(chan error, 1)
	go func() { stopped <- driver.Stop(context.Background(), handle, ports.StopInterrupt) }()
	limit := 2*grace + killWait + 3*time.Second
	select {
	case err := <-stopped:
		if !errors.Is(err, ErrStopUnconfirmed) {
			t.Errorf("a stop whose session's end nobody read returned %v", err)
		}
	case <-time.After(limit):
		t.Fatalf("the stop still waits %v after it began, on an end nobody reads", limit)
	}

	var read []ports.DriverEvent
	deadline := time.After(10 * time.Second)
	for open := true; open; {
		var event ports.DriverEvent
		select {
		case event, open = <-s.stream():
			if open {
				read = append(read, event)
			}
		case <-deadline:
			t.Fatalf("a reader that came back read %d events and no end", len(read))
		}
	}
	if len(read) != said+1 || read[len(read)-1].Kind != ports.EventSessionEnded {
		t.Fatalf("a reader that came back read %d of %d events, with the end last: %v", len(read), said+1,
			len(read) != 0 && read[len(read)-1].Kind == ports.EventSessionEnded)
	}
}

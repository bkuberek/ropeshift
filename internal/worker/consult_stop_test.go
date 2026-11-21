package worker_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// unconfirmedStops is a driver whose first stops send their last signal and read no end of the session, as a real
// driver's does when a process outlives its kill.
type unconfirmedStops struct {
	ports.AgentDriver
	unconfirmed int

	mu    sync.Mutex
	asked int
}

func (d *unconfirmedStops) Stop(ctx context.Context, h ports.SessionHandle, how ports.StopMode) error {
	d.mu.Lock()
	d.asked++
	unconfirmed := d.asked <= d.unconfirmed
	d.mu.Unlock()
	if unconfirmed {
		return fmt.Errorf("%s: %w", h.ID, ports.ErrStopUnconfirmed)
	}
	return d.AgentDriver.Stop(ctx, h, how)
}

func (d *unconfirmedStops) stops() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.asked
}

// FR-ORCH-9: a session stopped at its deadline whose stop read no end is stopped again, so a consultation that outlasts
// its deadline does not run on, and spend, with nobody reading it.
func TestAConsultationsStopIsAskedAgainWhileItsEndIsUnconfirmed(t *testing.T) {
	t.Parallel()
	r := aToolRig(t)
	driver := &unconfirmedStops{AgentDriver: r.config.Driver, unconfirmed: 1}
	r.config.Driver = driver
	r.config.ConsultDeadline = 200 * time.Millisecond
	r.nextSession([]fake.DriverStep{{Bound: true}, {Hold: true}})
	r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	deadline := time.Now().Add(5 * time.Second)
	for driver.stops() < 2 && time.Now().Before(deadline) {
		time.Sleep(superviseStep)
	}
	if stops := driver.stops(); stops < 2 {
		t.Fatalf("the session was stopped %d times, and the first stop read no end", stops)
	}
	if ended := r.awaitConsulted(t, "q1"); ended.Outcome != ledger.ConsultationUnreported {
		t.Errorf("the consultation ended %+v", ended)
	}
}

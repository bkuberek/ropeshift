package worker_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// screenRefusal is a driver whose orchestrator starts stop on a screen the start check does not know, until it stops
// refusing.
type screenRefusal struct {
	ports.AgentDriver
	refuses atomic.Bool
}

func (d *screenRefusal) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if spec.WorkOrder == "" && d.refuses.Load() {
		return ports.SessionHandle{}, fmt.Errorf("check: %w", ports.ErrUnknownScreen)
	}
	return d.AgentDriver.Start(ctx, spec)
}

// Start alone, with no stop before it, runs an orchestrator whose run ended on an unknown screen, and the next run
// reads the message the failed one left.
func TestAStartAloneRunsAnEndedOrchestratorThatReadsWhatWaited(t *testing.T) {
	o := newOrchestratorRig(t, aSession([]fake.DriverStep{{Hold: true}}))
	driver := &screenRefusal{AgentDriver: o.config.Driver}
	driver.refuses.Store(true)
	o.config.Driver = driver
	first := o.start(t)
	o.tick(t)
	_, state := o.told(t, "Are you there?")
	o.awaitNoted(t, 1)
	o.clock.Advance(31 * time.Second)
	deadline := time.Now().Add(20 * time.Second)
	for o.row(t).Actual != "ended" && time.Now().Before(deadline) {
		o.tick(t)
	}
	if ended := o.row(t); ended.Actual != "ended" {
		t.Fatalf("the failed start left %+v", ended)
	}

	driver.refuses.Store(false)
	next := o.start(t)
	if next.Run != first.Run+1 {
		t.Fatalf("the start left run %d after run %d", next.Run, first.Run)
	}
	// The wake the failed run never landed still stands, so the next run needs no new one to come due.
	o.awaitSessions(t, 1)
	if spec := o.specs()[0]; spec.Lane != next.Lane() || !strings.Contains(spec.Brief, "Are you there?") {
		t.Errorf("the next run's session is %+v", spec)
	}
	o.awaitMessage(t, state, messages.HandedOff)
}

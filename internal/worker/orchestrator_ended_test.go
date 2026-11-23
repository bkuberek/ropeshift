package worker_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// screenRefusal is a driver whose orchestrator starts fail with refusal, a screen the start check does not know unless
// a test names another, until it stops refusing. It counts the starts it refused.
type screenRefusal struct {
	ports.AgentDriver
	refuses atomic.Bool
	refusal error
	refused atomic.Int64
}

func (d *screenRefusal) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if spec.WorkOrder == "" && d.refuses.Load() {
		d.refused.Add(1)
		if d.refusal != nil {
			return ports.SessionHandle{}, d.refusal
		}
		return ports.SessionHandle{}, fmt.Errorf("check: %w", ports.ErrUnknownScreen)
	}
	return d.AgentDriver.Start(ctx, spec)
}

// A failed start of an orchestrator is a crash: the run goes on in backoff, under the breaker a crash charges, and the
// generation after the delay starts the session on the wake that waited, whether the start stopped on an unknown screen
// or failed in any other way. Nothing ends the run, and no member acts.
func TestAFailedStartOfAnOrchestratorStartsAgainAfterItsBackoff(t *testing.T) {
	for name, refusal := range map[string]error{"an unknown screen": nil, "a failed spawn": errors.New("the pane did not open")} {
		t.Run(name, func(t *testing.T) {
			o := newOrchestratorRig(t, aSession([]fake.DriverStep{{Hold: true}}))
			driver := &screenRefusal{AgentDriver: o.config.Driver, refusal: refusal}
			driver.refuses.Store(true)
			o.config.Driver = driver
			run := o.start(t)
			o.tick(t)
			_, state := o.told(t, "Are you there?")
			o.awaitNoted(t, 1)
			o.clock.Advance(31 * time.Second)
			o.await(t, "backoff after the failed start", func(row ledger.Orchestrator) bool { return row.Actual == string(lanes.Backoff) })
			if refused := driver.refused.Load(); refused != 1 {
				t.Fatalf("%d starts refused before the backoff passed, want 1", refused)
			}

			driver.refuses.Store(false)
			o.clock.Advance(31 * time.Second)
			o.awaitSessions(t, 1)
			if spec := o.specs()[0]; spec.Lane != run.Lane() || spec.Generation != 2 || !strings.Contains(spec.Brief, "Are you there?") {
				t.Errorf("the session after the backoff is %+v, want run %s's generation 2 on the wake", spec, run.Lane())
			}
			o.awaitMessage(t, state, messages.HandedOff)
			if row := o.row(t); row.Run != run.Run || row.Ended() {
				t.Errorf("the failed start left %+v", row)
			}
			// An orchestrator has no work order, so its next generation owes its prompt nothing the ledger keeps of one,
			// and asks for nothing that could be missing.
			for _, problem := range o.problems() {
				if strings.Contains(problem.Error(), "what its prompt owes") {
					t.Errorf("the next generation said %v", problem)
				}
			}
		})
	}
}

// Start alone, with no stop before it, runs an orchestrator whose run ended on an unknown screen, and the next run
// reads the message the failed one left. A profile with no restarts ends the run at the failed start.
func TestAStartAloneRunsAnEndedOrchestratorThatReadsWhatWaited(t *testing.T) {
	o := newOrchestratorRig(t, aSession([]fake.DriverStep{{Hold: true}}))
	o.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser(strings.Replace(orchestratorProfile, "max_restarts = 2", "max_restarts = 0", 1))
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

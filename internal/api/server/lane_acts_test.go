package server_test

import (
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// eventsAbout counts the events of a type about a subject.
func eventsAbout(t *testing.T, d *apitest.Daemon, subject ledger.Subject, eventType string) int {
	t.Helper()
	count := 0
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(subject)
		for _, event := range events {
			if event.Type == eventType {
				count++
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// A member's resume wakes work only when its wake names a person. Work parked on a usage limit or a sign-in waits on
// that and on nothing a member says, so a resume of it is refused with a conflict and records no signal, and the work
// stays parked. A wake that names a person fires.
func TestAResumeOfWorkThatWaitsOnNoMemberIsRefused(t *testing.T) {
	t.Parallel()
	key := apitest.WorldWorker + ":claude-code"
	for name, tt := range map[string]struct {
		expr  string
		words string
	}{
		"a usage limit": {lanes.UsageWake(key, 0), "when "},
		"a sign-in":     {lanes.SignInWake(key), "when "},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := apitest.Start(t)
			w := apitest.NewWorld(t, d)
			lane := w.Running("wo-waits", "Wait for the sign-in")
			lane.Park(tt.expr)
			_, err := d.Client.ResumeLane(t.Context(), "wo-waits")
			message := messageOf(t, err, http.StatusConflict, api.CodeIllegalMove)
			if !strings.Contains(message, "a resume does not wake work order wo-waits, which wakes "+tt.words) {
				t.Errorf("the resume was refused with %q", message)
			}
			if order, err := d.Client.WorkOrder(t.Context(), "wo-waits"); err != nil || order.State != workorders.Parked {
				t.Errorf("a refused resume left the work order %s: %v", order.State, err)
			}
			signal := ledger.Subject{Kind: parking.SubjectSignal, ID: string(parking.HumanResume) + ":" + lane.ID}
			if n := eventsAbout(t, d, signal, parking.EventSignal); n != 0 {
				t.Errorf("a refused resume recorded %d signals", n)
			}
		})
	}
	t.Run("a person", func(t *testing.T) {
		t.Parallel()
		d := apitest.Start(t)
		w := apitest.NewWorld(t, d)
		lane := w.Running("wo-person", "Wait for a person")
		lane.Park(lanes.HumanWake())
		resumed, err := d.Client.ResumeLane(t.Context(), "wo-person")
		if err != nil || len(resumed.Fired) != 1 {
			t.Fatalf("a resume of work that waits on a person fired %v: %v", resumed.Fired, err)
		}
		if order, err := d.Client.WorkOrder(t.Context(), "wo-person"); err != nil || order.State != workorders.Ready {
			t.Errorf("the resume left the work order %s: %v", order.State, err)
		}
	})
}

// A member's reset is for a lane its breaker stopped. A lane that did not trip is refused, and no reset is counted or
// recorded, as the orchestrator's reset of one is refused.
func TestAResetOfALaneThatDidNotTripIsRefused(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo-working", "Keep working")
	_, err := d.Client.ResetLane(t.Context(), "wo-working")
	message := messageOf(t, err, http.StatusConflict, api.CodeIllegalMove)
	if !strings.Contains(message, "the lane of work order wo-working is working, and only a tripped lane is reset") {
		t.Errorf("the reset was refused with %q", message)
	}
	subject := ledger.Subject{Kind: ledger.SubjectLane, ID: lane.ID}
	if n := eventsAbout(t, d, subject, ledger.EventLaneReset); n != 0 {
		t.Errorf("a refused reset recorded %d lane.reset events", n)
	}
	err = d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		resets, err := tx.Lanes().Resets(lane.ID)
		if resets != 0 {
			t.Errorf("a refused reset counted %d resets", resets)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	lane.Status(ledger.LaneStatus{ActualState: "tripped", Liveness: "dead", ModelReach: "reachable", LedgerReach: "reachable"})
	if _, err := d.Client.ResetLane(t.Context(), "wo-working"); err != nil {
		t.Errorf("a reset of a tripped lane was refused: %v", err)
	}
	if n := eventsAbout(t, d, subject, ledger.EventLaneReset); n != 1 {
		t.Errorf("a reset of a tripped lane recorded %d lane.reset events", n)
	}
}

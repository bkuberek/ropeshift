package control_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// aWokenLaneOnAShelllessPin leaves change work ready again on a lane its worker held, whose last claim pinned `reasoning`,
// the agent that has no shell and that change work cannot run as, as an earlier build's claim could pin it. The lane's row
// and its claim are written as that build left them, and the work order is the ledger's own.
func aWokenLaneOnAShelllessPin(t *testing.T) *planeRig {
	t.Helper()
	r := newPlaneRig(t, ledger.ProtocolVersion)
	r.ready("pa", "wo")
	err := r.stores["pa"].Write(t.Context(), "pa", "test.woken", func(tx ports.WriteTx) error {
		orders, err := tx.WorkOrders().List()
		if err != nil {
			return err
		}
		for i := range orders {
			if orders[i].ID == "wo" {
				orders[i].ResumeLaneID = "lane-wo"
			}
		}
		if err := tx.WorkOrders().ReplaceAll(orders); err != nil {
			return err
		}
		if err := tx.Claims().Record(workorders.Claim{
			ID: "claim-before", WorkOrder: "wo", Worker: "worker-1", Fence: 0, GrantedAt: 1, Lane: "lane-wo",
			Agent: "reasoning", Roster: workorders.RosterPin{Digest: "an-older-roster"},
		}); err != nil {
			return err
		}
		if err := tx.Lanes().Start("lane-wo", "wo", 0, ledger.LanePolicies{}); err != nil {
			return err
		}
		if err := tx.Lanes().Report(ledger.LaneRow{
			ID: "lane-wo", WorkOrder: "wo", WorkerID: "worker-1", Generation: 1, ReportedAt: 1,
			Status: ledger.LaneStatus{ActualState: "parked", Liveness: "dead", ModelReach: "reachable", LedgerReach: "reachable"},
		}); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "note.added", Actor: projectMember, Subject: ledger.Subject{Kind: "note", ID: "woken"}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A claim of a woken lane whose earlier claim pinned an agent that no longer fits its work resumes the lane on the agent the
// roster picks, through the store: the lease the worker is offered names that agent, and the event of the claim says which
// agent the lane left and why.
func TestAClaimOfAWokenLaneThatPinnedAnUnfitAgentResumesOnTheRostersPick(t *testing.T) {
	r := aWokenLaneOnAShelllessPin(t)
	if err := r.claim("pa", "wo"); err != nil {
		t.Fatal(err)
	}
	pick := roster.BuiltinDefaults().Change[roster.SizeMedium]
	var lease ledger.DesiredLease
	for _, offered := range r.desired().Leases {
		if offered.WorkOrder == "wo" {
			lease = offered
		}
	}
	if lease.Lane != "lane-wo" || lease.Agent != pick {
		t.Errorf("the worker is offered the lane %q as agent %q, want lane-wo as %q", lease.Lane, lease.Agent, pick)
	}
	var granted []ledger.Event
	err := r.stores["pa"].Read(t.Context(), "pa", func(tx ports.ReadTx) (err error) {
		granted, err = tx.EventsOfTypeAbout(workorders.EventType(workorders.TriggerClaimGranted), workorders.SubjectKind, []string{"wo"})
		return err
	})
	if err != nil || len(granted) != 1 {
		t.Fatalf("the claim left the events %+v, %v", granted, err)
	}
	var change workorders.Change
	if err := json.Unmarshal(granted[0].Data, &change); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"` + pick + `"`, `"reasoning"`, "denies the shell"} {
		if !strings.Contains(change.Reason, want) {
			t.Errorf("the reason %q of the claim lacks %q", change.Reason, want)
		}
	}
}

package control_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
)

// A lane says which agent it runs as, and a woken lane that left the agent its earlier claim pinned says why, in the
// sentence its claim's event holds, read when the lane is: the lane keeps no copy of it.
func TestALaneShowsItsAgentAndWhyItChanged(t *testing.T) {
	r := aWokenLaneOnAShelllessPin(t)
	workers := control.NewWorkers(r.stores["pa"], "pa", control.WorkerOptions{})
	before, err := workers.Lane(t.Context(), "lane-wo")
	if err != nil {
		t.Fatal(err)
	}
	if before.Agent != "reasoning" || before.AgentNote != "" {
		t.Errorf("before the claim the lane reads agent %q with the note %q, want the pinned agent and no note", before.Agent, before.AgentNote)
	}
	if err := r.claim("pa", "wo"); err != nil {
		t.Fatal(err)
	}
	pick := roster.BuiltinDefaults().Change[roster.SizeMedium]
	after, err := workers.Lane(t.Context(), "lane-wo")
	if err != nil {
		t.Fatal(err)
	}
	if after.Agent != pick {
		t.Errorf("the lane reads agent %q, want %q", after.Agent, pick)
	}
	for _, want := range []string{`"` + pick + `"`, `"reasoning"`, "denies the shell"} {
		if !strings.Contains(after.AgentNote, want) {
			t.Errorf("the lane's note %q lacks %q", after.AgentNote, want)
		}
	}
}

// A lane whose claim kept the agent its earlier claim pinned has no note.
func TestALaneThatKeptItsAgentHasNoNote(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	r.ready("pa", "wo")
	if err := r.claim("pa", "wo"); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewLeases(r.stores["pa"]).Dispatch(t.Context(), "pa", "wo", "lane-wo", projectMember); err != nil {
		t.Fatal(err)
	}
	err := r.stores["pa"].Write(t.Context(), "pa", "test.lane", func(tx ports.WriteTx) error {
		if err := tx.Lanes().Start("lane-wo", "wo", 1, ledger.LanePolicies{}); err != nil {
			return err
		}
		if err := tx.Lanes().Report(ledger.LaneRow{
			ID: "lane-wo", WorkOrder: "wo", WorkerID: "worker-1", Generation: 1, ReportedAt: 1,
			Status: ledger.LaneStatus{ActualState: "working", Liveness: "alive", ModelReach: "reachable", LedgerReach: "reachable"},
		}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: projectMember, Subject: ledger.Subject{Kind: "note", ID: "lane"}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := control.NewWorkers(r.stores["pa"], "pa", control.WorkerOptions{}).Lane(t.Context(), "lane-wo")
	if err != nil {
		t.Fatal(err)
	}
	if want := roster.BuiltinDefaults().Change[roster.SizeMedium]; view.Agent != want || view.AgentNote != "" {
		t.Errorf("the lane reads agent %q with the note %q, want %q and no note", view.Agent, view.AgentNote, want)
	}
}

package worker_test

import (
	"encoding/json"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// laneSetups reads what the ledger recorded of a lane's sessions' starts, oldest first.
func (r *laneRig) laneSetups(lane string) []lanes.Setup {
	r.t.Helper()
	var out []lanes.Setup
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: lane})
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Type != ledger.EventLaneSetup {
				continue
			}
			var setup lanes.Setup
			if err := json.Unmarshal(e.Data, &setup); err != nil {
				return err
			}
			out = append(out, setup)
		}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

// A lane records on itself what its session loaded at its start, the member's own pieces among them, for the
// generation that loaded it, so the lane page and Needs you can say what reached it.
func TestALaneRecordsWhatItsSessionLoaded(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	loaded := &lanes.Setup{
		Model: "resolved-model", Tools: []string{"Read", "Bash"},
		MCPServers: []lanes.SetupServer{{Name: "ropeshift", From: lanes.FromRopeshift}, {Name: "github", From: "user"}},
		Agents:     []string{"general-purpose", "reviewer"},
		Personal:   []lanes.PersonalItem{{Kind: lanes.PieceMCPServer, Name: "github", From: "user"}},
	}
	played := script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)})
	played.Setup = loaded
	r.driver.Play(played)
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	setups := r.laneSetups("lane-1")
	if len(setups) != 1 {
		t.Fatalf("the lane recorded %d setups: %+v", len(setups), setups)
	}
	got := setups[0]
	if got.Generation != 1 || got.Model != "resolved-model" || !slices.Equal(got.Agents, loaded.Agents) ||
		!slices.Equal(got.MCPServers, loaded.MCPServers) || !slices.Equal(got.Personal, loaded.Personal) {
		t.Errorf("the lane recorded %+v, want %+v for generation 1", got, loaded)
	}
}

// A session whose driver reported nothing of what it loaded records nothing, rather than a clean start it never saw.
func TestASessionThatReportedNothingRecordsNoSetup(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if setups := r.laneSetups("lane-1"); len(setups) != 0 {
		t.Errorf("the lane recorded %+v", setups)
	}
}

package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// m1Steps are the events of a work order's walk up to its lane's end, one for each
// step, in order.
var m1Steps = []string{
	workorders.EventCreated,
	workorders.EventType(workorders.TriggerAccepted),
	workorders.EventType(workorders.TriggerContractSubmitted),
	workorders.EventReviewPassed,
	workorders.EventType(workorders.TriggerReadyPassed),
	workorders.EventType(workorders.TriggerClaimGranted),
	workorders.EventLaneDispatched,
	workorders.EventType(workorders.TriggerLaneStarted),
	workorders.EventGenerationBound,
}

// TestM1WalkingSkeleton is the first milestone on the real binary: one work order in
// a repository file, claimed by hand, run through the Claude Code driver in one lane,
// ending with a typed outcome in the ledger. fakeclaude stands for the agent CLI, so
// the real driver, hooks, MCP server, and worker socket all run with no session.
func TestM1WalkingSkeleton(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		state   workorders.State
		last    workorders.Trigger
		outcome workorders.Outcome
		reason  string
	}{
		{"a lane completes", "lane-completed", workorders.Acceptance, workorders.TriggerLaneCompleted, workorders.OutcomeCompleted, ""},
		{"a lane reports it is blocked", "lane-blocked", workorders.Blocked, workorders.TriggerLaneBlocked, workorders.OutcomeBlocked, "the contract names a file that does not exist"},
		// The work order names no bound, so it has the recovery policy's three attempts, and its first failure
		// leaves two (ADR-0014).
		{"a session dies mid-turn", "crash-mid-turn", workorders.Failed, workorders.TriggerLaneFailed, workorders.OutcomeFailed, "crashed"},
		{"a lane ends with no outcome", "no-outcome", workorders.Blocked, workorders.TriggerLaneBlocked, workorders.OutcomeBlocked, "no_outcome"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInstall(t)
			repo := gittest.New(t)
			repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
			repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
			in.path = doubles.InstallFakeClaude(t, tt.fixture)
			d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
			in.waitReady(d)
			in.waitFor("the worker's session", func() bool {
				list := in.workers()
				return len(list) == 1 && list[0].Epoch >= 1
			})
			id := in.onlyWorkOrder()
			in.must("work", "review", id)
			in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
			in.must("lane", "start", id, "--lane", "lane-1")
			in.waitFor("the lane's outcome", func() bool { return in.show(id).State == tt.state })
			row := in.show(id)
			if row.ClaimID != "" || row.LeaseWorker != "" {
				t.Errorf("a lane's end left a holder: %+v", row)
			}
			d.kill()
			events := in.events(t, id)
			want := append(slices.Clone(m1Steps), workorders.EventType(tt.last))
			// A lane reports each response for the resume table, as many as it takes turns, and a baseline
			// once it has a usage report. Its first session waits for the qualification probe of the
			// installed version, and a wait past a second says so once, before the session binds.
			got := slices.DeleteFunc(eventTypes(events), func(event string) bool {
				return event == workorders.EventGenerationResponded || event == workorders.EventGenerationBaseline
			})
			if at := slices.Index(got, workorders.EventAdmissionHeld); at >= 0 {
				if at != len(m1Steps)-1 || !heldFor(t, events, lanes.HeldQualifying) {
					t.Errorf("a hold that is not the probe's, or not before the session: %v", got)
				}
				got = slices.Delete(got, at, at+1)
			}
			if !slices.Equal(got, want) {
				t.Errorf("events\n got %v\nwant %v", got, want)
			}
			var ended workorders.Change
			if err := json.Unmarshal(events[len(events)-1].Data, &ended); err != nil {
				t.Fatal(err)
			}
			if ended.Outcome != tt.outcome || ended.Reason != tt.reason || ended.Lane != "lane-1" || ended.Generation != 1 {
				t.Errorf("the outcome on record: %+v", ended)
			}
			if events[len(events)-1].Actor.Kind != ledger.ActorLane {
				t.Errorf("the outcome names %+v", events[len(events)-1].Actor)
			}
			var bound workorders.Change
			at := slices.IndexFunc(events, func(e ledger.Event) bool { return e.Type == workorders.EventGenerationBound })
			if at < 0 {
				t.Fatal("no binding")
			}
			if err := json.Unmarshal(events[at].Data, &bound); err != nil {
				t.Fatal(err)
			}
			var generation workorders.GenerationBinding
			if err := json.Unmarshal(bound.Payload, &generation); err != nil {
				t.Fatal(err)
			}
			if generation.ModelRequested != "opus" || generation.ModelResolved != "claude-fixture-5-5" {
				t.Errorf("the generation records %+v", generation)
			}
		})
	}
}

// heldFor reports that every hold among a work order's events names one reason.
func heldFor(t *testing.T, events []ledger.Event, reason string) bool {
	t.Helper()
	for _, event := range events {
		if event.Type != workorders.EventAdmissionHeld {
			continue
		}
		var change workorders.Change
		if err := json.Unmarshal(event.Data, &change); err != nil {
			t.Fatal(err)
		}
		var held struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(change.Payload, &held); err != nil || held.Reason != reason {
			return false
		}
	}
	return true
}

// TestM1NoSecretOnDisk reads every file a lane's generation left: the token reaches
// the session only through its environment (ADR-0016).
func TestM1NoSecretOnDisk(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-completed")
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	token := doubles.SeenEnv(t, in.path, "ROPESHIFT_LANE_TOKEN")
	if token == "" {
		t.Fatal("the session saw no token")
	}
	d.kill()
	generated := filepath.Join(in.paths().StateDir, "lanes", "lane-1")
	seen := 0
	err := filepath.WalkDir(generated, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		seen++
		raw, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(raw), token) {
			t.Errorf("%s holds the token", filepath.Base(path))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Error("no generated file to read")
	}
}

func (in *install) onlyWorkOrder() string {
	in.t.Helper()
	var id string
	in.waitFor("intake", func() bool {
		var list api.WorkOrderList
		if err := json.Unmarshal([]byte(in.must("work", "list", "--json")), &list); err != nil {
			in.t.Fatal(err)
		}
		if len(list.WorkOrders) != 1 {
			return false
		}
		id = list.WorkOrders[0].ID
		return true
	})
	return id
}

// events reads a stopped daemon's ledger, in revision order, about one work order.
func (in *install) events(t *testing.T, id string) []ledger.Event {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(in.paths().StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var out []ledger.Event
	err = store.Read(t.Context(), "default", func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, e := range events {
			if e.Subject.Kind == workorders.SubjectKind && e.Subject.ID == id {
				out = append(out, e)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func eventTypes(events []ledger.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

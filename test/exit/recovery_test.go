package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// The steps the scripts below share.
const (
	recoveryWake   = `{"hook": "UserPromptSubmit", "input": {"prompt": "the wake"}}`
	recoveryBrief  = `{"hook": "UserPromptSubmit", "input": {"prompt": "the brief"}}`
	recoveryResult = `{"emit": {"type": "result", "subtype": "success", "is_error": false, "total_cost_usd": 0.01, "usage": {"input_tokens": 3, "output_tokens": 5}}}`
	recoveryCrash  = `{"emit": {"type": "stream_event", "parent_tool_use_id": null, "event": {"type": "message_start", "message": {"usage": {"input_tokens": 3, "output_tokens": 0}}}}}, {"exit": 1}`
	recoveryReport = `{"mcp": "lane_report", "arguments": {"outcome": "completed", "artifacts": ["branch"], "checks": [{"name": "unit", "passed": true}]}}, {"hook": "Stop", "input": {}}`
)

// orchestratorRecovers is an orchestrator whose first wake makes one recovery call, and whose later wakes
// end at once.
func orchestratorRecovers(tool, reason string) string {
	call := `{"mcp": "` + tool + `", "arguments": {"work_order": "wo-1", "reason": "` + reason + `"}, "refusable": true, "keep_answer": true}`
	idle := `[` + recoveryWake + `, ` + recoveryResult + `]`
	return `"orchestrator-1": {"turns": [[{"init": true}, ` + recoveryWake + `, ` + call + `, ` + recoveryResult + `], ` +
		strings.Join([]string{idle, idle, idle, idle, idle, idle}, ", ") + `]}`
}

// recoveryFixture is one binary's script: what lane-1 and lane-2 do, and the orchestrator's call.
func recoveryFixture(lanes ...string) string {
	return `{"source": "B4b: an orchestrator's recovery acts, scripted", "turns": [[{"init": true}, ` + recoveryBrief + `, ` + recoveryResult + `]], "lanes": {` +
		strings.Join(lanes, ", ") + `}}`
}

// recoveryInstall starts a daemon whose one source holds a profile of its own, and whose user profile wakes
// an orchestrator at once.
func recoveryInstall(t *testing.T, fixture, profile string) (*install, string) {
	t.Helper()
	in := newInstall(t)
	in.path = doubles.InstallFakeClaude(t, "no-session")
	if err := os.WriteFile(filepath.Join(doubles.BinDir(in.path), doubles.FixtureFile), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(in.paths().StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.paths().StateDir, profiles.UserFile), []byte(wakesNow), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", profile)
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(func() {
		d.kill()
		if t.Failed() {
			t.Logf("the daemon said: %s", d.stderr.tail(60))
		}
	})
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	in.waitFor("the source", func() bool { return len(in.sources()) == 1 })
	for _, lane := range []string{"lane-1", "lane-2", ledger.Orchestrator{ID: ledger.OrchestratorOf(in.workers()[0].ID), Run: 1}.Lane()} {
		doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", lane))
	}
	return in, in.workers()[0].ID
}

// recoveryWork records wo-1 on the source's repository with the attempts it allows, zero for none of its
// own, as ledger work a member reviewed, and runs lane-1 on it.
func (in *install) recoveryWork(attempts int64) {
	in.t.Helper()
	_, err := control.NewWorkOrders(in.ledgerOf()).Record(in.t.Context(), soloScope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: "wo-1", Kind: workorders.KindChange, Title: "Add the retry budget", RepositoryID: in.sources()[0].Instance, MaxAttempts: attempts,
		},
		Statement: "Retry a failed call twice.", Criteria: []planning.Criterion{{ID: "c1", Text: "It retries twice."}},
	}, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	if err != nil {
		in.t.Fatal(err)
	}
	in.must("work", "review", "wo-1")
	in.must("work", "claim", "wo-1", "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", "wo-1", "--lane", "lane-1")
}

// laneOnRecord reads a lane once the ledger holds it, and says when it does not yet.
func (in *install) laneOnRecord(id string) (ledger.LaneView, bool) {
	in.t.Helper()
	code, out, _ := in.run("lane", "show", id, "--json")
	if code != 0 {
		return ledger.LaneView{}, false
	}
	var view ledger.LaneView
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		in.t.Fatal(err)
	}
	return view, true
}

// recoveryActs reads the recovery acts on record.
func (in *install) recoveryActs() []authority.RecoveryRecord {
	in.t.Helper()
	var out []authority.RecoveryRecord
	err := in.ledgerOf().Read(in.t.Context(), soloScope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(func(after int64, limit int) ([]ledger.Event, error) { return tx.Events(after, limit) })
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Type != authority.EventRecovery {
				continue
			}
			var record authority.RecoveryRecord
			if err := json.Unmarshal(event.Data, &record); err != nil {
				return err
			}
			out = append(out, record)
		}
		return nil
	})
	if err != nil {
		in.t.Fatal(err)
	}
	return out
}

// TestAnOrchestratorRestartsAStalledLaneWithinPolicy is FR-ORCH-8's first act on the real binary: a lane
// that shows no progress wakes its orchestrator, whose lane_restart ends the stalled generation and starts
// the next with start reason user_respawn, and the act is on record with its reason and policy version.
func TestAnOrchestratorRestartsAStalledLaneWithinPolicy(t *testing.T) {
	stalls := `"lane-1": {"turns": [[{"init": true}, ` + recoveryBrief + `, {"first_time": "stalled", "then": [{"hold": true}]}, ` +
		`{"after_first": "stalled", "then": [` + recoveryReport + `, ` + recoveryResult + `]}]]}`
	in, worker := recoveryInstall(t, recoveryFixture(stalls, orchestratorRecovers("lane_restart", "lane-1 shows no progress")),
		"[lifecycle]\nstall_after = \"1s\"\nhandoff_timeout = \"1s\"\n")
	in.recoveryWork(1)
	in.waitFor("the stall", func() bool {
		lane, ok := in.laneOnRecord("lane-1")
		return ok && slices.Contains(lane.Conditions, "stalled")
	})
	in.must("orchestrator", "start", worker)

	in.waitFor("the restart", func() bool { return len(in.lane("lane-1").Generations) >= 2 })
	generations := in.lane("lane-1").Generations
	if generations[1].StartReason != ledger.StartUserRespawn {
		t.Errorf("the next generation started for %s", generations[1].StartReason)
	}
	in.waitFor("the restarted lane's outcome", func() bool { return in.show("wo-1").State == workorders.Acceptance })
	acts := in.recoveryActs()
	if len(acts) != 1 {
		t.Fatalf("recovery acts %+v", acts)
	}
	act := acts[0]
	if act.Act != authority.RecoverRestart || act.Result != authority.RecoveryDone || act.Lane != "lane-1" || act.Reason != "lane-1 shows no progress" ||
		act.Actor != "orchestrator:"+worker || act.PolicyVersion != 1 || act.Generation < 1 {
		t.Errorf("the act on record %+v", act)
	}
}

// TestFailedWorkStaysFailedUntilTheOrchestratorRetriesIt is ADR-0014's change to the state machine on the
// real binary: a crashed lane leaves its work failed with the attempts the recovery policy gives work that
// names none, nothing retries it on its own, and the orchestrator's work_retry returns it to ready within
// that policy, with the next lane told why.
func TestFailedWorkStaysFailedUntilTheOrchestratorRetriesIt(t *testing.T) {
	crashes := `"lane-1": {"turns": [[{"init": true}, ` + recoveryCrash + `]]}`
	completes := `"lane-2": {"turns": [[{"init": true}, ` + recoveryBrief + `, ` + recoveryReport + `, ` + recoveryResult + `]]}`
	in, worker := recoveryInstall(t, recoveryFixture(crashes, completes, orchestratorRecovers("work_retry", "the crash was a network fault")),
		"[lifecycle]\nmax_restarts = 0\n")
	in.recoveryWork(0)
	in.waitFor("the failure", func() bool { return in.show("wo-1").State == workorders.Failed })
	time.Sleep(aFewPasses)
	if row := in.show("wo-1"); row.State != workorders.Failed || row.Attempts != 1 || row.MaxAttempts != 0 {
		t.Fatalf("failed work with attempts left moved on its own: %s, %d of %d", row.State, row.Attempts, row.MaxAttempts)
	}
	in.must("orchestrator", "start", worker)
	in.waitFor("the retry", func() bool { return in.show("wo-1").State == workorders.Ready })
	acts := in.recoveryActs()
	if len(acts) != 1 || acts[0].Act != authority.RecoverRetry || acts[0].Result != authority.RecoveryDone || acts[0].Lane != "lane-1" {
		t.Fatalf("recovery acts %+v", acts)
	}
	in.must("work", "claim", "wo-1", "--worker", worker, "--claim-id", "c2")
	in.must("lane", "start", "wo-1", "--lane", "lane-2")
	in.waitFor("the retried attempt's outcome", func() bool { return in.show("wo-1").State == workorders.Acceptance })
	prompts := in.firstPrompts(t)
	if !strings.Contains(prompts, "the crash was a network fault") || !strings.Contains(prompts, workorders.ReasonCrashed) {
		t.Errorf("the next lane was not told why: %q", prompts)
	}
}

// TestABreakerResetPastTheDaysBoundReachesAMember is the refusal on the real binary: with no breaker reset
// left in the day, an orchestrator's lane_reset changes nothing and raises a request that waits in Needs
// you with its reason and limit, and a member's approval resets the breaker as the member's decision.
func TestABreakerResetPastTheDaysBoundReachesAMember(t *testing.T) {
	crashes := `"lane-1": {"turns": [[{"init": true}, ` + recoveryCrash + `]]}`
	in, worker := recoveryInstall(t, recoveryFixture(crashes, orchestratorRecovers("lane_reset", "the crashes were a flaky test")),
		"[lifecycle]\nmax_restarts = 1\nrestart_window = \"1h\"\nrestart_backoff = \"10ms\"\n")
	policy := filepath.Join(t.TempDir(), "recovery.toml")
	body := strings.Replace(strings.Replace(authority.BuiltInRecoveryPolicyBody, "version = 1", "version = 2", 1), "max_breaker_resets_per_day = 2", "max_breaker_resets_per_day = 0", 1)
	if err := os.WriteFile(policy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	in.must("policy", "publish", "--file", policy)
	in.recoveryWork(1)
	in.waitFor("the trip", func() bool {
		lane, ok := in.laneOnRecord("lane-1")
		return ok && lane.ActualState == "tripped"
	})
	in.must("orchestrator", "start", worker)

	var request authority.Approval
	in.waitFor("the request", func() bool {
		var list api.ApprovalList
		if err := json.Unmarshal([]byte(in.must("approval", "list", "--state", "pending", "--json")), &list); err != nil {
			t.Fatal(err)
		}
		for _, row := range list.Approvals {
			if row.Kind == authority.KindRecovery {
				request = row
				return true
			}
		}
		return false
	})
	if request.Subject != string(authority.RecoverReset) || request.WorkOrder != "wo-1" || request.Lane != "lane-1" {
		t.Fatalf("the request %+v", request)
	}
	if lane := in.lane("lane-1"); lane.ActualState != "tripped" {
		t.Fatalf("a refused reset moved the lane to %s", lane.ActualState)
	}
	var needs api.NeedsYou
	if err := json.Unmarshal([]byte(in.must("needs", "--json")), &needs); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(needs.Items, func(item api.NeedItem) bool { return item.Approval != nil && item.Approval.ID == request.ID }) {
		t.Errorf("Needs you lacks the request: %+v", needs.Items)
	}
	shown := in.must("approval", "show", request.ID)
	for _, want := range []string{"the crashes were a flaky test", "max_breaker_resets_per_day", "outside recovery policy version 2"} {
		if !strings.Contains(shown, want) {
			t.Errorf("approval show lacks %q:\n%s", want, shown)
		}
	}
	acts := in.recoveryActs()
	if len(acts) == 0 || acts[0].Result != authority.RecoveryRefused || acts[0].Check != authority.CheckBreakerResets || acts[0].Approval != request.ID || acts[0].PolicyVersion != 2 {
		t.Fatalf("the refusal on record %+v", acts)
	}

	in.must("approval", "approve", request.ID, "--digest", request.SubjectDigest)
	in.waitFor("the member's reset", func() bool {
		return slices.ContainsFunc(in.lane("lane-1").Generations, func(g ledger.Generation) bool { return g.StartReason == ledger.StartReset })
	})
	acts = in.recoveryActs()
	if !slices.ContainsFunc(acts, func(a authority.RecoveryRecord) bool {
		return a.Result == authority.RecoveryDone && a.Approval == request.ID && strings.HasPrefix(a.Actor, string(ledger.ActorMember)+":")
	}) {
		t.Errorf("no grant on record: %+v", acts)
	}
}

// TestARetryPastThePolicysAttemptsReachesAMember is the other side of the policy's attempts on the real
// binary: under a policy of one attempt, the orchestrator's work_retry changes nothing and raises a request
// that waits in Needs you, and a member's approval adds an attempt and returns the work to ready.
func TestARetryPastThePolicysAttemptsReachesAMember(t *testing.T) {
	crashes := `"lane-1": {"turns": [[{"init": true}, ` + recoveryCrash + `]]}`
	in, worker := recoveryInstall(t, recoveryFixture(crashes, orchestratorRecovers("work_retry", "the crash was a network fault")),
		"[lifecycle]\nmax_restarts = 0\n")
	policy := filepath.Join(t.TempDir(), "recovery.toml")
	body := strings.Replace(strings.Replace(authority.BuiltInRecoveryPolicyBody, "version = 1", "version = 2", 1), "max_attempts = 3", "max_attempts = 1", 1)
	if err := os.WriteFile(policy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	in.must("policy", "publish", "--file", policy)
	in.recoveryWork(0)
	in.waitFor("the failure", func() bool { return in.show("wo-1").State == workorders.Failed })
	in.must("orchestrator", "start", worker)

	var request authority.Approval
	in.waitFor("the request", func() bool {
		var needs api.NeedsYou
		if err := json.Unmarshal([]byte(in.must("needs", "--json")), &needs); err != nil {
			t.Fatal(err)
		}
		for _, item := range needs.Items {
			if item.Approval != nil && item.Approval.Kind == authority.KindRecovery {
				request = *item.Approval
				return true
			}
		}
		return false
	})
	if request.Subject != string(authority.RecoverRetry) || request.WorkOrder != "wo-1" {
		t.Fatalf("the request %+v", request)
	}
	if row := in.show("wo-1"); row.State != workorders.Failed {
		t.Fatalf("a refused retry moved the work to %s", row.State)
	}
	shown := in.must("approval", "show", request.ID)
	for _, want := range []string{"the crash was a network fault", "max_attempts: the work order used 1 of max_attempts 1", "outside recovery policy version 2"} {
		if !strings.Contains(shown, want) {
			t.Errorf("approval show lacks %q:\n%s", want, shown)
		}
	}
	in.must("approval", "approve", request.ID, "--digest", request.SubjectDigest)
	in.waitFor("the member's retry", func() bool { return in.show("wo-1").State == workorders.Ready })
	if row := in.show("wo-1"); row.Attempts != 1 || row.MaxAttempts != 2 {
		t.Errorf("a member's grant left %d of %d attempts", row.Attempts, row.MaxAttempts)
	}
}

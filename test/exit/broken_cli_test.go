package exit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

// laneEndLimit bounds how long a lane on a CLI that cannot run may take to end. A start that fails
// ends at once, and a crashed session's first restart waits 30 seconds, which this stays under.
const laneEndLimit = 20 * time.Second

// TestALaneOnACLIThatFailsItsVersionCheckEndsAtOnce is a lane started on a `claude` that exits 70 to
// every run. The worker advertises the CLI as unusable, so no work that names the driver reaches it,
// and a lane a member starts anyway, on work that names none, fails at its start with the reason on
// record. No session starts, so nothing waits out a restart's back-off.
func TestALaneOnACLIThatFailsItsVersionCheckEndsAtOnce(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 3\nrestart_backoff = \"30s\"\n")
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallBrokenClaude(t, "exit 70")
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	t.Cleanup(d.stop)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		workers := in.workers()
		return len(workers) == 1 && workers[0].Epoch >= 1 && len(workers[0].Manifest.Drivers) == 1
	})
	worker := in.workers()[0]
	if driver := worker.Manifest.Drivers[0]; driver.Present || !strings.Contains(driver.Detail, "claude --version exited with status 70") {
		t.Errorf("the worker advertises %+v", driver)
	}
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", worker.ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	for deadline := time.Now().Add(laneEndLimit); in.show(id).Attempts == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_, lane, _ := in.run("lane", "show", "lane-1")
			t.Fatalf("the lane did not end within %v\nlane %s\nruns %q\ndaemon %s", laneEndLimit, lane, doubles.BrokenClaudeRuns(t, in.path), d.stderr.String())
		}
	}
	if log := d.stderr.String(); !strings.Contains(log, "lane lane-1: spawn_failed: claude --version exited with status 70") {
		t.Errorf("the daemon did not say why the lane failed:\n%s", log)
	}
	d.stop()
	var ended *workorders.Change
	for _, event := range in.events(t, id) {
		if event.Actor.Kind != ledger.ActorLane {
			continue
		}
		var change workorders.Change
		if err := json.Unmarshal(event.Data, &change); err != nil {
			t.Fatal(err)
		}
		if change.Outcome != "" {
			ended = &change
		}
	}
	if ended == nil || ended.Outcome != workorders.OutcomeFailed || ended.Reason != workorders.ReasonSpawnFailed || ended.Generation != 1 {
		t.Errorf("the lane's end on record: %+v", ended)
	}
	for _, run := range doubles.BrokenClaudeRuns(t, in.path) {
		if run != "--version" {
			t.Errorf("a CLI that failed its version check ran with %q", run)
		}
	}
}

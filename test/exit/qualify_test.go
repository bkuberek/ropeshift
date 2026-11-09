package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// qualified lists what the ledger recorded of the worker's probes, in order.
func (in *install) qualified() []lanes.Qualification {
	in.t.Helper()
	var out []lanes.Qualification
	err := in.ledgerOf().Read(in.t.Context(), "default", func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		if err != nil {
			return err
		}
		replayed, err := lanes.ReplayQualifications(events)
		if err != nil || len(replayed) > 1 {
			in.t.Errorf("the log replays to %+v, %v", replayed, err)
		}
		for _, event := range events {
			if event.Type == ledger.EventDriverQualified {
				var q lanes.Qualification
				if err := json.Unmarshal(event.Data, &q); err != nil {
					return err
				}
				out = append(out, q)
			}
		}
		return nil
	})
	if err != nil {
		in.t.Fatal(err)
	}
	return out
}

// times reads the moments a double wrote to one of its logs, in milliseconds.
func (in *install) times(kind string) []int64 {
	in.t.Helper()
	raw, err := os.ReadFile(filepath.Join(doubles.BinDir(in.path), kind+".log"))
	if err != nil {
		return nil
	}
	var out []int64
	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(line)
		if at, err := strconv.ParseInt(fields[len(fields)-1], 10, 64); err == nil {
			out = append(out, at)
		}
	}
	return out
}

// TestAnUpgradeIsProbedBeforeWorkRuns is FR-DRV-2 on the real binary with fakeclaude: a version the
// shipped matrix does not hold triggers one probe on this machine, no lane's session starts until it
// ends, and a CLI that updates itself under the running worker is probed again before the next lane.
func TestAnUpgradeIsProbedBeforeWorkRuns(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaudeVersion(t, "lane-completed", "2.1.399")
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
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	got := in.qualified()
	if len(got) != 1 || got[0].Version != "2.1.399" || got[0].Degraded || got[0].Driver != claudecode.DriverName {
		t.Fatalf("recorded %+v", got)
	}
	probes := len(doubles.ProbesSeenBy(t, doubles.BinDir(in.path)))
	if probes < 3 {
		t.Fatalf("%d probe sessions", probes)
	}
	ends, lane := in.times("probe-ended"), in.times("started")
	if len(ends) == 0 || len(lane) == 0 {
		t.Fatalf("probe ends %v, lane prompts %v", ends, lane)
	}
	for _, end := range ends {
		if end > lane[0] {
			t.Errorf("a probe session ended at %d, after the lane's first prompt at %d", end, lane[0])
		}
	}

	doubles.SetFakeClaudeVersion(t, in.path, "2.1.400")
	repo.Commit("beta.md", contract.ContractText("beta", "Beta", "Add the second budget."))
	var second string
	for _, other := range in.workOrders(2) {
		if other != id {
			second = other
		}
	}
	in.must("work", "review", second)
	in.must("work", "claim", second, "--worker", in.workers()[0].ID, "--claim-id", "c2")
	in.must("lane", "start", second, "--lane", "lane-2")
	in.waitFor("the second lane's outcome", func() bool { return in.show(second).State == workorders.Acceptance })
	got = in.qualified()
	if len(got) != 2 || got[1].Version != "2.1.400" {
		t.Fatalf("after the update, recorded %+v", got)
	}
	if again := len(doubles.ProbesSeenBy(t, doubles.BinDir(in.path))); again != 2*probes {
		t.Errorf("%d probe sessions for two versions, and one version took %d", again, probes)
	}
	d.kill()
}

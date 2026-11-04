package exit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

// contextProfile puts a lane's thresholds where a fixture reaches them.
const contextProfile = `[lifecycle]
max_restarts = 0
handoff_timeout = "30s"

[context]
min_headroom_tokens = 40

[context.roles.writer]
warn_tokens = 120
compact_tokens = 150
handoff_tokens = 180
`

// TestAContextPolicyRunsAcrossALane walks the policy on the real binary. A lane past its compact
// threshold at rest compacts through its stream, is told to go on, and the first call of that turn
// shows context fell. Later in that turn it crosses its handoff threshold: the next tool call is
// denied by a hook that reads only a file, the instruction arrives between two tool calls, the tool
// after it runs, and what the lane writes starts the next generation.
func TestAContextPolicyRunsAcrossALane(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", contextProfile)
	repo.Commit("requirements.md", packRequirementsFile)
	repo.Commit("contract.md", packContractFile)
	in.path = doubles.InstallFakeClaude(t, "context-lane")
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
	d.kill()

	dir := doubles.BinDir(in.path)
	prompts := logged(t, dir, "prompts")
	compact := indexOf(prompts, strconv.Quote("/compact"))
	wentOn := indexOf(prompts, strconv.Quote(lanes.AfterCompaction))
	if compact < 0 || wentOn < compact {
		t.Errorf("the worker did not compact and then go on: %q", prompts)
	}
	denied := logged(t, dir, "denied")
	if len(denied) != 1 || !strings.Contains(denied[0], lanes.ReasonHandOff) {
		t.Errorf("denied %q", denied)
	}
	worktree := filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", "lane-1")
	if _, err := os.Stat(filepath.Join(worktree, "denied-tool-ran")); !os.IsNotExist(err) {
		t.Errorf("the held tool ran: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "free-tool-ran")); err != nil {
		t.Errorf("the tool after the instruction: %v", err)
	}
	var generations []ledger.Generation
	err := in.ledgerOf().Read(t.Context(), "default", func(tx ports.ReadTx) error {
		var err error
		generations, err = tx.Lanes().Generations("lane-1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(generations) != 2 || generations[1].StartReason != ledger.StartHandoff {
		t.Errorf("generations %+v", generations)
	}
}

func logged(t *testing.T, dir, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name+".log"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for line := range strings.Lines(string(raw)) {
		_, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
		out = append(out, rest)
	}
	return out
}

func indexOf(lines []string, want string) int {
	for i, line := range lines {
		if line == want {
			return i
		}
	}
	return -1
}

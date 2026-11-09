package exit

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestACompactionAndAClearRunAcrossALane walks the hook sequences on the real binary, in both run
// modes. A compaction nobody asked for re-grounds the session and moves nothing, a clear begins a
// generation in the same process, the session it leaves is told its work, and the token that
// process started with reports the outcome as the generation it is in now.
func TestACompactionAndAClearRunAcrossALane(t *testing.T) {
	for name, profile := range map[string]string{"headless": "", "pane": paneProfile} {
		t.Run(name, func(t *testing.T) {
			if profile != "" {
				doubles.RequireTmux(t)
			}
			in := newInstall(t)
			repo := gittest.New(t)
			repo.Commit(".ropeshift/profile.toml", profile)
			repo.Commit("requirements.md", packRequirementsFile)
			repo.Commit("contract.md", packContractFile)
			in.path = doubles.InstallFakeClaude(t, "lane-clears")
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

			var generations []ledger.Generation
			err := in.ledgerOf().Read(t.Context(), "default", func(tx ports.ReadTx) error {
				var err error
				generations, err = tx.Lanes().Generations("lane-1")
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(generations) != 2 || generations[1].StartReason != ledger.StartClear || generations[1].HandoffIn != "" {
				t.Fatalf("generations %+v", generations)
			}
			if generations[0].SessionID == generations[1].SessionID || generations[1].SessionID == "" {
				t.Errorf("sessions %q and %q", generations[0].SessionID, generations[1].SessionID)
			}
			dir := doubles.BinDir(in.path)
			if seen := doubles.SeenBy(t, dir); len(seen) != 1 {
				t.Errorf("%d processes: a clear starts none", len(seen))
			}
			grounding := logged(t, dir, "grounding")
			if len(grounding) != 2 {
				t.Fatalf("grounding %q", grounding)
			}
			for i, want := range []string{"generation 1", "generation 2"} {
				if !strings.Contains(grounding[i], want) || !strings.Contains(grounding[i], id) {
					t.Errorf("after hook %d: %q", i, grounding[i])
				}
			}
			if !slices.ContainsFunc(grounding, func(line string) bool { return strings.Contains(line, "context pack") }) {
				t.Errorf("no pack named: %q", grounding)
			}
		})
	}
}

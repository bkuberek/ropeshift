package exit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/contextpacks"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

const packContractFile = `+++
kind = "contract"
id = "packed"
title = "Packed"
requirements = ["PK-1"]

[[criteria]]
id = "c1"
text = "It works"

[[work_orders]]
id = "packed-order"
title = "Packed work"
size = "small"
+++

The statement of the packed contract.
`

const packRequirementsFile = `+++
kind = "requirements"
id = "reqs"
title = "Requirements"

[[requirement]]
id = "PK-1"
text = "A requirement a lane reads."
+++
`

// packOf reads the only pack a daemon left, from a ledger nothing holds now.
func (in *install) packOf(t *testing.T) (contextpacks.Pack, []ledger.Generation) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(in.paths().StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var pack contextpacks.Pack
	var generations []ledger.Generation
	err = store.Read(t.Context(), "default", func(tx ports.ReadTx) error {
		packs, err := tx.Packs().All()
		if err != nil {
			return err
		}
		if len(packs) != 1 {
			t.Fatalf("%d packs", len(packs))
		}
		pack = packs[0]
		generations, err = tx.Lanes().Generations("lane-1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return pack, generations
}

// TestAPackReachesASession walks a pack from a repository's files to a session on the real binary:
// built at the claim, left in the worktree where git does not see it, held in the system prompt
// byte for byte, and the same knowledge read back through `ropeshift mcp`.
func TestAPackReachesASession(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", "[lifecycle]\nmax_restarts = 0\n")
	repo.Commit("requirements.md", packRequirementsFile)
	repo.Commit("contract.md", packContractFile)
	in.path = doubles.InstallFakeClaude(t, "pack-lane")
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

	pack, generations := in.packOf(t)
	dir := doubles.BinDir(in.path)
	var prompts []string
	for _, seen := range doubles.SeenBy(t, dir) {
		if seen.SystemPrompt != "" {
			prompts = append(prompts, seen.SystemPrompt)
		}
	}
	if len(prompts) != 1 || prompts[0] != pack.Text {
		t.Fatalf("a session held %d prompts, and the ledger holds %q", len(prompts), pack.Text)
	}
	for _, text := range []string{"The statement of the packed contract.", "A requirement a lane reads."} {
		if !strings.Contains(pack.Text, text) {
			t.Errorf("the pack does not say %q:\n%s", text, pack.Text)
		}
	}
	if len(generations) != 1 || generations[0].PackID != pack.ID || generations[0].PackVersion != pack.Version {
		t.Errorf("generations %+v", generations)
	}
	worktree := filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", "lane-1")
	onDisk, err := os.ReadFile(filepath.Join(worktree, lanes.PackFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != pack.Text {
		t.Errorf("the file holds %q", onDisk)
	}
	status := exec.Command("git", "status", "--porcelain", "--ignored=no")
	status.Dir = worktree
	if out, err := status.CombinedOutput(); err != nil || len(out) != 0 {
		t.Errorf("git sees %q: %v", out, err)
	}
	reads, err := os.ReadFile(filepath.Join(dir, "tool.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reads), "A requirement a lane reads.") {
		t.Errorf("a read through the MCP server found %s", reads)
	}
	if !strings.Contains(string(reads), "Nothing on record matches this.") {
		t.Errorf("a miss through the MCP server read %s", reads)
	}
}

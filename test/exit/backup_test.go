package exit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
)

// TestABackupOpensAsALedger is the online backup path through the binary: a running daemon copies
// its ledger to a file a member names, a path already taken is refused, and a new installation runs
// on the copy as its own ledger, holding what it held when the copy was made.
func TestABackupOpensAsALedger(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	in.must("work", "create", "--id", "w1", "--title", "Kept by the backup", "--criterion", "C")
	path := filepath.Join(t.TempDir(), "ledger-backup.db")
	if out := in.must("backup", path); !strings.Contains(out, path) {
		t.Errorf("the backup said %q", out)
	}
	in.must("work", "create", "--id", "w2", "--title", "After the backup", "--criterion", "C")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := in.run("backup", path); code != cli.ExitUsage || !strings.Contains(stderr, "exists") {
		t.Fatalf("a backup over the first: exit %d, stderr %q", code, stderr)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
		t.Fatalf("a refused backup changed the file: %v", err)
	}
	d.kill()

	restored := newInstall(t)
	state := restored.paths().StateDir
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, api.LedgerFileName), before, 0o600); err != nil {
		t.Fatal(err)
	}
	r := restored.start()
	restored.waitReady(r)
	if got := restored.show("w1").Title; got != "Kept by the backup" {
		t.Errorf("the copy holds %q", got)
	}
	if code, _, _ := restored.run("work", "show", "w2"); code != cli.ExitNotFound {
		t.Errorf("work created after the backup reads with exit %d", code)
	}
}

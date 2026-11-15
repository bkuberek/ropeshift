package entry

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// The worker's checkouts name the installation's state and run directories as its own, so a manifest never
// registers a clone inside them, such as a lane's worktree (ADR-0022). Dropping them from the wiring would
// leave every other test green, so the checkouts are asked about a path in each.
func TestTheWorkersCheckoutsReserveTheInstallationsOwnDirectories(t *testing.T) {
	root := t.TempDir()
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run")}
	for _, dir := range []string{filepath.Join(paths.StateDir, "lanes", "one"), filepath.Join(paths.RunDir, "sockets"), filepath.Join(root, "work", "api")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	kept := soloCheckouts(paths)
	for at, want := range map[string]bool{
		filepath.Join(paths.StateDir, "lanes", "one"): true,
		filepath.Join(paths.RunDir, "sockets"):        true,
		paths.StateDir:                                true,
		filepath.Join(root, "work", "api"):            false,
	} {
		if _, reserved, err := kept.Resolve(t.Context(), at); err != nil || reserved != want {
			t.Errorf("%s is reserved: %v, want %v (%v)", at, reserved, want, err)
		}
	}
}

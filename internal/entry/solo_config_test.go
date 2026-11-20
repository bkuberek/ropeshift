package entry

import (
	"io"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
)

// A solo daemon's server creates the repositories a plan approval names on its own worker, so approving a version
// that creates one is not refused as having no creator.
func TestASoloDaemonsServerConfigCarriesItsWorkerAsTheCreator(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run"), WorktreeDir: filepath.Join(root, "worktrees")}
	local := &soloWorker{}
	config := soloConfig(soloWiring{paths: paths, kept: soloCheckouts(paths), local: local, state: &sqlite.State{}, stderr: io.Discard})
	if config.Creator == nil {
		t.Fatal("a solo daemon's server configuration carries no creator")
	}
	if config.Creator != any(local) {
		t.Errorf("the creator is %T, want the daemon's own worker", config.Creator)
	}
	if config.Checkouts == nil || config.LocalWorker == nil {
		t.Error("the worker's machine is not wired beside the creator")
	}
}

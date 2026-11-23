package entry

import (
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/test/gittest"
)

// A daemon source is the checkout of its record in the first project, as a lane's start reads it, so the daemon's
// worker holds it there: its orchestrator serves that project, and a member's message to it is taken, before any
// `repository add` keeps a checkout. Another project holds none of the source.
func TestADaemonSourceIsACheckoutOfTheFirstProject(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("README.md", "shop\n")
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: repo.Env()}
	hosts := &soloHosts{kept: kept, opts: soloOptions{sources: repeatedFlag{repo.Dir}}}
	id := repo.Roots("HEAD")[0]
	home, err := hosts.host(t.Context(), soloScope)
	if err != nil {
		t.Fatal(err)
	}
	if !home.Checkouts[id] {
		t.Errorf("the first project holds %v, want the source's record %s", home.Checkouts, id)
	}
	other, err := hosts.host(t.Context(), "another")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Checkouts) != 0 {
		t.Errorf("another project holds %v of the daemon's source", other.Checkouts)
	}
}

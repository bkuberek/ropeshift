package entry

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
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

// A daemon source that git cannot read, and that registration would not refuse either, is the checkout of no
// record. It does not fail the hosts of the first project, which the other sources still hold.
func TestASourceWithAGitErrorLeavesTheOtherHostsAlone(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit("README.md", "shop\n")
	notARepository := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notARepository, []byte("not a repository\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: repo.Env()}
	opts := soloOptions{sources: repeatedFlag{notARepository, repo.Dir}}
	if _, _, err := sourceInstance(t.Context(), opts, kept, soloScope, notARepository); err == nil {
		t.Fatal("a plain file is a source git can read")
	} else if _, refused := repofiles.Refusal(err); refused {
		t.Fatalf("a plain file is a refusal, which this test does not mean: %v", err)
	}
	hosts := &soloHosts{kept: kept, opts: opts}
	id := repo.Roots("HEAD")[0]
	home, err := hosts.host(t.Context(), soloScope)
	if err != nil {
		t.Fatalf("a source with a git error fails the hosts: %v", err)
	}
	if !home.Checkouts[id] {
		t.Errorf("the first project holds %v, want the valid source's record %s", home.Checkouts, id)
	}
}

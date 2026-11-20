package worker_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/gittest"
)

// diskRig is a worker writing the repositories a plan approval creates on this machine's own disk, in a folder the
// test owns: the planning clone's folder at work/tt, with work/tt/repos in it, inside the bound work.
type diskRig struct {
	t     *testing.T
	root  string
	env   []string
	state string
	forge *fake.ForgeAccounts
	kept  *checkouts.File
}

func newDiskRig(t *testing.T) *diskRig {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &diskRig{t: t, root: root, env: gittest.New(t).Env(), state: filepath.Join(root, "state"), forge: fake.NewForgeAccounts()}
	r.forge.Orgs = []string{"acme"}
	for _, dir := range []string{r.state, r.at("work/tt/repos")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.kept = &checkouts.File{Path: filepath.Join(r.state, checkouts.FileName), Env: r.env, Reserved: []string{r.state}}
	return r
}

func (r *diskRig) at(rel string) string { return filepath.Join(r.root, filepath.FromSlash(rel)) }

// creator is a worker's repository creator on this rig's disk, reaching it through folders and the forge through forge.
func (r *diskRig) creator(folders ports.ProjectFolders, forge ports.ForgeAccounts) ports.RepositoryCreator {
	return worker.New(nil, nil, clock.System{}, worker.Config{Name: "atlas", Templates: &worker.TemplateRuntime{
		Folders: folders, Checkouts: r.kept, Forge: forge, Render: render, StateDir: r.state,
	}}).RepositoryCreator()
}

func (r *diskRig) folders() ports.ProjectFolders { return projectfolders.Folders{Env: r.env} }

// job is the creation of the repository at rel, its own approval's, on GitHub under acme.
func (r *diskRig) job(rel string) ledger.RepositoryCreation {
	at := r.at(rel)
	return ledger.RepositoryCreation{
		Key: ledger.CreationKey(at, filepath.Base(at)), Project: "tt", Name: filepath.Base(at), Path: at,
		Bound: r.at("work"), PlanningClone: r.at("work/tt"),
		Create: ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "acme"}, ForgeName: filepath.Base(at),
	}
}

// link places a symbolic link at rel that leads to target.
func (r *diskRig) link(rel, target string) {
	r.t.Helper()
	if err := os.Symlink(r.at(target), r.at(rel)); err != nil {
		r.t.Fatal(err)
	}
}

// holdingPreview runs a preview's check of first that holds the folder its path leads to until the returned release.
func (r *diskRig) holdingPreview(creator func(ports.ForgeAccounts) ports.RepositoryCreator, first ledger.RepositoryCreation) func() {
	r.t.Helper()
	forge := &slowSignIn{ForgeAccounts: r.forge, began: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = creator(forge).Check(context.Background(), first)
	}()
	<-forge.began
	return func() {
		close(forge.release)
		<-done
	}
}

// On a real disk, two spellings of one folder, one through a link, take turns: a preview's check of the second is
// refused as path_busy while the first holds the folder, naming where both lead.
func TestOnADiskTwoSpellingsOfOneFolderShareTheCreationLock(t *testing.T) {
	t.Parallel()
	r := newDiskRig(t)
	r.link("work/alias", "work/tt")
	var subject ports.RepositoryCreator
	creator := func(forge ports.ForgeAccounts) ports.RepositoryCreator {
		subject = r.creator(r.folders(), forge)
		return subject
	}
	release := r.holdingPreview(creator, r.job("work/tt/repos/api"))
	defer release()
	checked, err := subject.Check(t.Context(), r.job("work/alias/repos/api"))
	if err != nil || checked.Refusal == nil || checked.Refusal.Reason != ledger.TemplateRefusedBusy || checked.Leads != r.at("work/tt/repos/api") {
		t.Errorf("the check through the link is %+v (%v), want refused as path_busy, leading to %s", checked, err, r.at("work/tt/repos/api"))
	}
}

// swappedAfterLock swaps a folder above the path for a link once the creation took the folder its path led to.
type swappedAfterLock struct {
	ports.ProjectFolders
	once sync.Once
	swap func()
}

func (f *swappedAfterLock) Inspect(ctx context.Context, at string) (ports.FolderInspection, error) {
	found, err := f.ProjectFolders.Inspect(ctx, at)
	f.once.Do(f.swap)
	return found, err
}

// On a real disk, a folder above the path that is swapped for a link once the creation took the folder its path led
// to is refused as folder_changed, and nothing is written where the path leads now.
func TestOnADiskAParentSwappedAfterTheLockIsRefused(t *testing.T) {
	t.Parallel()
	r := newDiskRig(t)
	if err := os.MkdirAll(r.at("work/tt/other"), 0o755); err != nil {
		t.Fatal(err)
	}
	folders := &swappedAfterLock{ProjectFolders: r.folders(), swap: func() {
		if err := os.Rename(r.at("work/tt/repos"), r.at("work/tt/before")); err != nil {
			t.Error(err)
		}
		r.link("work/tt/repos", "work/tt/other")
	}}
	report, err := r.creator(folders, r.forge).Create(t.Context(), r.job("work/tt/repos/api"))
	if err != nil || report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedChanged {
		t.Fatalf("the creation reports %+v (%v), want refused as folder_changed", report, err)
	}
	if entries, err := os.ReadDir(r.at("work/tt/other")); err != nil || len(entries) != 0 {
		t.Errorf("the folder the path leads to now holds %v (%v)", entries, err)
	}
	if created := r.forge.Repositories(); len(created) != 0 {
		t.Errorf("the forge created %v", created)
	}
}

// On a real disk, a link above the path that leads outside the bound is refused as path_outside_bound, naming where it
// leads, and nothing is written there.
func TestOnADiskALinkAboveThePathThatLeadsOutsideTheBoundIsRefused(t *testing.T) {
	t.Parallel()
	r := newDiskRig(t)
	if err := os.MkdirAll(r.at("elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.at("work/tt/repos")); err != nil {
		t.Fatal(err)
	}
	r.link("work/tt/repos", "elsewhere")
	job := r.job("work/tt/repos/api")
	creator := r.creator(r.folders(), r.forge)
	checked, err := creator.Check(t.Context(), job)
	if err != nil || checked.Refusal == nil || checked.Refusal.Reason != ledger.TemplateRefusedOutsideBound || checked.Leads != r.at("elsewhere/api") {
		t.Fatalf("the check is %+v (%v), want refused as path_outside_bound, leading to %s", checked, err, r.at("elsewhere/api"))
	}
	report, err := creator.Create(t.Context(), job)
	if err != nil || report.Reason != ledger.TemplateRefusedOutsideBound || !strings.Contains(report.Detail, r.at("elsewhere/api")) {
		t.Errorf("the creation reports %+v (%v), want refused naming %s", report, err, r.at("elsewhere/api"))
	}
	if entries, err := os.ReadDir(r.at("elsewhere")); err != nil || len(entries) != 0 {
		t.Errorf("outside the bound holds %v (%v)", entries, err)
	}
}

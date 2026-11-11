//go:build chaos

package chaos

import (
	"context"
	"os"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/contract"
)

// The writes of an installation with projects (ADR-0021) run in a state directory of their own, inside
// the world's target, so the world's ledger stays what its checks read.
const (
	installationProject  = ledger.WorkspaceID("pa")
	installationIdentity = "example.com/acme/invoices"
)

func init() {
	childRoles["installation-detect"] = roleInstallationDetect
	childRoles["installation-window"] = roleInstallationWindow
}

// openInstallation opens a prepared state directory and its one project, as the daemon does.
func openInstallation(ctx context.Context, dir string) (*sqlite.State, ports.Store, error) {
	state, err := sqlite.OpenState(ctx, dir, sqlstore.Options{})
	if err != nil {
		return nil, nil, err
	}
	projects, err := state.List(ctx)
	if err != nil || len(projects) != 1 {
		_ = state.Close()
		return nil, nil, contract.Violatedf("the state directory holds %v: %v", projects, err)
	}
	store, err := state.Open(ctx, projects[0])
	if err != nil {
		_ = state.Close()
		return nil, nil, err
	}
	return state, store, nil
}

func roleInstallationDetect(c *child) error {
	state, store, err := openInstallation(c.ctx, c.spec.Path)
	if err != nil {
		return err
	}
	defer state.Close()
	_, err = control.NewRepositories(store).Detect(c.ctx, installationProject, control.DetectedRepository{
		ID: "r1", Remote: "https://" + installationIdentity + ".git", DefaultBranch: "main",
	}, ledger.Actor{Kind: ledger.ActorSystem, ID: "solo"})
	return err
}

func roleInstallationWindow(c *child) error {
	state, store, err := openInstallation(c.ctx, c.spec.Path)
	if err != nil {
		return err
	}
	defer state.Close()
	_, err = control.NewAutonomy(store).OpenWindow(c.ctx, installationProject, c.spec.Worker, c.spec.Key, 60_000, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	return err
}

// prepareInstallation makes a state directory with one project and one registered worker.
func prepareInstallation(w *world, name string) (string, error) {
	dir := filepath.Join(w.target.Dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ctx := context.Background()
	state, err := sqlite.OpenState(ctx, dir, sqlstore.Options{})
	if err != nil {
		return "", err
	}
	defer state.Close()
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "solo"}
	_, store, err := state.CreateProject(ctx, ledger.Project{ID: installationProject, Name: "alpha"}, system)
	if err != nil {
		return "", err
	}
	plane := control.NewWorkers(store, installationProject, control.WorkerOptions{})
	token, err := plane.CreateJoinToken(ctx, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
	if err != nil {
		return "", err
	}
	_, err = plane.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: "worker-1", Name: "w"})
	return dir, err
}

// installationDetectCase kills a repository's registration in a project at each step, and between the
// commit of the repository index and the project's. Wherever the kill lands, a record never stands
// without its entry in the index, and the retry leaves one of each.
func installationDetectCase(op string, steps []string) crashCase {
	return crashCase{
		name:  "register a repository in a project of an installation: " + op,
		op:    op,
		steps: steps,
		prepare: func(w *world) (childSpec, error) {
			dir, err := prepareInstallation(w, "installation-detect")
			return childSpec{Role: "installation-detect", Path: dir}, err
		},
		between: func(_ *world, spec childSpec) error { return checkIndexHoldsEveryRecord(spec.Path, false) },
		settled: func(_ *world, spec childSpec) error { return checkIndexHoldsEveryRecord(spec.Path, true) },
	}
}

// checkIndexHoldsEveryRecord reads the project's records and the index: every record holds its entry, and
// a settled registration has one record and one entry.
func checkIndexHoldsEveryRecord(dir string, settled bool) error {
	ctx := context.Background()
	state, store, err := openInstallation(ctx, dir)
	if err != nil {
		return err
	}
	defer state.Close()
	var records []ledger.Repository
	err = store.Read(ctx, installationProject, func(tx ports.ReadTx) error {
		records, err = tx.Repositories().Repositories()
		return err
	})
	if err != nil {
		return err
	}
	index, err := state.Index(ctx)
	if err != nil {
		return err
	}
	held := map[string]bool{}
	for _, entry := range index {
		held[entry.Identity] = entry.Project == installationProject
	}
	for _, record := range records {
		if !held[ledger.RepositoryIdentity(record.Remote, record.ID)] {
			return contract.Violatedf("repository %s stands with no entry in the index %+v", record.ID, index)
		}
	}
	if settled && (len(records) != 1 || len(index) != 1 || index[0].Identity != installationIdentity) {
		return contract.Violatedf("a settled registration left records %+v and index %+v", records, index)
	}
	return nil
}

// installationWindowCase kills a run window's opening, a write of the installation's own records, at each
// step: the window and its event in the installation's log commit together or not at all.
func installationWindowCase() crashCase {
	return crashCase{
		name:  "open a run window in an installation",
		op:    "autonomy.window_open",
		steps: withSteps("inserted"),
		prepare: func(w *world) (childSpec, error) {
			dir, err := prepareInstallation(w, "installation-window")
			return childSpec{Role: "installation-window", Path: dir, Worker: "worker-1", Key: w.rig.ID("window")}, err
		},
		between: func(_ *world, spec childSpec) error { return checkWindowAndItsEvent(spec, false) },
		settled: func(_ *world, spec childSpec) error { return checkWindowAndItsEvent(spec, true) },
	}
}

func checkWindowAndItsEvent(spec childSpec, settled bool) error {
	ctx := context.Background()
	state, store, err := openInstallation(ctx, spec.Path)
	if err != nil {
		return err
	}
	defer state.Close()
	opened := false
	err = store.Installation(installationProject).Read(ctx, func(tx ports.SharedReadTx) error {
		_, err := tx.Workers().Window(spec.Key)
		opened = err == nil
		return nil
	})
	if err != nil {
		return err
	}
	events, err := state.Installation.Events(ctx, 0, 1000)
	if err != nil {
		return err
	}
	logged := 0
	for _, e := range events {
		if e.Type == "autonomy.window_opened" {
			logged++
		}
	}
	if opened != (logged == 1) || logged > 1 {
		return contract.Violatedf("the window stands %v with %d events in the installation's log", opened, logged)
	}
	if settled && !opened {
		return contract.Violatedf("a settled opening left no window")
	}
	return nil
}

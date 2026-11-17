package entry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/worker"
)

// With no root in the user's profile the doctor names the built-in root and finds nothing inside a clone, so a
// machine where lanes work says nothing to fix.
func TestTheDoctorReportsNothingForTheBuiltInWorktreeRoot(t *testing.T) {
	w := newWorktreeWorkspace(t)
	report, err := worktreeReport(t.Context(), w.paths)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(w.paths.WorktreeDir, "{project}", "{repo}"); report.Root != want || report.Source != lanes.RootFromDefault {
		t.Errorf("the root is %q from %q, want %q from %q", report.Root, report.Source, want, lanes.RootFromDefault)
	}
	if len(report.Inside) != 0 {
		t.Errorf("the built-in root is inside %+v", report.Inside)
	}
}

// A root in the user's profile that falls inside a registered clone is reported for each checkout of every
// project, with the clone, and a lane there is refused with the same clone. A root outside every clone is
// reported with nothing inside.
func TestTheDoctorReportsAUserRootInsideARegisteredClone(t *testing.T) {
	w := newWorktreeWorkspace(t)
	w.userRoot(t, filepath.Join(w.workspace.Dir, "lanes", "{project}", "{repo}"))
	report, err := worktreeReport(t.Context(), w.paths)
	if err != nil {
		t.Fatal(err)
	}
	if report.Source != lanes.RootFromUser || report.Root != filepath.Join(w.workspace.Dir, "lanes", "{project}", "{repo}") {
		t.Errorf("the root is %q from %q", report.Root, report.Source)
	}
	want := []lanes.RootInsideClone{
		{Project: "prj-1", Checkout: w.clones["api"], Root: filepath.Join(w.workspace.Dir, "lanes", "prj-1", "api"), Clone: w.workspace.Dir},
		{Project: "prj-1", Checkout: w.clones["web"], Root: filepath.Join(w.workspace.Dir, "lanes", "prj-1", "web"), Clone: w.workspace.Dir},
		{Project: "prj-1", Checkout: w.workspace.Dir, Root: filepath.Join(w.workspace.Dir, "lanes", "prj-1", filepath.Base(w.workspace.Dir)), Clone: w.workspace.Dir},
		{Project: "prj-2", Checkout: w.clones["other"], Root: filepath.Join(w.workspace.Dir, "lanes", "prj-2", filepath.Base(w.clones["other"])), Clone: w.workspace.Dir},
	}
	if len(report.Inside) != len(want) {
		t.Fatalf("inside a clone: %+v, want %+v", report.Inside, want)
	}
	for i := range want {
		if report.Inside[i] != want[i] {
			t.Errorf("finding %d is %+v, want %+v", i, report.Inside[i], want[i])
		}
	}

	_, err = w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-1", Repository: w.clones["api"], Project: "prj-1"})
	var refused *worker.WorktreeInsideClone
	if !errors.As(err, &refused) || refused.Clone != want[0].Clone || refused.Root != want[0].Root {
		t.Errorf("the lane's refusal is %v, and the doctor said %+v", err, want[0])
	}

	w.userRoot(t, filepath.Join(w.home, "lanes", "{project}", "{repo}"))
	report, err = worktreeReport(t.Context(), w.paths)
	if err != nil || report.Source != lanes.RootFromUser || len(report.Inside) != 0 {
		t.Errorf("a root outside every clone: %+v (%v)", report, err)
	}
}

// A profile the worker refuses is the doctor's error, because no lane starts on it.
func TestTheDoctorReportsAProfileTheWorkerRefuses(t *testing.T) {
	w := newWorktreeWorkspace(t)
	if err := os.WriteFile(filepath.Join(w.paths.StateDir, profiles.UserFile), []byte("[worktree]\nrooot = \"/x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := worktreeReport(t.Context(), w.paths); !errors.Is(err, lanes.ErrBadProfile) {
		t.Errorf("got %v", err)
	}
}

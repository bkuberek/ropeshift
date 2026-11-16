package entry

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// With no root in any layer, the installation's profiles put a lane's worktree under its worktree directory,
// by project and clone folder: the default root outside every clone (FR-PROF-3). A root a layer names wins.
func TestTheInstallationsProfilesDefaultToTheWorktreeDirectory(t *testing.T) {
	root := t.TempDir()
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run"), WorktreeDir: filepath.Join(root, "data", "worktrees")}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profile, err := soloProfiles(paths).Load(t.Context(), filepath.Join(root, "work", "api"), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(paths.WorktreeDir, "{project}", "{repo}"); profile.Worktree.Root != want || profile.Worktree.RootSource != lanes.RootFromDefault {
		t.Errorf("the root is %q from %q, want %q", profile.Worktree.Root, profile.Worktree.RootSource, want)
	}
	place, err := lanes.Place(profile.Worktree, filepath.Join(root, "work", "repos", "api"), "prj-1", "lane-1", "")
	if want := filepath.Join(paths.WorktreeDir, "prj-1", "api", "lane-1"); err != nil || place.Path != want {
		t.Errorf("a lane is placed at %q (%v), want %q", place.Path, err, want)
	}
	if err := os.WriteFile(filepath.Join(paths.StateDir, profiles.UserFile), []byte("[worktree]\nroot = \"/mine/{repo}\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if mine, err := soloProfiles(paths).Load(t.Context(), filepath.Join(root, "work", "api"), ""); err != nil || mine.Worktree.Root != "/mine/{repo}" {
		t.Errorf("a user's root reads %+v (%v)", mine.Worktree, err)
	}
}

// The lane runtime refuses a worktree root inside any clone the worker keeps, so the provisioner holds the
// worker's checkouts. Leaving them out would pass every test of the refusal that builds its own.
func TestTheLaneRuntimeChecksRootsAgainstTheWorkersCheckouts(t *testing.T) {
	root := t.TempDir()
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run"), WorktreeDir: filepath.Join(root, "worktrees")}
	kept := soloCheckouts(paths)
	runtime, _, err := soloLanes(paths, soloOptions{}, kept, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Provisioner.Checkouts != ports.Checkouts(kept) {
		t.Errorf("the provisioner checks roots against %v", runtime.Provisioner.Checkouts)
	}
}

// The worker's checkouts name the installation's state and run directories as its own, so a manifest never
// registers a clone inside them, such as a lane's worktree (ADR-0022). Dropping them from the wiring would
// leave every other test green, so the checkouts are asked about a path in each.
func TestTheWorkersCheckoutsReserveTheInstallationsOwnDirectories(t *testing.T) {
	root := t.TempDir()
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run"), WorktreeDir: filepath.Join(root, "data", "worktrees")}
	for _, dir := range []string{
		filepath.Join(paths.StateDir, "lanes", "one"), filepath.Join(paths.RunDir, "sockets"), filepath.Join(paths.WorktreeDir, "prj-1", "api", "lane-1"),
		filepath.Join(root, "work", "api"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	kept := soloCheckouts(paths)
	for at, want := range map[string]bool{
		filepath.Join(paths.StateDir, "lanes", "one"):              true,
		filepath.Join(paths.RunDir, "sockets"):                     true,
		paths.StateDir:                                             true,
		paths.WorktreeDir:                                          true,
		filepath.Join(paths.WorktreeDir, "prj-1", "api", "lane-1"): true,
		filepath.Join(root, "work", "api"):                         false,
	} {
		if _, reserved, err := kept.Resolve(t.Context(), at); err != nil || reserved != want {
			t.Errorf("%s is reserved: %v, want %v (%v)", at, reserved, want, err)
		}
	}
}

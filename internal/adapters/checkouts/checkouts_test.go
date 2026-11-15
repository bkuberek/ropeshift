package checkouts

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// cloneInto clones a new repository with one commit to a path, so each clone is a repository of its own.
func cloneInto(t *testing.T, path string) *gittest.Repo {
	t.Helper()
	origin := gittest.New(t)
	origin.Commit("README.txt", path+"\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	origin.Git("clone", "--quiet", origin.Dir, path)
	return origin
}

func real(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func paths(clones []ports.Clone) []string {
	out := make([]string, 0, len(clones))
	for _, clone := range clones {
		out = append(out, clone.Path)
	}
	slices.Sort(out)
	return out
}

// A path that is the top of a clone is that clone, and a folder is searched one level down by default
// and further with a deeper depth. Nothing inside a clone is searched.
func TestFindReadsAClonesTopOrTheClonesBelowAFolder(t *testing.T) {
	root := real(t, t.TempDir())
	origin := cloneInto(t, filepath.Join(root, "api"))
	cloneInto(t, filepath.Join(root, "web"))
	cloneInto(t, filepath.Join(root, "group", "deep"))
	cloneInto(t, filepath.Join(root, "api", "vendor", "nested"))
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &File{Path: filepath.Join(t.TempDir(), FileName), Env: origin.Env()}

	clones, unread, err := f.Find(t.Context(), []string{filepath.Join(root, "api")}, 1)
	if err != nil || len(unread) != 0 || len(clones) != 1 {
		t.Fatalf("the top of a clone: %+v, unread %+v: %v", clones, unread, err)
	}
	api := clones[0]
	if api.Path != filepath.Join(root, "api") || api.Directory != "api" || api.Remote != origin.Dir || api.DefaultBranch != "main" || len(api.ID) != 40 {
		t.Errorf("the clone reads %+v", api)
	}

	clones, _, err = f.Find(t.Context(), []string{root}, 1)
	if want := []string{filepath.Join(root, "api"), filepath.Join(root, "web")}; err != nil || !slices.Equal(paths(clones), want) {
		t.Errorf("one level down found %v, want %v: %v", paths(clones), want, err)
	}
	clones, _, err = f.Find(t.Context(), []string{root}, 2)
	if want := []string{filepath.Join(root, "api"), filepath.Join(root, "group", "deep"), filepath.Join(root, "web")}; err != nil || !slices.Equal(paths(clones), want) {
		t.Errorf("two levels down found %v, want %v: %v", paths(clones), want, err)
	}
	// The same clone named twice is one clone.
	clones, _, _ = f.Find(t.Context(), []string{root, filepath.Join(root, "web")}, 1)
	if len(clones) != 2 {
		t.Errorf("a clone named twice: %v", paths(clones))
	}
}

// FR-REPO-13: a path that is no clone and holds none is refused with the reason, and so is a clone git
// cannot describe.
func TestFindSaysWhyAPathHoldsNoClone(t *testing.T) {
	root := real(t, t.TempDir())
	origin := gittest.New(t)
	empty := filepath.Join(root, "empty")
	origin.Git("init", "--quiet", empty)
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plain"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &File{Path: filepath.Join(t.TempDir(), FileName), Env: origin.Env()}
	clones, unread, err := f.Find(t.Context(), []string{filepath.Join(root, "missing"), file, filepath.Join(root, "plain"), empty}, 1)
	if err != nil || len(clones) != 0 {
		t.Fatalf("found %+v: %v", clones, err)
	}
	reasons := map[string]string{}
	for _, u := range unread {
		reasons[filepath.Base(u.Path)] = u.Reason
	}
	for name, want := range map[string]string{"missing": "does not exist", "file.txt": "not a directory", "plain": "holds none 1 level down", "empty": "cannot describe"} {
		if !strings.Contains(reasons[name], want) {
			t.Errorf("%s: %q, want %q", name, reasons[name], want)
		}
	}
}

// The file keeps one path per repository of each project, and survives a new reader.
func TestTheFileKeepsACheckoutPerProjectRepository(t *testing.T) {
	file := filepath.Join(t.TempDir(), FileName)
	f := &File{Path: file}
	ctx := t.Context()
	if kept, err := f.Of(ctx, "alpha"); err != nil || len(kept) != 0 {
		t.Fatalf("a new file: %v, %v", kept, err)
	}
	for _, keep := range []struct{ project, repository, path string }{
		{"alpha", "r1", "/work/api"}, {"alpha", "r2", "/work/web"}, {"beta", "r1", "/fork/api"}, {"alpha", "r1", "/moved/api"},
	} {
		if err := f.Keep(ctx, ledger.WorkspaceID(keep.project), keep.repository, keep.path); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Forget(ctx, "alpha", "r2"); err != nil {
		t.Fatal(err)
	}
	if err := f.Forget(ctx, "alpha", "never"); err != nil {
		t.Fatal(err)
	}
	again := &File{Path: file}
	alpha, err := again.Of(ctx, "alpha")
	if err != nil || len(alpha) != 1 || alpha["r1"] != "/moved/api" {
		t.Errorf("alpha keeps %v: %v", alpha, err)
	}
	if beta, err := again.Of(ctx, "beta"); err != nil || beta["r1"] != "/fork/api" {
		t.Errorf("beta keeps %v: %v", beta, err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the file: %v, %v", info, err)
	}
}

// Resolving a path follows every symbolic link in it, says when the path it comes to is inside a directory
// the installation keeps for itself, and says nothing is there when nothing is.
func TestResolveFollowsLinksAndNamesTheInstallationsDirectories(t *testing.T) {
	root := real(t, t.TempDir())
	state := filepath.Join(root, "state")
	for _, dir := range []string{filepath.Join(root, "work", "api"), filepath.Join(state, "lanes", "one"), filepath.Join(root, "outside")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(root, "work", "alias"): filepath.Join(root, "work", "api"),
		filepath.Join(root, "work", "away"):  filepath.Join(root, "outside"),
		filepath.Join(root, "work", "lane"):  filepath.Join(state, "lanes", "one"),
		filepath.Join(root, "state-link"):    state,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	f := &File{Path: filepath.Join(state, FileName), Reserved: []string{filepath.Join(root, "state-link"), filepath.Join(root, "run")}}
	for at, want := range map[string]struct {
		real     string
		reserved bool
	}{
		filepath.Join(root, "work", "api"):                {filepath.Join(root, "work", "api"), false},
		filepath.Join(root, "work", "alias"):              {filepath.Join(root, "work", "api"), false},
		filepath.Join(root, "work", "away"):               {filepath.Join(root, "outside"), false},
		filepath.Join(root, "work", "lane"):               {filepath.Join(state, "lanes", "one"), true},
		filepath.Join(state, "lanes"):                     {filepath.Join(state, "lanes"), true},
		filepath.Join(root, "work", "alias", "..", "api"): {filepath.Join(root, "work", "api"), false},
	} {
		real, reserved, err := f.Resolve(t.Context(), at)
		if err != nil || real != want.real || reserved != want.reserved {
			t.Errorf("%s resolves to %q, reserved %v: %v; want %q, reserved %v", at, real, reserved, err, want.real, want.reserved)
		}
	}
	if _, _, err := f.Resolve(t.Context(), filepath.Join(root, "work", "ghost")); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a path nothing is at: %v", err)
	}
}

// ignoresCase says the disk a directory is on reads two spellings that differ in case as one name, as a macOS
// volume does by default. A test of a case variant skips where it does not, because there the two are two files.
func ignoresCase(t *testing.T, dir string) bool {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "Probe"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "pROBE"))
	return err == nil
}

// Two paths are one file when the disk says so, whatever the string: a path through a link in a parent, and
// on a disk that ignores case a path that differs only in case.
func TestSameFileComparesTheFilesPathsNameAndNotHowTheyAreSpelled(t *testing.T) {
	root := real(t, t.TempDir())
	for _, dir := range []string{filepath.Join(root, "work", "plan"), filepath.Join(root, "work", "api")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "work"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	f := &File{Path: filepath.Join(t.TempDir(), FileName)}
	for _, one := range []struct {
		a, b string
		same bool
	}{
		{filepath.Join(root, "work", "plan"), filepath.Join(root, "work", "plan"), true},
		{filepath.Join(root, "alias", "plan"), filepath.Join(root, "work", "plan"), true},
		{filepath.Join(root, "alias", "api", "..", "plan"), filepath.Join(root, "work", "plan"), true},
		{filepath.Join(root, "work", "plan"), filepath.Join(root, "work", "api"), false},
	} {
		if same, err := f.SameFile(t.Context(), one.a, one.b); err != nil || same != one.same {
			t.Errorf("%s and %s are the same file: %v (%v), want %v", one.a, one.b, same, err, one.same)
		}
	}
	if _, err := f.SameFile(t.Context(), filepath.Join(root, "work", "plan"), filepath.Join(root, "work", "ghost")); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a path nothing is at: %v", err)
	}
	if ignoresCase(t, root) {
		if same, err := f.SameFile(t.Context(), filepath.Join(root, "work", "PLAN"), filepath.Join(root, "work", "plan")); err != nil || !same {
			t.Errorf("a spelling that differs in case is the same directory: %v (%v)", same, err)
		}
	}
}

// A path is inside a reserved directory when the directory above it, at any height, is that directory by the
// file each names. The reserved directory may be spelled through a link, or on a disk that ignores case in
// other letters, and a reserved directory nothing is at holds nothing.
func TestAPathInsideAReservedDirectoryIsFoundByTheFileThatDirectoryNames(t *testing.T) {
	root := real(t, t.TempDir())
	for _, dir := range []string{filepath.Join(root, "work", "state", "lanes", "one"), filepath.Join(root, "work", "other")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "work"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	lane := filepath.Join(root, "work", "state", "lanes", "one")
	for name, one := range map[string]struct {
		reserved []string
		path     string
		want     bool
	}{
		"a reserved directory spelled through a link": {[]string{filepath.Join(root, "alias", "state")}, lane, true},
		"the reserved directory itself":               {[]string{filepath.Join(root, "work", "state")}, filepath.Join(root, "work", "state"), true},
		"a path beside the reserved directory":        {[]string{filepath.Join(root, "alias", "state")}, filepath.Join(root, "work", "other"), false},
		"a reserved directory nothing is at":          {[]string{filepath.Join(root, "ghost"), filepath.Join(root, "alias", "state")}, lane, true},
		"only a reserved directory nothing is at":     {[]string{filepath.Join(root, "ghost")}, lane, false},
		"no reserved directory":                       {nil, lane, false},
	} {
		if got, err := insideByFile(one.reserved, one.path); err != nil || got != one.want {
			t.Errorf("%s: inside is %v (%v), want %v", name, got, err, one.want)
		}
	}
	if ignoresCase(t, root) {
		// The installation names its state directory in one case, and the disk gives the lane's path in another.
		f := &File{Path: filepath.Join(root, FileName), Reserved: []string{filepath.Join(root, "WORK", "STATE")}}
		if _, reserved, err := f.Resolve(t.Context(), lane); err != nil || !reserved {
			t.Errorf("a path under a reserved directory spelled in other case is reserved: %v (%v)", reserved, err)
		}
	}
}

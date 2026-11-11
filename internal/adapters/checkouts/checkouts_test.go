package checkouts

import (
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

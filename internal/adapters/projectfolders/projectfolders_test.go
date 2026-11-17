package projectfolders_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

func folders(t *testing.T) (projectfolders.Folders, string, *gittest.Repo) {
	t.Helper()
	gittest.Require(t)
	home := gittest.New(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return projectfolders.Folders{Env: home.Env()}, root, home
}

// Inspect says what is at a path: absent, an empty folder, a folder with files, a file, a link, and what holds each.
func TestInspectReadsWhatIsAtAPath(t *testing.T) {
	f, root, _ := folders(t)
	empty := filepath.Join(root, "empty")
	full := filepath.Join(root, "full")
	file := filepath.Join(root, "file")
	link := filepath.Join(root, "link")
	for _, dir := range []string{empty, full} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(full, "a"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("f"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(empty, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
		want ports.FolderInspection
	}{
		{"absent", filepath.Join(root, "absent"), ports.FolderInspection{ParentDirectory: true, Real: filepath.Join(root, "absent")}},
		{"empty", empty, ports.FolderInspection{Exists: true, Directory: true, Empty: true, ParentDirectory: true, Real: empty}},
		{"full", full, ports.FolderInspection{Exists: true, Directory: true, ParentDirectory: true, Real: full}},
		{"file", file, ports.FolderInspection{Exists: true, ParentDirectory: true, Real: file}},
		{"link", link, ports.FolderInspection{Exists: true, Link: true, ParentDirectory: true, Real: empty}},
		{"no parent", filepath.Join(root, "missing", "deep"), ports.FolderInspection{Real: filepath.Join(root, "missing", "deep")}},
	}
	for _, c := range cases {
		got, err := f.Inspect(t.Context(), c.path)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v (%v), want %+v", c.name, got, err, c.want)
		}
	}
}

// A path under a link is found where the link leads, and a path inside a clone names the clone's top.
func TestInspectFollowsLinksAndNamesTheCloneThatHoldsAPath(t *testing.T) {
	f, root, _ := folders(t)
	clone := gittest.New(t)
	top, err := filepath.EvalSymlinks(clone.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(top, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(top, "repos"), link); err != nil {
		t.Fatal(err)
	}
	got, err := f.Inspect(t.Context(), filepath.Join(link, "api"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(top, "repos", "api"); got.Real != want || got.CloneTop != top || !got.ParentDirectory || got.Exists {
		t.Errorf("a path under a link reads %+v, want real %s in the clone at %s", got, want, top)
	}
	loose, err := f.Inspect(t.Context(), filepath.Join(root, "loose"))
	if err != nil || loose.CloneTop != "" {
		t.Errorf("a path in no clone reads %+v (%v)", loose, err)
	}
}

// Write makes the folder and never its parents, creates each file only when it is absent, and keeps a file it finds.
func TestWriteMakesTheFolderAndKeepsWhatItFinds(t *testing.T) {
	f, root, _ := folders(t)
	folder := filepath.Join(root, "harbor")
	files := []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}, {Path: "plan/goal.md", Text: "goal\n"}}
	if err := f.Write(t.Context(), folder, files); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "AGENTS.md"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(folder, "plan", "goal.md")); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(t.Context(), folder, files); err != nil {
		t.Fatal(err)
	}
	kept, _ := os.ReadFile(filepath.Join(folder, "AGENTS.md"))
	restored, _ := os.ReadFile(filepath.Join(folder, "plan", "goal.md"))
	if string(kept) != "mine\n" || string(restored) != "goal\n" {
		t.Errorf("a repeated write left %q and %q", kept, restored)
	}
	if err := f.Write(t.Context(), filepath.Join(root, "no", "parent"), files); err == nil {
		t.Error("a write made the parents of a folder")
	}
	if _, err := os.Stat(filepath.Join(root, "no")); err == nil {
		t.Error("a write left a parent behind")
	}
	if err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "../outside", Text: "x"}}); err == nil {
		t.Error("a write went outside the folder")
	}
	if _, err := os.Stat(filepath.Join(root, "outside")); err == nil {
		t.Error("a file was written outside the folder")
	}
}

// A folder that became a link after it was inspected is not written through.
func TestWriteRefusesAFolderThatIsNowALink(t *testing.T) {
	f, root, _ := folders(t)
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "harbor")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(t.Context(), link, []ports.FolderFile{{Path: "AGENTS.md", Text: "x"}}); err == nil {
		t.Error("a write went through a link")
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("the link's target holds %v", entries)
	}
}

// Files lists the files under a folder, sorted, and leaves out the git directory at its top.
func TestFilesListsTheFolderWithoutItsGitDirectory(t *testing.T) {
	f, root, _ := folders(t)
	folder := filepath.Join(root, "harbor")
	if err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "b.md", Text: "b"}, {Path: "a/c.md", Text: "c"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false); err != nil {
		t.Fatal(err)
	}
	got, err := f.Files(t.Context(), folder)
	if want := []string{"a/c.md", "b.md"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Files reads %v (%v), want %v", got, err, want)
	}
	if got, err := f.Files(t.Context(), filepath.Join(root, "nothing")); err != nil || len(got) != 0 {
		t.Errorf("a path with nothing at it holds %v (%v)", got, err)
	}
}

// The first commit makes the folder a repository of its own and commits every file, and made again it answers with
// the commit it made.
func TestFirstCommitIsMadeOnceAndAnswersWithTheCommitAfterward(t *testing.T) {
	f, root, _ := folders(t)
	folder := filepath.Join(root, "harbor")
	if err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}}); err != nil {
		t.Fatal(err)
	}
	first, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false)
	if err != nil || len(first) < 7 {
		t.Fatalf("the first commit is %q (%v)", first, err)
	}
	// A worker that journaled its git init is resuming, and finds its own commit.
	again, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", true)
	if err != nil || again != first {
		t.Errorf("a second first commit is %q (%v), want %q", again, err, first)
	}
	inspected, err := f.Inspect(t.Context(), folder)
	if err != nil || inspected.CloneTop != inspected.Real {
		t.Errorf("the folder reads %+v (%v), want a clone of its own", inspected, err)
	}
}

// A commit git refuses says what git said, with no command line in front, and leaves the repository git init made.
func TestFirstCommitSaysWhatGitSaidWhenItRefuses(t *testing.T) {
	gittest.Require(t)
	f := projectfolders.Folders{Env: gitEnv(t, "[user]\n\tuseConfigOnly = true\n", nil)}
	folder := filepath.Join(t.TempDir(), "harbor")
	if err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}}); err != nil {
		t.Fatal(err)
	}
	_, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false)
	if err == nil {
		t.Fatal("a commit with no identity was made")
	}
	if strings.HasPrefix(err.Error(), "git ") || strings.Contains(err.Error(), "exit status") {
		t.Errorf("the error carries the command line: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(folder, ".git")); statErr != nil {
		t.Errorf("git init left no repository: %v", statErr)
	}
}

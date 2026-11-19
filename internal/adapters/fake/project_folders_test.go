package fake_test

import (
	"errors"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ports"
)

// The fake reads a machine a test placed as the adapter reads a disk: what is at a path, what holds it, and the
// folder a write made.
func TestProjectFoldersReadWhatATestPlaced(t *testing.T) {
	machine := fake.NewProjectFolders()
	machine.Dir("/srv/code")
	machine.Clone("/srv/clone")
	machine.Dir("/srv/clone/repos")
	machine.Place("/srv/full/notes.txt", "mine")
	machine.Link("/srv/alias", "/srv/code")
	cases := []struct {
		path string
		want ports.FolderInspection
	}{
		{"/srv/code/harbor", ports.FolderInspection{ParentDirectory: true, Real: "/srv/code/harbor"}},
		{"/srv/code", ports.FolderInspection{Exists: true, Directory: true, Empty: true, ParentDirectory: true, Real: "/srv/code"}},
		{"/srv/full", ports.FolderInspection{Exists: true, Directory: true, ParentDirectory: true, Real: "/srv/full"}},
		{"/srv/alias", ports.FolderInspection{Exists: true, Link: true, ParentDirectory: true, Real: "/srv/code"}},
		{"/srv/clone/repos/api", ports.FolderInspection{ParentDirectory: true, Real: "/srv/clone/repos/api", CloneTop: "/srv/clone"}},
		{"/nowhere/deep", ports.FolderInspection{Real: "/nowhere/deep"}},
	}
	for _, c := range cases {
		if got, err := machine.Inspect(t.Context(), c.path); err != nil || got != c.want {
			t.Errorf("%s reads %+v (%v), want %+v", c.path, got, err, c.want)
		}
	}
}

// A write makes the folder, never its parents, keeps a file that exists, and lists what it made once.
func TestProjectFoldersWriteAndCommit(t *testing.T) {
	machine := fake.NewProjectFolders()
	machine.Dir("/srv/code")
	files := []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}, {Path: "plan/goal.md", Text: "goal"}}
	id, err := machine.Write(t.Context(), "/srv/code/harbor", files, "")
	if err != nil || id == "" {
		t.Fatalf("a write names the folder %q (%v)", id, err)
	}
	machine.Place("/srv/code/harbor/AGENTS.md", "mine")
	if again, err := machine.Write(t.Context(), "/srv/code/harbor", files, id); err != nil || again != id {
		t.Fatalf("a repeated write says %q (%v), want %q", again, err, id)
	}
	if _, err := machine.Write(t.Context(), "/srv/code/other", files, id); !errors.Is(err, ports.ErrFolderChanged) {
		t.Errorf("a write of another folder than the one named: %v", err)
	}
	if text, _ := machine.Text("/srv/code/harbor/AGENTS.md"); text != "mine" {
		t.Errorf("a repeated write replaced a file: %q", text)
	}
	if got, err := machine.Files(t.Context(), "/srv/code/harbor"); err != nil || !slices.Equal(got, []string{"AGENTS.md", "plan/goal.md"}) {
		t.Errorf("the folder holds %v (%v)", got, err)
	}
	if _, err := machine.Write(t.Context(), "/srv/none/harbor", files, ""); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a write made the parents of a folder: %v", err)
	}
	first, err := machine.FirstCommit(t.Context(), "/srv/code/harbor", "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}})
	if err != nil || first == "" {
		t.Fatalf("the first commit is %q (%v)", first, err)
	}
	if _, err := machine.FirstCommit(t.Context(), "/srv/code/harbor", "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err == nil {
		t.Error("a repository the worker did not journal was taken as its own")
	}
	if _, err := machine.FirstCommit(t.Context(), "/srv/code/harbor", "chore: start harbor", true, "another", ports.CommitSpec{Paths: []string{"AGENTS.md"}}); !errors.Is(err, ports.ErrFolderChanged) {
		t.Errorf("a first commit in a folder that is not the one written: %v", err)
	}
	if again, err := machine.FirstCommit(t.Context(), "/srv/code/harbor", "chore: start harbor", true, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err != nil || again != first {
		t.Errorf("a second first commit is %q (%v), want %q", again, err, first)
	}
	if got := machine.Inspected("/srv/code/harbor"); got.CloneTop != "/srv/code/harbor" {
		t.Errorf("the committed folder reads %+v, want a clone of its own", got)
	}
	if done := machine.Done(); len(done) != 2 {
		t.Errorf("the machine did %v, want one write and one commit", done)
	}
}

// A commit that fails still leaves the repository `git init` made, with no commit in it.
func TestProjectFoldersFailACommitAsGitDoes(t *testing.T) {
	machine := fake.NewProjectFolders()
	machine.Dir("/srv/code")
	machine.CommitError = errors.New("Author identity unknown")
	id, err := machine.Write(t.Context(), "/srv/code/harbor", []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.FirstCommit(t.Context(), "/srv/code/harbor", "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err == nil {
		t.Fatal("a commit with no identity was made")
	}
	if machine.Commit("/srv/code/harbor") != "" {
		t.Error("a failed commit left a commit")
	}
}

// A first commit starts the repository on the branch it names, on the default branch when it names none, and a branch
// git refuses makes no repository.
func TestProjectFoldersFirstCommitStartsOnTheNamedBranch(t *testing.T) {
	machine := fake.NewProjectFolders()
	machine.Dir("/srv/code")
	machine.SetDefaultBranch("devel")
	files := []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}}
	spec := func(branch string) ports.CommitSpec {
		return ports.CommitSpec{Paths: []string{"AGENTS.md"}, Branch: branch}
	}
	for folder, tc := range map[string]struct{ branch, want string }{
		"/srv/code/named": {"trunk", "trunk"},
		"/srv/code/empty": {"", "devel"},
	} {
		id, err := machine.Write(t.Context(), folder, files, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := machine.FirstCommit(t.Context(), folder, "chore: start", false, id, spec(tc.branch)); err != nil {
			t.Fatal(err)
		}
		if state, err := machine.Remote(t.Context(), folder); err != nil || state.Branch != tc.want {
			t.Errorf("%s is on %q (%v), want %q", folder, state.Branch, err, tc.want)
		}
	}

	id, err := machine.Write(t.Context(), "/srv/code/bad", files, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.FirstCommit(t.Context(), "/srv/code/bad", "chore: start", false, id, spec("bad name~")); err == nil {
		t.Error("a first commit on a branch git refuses was made")
	}
	if got := machine.Inspected("/srv/code/bad"); got.CloneTop != "" {
		t.Errorf("a refused branch made a repository: %+v", got)
	}
}

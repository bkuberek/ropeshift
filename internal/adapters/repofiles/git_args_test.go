package repofiles

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/test/gittest"
)

// absent fails when the file exists, because git wrote it for an argument it read as `--output`.
func absent(t *testing.T, file string) {
	t.Helper()
	if _, err := os.Stat(file); err == nil {
		t.Errorf("git created %s, so it read a value that came from outside as an option", file)
	}
}

// Intake fetches the remote after `--`, so a remote whose name reads as an option is a name and never an
// option: here `--upload-pack`, which would run the script it names.
func TestIntakeFetchReadsARemoteNamedLikeAnOptionAsAName(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	ran := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "upload-pack")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n: > '"+ran+"'\nexec git-upload-pack \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	clone.Git("remote", "rename", "origin", "zorigin")
	clone.Git("config", "remote.--upload-pack="+script+".url", origin.Dir)
	clone.Git("config", "branch.main.remote", "zorigin")
	recorded := gittest.RecordGit(t)

	New(Config{Repository: clone.Dir, Env: clone.Env()}).fetch(t.Context())

	absent(t, ran)
	if got, want := recorded("fetch"), []string{"fetch --quiet -- --upload-pack=" + script}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("git ran %q, want %q", got, want)
	}
}

// A configured ref that reads as an option is a revision to git, which does not have it, and never an option: a
// ref of `--output=<file>` would create the file.
func TestARefThatReadsAsAnOptionIsNeverOneToRevList(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	root := repo.Roots("HEAD")[0]
	for name, identity := range map[string]string{"the first parent root": "", "a configured identity": root} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "owned")
			_, err := New(Config{Repository: repo.Dir, Env: repo.Env(), Ref: "--output=" + file, Identity: identity}).Instance(t.Context())
			if err == nil {
				t.Error("a ref that reads as an option was read as a revision, want an error")
			}
			absent(t, file)
		})
	}
}

// The history check puts `--end-of-options` before the commit, so a commit that reads as an option is a
// revision to git and never an option.
func TestHoldsReadsACommitThatReadsAsAnOptionAsARevision(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	blob := repo.Git("rev-parse", "HEAD:README.txt")
	file := filepath.Join(t.TempDir(), "owned")

	held, _ := New(Config{Repository: repo.Dir, Env: repo.Env()}).holds(t.Context(), "--output="+file, "README.txt", blob)

	if held {
		t.Error("a commit that reads as an option held the blob")
	}
	absent(t, file)
}

// The blob lookup puts `--end-of-options` before `<commit>:<path>`, so a commit that reads as an option is a
// revision to git and never an option: `--abbrev-ref=bogus` is an option rev-parse acts on and refuses.
func TestBlobAtReadsACommitThatReadsAsAnOptionAsARevision(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")

	_, err := New(Config{Repository: repo.Dir, Env: repo.Env()}).blobAt(t.Context(), "--abbrev-ref=bogus", "README.txt")

	if err == nil {
		t.Fatal("a commit that reads as an option named a blob, want an error")
	}
	if strings.Contains(err.Error(), "unknown mode for --abbrev-ref") {
		t.Errorf("rev-parse read the commit as an option: %v", err)
	}
}

// The history check reads a path literally, as the documents source does, so a path that holds a wildcard names
// that file and no other: the blob of plan/a.md is not held at the path plan/*.md.
func TestHoldsReadsAPathWithAWildcardLiterally(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("plan/a.md", "one\n")
	blob := repo.Git("rev-parse", "HEAD:plan/a.md")
	commit := repo.Git("rev-parse", "HEAD")

	held, err := New(Config{Repository: repo.Dir, Env: repo.Env()}).holds(t.Context(), commit, "plan/*.md", blob)
	if err != nil {
		t.Fatal(err)
	}

	if held {
		t.Error("the path plan/*.md matched plan/a.md as a pattern, want a literal path that no file has")
	}
}

// Listing a commit puts `--end-of-options` before it, so a commit that reads as an option is a revision to git
// that it does not have, and ls-tree does not read it as its own option.
func TestTreeReadsACommitThatReadsAsAnOptionAsARevision(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "one\n")

	_, err := New(Config{Repository: repo.Dir, Env: repo.Env()}).Tree(t.Context(), "--name-only")

	if err == nil || !strings.Contains(err.Error(), "Not a valid object name --name-only") {
		t.Errorf("listing a commit named like an option gave %v, want git to name it as an object it does not have", err)
	}
}

// A tree listing of one directory holds that directory's files and nothing else: a top-level directory named `--`
// is not a path to list, which `--` after `--end-of-options` would make it.
func TestTreeOfADirectoryHoldsOnlyThatDirectoryWhenASiblingIsNamedLikeTheSeparator(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("docs/a.md", "one\n")
	repo.Commit("--/b.md", "two\n")
	commit := repo.Git("rev-parse", "HEAD")

	blobs, err := New(Config{Repository: repo.Dir, Env: repo.Env(), Directory: "docs"}).Tree(t.Context(), commit)
	if err != nil {
		t.Fatal(err)
	}

	var paths []string
	for _, blob := range blobs {
		paths = append(paths, blob.Path)
	}
	if want := []string{"docs/a.md"}; !slices.Equal(paths, want) {
		t.Errorf("the tree of docs holds %q, want %q", paths, want)
	}
}

// A directory is a path, so one that holds pathspec magic names that folder and no other: `:(top)docs` lists no file
// of a repository that has docs/ at its top and no folder of that name.
func TestTreeReadsADirectoryWithPathspecMagicLiterally(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("docs/a.md", "one\n")
	commit := repo.Git("rev-parse", "HEAD")

	blobs, err := New(Config{Repository: repo.Dir, Env: repo.Env(), Directory: ":(top)docs"}).Tree(t.Context(), commit)
	if err != nil {
		t.Fatal(err)
	}

	if len(blobs) != 0 {
		t.Errorf("the directory :(top)docs listed %+v, want nothing: no folder has that name", blobs)
	}
}

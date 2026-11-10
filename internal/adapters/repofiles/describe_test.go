package repofiles

import (
	"testing"

	"ropeshift.com/ropeshift/test/gittest"
)

func describe(t *testing.T, repo *gittest.Repo) (remote, branch string) {
	t.Helper()
	description, err := New(Config{Repository: repo.Dir, Env: repo.Env()}).Describe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return description.Remote, description.DefaultBranch
}

func TestARepositoryWithNoRemoteSaysSo(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	if remote, branch := describe(t, repo); remote != "" || branch != "main" {
		t.Errorf("remote %q, default branch %q", remote, branch)
	}
}

func TestACloneNamesItsRemoteAndTheBranchTheRemoteDefaultsTo(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	// The checkout is on another branch, which is not the repository's default.
	clone.Git("switch", "--quiet", "-c", "feature/work")
	remote, branch := describe(t, clone)
	if remote != origin.Dir || branch != "main" {
		t.Errorf("remote %q, default branch %q", remote, branch)
	}
}

// A remote URL can carry a token, and the record is copied into backups and exports.
func TestARemoteURLKeepsNoCredential(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.txt", "one\n")
	repo.Git("remote", "add", "origin", "https://person:s3cret@example.invalid/acme/app.git")
	if remote, _ := describe(t, repo); remote != "https://example.invalid/acme/app.git" {
		t.Errorf("remote %q", remote)
	}
	repo.Git("remote", "set-url", "origin", "git@example.invalid:acme/app.git")
	if remote, _ := describe(t, repo); remote != "git@example.invalid:acme/app.git" {
		t.Errorf("remote %q", remote)
	}
}

package profiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

const (
	haiku  = "[agent]\nmodel = \"haiku\"\n"
	sonnet = "[agent]\nmodel = \"sonnet\"\n"
	broken = "name = unquoted\n"
)

// readModel loads the profile of a lane that begins at base and returns the model it runs.
func readModel(t *testing.T, repo *gittest.Repo, base string) string {
	t.Helper()
	profile, err := Filesystem{Env: repo.Env()}.Load(t.Context(), repo.Dir, base, "")
	if err != nil {
		t.Fatalf("base %q: %v", base, err)
	}
	return profile.Agent.Model
}

func writeWorkingTree(t *testing.T, repo *gittest.Repo, text string) {
	t.Helper()
	path := filepath.Join(repo.Dir, RepositoryFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func baseModel(t *testing.T) string {
	t.Helper()
	profile, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return profile.Agent.Model
}

// An edit of the layer in the clone that no commit holds applies to no lane (FR-PROF-5).
func TestAnUncommittedLayerAppliesToNoLane(t *testing.T) {
	repo := gittest.New(t)
	base := repo.Commit("README.md", "a repository\n")
	writeWorkingTree(t, repo, haiku)
	if got := readModel(t, repo, base); got != baseModel(t) {
		t.Errorf("an untracked layer set the model to %q", got)
	}
	if got := readModel(t, repo, ""); got != baseModel(t) {
		t.Errorf("an untracked layer set the default branch's model to %q", got)
	}
	committed := repo.Commit(RepositoryFile, sonnet)
	writeWorkingTree(t, repo, haiku)
	for _, base := range []string{committed, ""} {
		if got := readModel(t, repo, base); got != "sonnet" {
			t.Errorf("at %q an edited layer set the model to %q, want the committed sonnet", base, got)
		}
	}
}

// A layer committed to the base branch applies at the next lane, which begins from the new tip.
func TestACommittedLayerAppliesAtTheNextLane(t *testing.T) {
	repo := gittest.New(t)
	before := repo.Commit("README.md", "a repository\n")
	after := repo.Commit(RepositoryFile, haiku)
	if got := readModel(t, repo, before); got != baseModel(t) {
		t.Errorf("the lane that began before the layer runs %q", got)
	}
	if got := readModel(t, repo, after); got != "haiku" {
		t.Errorf("the next lane runs %q", got)
	}
}

// A layer on a branch that was not merged into the commit a lane begins from does not apply to it.
func TestALayerOnAnUnmergedBranchDoesNotApply(t *testing.T) {
	repo := gittest.New(t)
	base := repo.Commit("README.md", "a repository\n")
	repo.Git("checkout", "--quiet", "-b", "topic")
	topic := repo.Commit(RepositoryFile, haiku)
	repo.Git("checkout", "--quiet", "main")
	if got := readModel(t, repo, base); got != baseModel(t) {
		t.Errorf("a layer on another branch set the model to %q", got)
	}
	if got := readModel(t, repo, ""); got != baseModel(t) {
		t.Errorf("a layer on another branch set the default branch's model to %q", got)
	}
	if got := readModel(t, repo, topic); got != "haiku" {
		t.Errorf("a lane that begins on that branch runs %q", got)
	}
}

// A review lane begins from the commit under review, so it reads the layer that commit holds, whatever the
// base branch holds since.
func TestAReviewLaneReadsTheLayerAtTheReviewedCommit(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "a repository\n")
	reviewed := repo.Commit(RepositoryFile, haiku)
	repo.Commit(RepositoryFile, sonnet)
	if got := readModel(t, repo, reviewed); got != "haiku" {
		t.Errorf("the review lane runs %q", got)
	}
	if got := readModel(t, repo, ""); got != "sonnet" {
		t.Errorf("the default branch's tip runs %q", got)
	}
}

// A layer that cannot be read at the commit stops the lane, with the file and the commit named, while the same
// text only in the working tree is nothing.
func TestABrokenLayerAtTheCommitStopsTheLaneAndNamesIt(t *testing.T) {
	for name, text := range map[string]string{
		"bad TOML":       broken,
		"an unknown key": "[lifecycle]\nstall_aftr = \"1m\"\n",
		"a raised mode":  "[agent]\npermission_mode = \"auto\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			repo := gittest.New(t)
			clean := repo.Commit("README.md", "a repository\n")
			bad := repo.Commit(RepositoryFile, text)
			user := filepath.Join(t.TempDir(), "profile.toml")
			if err := os.WriteFile(user, []byte("[agent]\npermission_mode = \"default\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Filesystem{User: user, Env: repo.Env()}.Load(t.Context(), repo.Dir, bad, "")
			if !errors.Is(err, lanes.ErrBadProfile) {
				t.Fatalf("a layer that does not parse gave %v", err)
			}
			if !strings.Contains(err.Error(), RepositoryFile) || !strings.Contains(err.Error(), bad) {
				t.Errorf("the failure %q does not name the file and the commit %s", err, bad)
			}
			// The same text only in the working tree, with the commit before it as the base, is no layer.
			if _, err := (Filesystem{User: user, Env: repo.Env()}).Load(t.Context(), repo.Dir, clean, ""); err != nil {
				t.Errorf("a broken layer outside the commit stopped the lane: %v", err)
			}
		})
	}
}

func TestACommitTheRepositoryDoesNotHoldIsNotFound(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "a repository\n")
	missing := strings.Repeat("ab", 20)
	_, err := Filesystem{Env: repo.Env()}.Load(t.Context(), repo.Dir, missing, "")
	if !errors.Is(err, ports.ErrNotFound) || !strings.Contains(err.Error(), missing) {
		t.Errorf("an unknown commit gave %v", err)
	}
}

// The lane's own overrides and the user's layer are not the repository's, so their failures name neither.
func TestAFailureOfAnotherLayerNamesNoCommit(t *testing.T) {
	repo := gittest.New(t)
	base := repo.Commit(RepositoryFile, haiku)
	_, err := Filesystem{Env: repo.Env()}.Load(t.Context(), repo.Dir, base, broken)
	if !errors.Is(err, lanes.ErrBadProfile) || strings.Contains(err.Error(), base) {
		t.Errorf("broken overrides gave %v", err)
	}
	user := filepath.Join(t.TempDir(), UserFile)
	if err := os.WriteFile(user, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Filesystem{User: user, Env: repo.Env()}.Load(t.Context(), repo.Dir, base, "")
	if !errors.Is(err, lanes.ErrBadProfile) || strings.Contains(err.Error(), base) {
		t.Errorf("a broken user layer gave %v", err)
	}
}

// A file that the working tree still holds but the base commit deleted is no layer.
func TestAFileDeletedAtTheBaseCommitIsNoLayer(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit(RepositoryFile, haiku)
	repo.Git("rm", "--quiet", "--cached", "--", RepositoryFile)
	repo.Git("commit", "--quiet", "-m", "delete the layer")
	base := repo.Git("rev-parse", "HEAD")
	writeWorkingTree(t, repo, haiku)
	for _, at := range []string{base, ""} {
		if got := readModel(t, repo, at); got != baseModel(t) {
			t.Errorf("at %q a layer deleted there set the model to %q", at, got)
		}
	}
}

// Only a regular file is a layer: a link at the path names a file this read does not follow.
func TestALinkAtTheLayerPathIsRefused(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("elsewhere.toml", haiku)
	if err := os.MkdirAll(filepath.Join(repo.Dir, filepath.Dir(RepositoryFile)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../elsewhere.toml", filepath.Join(repo.Dir, RepositoryFile)); err != nil {
		t.Skip("no symbolic links here")
	}
	repo.Git("add", "--", RepositoryFile)
	repo.Git("commit", "--quiet", "-m", "link the layer")
	_, err := Filesystem{Env: repo.Env()}.Load(t.Context(), repo.Dir, "", "")
	if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), RepositoryFile) {
		t.Errorf("a linked layer gave %v", err)
	}
}

// An empty base is the default branch's tip, which is the remote's copy when the clone has one, as the commit a
// lane begins from is.
func TestTheDefaultTipFollowsTheRemote(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.md", "a repository\n")
	clone := origin.Clone("clone")
	origin.Commit(RepositoryFile, haiku)
	clone.Git("fetch", "--quiet", "origin")
	if got := readModel(t, clone, ""); got != "haiku" {
		t.Errorf("the default tip runs %q, want the remote's layer", got)
	}
}

// A clone whose remote records no HEAD has no default branch to read, and the branch the checkout has out is not
// taken for it, because it may hold a layer nobody merged.
func TestACloneWhoseRemoteRecordsNoHeadHasNoDefaultTip(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.md", "a repository\n")
	clone := origin.Clone("clone")
	clone.Git("checkout", "--quiet", "-b", "topic")
	clone.Commit(RepositoryFile, haiku)
	if got := readModel(t, clone, ""); got != baseModel(t) {
		t.Errorf("the checked-out branch's layer set the default tip's model to %q", got)
	}
	clone.Git("remote", "set-head", "origin", "--delete")
	_, err := Filesystem{Env: clone.Env()}.Load(t.Context(), clone.Dir, "", "")
	if err == nil || !strings.Contains(err.Error(), "no HEAD") {
		t.Errorf("a remote with no HEAD gave %v", err)
	}
}

// The remote is the one git finds, whatever its name.
func TestTheDefaultTipFollowsARemoteOfAnyName(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.md", "a repository\n")
	clone := origin.Clone("clone")
	clone.Git("remote", "rename", "origin", "upstream")
	origin.Commit(RepositoryFile, haiku)
	clone.Git("fetch", "--quiet", "upstream")
	if got := readModel(t, clone, ""); got != "haiku" {
		t.Errorf("the default tip runs %q, want the remote's layer", got)
	}
}

// A base is the full hash of a commit. Anything else, an option, a branch, and an abbreviation included, is
// refused before git reads it.
func TestABaseThatIsNotAFullHashIsRefused(t *testing.T) {
	repo := gittest.New(t)
	full := repo.Commit("README.md", "a repository\n")
	for _, base := range []string{"--output=x", "main", "HEAD", full[:12], strings.ToUpper(full), full + "0"} {
		_, err := Filesystem{Env: repo.Env()}.Load(t.Context(), repo.Dir, base, "")
		if !errors.Is(err, ErrNotAHash) || errors.Is(err, ports.ErrNotFound) {
			t.Errorf("the base %q gave %v", base, err)
		}
	}
	if got := readModel(t, repo, full); got != baseModel(t) {
		t.Errorf("the full hash runs %q", got)
	}
}

// What is at the layer's path must be a regular file: a directory and a submodule are layers that cannot be read.
func TestAPathThatIsNotAFileIsRefused(t *testing.T) {
	t.Run("a directory", func(t *testing.T) {
		repo := gittest.New(t)
		repo.Commit("README.md", "a repository\n")
		bad := repo.Commit(RepositoryFile+"/inner.toml", haiku)
		assertRefused(t, repo, bad)
	})
	t.Run("a submodule", func(t *testing.T) {
		repo := gittest.New(t)
		repo.Commit("README.md", "a repository\n")
		repo.Git("update-index", "--add", "--cacheinfo", "160000,"+repo.Git("rev-parse", "HEAD")+","+RepositoryFile)
		repo.Git("commit", "--quiet", "-m", "add a submodule at the layer's path")
		assertRefused(t, repo, repo.Git("rev-parse", "HEAD"))
	})
}

func assertRefused(t *testing.T, repo *gittest.Repo, base string) {
	t.Helper()
	_, err := Filesystem{Env: repo.Env()}.Load(t.Context(), repo.Dir, base, "")
	if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), RepositoryFile) || !strings.Contains(err.Error(), base) {
		t.Errorf("a layer path that is not a file gave %v", err)
	}
}

// A layer over the limit is refused before it is read, and one at the limit is read.
func TestALayerOverTheSizeLimitIsRefused(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "a repository\n")
	padded := func(size int) string { return strings.Repeat("#", size-1) + "\n" }
	atLimit := repo.Commit(RepositoryFile, padded(MaxLayerBytes))
	if _, err := (Filesystem{Env: repo.Env()}).Load(t.Context(), repo.Dir, atLimit, ""); err != nil {
		t.Errorf("a layer of %d bytes gave %v", MaxLayerBytes, err)
	}
	over := repo.Commit(RepositoryFile, padded(MaxLayerBytes+1))
	_, err := Filesystem{Env: repo.Env()}.Load(t.Context(), repo.Dir, over, "")
	if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), RepositoryFile) || !strings.Contains(err.Error(), over) {
		t.Errorf("a layer of %d bytes gave %v", MaxLayerBytes+1, err)
	}
}

// With no repository there is no layer and no git to run.
func TestNoRepositoryReadsNoGit(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := (Filesystem{}).Load(t.Context(), "", "abc", ""); err != nil {
		t.Fatal(err)
	}
}

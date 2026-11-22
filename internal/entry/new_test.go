package entry

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/test/gittest"
)

// newOutcome is what `new --json` prints.
type newOutcome struct {
	Path     string   `json:"path"`
	Project  string   `json:"project"`
	Written  []string `json:"written"`
	Left     []string `json:"left"`
	Commands []string `json:"commands"`
}

// gitOf gives every git command of the test, the CLI's included, the repository's own configuration, and
// stops git looking for a clone above the test's own directory, so the machine's folders cannot answer.
func gitOf(t *testing.T, repo *gittest.Repo) {
	t.Helper()
	for _, entry := range repo.Env() {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(repo.Dir))
}

// runNew runs `new --json` through the binary's router, with no daemon, no session, and no delegate
// credential, and reads its document.
func runNew(t *testing.T, args ...string) newOutcome {
	t.Helper()
	isolate(t)
	for _, name := range []string{authority.EnvMemberToken, authority.EnvDelegateToken, api.EnvProject} {
		t.Setenv(name, "")
	}
	code, stdout, stderr := runMain(append(append([]string{commandCLI, "new"}, args...), "--json")...)
	if code != exitOK || stderr != "" {
		t.Fatalf("new %v: exit %d, stderr %q", args, code, stderr)
	}
	var outcome newOutcome
	if err := json.Unmarshal([]byte(stdout), &outcome); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	return outcome
}

// FR-TPL-2 on real clones: the printed commands include git init, add, and commit for a folder in no clone,
// and for one in a clone's ignored repos/, and leave them out for a folder that is a clone's top, and for one
// inside a clone that tracks it. No git command changes a repository: no folder gets a .git, and the clone's
// HEAD stays where it was.
func TestNewChoosesTheGitCommandsFromTheCloneThatHoldsTheFolder(t *testing.T) {
	cases := []struct {
		name   string
		folder func(clone string) string
		git    bool
	}{
		{"a folder in no clone", func(clone string) string { return filepath.Join(filepath.Dir(clone), "elsewhere", "orchid-api") }, true},
		{"a folder that is a clone's top", func(clone string) string { return clone }, false},
		{"a folder in a clone's ignored repos/", func(clone string) string { return filepath.Join(clone, "repos", "orchid-api") }, true},
		{"a folder inside a clone that tracks it", func(clone string) string { return filepath.Join(clone, "services", "orchid-api") }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := gittest.New(t)
			gitOf(t, repo)
			head := repo.Commit(".gitignore", "/repos/\n")
			folder := tc.folder(repo.Dir)
			outcome := runNew(t, "repository", folder)
			name := outcome.Project
			if want := projectfiles.Commands(projectfiles.Repository, folder, name, nil, tc.git); !slices.Equal(outcome.Commands, want) {
				t.Errorf("commands %v, want %v", outcome.Commands, want)
			}
			if folder != repo.Dir {
				if _, err := os.Stat(filepath.Join(folder, ".git")); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("the folder has a .git (%v)", err)
				}
			}
			if now := repo.Git("rev-parse", "HEAD"); now != head {
				t.Errorf("the clone's HEAD moved from %s to %s", head, now)
			}
		})
	}
}

// A clone's own configuration names programs git runs when it reads the clone, and `new` reads a clone it does not
// trust, in a dry run as in a run. A planted core.fsmonitor never runs, however `new` reads the folder's clone.
func TestNewRunsNoProgramTheCloneNamesWhenItReadsTheClone(t *testing.T) {
	repo := gittest.New(t)
	gitOf(t, repo)
	repo.Commit("README.md", "a clone\n")
	ran := filepath.Join(t.TempDir(), "ran")
	monitor := filepath.Join(t.TempDir(), "monitor.sh")
	if err := os.WriteFile(monitor, []byte("#!/bin/sh\necho ran >> '"+ran+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	repo.Git("config", "core.fsmonitor", monitor)
	repo.Git("status", "--short")
	if _, err := os.Stat(ran); err != nil {
		t.Fatalf("the planted program does not run when git reads the clone: %v", err)
	}
	if err := os.Remove(ran); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(repo.Dir, "plan")
	for name, args := range map[string][]string{
		"a dry run": {"new", "workspace", folder, "--create-remote", "github:acme", "--dry-run", "--json"},
		"a run":     {"new", "workspace", folder, "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.Remove(ran); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			isolate(t)
			for _, name := range []string{authority.EnvMemberToken, authority.EnvDelegateToken, api.EnvProject} {
				t.Setenv(name, "")
			}
			runMain(append([]string{commandCLI}, args...)...)
			if _, err := os.Stat(ran); err == nil {
				t.Error("a program the clone names ran when new read the clone")
			}
		})
	}
}

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

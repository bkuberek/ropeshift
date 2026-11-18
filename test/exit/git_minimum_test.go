package exit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
)

// oldGit is the release of the git stub the tests below put on PATH, which is below the minimum.
const oldGit = "2.25.1"

// refusedForGit holds a run to the refusal a git below the minimum gets: exit 1, a message that names the
// command, the minimum, and the version found, and no git command past the version check.
func refusedForGit(t *testing.T, command string, path string, code int, stdout, stderr string) {
	t.Helper()
	if code != cli.ExitError {
		t.Errorf("exit %d, want %d\nstdout %s\nstderr %s", code, cli.ExitError, stdout, stderr)
	}
	for _, want := range []string{"ropeshift " + command + ": ", "2.31.0", oldGit} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not say %q", stderr, want)
		}
	}
	if runs := doubles.GitStubRuns(t, path); !slices.Equal(runs, []string{"--version"}) {
		t.Errorf("git ran with %q, want only its version check", runs)
	}
}

func TestNewRefusesAGitBelowTheMinimumBeforeItWritesOrReadsGit(t *testing.T) {
	in := newInstall(t)
	in.path = doubles.InstallGitStub(t, oldGit, in.path)
	folder := filepath.Join(in.home, "acme")
	code, stdout, stderr := in.run("new", "monorepo", folder)
	refusedForGit(t, "new monorepo", in.path, code, stdout, stderr)
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("the folder was written despite the refusal: %v", err)
	}
}

func TestAdoptRefusesAGitBelowTheMinimumBeforeItReadsGit(t *testing.T) {
	in := newInstall(t)
	in.path = doubles.InstallGitStub(t, oldGit, in.path)
	code, stdout, stderr := in.run("adopt", "--dry-run")
	refusedForGit(t, "adopt", in.path, code, stdout, stderr)
}

func TestInitRefusesAGitBelowTheMinimumBeforeItStartsAnything(t *testing.T) {
	in := newInstall(t)
	in.member = false
	in.path = doubles.InstallGitStub(t, oldGit, in.path)
	code, stdout, stderr := in.runWith(openTerminal(t, apitest.Passphrase, apitest.Passphrase), "", nil, "init")
	refusedForGit(t, "init", in.path, code, stdout, stderr)
	if code, _, _ := in.run("health"); code != cli.ExitUnreachable {
		t.Errorf("a daemon is answering after the refusal: exit %d", code)
	}
}

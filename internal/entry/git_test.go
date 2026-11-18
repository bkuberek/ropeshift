package entry

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/test/apitest"
)

func TestRequireGitLetsABuildAtTheMinimumStart(t *testing.T) {
	probe := fake.GitProbe{Found: lanes.MinimumGit}
	if err := requireGit(t.Context(), probe); err != nil {
		t.Errorf("git at the minimum is refused: %v", err)
	}
}

func TestRequireGitRefusesABuildBelowTheMinimumNamingBoth(t *testing.T) {
	err := requireGit(t.Context(), fake.GitProbe{Found: lanes.GitVersion{Major: 2, Minor: 25, Patch: 1}})
	if err == nil {
		t.Fatal("git 2.25.1 is accepted")
	}
	for _, want := range []string{"2.31.0", "2.25.1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
}

func TestRequireGitRefusesAMachineWhereGitCannotRun(t *testing.T) {
	err := requireGit(t.Context(), fake.GitProbe{Err: errors.New("git is not on PATH")})
	if err == nil || !strings.Contains(err.Error(), "2.31.0") || !strings.Contains(err.Error(), "not on PATH") {
		t.Errorf("the refusal is %v", err)
	}
}

// With no git on PATH a daemon, solo or worker, refuses before it opens any state, with the exit code every
// failure to start has, and its message names the minimum.
func TestTheDaemonRefusesToStartWithNoGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv(api.EnvStateDir, apitest.ShortDir(t))
	t.Setenv(api.EnvRunDir, apitest.ShortDir(t))
	for name, run := range map[string]func([]string, *bytes.Buffer, *bytes.Buffer) int{
		"solo":   func(args []string, out, errs *bytes.Buffer) int { return runSolo(args, out, errs) },
		"worker": func(args []string, out, errs *bytes.Buffer) int { return runWorker(args, out, errs) },
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(nil, &stdout, &stderr)
			if code != exitError {
				t.Errorf("exit %d, want %d; stderr %q", code, exitError, stderr.String())
			}
			if want := "ropeshiftd " + name + ": "; !strings.HasPrefix(stderr.String(), want) || !strings.Contains(stderr.String(), "2.31.0") {
				t.Errorf("stderr is %q, want it to start with %q and name 2.31.0", stderr.String(), want)
			}
		})
	}
}

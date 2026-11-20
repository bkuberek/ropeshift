package practicegit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
)

const badObjectCommit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// exitedWith returns the error of a process that exits with the code, as git's does.
func exitedWith(t *testing.T, code int) *exec.ExitError {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the process is a shell command")
	}
	var exit *exec.ExitError
	if err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run(); !errors.As(err, &exit) {
		t.Fatalf("the process ended with %v, want an exit status", err)
	}
	return exit
}

// Git before 2.44 answers `rev-list --missing=print` for an absent commit with exit 128 and `fatal: bad object`, so
// the classification reads the exit code and the commit the message names, and nothing else.
func TestSnapshotBadObjectReadsExit128NamingTheCommit(t *testing.T) {
	t.Parallel()
	failed := func(code int, stderr string) error {
		return fmt.Errorf("git rev-list --objects: %w: %s", exitedWith(t, code), stderr)
	}
	for name, tt := range map[string]struct {
		err  error
		want bool
	}{
		"no error":                       {nil, false},
		"exit 128 naming the commit":     {failed(128, "fatal: bad object "+badObjectCommit), true},
		"exit 128 naming another commit": {failed(128, "fatal: bad object "+badObjectCommit[:39]+"0"), false},
		"exit 128 of another failure":    {failed(128, "fatal: not a git repository"), false},
		"another exit code":              {failed(1, "fatal: bad object "+badObjectCommit), false},
		"an error that is no exit":       {errors.New("fatal: bad object " + badObjectCommit), false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := snapshotBadObject(tt.err, badObjectCommit); got != tt.want {
				t.Errorf("snapshotBadObject = %v, want %v", got, tt.want)
			}
		})
	}
}

// The check maps what such a git prints to ports.ErrNotFound, whatever git the machine has: a git that fails as git
// 2.43 does stands in for it.
func TestSnapshotTreesHeldMapsABadObjectToNotFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the git is a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'fatal: bad object " + badObjectCommit + "' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	err := snapshotTreesHeld(t.Context(), gitcli.Git{Dir: t.TempDir(), Env: []string{"PATH=" + dir}}, badObjectCommit)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("snapshotTreesHeld = %v, want ports.ErrNotFound", err)
	}
}

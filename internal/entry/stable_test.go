package entry

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ports"
)

// A daemon in a systemd unit that cannot reach a user manager says that a stop ends its lanes, and a
// daemon that runs in no unit has no stop to fear.
func TestADaemonSaysWhenAStopWouldEndItsLanes(t *testing.T) {
	tests := []struct {
		name string
		goos string
		env  map[string]string
		says bool
	}{
		{"in a unit with no user manager", "linux", map[string]string{"INVOCATION_ID": "abc", "PATH": "/usr/bin"}, true},
		{"in no unit", "linux", map[string]string{"PATH": "/usr/bin"}, false},
		{"on macOS", "darwin", map[string]string{"INVOCATION_ID": "abc"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			runner := laneRunner(tt.goos, func(name string) string { return tt.env[name] }, &stderr)
			if _, plain := runner.(osexec.Runner); !plain {
				t.Errorf("runner %T", runner)
			}
			if said := strings.Contains(stderr.String(), "ends its lanes"); said != tt.says {
				t.Errorf("stderr %q", stderr.String())
			}
		})
	}
}

// A worker that installs a new binary at its stable link runs it once, there, so the system's first
// run of a new file falls on the worker and not on a hook inside its budget (NFR-PERF-2). A start on
// the binary the link already names runs nothing.
func TestANewBinaryRunsOnceAtInstall(t *testing.T) {
	paths := api.Paths{StateDir: t.TempDir(), RunDir: t.TempDir()}
	link := paths.StableBinary()
	runner := fake.NewProcessRunner()
	runner.Install(link, func(ports.ProcessSpec, <-chan struct{}) int { return 0 })
	runs := func() int {
		count := 0
		for _, spec := range runner.Spawned() {
			if spec.Path == link && slices.Equal(spec.Args, []string{"version"}) {
				count++
			}
		}
		return count
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	this, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}

	if err := keepStableBinary(t.Context(), paths, runner); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != this {
		t.Fatalf("the link names %q: %v", target, err)
	}
	if got := runs(); got != 1 {
		t.Errorf("a new binary ran %d times", got)
	}
	if err := keepStableBinary(t.Context(), paths, runner); err != nil {
		t.Fatal(err)
	}
	if got := runs(); got != 1 {
		t.Errorf("the same binary ran again: %d runs", got)
	}

	// An upgrade left the link on the binary before it.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "ropeshift-before"), link); err != nil {
		t.Fatal(err)
	}
	if err := keepStableBinary(t.Context(), paths, runner); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(link); target != this {
		t.Errorf("the link names %q after an upgrade", target)
	}
	if got := runs(); got != 2 {
		t.Errorf("the upgraded binary ran %d times in all", got-1)
	}
}

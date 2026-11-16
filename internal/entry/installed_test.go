package entry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	buildversion "ropeshift.com/ropeshift/internal/version"
)

// stubBinary writes an executable that answers `version --json` with output, under a name.
func stubBinary(t *testing.T, name, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat <<'EOF'\n"+output+"\nEOF\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheInstalledVersionIsWhatTheBinaryAtItsPathReports(t *testing.T) {
	tests := []struct {
		name    string
		binary  string
		output  string
		want    string
		wantErr bool
	}{
		{"a daemon binary", commandDaemon, `{"command":"ropeshiftd","version":"v0.2.0"}`, "v0.2.0", false},
		{"the CLI binary", commandCLI, `{"command":"ropeshiftd","version":"v0.2.0-rc.1"}`, "v0.2.0-rc.1", false},
		{"output that is no JSON", commandDaemon, `not a version`, "", true},
		{"a binary that reports no version", commandDaemon, `{"command":"ropeshiftd"}`, "", true},
		{"a binary that is no ropeshift", "other", `{"version":"v0.2.0"}`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readInstalledVersion(t.Context(), stubBinary(t, tt.binary, tt.output))
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("read %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
	if got, err := readInstalledVersion(t.Context(), filepath.Join(t.TempDir(), commandDaemon)); got != "" || err == nil {
		t.Errorf("a path with no binary read %q, %v", got, err)
	}
}

// The binary runs with no environment, so it reads no state directory and no daemon of the person's.
func TestTheInstalledBinaryRunsWithNoEnvironment(t *testing.T) {
	t.Setenv("ROPESHIFT_SENTINEL", "set")
	path := filepath.Join(t.TempDir(), commandDaemon)
	script := "#!/bin/sh\nif [ -n \"$ROPESHIFT_SENTINEL\" ]; then echo '{\"version\":\"leaked\"}'; else echo '{\"version\":\"v0.2.0\"}'; fi\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := readInstalledVersion(t.Context(), path); err != nil || got != "v0.2.0" {
		t.Errorf("read %q, %v: the binary saw the environment", got, err)
	}
}

func TestTheInstalledBuildIsReadByItsTickAndNeverByAsking(t *testing.T) {
	reads := 0
	answer := "v0.2.0"
	var fail error
	build := &installedBuild{path: "/opt/homebrew/Cellar/ropeshift/0.2.0/bin/ropeshiftd", read: func(_ context.Context, path string) (string, error) {
		reads++
		if path != "/opt/homebrew/Cellar/ropeshift/0.2.0/bin/ropeshiftd" {
			t.Errorf("read %s", path)
		}
		return answer, fail
	}}
	tick := build.tick()
	if got := build.Installed(); got != (buildversion.Installed{}) || reads != 0 {
		t.Errorf("before the first tick: %+v after %d reads", got, reads)
	}
	if err := tick.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if got := build.Installed(); got.Version != "v0.2.0" || got.Path == "" {
			t.Errorf("after a tick: %+v", got)
		}
	}
	if reads != 1 {
		t.Errorf("asking for the installed build read the disk: %d reads for one tick", reads)
	}
	answer = "v0.3.0"
	_ = tick.Run(t.Context())
	if got := build.Installed().Version; got != "v0.3.0" || reads != 2 {
		t.Errorf("after the next tick: %q after %d reads", got, reads)
	}
	// A build that can no longer be read leaves nothing to compare, and so nothing to warn of.
	fail = errors.New("no such file")
	if err := tick.Run(t.Context()); err != nil {
		t.Errorf("an unreadable build failed the tick: %v", err)
	}
	if got := build.Installed().Version; got != "" {
		t.Errorf("an unreadable build still reports %q", got)
	}
}

// The path to read is the one the service manager started the daemon by, so a link a package manager
// moves to a newer build shows the newer one.
func TestTheDaemonsPathIsTheOneItWasStartedBy(t *testing.T) {
	started := os.Args
	t.Cleanup(func() { os.Args = started })
	os.Args = []string{"/opt/homebrew/opt/ropeshift/bin/ropeshiftd", "solo"}
	if got, err := daemonPath(); err != nil || got != "/opt/homebrew/opt/ropeshift/bin/ropeshiftd" {
		t.Errorf("started by an absolute path: %q, %v", got, err)
	}
	os.Args = []string{"ropeshiftd", "solo"}
	executable, err := os.Executable()
	if got, gotErr := daemonPath(); got != executable || (gotErr != nil) != (err != nil) {
		t.Errorf("started by a name: %q, %v; want the executable %q", got, gotErr, executable)
	}
}

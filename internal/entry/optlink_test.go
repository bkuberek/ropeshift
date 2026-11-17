package entry

import (
	"os"
	"path/filepath"
	"testing"
)

// brewPrefix builds the part of a Homebrew prefix a keg needs in a temporary directory: a keg of each
// version in the Cellar with a binary, and an opt link to the one named current, if any.
func brewPrefix(t *testing.T, current string, versions ...string) string {
	t.Helper()
	prefix := t.TempDir()
	for _, version := range versions {
		bin := filepath.Join(prefix, "Cellar", "ropeshift", version, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, commandDaemon), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if current != "" {
		if err := os.MkdirAll(filepath.Join(prefix, "opt"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", "Cellar", "ropeshift", current), filepath.Join(prefix, "opt", "ropeshift")); err != nil {
			t.Fatal(err)
		}
	}
	return prefix
}

func TestAKegsBinaryIsNamedByItsOptLink(t *testing.T) {
	prefix := brewPrefix(t, "0.2.0", "0.1.0", "0.2.0")
	optLink := filepath.Join(prefix, "opt", "ropeshift", "bin", commandDaemon)
	older := filepath.Join(prefix, "Cellar", "ropeshift", "0.1.0", "bin", commandDaemon)
	removed := filepath.Join(prefix, "Cellar", "ropeshift", "0.0.9", "bin", commandDaemon)
	tests := []struct {
		name string
		path string
		want string
	}{
		{"the current keg", filepath.Join(prefix, "Cellar", "ropeshift", "0.2.0", "bin", commandDaemon), optLink},
		{"a keg an upgrade replaced", older, optLink},
		{"a keg that was removed", removed, optLink},
		{"the opt link itself", optLink, optLink},
		{"a path outside any Cellar", filepath.Join(prefix, "bin", commandDaemon), filepath.Join(prefix, "bin", commandDaemon)},
		{"a path that ends in the Cellar", filepath.Join(prefix, "Cellar"), filepath.Join(prefix, "Cellar")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := preferOptLink(tt.path); got != tt.want {
				t.Errorf("preferOptLink(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestAKegBinaryKeepsItsPathWhenThereIsNoOptLink(t *testing.T) {
	prefix := brewPrefix(t, "", "0.1.0")
	path := filepath.Join(prefix, "Cellar", "ropeshift", "0.1.0", "bin", commandDaemon)
	if got := preferOptLink(path); got != path {
		t.Errorf("with no opt link, preferOptLink(%q) = %q", path, got)
	}
}

// A service that ropeshift installed names the Cellar path on Linux, where an executable's path is
// resolved. After an upgrade the daemon reads the installed build by the opt link, so it still compares
// with the build that is installed when its own keg is gone.
func TestTheDaemonReadsTheInstalledBuildByTheOptLinkAfterAnUpgrade(t *testing.T) {
	prefix := brewPrefix(t, "0.2.0", "0.2.0")
	started := os.Args
	t.Cleanup(func() { os.Args = started })
	os.Args = []string{filepath.Join(prefix, "Cellar", "ropeshift", "0.1.0", "bin", commandDaemon), "solo"}
	want := filepath.Join(prefix, "opt", "ropeshift", "bin", commandDaemon)
	if got, err := daemonPath(); err != nil || got != want {
		t.Errorf("started from a keg that is gone: %q, %v; want %q", got, err, want)
	}
}

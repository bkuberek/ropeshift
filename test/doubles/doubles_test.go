package doubles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A record is named for a process, and process IDs come round: a session that started later can hold
// the smaller number, and a name sorts as text in any case. Sessions read in the order they started.
func TestSessionsReadInTheOrderTheyStarted(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, SeenDir), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour)
	for i, record := range []struct{ name, body string }{{"99990.json", `{"args":["first"]}`}, {"105.json", `{"args":["second"]}`}} {
		path := filepath.Join(dir, SeenDir, record.name)
		if err := os.WriteFile(path, []byte(record.body), 0o600); err != nil {
			t.Fatal(err)
		}
		at := start.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	seen := SeenBy(t, dir)
	if len(seen) != 2 || seen[0].Args[0] != "first" || seen[1].Args[0] != "second" {
		t.Errorf("seen %+v", seen)
	}
}

// A machine with no Go runs what a bundle holds: each binary by its name, and the canary from its own
// directory there. A name the bundle lacks fails, and nothing is built.
func TestAPrebuiltBundleNeedsNoGo(t *testing.T) {
	bundle := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundle, PrebuiltFakeClaude), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPrebuilt, bundle)
	t.Setenv("PATH", t.TempDir())
	out := t.TempDir()

	path, err := Binary(out, PrebuiltFakeClaude, "ropeshift.dev/ropeshift/test/doubles/fakeclaude")
	if err != nil || path != filepath.Join(bundle, PrebuiltFakeClaude) {
		t.Errorf("the double is %q: %v", path, err)
	}
	if path, err := Binary(out, PrebuiltChaos, "ropeshift.dev/ropeshift/cmd/ropeshift", "chaos"); err == nil {
		t.Errorf("a binary the bundle lacks is %q", path)
	}
	if entries, err := os.ReadDir(out); err != nil || len(entries) != 0 {
		t.Errorf("something was built: %v, %v", entries, err)
	}
	if dir, _, _ := strings.Cut(CanaryPath(t), string(os.PathListSeparator)); dir != filepath.Join(bundle, PrebuiltCanary) {
		t.Errorf("the canary is in %q", dir)
	}
}

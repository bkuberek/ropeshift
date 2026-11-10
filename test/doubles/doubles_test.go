package doubles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
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

	path, err := Binary(out, PrebuiltFakeClaude, "ropeshift.com/ropeshift/test/doubles/fakeclaude")
	if err != nil || path != filepath.Join(bundle, PrebuiltFakeClaude) {
		t.Errorf("the double is %q: %v", path, err)
	}
	if path, err := Binary(out, PrebuiltChaos, "ropeshift.com/ropeshift/cmd/ropeshift", "chaos"); err == nil {
		t.Errorf("a binary the bundle lacks is %q", path)
	}
	if entries, err := os.ReadDir(out); err != nil || len(entries) != 0 {
		t.Errorf("something was built: %v, %v", entries, err)
	}
	if dir, _, _ := strings.Cut(CanaryPath(t), string(os.PathListSeparator)); dir != filepath.Join(bundle, PrebuiltCanary) {
		t.Errorf("the canary is in %q", dir)
	}
}

// A pane that is still writing into the test's directory when the test ends must not make the
// directory's removal fail: the private tmux server and its panes end before the files go.
func TestPrivateTmuxEndsBeforeTheTestsFilesAreRemoved(t *testing.T) {
	RequireTmux(t)
	terminal := PrivateTmux(t)
	dir := t.TempDir()
	loop := `i=0; while :; do i=$((i+1)); : > f$i; done`
	spec := ports.PaneSpec{Name: "writer", Dir: dir, Width: 130, Height: 45, Command: []string{"/bin/sh", "-c", loop}}
	if _, err := terminal.Open(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "f1")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane wrote nothing")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

package claudecode

import (
	"errors"
	"os"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// markedPane is a session in a fake pane, with a marker path its hook would read.
func markedPane(t *testing.T) (*paneSession, *fake.Terminal, string) {
	t.Helper()
	terminal := fake.NewTerminal()
	spec := policySpec(t)
	spec.RunMode = ports.RunModeInteractive
	name := paneName(spec)
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: name, Command: []string{"claude", "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(spec.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	driver := &Driver{terminal: terminal}
	return driver.newPaneSession(spec, name, ports.SessionHandle{ID: "s"}, lanes.CostMeter{}), terminal, markerPath(spec)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("never saw %s", what)
}

func fresh(path string) bool {
	info, err := os.Stat(path)
	return err == nil && lanes.Attached(info.ModTime(), time.Now())
}

func attach(t *testing.T, terminal *fake.Terminal, name string) {
	t.Helper()
	go func() { _ = terminal.Attach(t.Context(), name, ports.Console{}) }()
	eventually(t, "the client attached", func() bool { return len(terminal.Attached()) != 0 })
}

// The marker is how a hook, which has milliseconds and no business with tmux, learns that a person
// is attached: present and fresh while a client is, and gone when none is.
func TestTheMarkerFollowsTheClientsOfThePane(t *testing.T) {
	s, terminal, marker := markedPane(t)
	go s.markAttached(5 * time.Millisecond)
	t.Cleanup(func() { close(s.gone) })

	time.Sleep(30 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a marker for a pane nobody attached to")
	}
	attach(t, terminal, s.name)
	eventually(t, "a fresh marker", func() bool { return fresh(marker) })

	// It is touched again and again, so it stays fresh past the bound.
	first, _ := os.Stat(marker)
	eventually(t, "a later touch", func() bool {
		info, err := os.Stat(marker)
		return err == nil && info.ModTime().After(first.ModTime())
	})

	terminal.Detach(s.name)
	eventually(t, "no marker after the detach", func() bool {
		_, err := os.Stat(marker)
		return errors.Is(err, os.ErrNotExist)
	})
}

// A read that did not finish says nothing, and the marker is left to go stale, which is how a worker
// that cannot tell reads to the hook.
func TestTheMarkerIsLeftToGoStaleWhenTheWorkerCannotTell(t *testing.T) {
	s, terminal, marker := markedPane(t)
	go s.markAttached(5 * time.Millisecond)
	t.Cleanup(func() { close(s.gone) })
	attach(t, terminal, s.name)
	eventually(t, "a fresh marker", func() bool { return fresh(marker) })

	terminal.FailReads(errors.New("tmux did not answer"))
	time.Sleep(30 * time.Millisecond)
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("a read that failed took the marker away: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	after, err := os.Stat(marker)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the marker was touched while the worker could not tell: %v, %v", after, err)
	}
}

func TestTheMarkerGoesWithTheSession(t *testing.T) {
	s, terminal, marker := markedPane(t)
	done := make(chan struct{})
	go func() {
		s.markAttached(5 * time.Millisecond)
		close(done)
	}()
	attach(t, terminal, s.name)
	eventually(t, "a fresh marker", func() bool { return fresh(marker) })

	close(s.gone)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the marker's goroutine outlived its session")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a marker outlived its session: %v", err)
	}
}

func TestTheMarkerEndsWhenThePaneIsGone(t *testing.T) {
	s, terminal, marker := markedPane(t)
	done := make(chan struct{})
	go func() {
		s.markAttached(5 * time.Millisecond)
		close(done)
	}()
	attach(t, terminal, s.name)
	eventually(t, "a fresh marker", func() bool { return fresh(marker) })

	if err := terminal.Close(t.Context(), s.name); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the marker's goroutine outlived its pane")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a marker outlived its pane: %v", err)
	}
}

// A session with no directory of its own gets no marker, and its beat does nothing, so a relative path
// never lands in the worker's working directory.
func TestASessionWithNoDirectoryHasNoMarker(t *testing.T) {
	spec := policySpec(t)
	spec.RunMode, spec.ConfigDir = ports.RunModeInteractive, ""
	if got := markerPath(spec); got != "" {
		t.Errorf("marker %q", got)
	}
	if got := compilePolicy(spec).AttachedMarker; got != "" {
		t.Errorf("a policy names marker %q", got)
	}
	s, _, _ := markedPane(t)
	s.marker = ""
	done := make(chan struct{})
	go func() {
		s.markAttached(time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a beat with no marker went on")
	}
}

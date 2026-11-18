package claudecode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// foldStream reads a session's events until it has seen the turns it is told to wait for, and folds them as a
// worker does: any event's tool calls, each turn's end, and each line it could not read.
func foldStream(t *testing.T, events <-chan ports.DriverEvent, turns int) *lanes.DigestBuilder {
	t.Helper()
	builder := lanes.NewDigest(lanes.DigestStream, "/work/lane", 100)
	deadline := time.After(20 * time.Second)
	for seen := 0; seen < turns; {
		select {
		case event, open := <-events:
			if !open {
				t.Fatalf("the stream closed after %d of %d turns", seen, turns)
			}
			if len(event.Tools) != 0 {
				builder.Calls(event.Seq, event.Tools)
			}
			switch event.Kind {
			case ports.EventTurnEnded:
				builder.Turn(event.Seq, lanes.TurnFacts{DurationMs: event.Turn.DurationMs, APIMs: event.Turn.APIMs})
				seen++
			case ports.EventMalformed:
				builder.Missed()
			}
		case <-deadline:
			t.Fatalf("the stream gave %d of %d turns", seen, turns)
		}
	}
	return builder
}

// TestARecordedStreamFoldsIntoItsDigest is FR-SLOG-1 on a headless session: the tool calls of the session and
// of its subagent by name, the paths its file-writing tools were called with, and the turns with their times.
// A file a shell command wrote is no path of the digest, and a secret in a command, an edit, or a result is
// nowhere in it.
func TestARecordedStreamFoldsIntoItsDigest(t *testing.T) {
	driver, _, spec := session(t, "digest-stream", nil)
	handle, err := driver.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	events, err := driver.Events(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	builder := foldStream(t, events, 2)
	if err := driver.Stop(t.Context(), handle, ports.StopAtRest); err != nil {
		t.Fatal(err)
	}
	digest := builder.Finish(5000)
	raw, err := json.MarshalIndent(digest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "digest-stream.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw)+"\n" != string(want) {
		t.Errorf("digest\n--- got\n%s\n--- want\n%s", raw, want)
	}
	state, err := builder.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{string(raw), string(state)} {
		for _, leaked := range []string{"sk-live", "Authorization", "curl", "a prompt nobody should keep", "I changed the retry budget", "shell-written"} {
			if strings.Contains(kept, leaked) {
				t.Errorf("the digest holds %q: %s", leaked, kept)
			}
		}
	}
}

// TestALineTheDriverCannotReadCostsTheDigestItsCompleteness holds `complete` for a stream: a line that did not
// parse is an event the worker knows it missed.
func TestALineTheDriverCannotReadCostsTheDigestItsCompleteness(t *testing.T) {
	driver, _, spec := session(t, "malformed-line", nil)
	handle, err := driver.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	events, err := driver.Events(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	builder := foldStream(t, events, 1)
	if err := driver.Stop(t.Context(), handle, ports.StopAtRest); err != nil {
		t.Fatal(err)
	}
	if builder.Finish(5000).Complete {
		t.Error("a line that did not parse left the digest complete")
	}
}

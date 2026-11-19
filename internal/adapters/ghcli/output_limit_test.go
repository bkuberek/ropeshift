package ghcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// standIn is a `gh` that runs a shell script, so a test controls what it writes.
func standIn(t *testing.T, script string) Forge {
	t.Helper()
	path := filepath.Join(t.TempDir(), Binary)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return Forge{Path: path, Env: os.Environ()}
}

// What `gh` writes to stdout up to maxOutput is read whole.
func TestRunKeepingReadsStdoutUpToTheLimit(t *testing.T) {
	forge := standIn(t, fmt.Sprintf("head -c %d /dev/zero", maxOutput))
	out, err := forge.runKeeping(t.Context(), t.TempDir(), "pr", "list")
	if err != nil || len(out) != maxOutput {
		t.Fatalf("stdout of exactly the limit read %d bytes: %v", len(out), err)
	}
}

// A `gh` that writes past maxOutput is an error, not a partial read, because every caller parses the whole output.
func TestRunKeepingFailsForStdoutPastTheLimit(t *testing.T) {
	forge := standIn(t, fmt.Sprintf("head -c %d /dev/zero", maxOutput+1))
	out, err := forge.runKeeping(t.Context(), t.TempDir(), "pr", "list")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d bytes", maxOutput)) {
		t.Fatalf("stdout past the limit gave %d bytes and %v", len(out), err)
	}
	if len(out) != 0 {
		t.Errorf("the error carries %d bytes of output", len(out))
	}
}

// A `gh` that never stops writing is stopped, and the error is the limit's, not the context's.
func TestRunKeepingStopsAGhThatNeverStopsWriting(t *testing.T) {
	forge := standIn(t, "exec yes")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_, err := forge.runKeeping(ctx, t.TempDir(), "pr", "list")
	if err == nil || !strings.Contains(err.Error(), "more than") || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("an endless stdout gave %v", err)
	}
}

// The stderr of a failed `gh` is read through a limit too, and the error keeps the start of it, clipped.
func TestRunKeepingBoundsStderr(t *testing.T) {
	forge := standIn(t, "echo start >&2\nhead -c 4000000 /dev/zero | tr '\\000' x >&2\nexit 3")
	_, err := forge.runKeeping(t.Context(), t.TempDir(), "pr", "list")
	var failed *ghError
	if !errors.As(err, &failed) || failed.code != 3 {
		t.Fatalf("a failed gh gave %v", err)
	}
	if len(failed.stderr) > maxDetail || !strings.HasPrefix(failed.stderr, "start") {
		t.Errorf("stderr is %d bytes and starts %.10q", len(failed.stderr), failed.stderr)
	}
}

// The stderr of a failed log read keeps its start, clipped, and holds no escape sequence or hidden character.
func TestRunTailBoundsAndCleansStderr(t *testing.T) {
	forge := standIn(t, "printf 'start \\033[31mred\\033[0m \\342\\200\\256hidden\\n' >&2\nhead -c 4000000 /dev/zero | tr '\\000' x >&2\nexit 3")
	_, err := forge.runTail(t.Context(), t.TempDir(), 1024, "run", "view")
	var failed *ghError
	if !errors.As(err, &failed) || failed.code != 3 {
		t.Fatalf("a failed gh gave %v", err)
	}
	if len(failed.stderr) > maxDetail || !strings.HasPrefix(failed.stderr, "start red hidden") {
		t.Errorf("stderr is %d bytes and starts %.30q", len(failed.stderr), failed.stderr)
	}
}

// A `gh` that fills stderr past the limit is read to the limit and no further, so memory stays bounded.
func TestRunTailReadsStderrOnlyToTheLimit(t *testing.T) {
	forge := standIn(t, "head -c 4000000 /dev/zero | tr '\\000' ' ' >&2\necho 'rate limit exceeded' >&2\nexit 1")
	_, err := forge.runTail(t.Context(), t.TempDir(), 1024, "run", "view")
	var failed *ghError
	if !errors.As(err, &failed) {
		t.Fatalf("a failed gh gave %v", err)
	}
	if strings.Contains(failed.stderr, "rate limit") {
		t.Errorf("stderr past the limit was read: %q", failed.stderr)
	}
}

// The stderr a failed `gh` leaves in the error is text a stranger may have written, so no control sequence reaches
// the error.
func TestRunKeepingSanitizesStderr(t *testing.T) {
	forge := standIn(t, "printf '\\033[31mboom\\033]0;title\\007 now\\n' >&2\nexit 3")
	_, err := forge.runKeeping(t.Context(), t.TempDir(), "pr", "list")
	var failed *ghError
	if !errors.As(err, &failed) || failed.code != 3 {
		t.Fatalf("a failed gh gave %v", err)
	}
	if strings.ContainsAny(failed.stderr, "\x1b\x07") {
		t.Errorf("stderr holds a control sequence: %q", failed.stderr)
	}
	if !strings.Contains(failed.stderr, "boom") {
		t.Errorf("stderr is %q, want it to keep the text", failed.stderr)
	}
}

// A `gh` whose child keeps stdout open after it exits does not hold runTail past the wait delay.
func TestRunTailDoesNotWaitForAChildThatKeepsStdoutOpen(t *testing.T) {
	forge := standIn(t, "sleep 20 &\necho log")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = forge.runTail(t.Context(), t.TempDir(), 1024, "run", "view")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runTail waited for the child that kept stdout open")
	}
}

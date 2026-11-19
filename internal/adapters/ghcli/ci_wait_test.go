package ghcli

import (
	"strings"
	"testing"
)

// A `gh` that exited with success, and left a child that keeps stdout open, has answered: the wait for the child
// ending is no failure.
func TestRunTailReadsTheAnswerOfAGhWhoseChildKeepsStdoutOpen(t *testing.T) {
	forge := standIn(t, "sleep 5 &\necho the log")
	out, err := forge.runTail(t.Context(), t.TempDir(), 1024, "run", "view")
	if err != nil || !strings.Contains(out, "the log") {
		t.Fatalf("runTail gave %q and %v, want the log and no error", out, err)
	}
}

// A `gh` that failed is still a failure when its child keeps stdout open.
func TestRunTailStillFailsForAGhThatExitedWithAFailure(t *testing.T) {
	forge := standIn(t, "sleep 5 &\necho no >&2\nexit 3")
	if _, err := forge.runTail(t.Context(), t.TempDir(), 1024, "run", "view"); err == nil {
		t.Fatal("a failed gh gave no error")
	}
}

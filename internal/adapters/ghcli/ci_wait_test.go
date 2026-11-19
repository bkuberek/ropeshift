package ghcli

import (
	"errors"
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

// runKeeping, which `gh api --include` goes through, treats a `gh` that exited with success and left a child holding
// stdout the same way: what it wrote is returned, and a failure is still a failure.
func TestRunKeepingReadsTheAnswerOfAGhWhoseChildKeepsStdoutOpen(t *testing.T) {
	forge := standIn(t, "sleep 5 &\necho the answer")
	out, err := forge.runKeeping(t.Context(), t.TempDir(), "api", "--include")
	if err != nil || !strings.Contains(string(out), "the answer") {
		t.Fatalf("runKeeping gave %q and %v, want the answer and no error", out, err)
	}
	failing := standIn(t, "sleep 5 &\necho partial\necho no >&2\nexit 3")
	out, err = failing.runKeeping(t.Context(), t.TempDir(), "api", "--include")
	var failed *ghError
	if !errors.As(err, &failed) || failed.code != 3 || !strings.Contains(string(out), "partial") {
		t.Errorf("a failed gh gave %q and %v, want its output and its exit code", out, err)
	}
}

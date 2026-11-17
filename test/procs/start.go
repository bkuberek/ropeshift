package procs

import (
	"os/exec"
	"testing"
)

// Start starts a process that a test owns and must not outlive it, such as a daemon. A test that ends
// by a timeout or a kill never runs its cleanups, so where the system allows it the process ends with
// the test process itself: see the platform's start for what it does and what it leaves.
//
// The caller still waits for the process and ends it in its own cleanup. Start only makes sure that a
// test process that never gets to clean up does not leave the process running.
func Start(t testing.TB, cmd *exec.Cmd) error {
	t.Helper()
	return start(t, cmd)
}

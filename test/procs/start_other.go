//go:build !linux

package procs

import (
	"os/exec"
	"syscall"
	"testing"
)

// start puts the process in a group of its own and kills the group when the test ends. macOS has no
// parent-death signal, so a test process that is killed, or that ends by a timeout, never runs its
// cleanups and leaves the process running. That gap stays: only a test that ends normally, or that
// fails, ends its processes here.
func start(t testing.TB, cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if !cmd.SysProcAttr.Setsid {
		cmd.SysProcAttr.Setpgid = true
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	// The group is the process and what it started that stayed in it. A group nothing holds any longer
	// is no group, and the signal finds none.
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	return nil
}

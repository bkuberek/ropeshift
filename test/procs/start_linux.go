//go:build linux

package procs

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"testing"
)

// The kernel sends a child its parent-death signal when the thread that created it ends, not when the
// process does (go.dev/issue/27505). A start from an ordinary goroutine runs on whatever thread the
// scheduler has, and that thread may end while the test still runs, taking the child with it. So every
// start here is made by one goroutine that stays locked to its own thread for the life of the process.
// That thread ends only when the test process does, which is the moment the signal is wanted.
type launch struct {
	cmd     *exec.Cmd
	started chan<- error
}

var launcher = sync.OnceValue(func() chan<- launch {
	requests := make(chan launch)
	go func() {
		runtime.LockOSThread()
		for request := range requests {
			request.started <- request.cmd.Start()
		}
	}()
	return requests
})

// start has the kernel kill the process with SIGKILL when the test process dies, whatever way it dies.
// The signal reaches the process itself and not what it started: a daemon's lanes outlive it by design.
func start(_ testing.TB, cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	started := make(chan error, 1)
	launcher() <- launch{cmd: cmd, started: started}
	return <-started
}

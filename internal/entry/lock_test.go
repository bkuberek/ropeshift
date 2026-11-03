package entry

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// The two helper processes of TestAKilledDaemonLeavesItsInstallationFree find their part here.
const (
	envLockHolder = "ROPESHIFT_TEST_LOCK_HOLDER"
	envLockSharer = "ROPESHIFT_TEST_LOCK_SHARER"
)

// TestAKilledDaemonLeavesItsInstallationFree holds the one-daemon lock to the process that took it.
// A daemon forks git and the agent CLI all the time, and a child forked an instant before a SIGKILL
// shares the lock's open file until it execs. Under load that took hundreds of milliseconds, so a
// daemon started at once after the kill found the installation held. Here the child that shares the
// open file lives on, so a lock that follows the open file never ends.
func TestAKilledDaemonLeavesItsInstallationFree(t *testing.T) {
	dir := t.TempDir()
	stdin, feed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	holder := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsTheInstallation$")
	holder.Env = append(os.Environ(), envLockHolder+"="+dir)
	holder.Stdin = stdin
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	exited := make(chan struct{})
	go func() {
		_ = holder.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = feed.Close()
		_ = holder.Process.Kill()
		<-exited
	})
	sharer := readSharer(t, out)
	t.Cleanup(func() {
		if exists(sharer) {
			_ = syscall.Kill(sharer, syscall.SIGKILL)
		}
	})

	if _, err := holdInstallation(dir); !errors.Is(err, errBusy) {
		t.Fatalf("a second daemon beside a live one: %v", err)
	}
	if err := holder.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-exited
	if !exists(sharer) {
		t.Fatal("the child that shares the lock's file died with the daemon, so the check proves nothing")
	}
	lock, err := holdInstallation(dir)
	if err != nil {
		t.Fatalf("after the daemon's death, with its child alive: %v", err)
	}
	_ = lock.Close()
}

func readSharer(t *testing.T, out io.Reader) int {
	t.Helper()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the holder said %q: %v", line, err)
	}
	pid, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(line), "sharer "))
	if err != nil {
		t.Fatalf("the holder said %q", line)
	}
	return pid
}

func exists(pid int) bool { return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

// TestHelperHoldsTheInstallation is the daemon: it takes the lock, starts a child that shares the
// lock's open file the way a fork does until it execs, says the child's PID, and waits to be killed.
func TestHelperHoldsTheInstallation(t *testing.T) {
	dir := os.Getenv(envLockHolder)
	if dir == "" {
		t.Skip("a helper of TestAKilledDaemonLeavesItsInstallationFree")
	}
	lock, err := holdInstallation(dir)
	if err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestHelperSharesTheLock$")
	child.Env = append(os.Environ(), envLockSharer+"=1")
	child.Stdin = os.Stdin
	child.ExtraFiles = []*os.File{lock}
	if err := child.Start(); err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sharer %d\n", child.Process.Pid)
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// TestHelperSharesTheLock is the child: it holds the open file until the test closes its stdin.
func TestHelperSharesTheLock(t *testing.T) {
	if os.Getenv(envLockSharer) == "" {
		t.Skip("a helper of TestAKilledDaemonLeavesItsInstallationFree")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

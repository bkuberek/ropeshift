package shim

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

// The two helper processes of TestAKilledShimLeavesItsGenerationFree find their part here.
const (
	envLockHolder = "ROPESHIFT_TEST_SHIM_LOCK_HOLDER"
	envLockSharer = "ROPESHIFT_TEST_SHIM_LOCK_SHARER"
)

// TestAKilledShimLeavesItsGenerationFree holds a generation's lock to the shim that took it. A shim
// forks its child after it takes the lock, and a child forked an instant before a SIGKILL shares the
// lock's open file until it execs, so a lock that follows the open file outlives the shim and the
// shim started next for that generation refuses. Here the child that shares the open file lives on,
// so such a lock never ends.
func TestAKilledShimLeavesItsGenerationFree(t *testing.T) {
	dir := t.TempDir()
	stdin, feed, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	holder := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsAGeneration$")
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
		if alive(sharer) {
			_ = syscall.Kill(sharer, syscall.SIGKILL)
		}
	})

	if _, err := lock(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("a second shim beside a live one: %v", err)
	}
	if err := holder.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-exited
	if !alive(sharer) {
		t.Fatal("the child that shares the lock's file died with the shim, so the check proves nothing")
	}
	held, err := lock(dir)
	if err != nil {
		t.Fatalf("after the shim's death, with its child alive: %v", err)
	}
	unlock(held)
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

func alive(pid int) bool { return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

// TestHelperHoldsAGeneration is the shim: it takes the lock, starts a child that shares the lock's
// open file the way a fork does until it execs, says the child's PID, and waits to be killed.
func TestHelperHoldsAGeneration(t *testing.T) {
	dir := os.Getenv(envLockHolder)
	if dir == "" {
		t.Skip("a helper of TestAKilledShimLeavesItsGenerationFree")
	}
	held, err := lock(dir)
	if err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestHelperSharesAGeneration$")
	child.Env = append(os.Environ(), envLockSharer+"=1")
	child.Stdin = os.Stdin
	child.ExtraFiles = []*os.File{held}
	if err := child.Start(); err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sharer %d\n", child.Process.Pid)
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// TestHelperSharesAGeneration is the child: it holds the open file until the test closes its stdin.
func TestHelperSharesAGeneration(t *testing.T) {
	if os.Getenv(envLockSharer) == "" {
		t.Skip("a helper of TestAKilledShimLeavesItsGenerationFree")
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

//go:build linux

package procs

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const startedByParent = "ROPESHIFT_PROCS_STARTED_BY_PARENT"

// TestStartedProcessEndsWithItsParent kills a process that started a child with Start, with no chance
// to clean up, and shows the child ends with it. The child is started from a goroutine that ends at
// once, which is what an ordinary test's goroutine does, and must outlive that goroutine's thread.
func TestStartedProcessEndsWithItsParent(t *testing.T) {
	if os.Getenv(startedByParent) != "" {
		parentStartsAChild(t)
		return
	}
	parent := exec.Command(os.Args[0], "-test.run=^TestStartedProcessEndsWithItsParent$")
	parent.Env = append(os.Environ(), startedByParent+"=1")
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() {
		_ = parent.Wait()
		close(waited)
	}()
	var child int
	line, err := bufio.NewReader(out).ReadString('\n')
	if err == nil {
		child, err = strconv.Atoi(line[:len(line)-1])
	}
	if err != nil || child <= 1 {
		_ = parent.Process.Kill()
		t.Fatalf("the parent reported no child: %q: %v", line, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	time.Sleep(300 * time.Millisecond)
	if !Alive(child) {
		t.Fatal("the child ended while its parent still ran")
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-waited
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := Command(child); err != nil {
			return
		}
	}
	t.Error("the child outlived its parent")
}

// parentStartsAChild is the parent of the test above: it starts a child, says its process ID, and waits
// to be killed.
func parentStartsAChild(t *testing.T) {
	cmd := exec.Command("sleep", "300")
	started := make(chan error)
	go func() { started <- Start(t, cmd) }()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString(strconv.Itoa(cmd.Process.Pid) + "\n")
	time.Sleep(time.Minute)
}

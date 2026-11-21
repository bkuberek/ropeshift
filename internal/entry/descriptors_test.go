package entry

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/test/apitest"
)

// envDescriptorProbe names the descriptor the helper process says it holds.
const envDescriptorProbe = "ROPESHIFT_TEST_DESCRIPTOR"

// TestHelperSaysWhetherItHoldsADescriptor is the child of the tests below: it says whether the descriptor the
// test names is open in it.
func TestHelperSaysWhetherItHoldsADescriptor(t *testing.T) {
	raw := os.Getenv(envDescriptorProbe)
	if raw == "" {
		t.Skip("a helper of the descriptor tests")
	}
	fd, err := strconv.Atoi(raw)
	if err != nil {
		fmt.Printf("error %v\n", err)
		os.Exit(1)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		fmt.Println("closed")
	} else {
		fmt.Println("open")
	}
	os.Exit(0)
}

// handedDown is a descriptor this process holds that is not close-on-exec, as one it was started with is.
func handedDown(t *testing.T) int {
	t.Helper()
	var pair [2]int
	if err := syscall.Pipe(pair[:]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(pair[0]); _ = syscall.Close(pair[1]) })
	if closesOnExec(t, pair[1]) {
		t.Fatalf("descriptor %d is close-on-exec already, so the test proves nothing", pair[1])
	}
	return pair[1]
}

func closesOnExec(t *testing.T, fd int) bool {
	t.Helper()
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	return flags&unix.FD_CLOEXEC != 0
}

// helperCommand is the helper process, asked about fd, with its answer on stdout.
func helperCommand(fd int) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperSaysWhetherItHoldsADescriptor$")
	cmd.Env = append(os.Environ(), envDescriptorProbe+"="+strconv.Itoa(fd))
	return cmd
}

func childHolds(t *testing.T, cmd *exec.Cmd) bool {
	t.Helper()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	switch got := strings.TrimSpace(stdout.String()); got {
	case "open":
		return true
	case "closed":
		return false
	default:
		t.Fatalf("the helper said %q", got)
		return false
	}
}

// A descriptor this process was started with, such as a terminal a harness handed down on a descriptor of its
// own, is not handed on to what it starts, because a child that outlives it, such as a terminal server, would
// hold that descriptor after the process that owned it ended.
func TestADescriptorThisProcessWasGivenIsNotHandedToItsChildren(t *testing.T) {
	fd := handedDown(t)
	if !childHolds(t, helperCommand(fd)) {
		t.Fatalf("a child holds descriptor %d before the mark, so the test proves nothing", fd)
	}
	keepDescriptorsFromChildren()
	if childHolds(t, helperCommand(fd)) {
		t.Errorf("a child holds descriptor %d after the mark", fd)
	}
}

// The daemon ropeshift init starts is the first process the descriptors of init's own start would reach, and it
// starts the terminal server.
func TestTheDaemonInitStartsHoldsNoDescriptorInitWasGiven(t *testing.T) {
	fd := handedDown(t)
	cmd := initDaemonCommand(os.Args[0])
	cmd.Args = []string{commandDaemon, "-test.run=^TestHelperSaysWhetherItHoldsADescriptor$"}
	cmd.Env = append(os.Environ(), envDescriptorProbe+"="+strconv.Itoa(fd))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	daemon, err := launchInitDaemon(cmd, initDaemonStop)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-daemon.exited; err != nil {
		t.Fatalf("the stand-in for the daemon failed: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "closed" {
		t.Errorf("the daemon init started says descriptor %d is %q", fd, got)
	}
}

// Every entry point of the daemon marks what it was started with before it starts anything, a service manager's or a
// shell's descriptors included, so no process it starts, git first and the terminal server last, holds one. Each runs
// here to its first refusal, which is before it starts a process of its own.
func TestEveryDaemonEntryPointMarksItsDescriptorsBeforeItStartsAnything(t *testing.T) {
	entries := []struct {
		name string
		run  func(stdout, stderr io.Writer) int
		// want is the exit that tells where the entry point stopped: git is not on PATH for the first two, and a
		// server has no database to connect to.
		want int
	}{
		{"solo", func(stdout, stderr io.Writer) int { return runSolo(nil, stdout, stderr) }, exitError},
		{"worker", func(stdout, stderr io.Writer) int { return runWorker(nil, stdout, stderr) }, exitError},
		{"server", func(stdout, stderr io.Writer) int { return runServer(nil, stdout, stderr) }, exitUsage},
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			fd := handedDown(t)
			t.Setenv("PATH", t.TempDir())
			t.Setenv(api.EnvStateDir, apitest.ShortDir(t))
			t.Setenv(api.EnvRunDir, apitest.ShortDir(t))
			t.Setenv(envDatabase, "")
			var stdout, stderr bytes.Buffer
			if code := entry.run(&stdout, &stderr); code != entry.want {
				t.Fatalf("exit %d, want %d; stderr %q", code, entry.want, stderr.String())
			}
			if !closesOnExec(t, fd) {
				t.Errorf("descriptor %d is still handed to the processes the daemon starts", fd)
			}
		})
	}
}

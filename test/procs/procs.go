// Package procs signals the processes a test recorded, and only those. A process ID outlives its
// process, so an ID read from a state file or a server may name some other process by the time a
// test ends. Every signal a test helper sends by ID goes through here, and is sent only while the
// command line of the process holding that ID still carries what the test recorded.
package procs

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// ErrGone says no process holds the ID.
var ErrGone = errors.New("no such process")

// Command is the command line of the process holding an ID, with its arguments. It fails with
// ErrGone when no process holds it.
func Command(pid int) (string, error) {
	if pid <= 1 {
		return "", fmt.Errorf("process ID %d is not one a test records", pid)
	}
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", ErrGone
	}
	if err != nil {
		return "", err
	}
	command := strings.TrimSpace(string(out))
	if command == "" {
		return "", ErrGone
	}
	return command, nil
}

// Signal sends a signal to the process holding an ID, only while its command line holds every
// marker. It reports whether the signal was sent. A process that is gone is not an event and leaves
// no reason. A process that is there and does not match, or that could not be read, is left alone,
// and the reason says why, for the caller to log.
func Signal(pid int, signal syscall.Signal, markers ...string) (sent bool, skipped string) {
	if len(markers) == 0 {
		return false, fmt.Sprintf("process %d: nothing was recorded to match it against", pid)
	}
	command, err := Command(pid)
	if errors.Is(err, ErrGone) {
		return false, ""
	}
	if err != nil {
		return false, fmt.Sprintf("process %d left alone: its command line could not be read: %v", pid, err)
	}
	for _, marker := range markers {
		if marker == "" || !strings.Contains(command, marker) {
			return false, fmt.Sprintf("process %d left alone: %q does not hold %q", pid, command, marker)
		}
	}
	if err := syscall.Kill(pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Sprintf("process %d: %v", pid, err)
	}
	return true, ""
}

// Alive says whether a process with this ID still exists.
func Alive(pid int) bool {
	return pid > 0 && !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

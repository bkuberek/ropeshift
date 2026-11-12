package procs

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hold starts a process that does nothing for a minute and reports when it has ended and been collected.
func hold(t *testing.T, name string, args ...string) (pid int, ended func(time.Duration) bool) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	return cmd.Process.Pid, func(within time.Duration) bool {
		select {
		case <-done:
			return true
		case <-time.After(within):
			return false
		}
	}
}

func TestSignalLeavesAProcessWhoseCommandIsNotTheRecordedOne(t *testing.T) {
	pid, ended := hold(t, "sleep", "71")

	sent, skipped := Signal(pid, syscall.SIGKILL, "lane-shim")

	if sent || ended(500*time.Millisecond) {
		t.Error("a process that is not the recorded one was signaled")
	}
	if !strings.Contains(skipped, "left alone") {
		t.Errorf("the reason is %q", skipped)
	}
}

func TestSignalNeedsEveryMarker(t *testing.T) {
	pid, ended := hold(t, "sleep", "72")

	if sent, _ := Signal(pid, syscall.SIGKILL, "sleep", "lane-shim"); sent || ended(500*time.Millisecond) {
		t.Error("a process that holds one of two markers was signaled")
	}
}

func TestSignalNeedsAMarker(t *testing.T) {
	pid, ended := hold(t, "sleep", "73")

	if sent, skipped := Signal(pid, syscall.SIGKILL); sent || skipped == "" || ended(500*time.Millisecond) {
		t.Errorf("a process was signaled with nothing recorded: sent %v, reason %q", sent, skipped)
	}
	if sent, _ := Signal(pid, syscall.SIGKILL, ""); sent || ended(500*time.Millisecond) {
		t.Error("an empty marker matched a process")
	}
}

func TestSignalStopsAProcessWhoseCommandIsTheRecordedOne(t *testing.T) {
	pid, ended := hold(t, "sleep", "74")

	sent, skipped := Signal(pid, syscall.SIGKILL, "sleep", "74")

	if !sent || skipped != "" {
		t.Errorf("sent %v, reason %q", sent, skipped)
	}
	if !ended(5 * time.Second) {
		t.Error("the recorded process still runs")
	}
}

func TestSignalIsSilentAboutAProcessThatIsGone(t *testing.T) {
	pid, ended := hold(t, "sleep", "75")
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	ended(5 * time.Second)

	if sent, skipped := Signal(pid, syscall.SIGKILL, "sleep"); sent || skipped != "" {
		t.Errorf("sent %v, reason %q", sent, skipped)
	}
	if _, err := Command(pid); !errors.Is(err, ErrGone) {
		t.Errorf("the command of a gone process is %v", err)
	}
}

func TestSignalNeverSignalsTheInitProcess(t *testing.T) {
	if sent, _ := Signal(1, syscall.SIGKILL, "launchd", "init", "systemd"); sent {
		t.Error("process 1 was signaled")
	}
}

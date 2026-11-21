package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A step that runs past its limit is killed with what it started, and fails the run with its name and the
// end of what it wrote, so a first run that hangs names the step and does not reach the job's timeout.
func TestAStepPastItsLimitFailsWithItsNameAndTheTailOfItsOutput(t *testing.T) {
	child := filepath.Join(t.TempDir(), "child")
	script := `echo the marker on stdout; echo the marker on stderr >&2; sleep 5 & echo $! > ` + child + `; wait`
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	step := runStep(ctx, "the slow step", []string{"/bin/sh", "-c", script}, nil)
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("a step with a limit of 300ms ran %v", took)
	}
	_, err := (&run{}).must(step)
	if err == nil {
		t.Fatalf("a step past its limit passed: %+v", step)
	}
	for _, want := range []string{`"the slow step"`, "did not end within its limit", "the marker on stdout", "the marker on stderr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure lacks %q:\n%v", want, err)
		}
	}
	raw, err := os.ReadFile(child)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d, which the step started, still runs after the step was killed", pid)
		}
	}
}

// tail keeps the last lines of what a step wrote, and no more than its last bytes.
func TestTailKeepsTheEndOfTheOutput(t *testing.T) {
	var lines []string
	for i := range 100 {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	got := tail(strings.Join(lines, "\n") + "\n")
	if want := strings.Join(lines[100-tailLines:], "\n"); got != want {
		t.Errorf("tail of 100 lines:\n%s\nwant:\n%s", got, want)
	}
	if long := tail(strings.Repeat("x", 3*tailBytes)); len(long) != tailBytes {
		t.Errorf("tail of one long line has %d bytes, want %d", len(long), tailBytes)
	}
}

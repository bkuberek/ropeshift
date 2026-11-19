package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// RecordGit puts a `git` on PATH that appends each run's arguments to a log, one run a line, and runs the real one.
// It returns a function that lists the runs of one verb, as their arguments joined by spaces. The test must not run
// in parallel, because it changes PATH.
func RecordGit(t *testing.T) func(verb string) []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func(verb string) []string {
		t.Helper()
		raw, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		var runs []string
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, verb+" ") {
				runs = append(runs, line)
			}
		}
		return runs
	}
}

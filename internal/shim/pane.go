package shim

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
)

const envTerm = "TERM="

// PaneMain is `ropeshift lane-pane <file>`. It replaces itself with the session, so the
// process the pane reports is the session's, and a signal reaches it and nothing else.
// The session's environment is the one in the file, plus the pane's TERM, so it sees no
// TMUX variable and a lane cannot reach the server that holds the other lanes.
func PaneMain(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: ropeshift lane-pane <file>")
		return exitUsage
	}
	launch, err := shimlink.TakePaneLaunch(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "lane-pane: %v\n", err)
		return exitError
	}
	if err := os.Chdir(launch.Dir); err != nil {
		fmt.Fprintf(stderr, "lane-pane: %v\n", err)
		return exitError
	}
	err = syscall.Exec(launch.Path, append([]string{launch.Path}, launch.Args...), paneEnvironment(launch.Env, os.Getenv("TERM")))
	fmt.Fprintf(stderr, "lane-pane: %v\n", err)
	return exitError
}

func paneEnvironment(env []string, term string) []string {
	out := slices.DeleteFunc(slices.Clone(env), func(entry string) bool { return strings.HasPrefix(entry, envTerm) })
	if term == "" {
		return out
	}
	return append(out, envTerm+term)
}

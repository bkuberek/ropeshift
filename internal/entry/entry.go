// Package entry is the shared main of both commands (D5) and the only package
// that wires adapters to ports. A release holds one binary, and packaging links
// ropeshiftd to it, so the command is the name the process started under.
package entry

import (
	"fmt"
	"io"
	"path/filepath"
)

// Exit codes shared by every command.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const (
	commandCLI    = "ropeshift"
	commandDaemon = "ropeshiftd"
)

// Main runs one command and returns its exit code. args is the whole argument
// vector, starting with the name the process started under.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ropeshift: no program name")
		return exitUsage
	}
	command := commandName(args[0])
	rest := args[1:]
	if len(rest) == 0 {
		fmt.Fprintf(stderr, "usage: %s version [--json]\n", command)
		return exitUsage
	}
	switch rest[0] {
	case "version":
		return runVersion(command, rest[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n", command, rest[0])
		return exitUsage
	}
}

// commandName maps the started name to a command. A renamed binary acts as the
// CLI, because that is the command that can explain itself.
func commandName(arg0 string) string {
	if filepath.Base(arg0) == commandDaemon {
		return commandDaemon
	}
	return commandCLI
}

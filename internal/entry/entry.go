// Package entry is the shared main of both commands (D5) and the only package
// that wires adapters to ports. A release holds one binary, and packaging links
// ropeshiftd to it, so the command is the name the process started under.
package entry

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/hook"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/mcpserver"
	"ropeshift.dev/ropeshift/internal/shim"
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
	if rest[0] == "version" {
		return runVersion(command, rest[1:], stdout, stderr)
	}
	if command == commandDaemon {
		if rest[0] == "solo" {
			return runSolo(rest[1:], stdout, stderr)
		}
		fmt.Fprintf(stderr, "%s: unknown command %q\n", command, rest[0])
		return exitUsage
	}
	if rest[0] == "hook" {
		return hook.Main(rest[1:], os.Stdin, stdout, os.Getenv)
	}
	if rest[0] == "mcp" {
		return mcpserver.Main(os.Stdin, stdout, stderr, os.Getenv)
	}
	if rest[0] == "statusline" {
		return hook.StatusMain(rest[1:], os.Stdin, stdout, os.Getenv)
	}
	if rest[0] == "lane-pane" {
		return shim.PaneMain(rest[1:], stderr)
	}
	if rest[0] == "lane-shim" {
		return shim.Main(rest[1:], stderr)
	}
	if !cli.IsCommand(rest[0]) {
		fmt.Fprintf(stderr, "%s: unknown command %q\n", command, rest[0])
		return exitUsage
	}
	return cli.Run(rest, stdout, stderr, cliOptions())
}

// cliOptions finds the local control plane. A path that does not resolve leaves the
// socket empty, and the command reports it cannot reach anything.
func cliOptions() cli.Options {
	paths, err := installationPaths()
	if err != nil {
		return cli.Options{}
	}
	return cli.Options{
		Socket: paths.ControlSocket(),
		Doctor: func(ctx context.Context) (ledger.Manifest, error) { return runDoctor(ctx, paths) },
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

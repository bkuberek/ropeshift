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
	"strings"

	"golang.org/x/term"

	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
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
	// A missing command is a usage error, as an unknown one is, so it exits 2 with the list on
	// stderr. Asking for help is not an error, so help exits 0 with the list on stdout.
	if len(rest) == 0 {
		cli.WriteList(stderr, command, groups(command))
		return exitUsage
	}
	switch {
	case cli.AsksForHelp(rest[0]):
		rest = append([]string{"help"}, rest[1:]...)
	case rest[0] == "-version" || rest[0] == "--version":
		rest = append([]string{"version"}, rest[1:]...)
	}
	if r, ok := findRoute(command, rest[0]); ok {
		// A route answers help before it runs, because some read stdin or act on their first argument.
		if r.Name != "help" && asksForHelp(rest[1:]) {
			r.writeUsage(stdout, command)
			return exitOK
		}
		return r.run(rest[1:], stdout, stderr)
	}
	if command == commandCLI && cli.IsCommand(rest[0]) {
		return cli.Run(rest, stdout, stderr, cliOptions())
	}
	kind := "command"
	if strings.HasPrefix(rest[0], "-") {
		kind = "flag"
	}
	fmt.Fprintf(stderr, "%s: unknown %s %q\nRun '%s help' for a list of commands.\n", command, kind, rest[0], command)
	return exitUsage
}

// cliOptions finds the local control plane. A path that does not resolve leaves the
// socket empty, and the command reports it cannot reach anything.
func cliOptions() cli.Options {
	opts := cli.Options{Session: os.Getenv(authority.EnvMemberToken), ReadPassphrase: localauth.ReadPassphrase}
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		opts.Terminal = os.Stdin
	}
	if machine, err := liveServices(); err == nil {
		opts.InstallService, opts.UninstallService = machine.install, machine.uninstall
	}
	paths, err := installationPaths()
	if err != nil {
		return opts
	}
	opts.Socket = paths.ControlSocket()
	opts.Doctor = func(ctx context.Context) (ledger.Manifest, error) { return runDoctor(ctx, paths) }
	opts.StartDaemon = startDaemonForInit(paths)
	opts.StateDir = paths.StateDir
	return opts
}

// commandName maps the started name to a command. A renamed binary acts as the
// CLI, because that is the command that can explain itself.
func commandName(arg0 string) string {
	if filepath.Base(arg0) == commandDaemon {
		return commandDaemon
	}
	return commandCLI
}

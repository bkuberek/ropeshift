package entry

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/hook"
	"ropeshift.dev/ropeshift/internal/mcpserver"
	"ropeshift.dev/ropeshift/internal/shim"
)

// route is a first word one binary answers itself, with what help says about it. Every other first
// word the CLI answers is a command of internal/cli, whose registry describes it.
type route struct {
	cli.Command
	run func(args []string, stdout, stderr io.Writer) int
	// flags builds the command's flag set for its help. Nil means its help lists no flags.
	flags func() *flag.FlagSet
}

// routeGroup is the routes help prints under one heading.
type routeGroup struct {
	title  string
	routes []route
}

// routes lists what a binary answers itself, in the groups and the order help prints them. Main
// dispatches through it and help lists it, so no route runs without a line in help.
func routes(command string) []routeGroup {
	common := routeGroup{"Help and version", []route{
		{
			Command: cli.Command{Name: "help", Args: "[<command>]", Summary: "print every command, or one command's usage and flags"},
			run:     func(args []string, stdout, stderr io.Writer) int { return runHelp(command, args, stdout, stderr) },
		},
		{
			Command: cli.Command{Name: "version", Args: "[--json]", Summary: "print the version"},
			run:     func(args []string, stdout, stderr io.Writer) int { return runVersion(command, args, stdout, stderr) },
			flags:   func() *flag.FlagSet { set, _ := versionFlags(command); return set },
		},
	}}
	if command == commandDaemon {
		return []routeGroup{{"Run the daemon", []route{{
			Command: cli.Command{
				Name: "solo", Args: "[flags]", Summary: "run the control plane and this machine's worker in one process",
				Detail: "ropeshift service install -- <flags> passes the same flags to it under the service manager.",
			},
			run:   runSolo,
			flags: func() *flag.FlagSet { return soloFlags(new(soloOptions)) },
		}}}, common}
	}
	return []routeGroup{common, {"Started by an agent CLI or the worker, not by hand", []route{
		{
			Command: cli.Command{
				Name: "hook", Args: "<event>", Summary: "report one hook event to the worker",
				Detail: "An agent CLI runs this command from a lane's hooks, with the lane's token in its environment,\n" +
					"and a person does not run it by hand. It spools an event the worker cannot take, and always exits 0.",
			},
			run: func(args []string, stdout, _ io.Writer) int { return hook.Main(args, os.Stdin, stdout, os.Getenv) },
		},
		{
			Command: cli.Command{
				Name: "mcp", Summary: "serve a lane's tools to its agent CLI over stdio",
				Detail: "An agent CLI starts this command, with the lane's token in its environment, and speaks MCP\n" +
					"with it over stdin and stdout. A person does not run it by hand: from a terminal, it waits for\n" +
					"MCP messages on stdin. It reaches only the local worker.",
			},
			run: func(_ []string, stdout, stderr io.Writer) int {
				return mcpserver.Main(os.Stdin, stdout, stderr, os.Getenv)
			},
		},
		{
			Command: cli.Command{
				Name: "statusline", Args: "[--pass]", Summary: "report one status line input to the worker",
				Detail: "An interactive lane's agent CLI runs this command for its status line, and a person does not\n" +
					"run it by hand. --pass also prints the input, for a person's own status line command after it in a pipe.",
			},
			run: func(args []string, stdout, _ io.Writer) int {
				return hook.StatusMain(args, os.Stdin, stdout, os.Getenv)
			},
		},
		{
			Command: cli.Command{
				Name: "lane-pane", Args: "<file>", Summary: "replace itself with an interactive session in its pane",
				Detail: "The worker starts this command in a lane's terminal pane, and a person does not run it by hand.\n" +
					"It reads the session's environment from a private file, and removes that file first.",
			},
			run: func(args []string, _, stderr io.Writer) int { return shim.PaneMain(args, stderr) },
		},
		{
			Command: cli.Command{
				Name: "lane-shim", Args: "--dir D --socket S [--linger D] [--journal-cap N] -- <command> [args]",
				Summary: "hold a headless session's pipes in a process of its own",
				Detail: "The worker starts this command for each headless session, so the session outlives the worker,\n" +
					"and a person does not run it by hand. It journals every line, serves the journal on a private\n" +
					"socket, and writes each input at most once.",
			},
			run: func(args []string, _, stderr io.Writer) int { return shim.Main(args, stderr) },
		},
	}}}
}

// findRoute returns the route a binary answers for a first word.
func findRoute(command, word string) (route, bool) {
	for _, group := range routes(command) {
		for _, r := range group.routes {
			if r.Name == word {
				return r, true
			}
		}
	}
	return route{}, false
}

// groups is what help lists for a binary: the CLI's commands, then its own routes.
func groups(command string) []cli.Group {
	var all []cli.Group
	if command == commandCLI {
		all = cli.Groups()
	}
	for _, group := range routes(command) {
		listed := cli.Group{Title: group.title}
		for _, r := range group.routes {
			listed.Commands = append(listed.Commands, r.Command)
		}
		all = append(all, listed)
	}
	return all
}

// writeUsage prints one route's help.
func (r route) writeUsage(w io.Writer, command string) {
	var set *flag.FlagSet
	if r.flags != nil {
		set = r.flags()
	}
	cli.WriteUsage(w, command, r.Command, set)
}

// runHelp is `help`. With no command it lists every command, and with one it prints that command's
// usage and flags, or for a group's first word, the commands under it.
func runHelp(command string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		cli.WriteList(stdout, command, groups(command))
		return exitOK
	}
	if cli.AsksForHelp(args[0]) {
		args = []string{"help"}
	}
	if r, ok := findRoute(command, args[0]); ok {
		r.writeUsage(stdout, command)
		return exitOK
	}
	if command == commandCLI && cli.IsCommand(args[0]) {
		return cli.Run(append(slices.Clone(args), "--help"), stdout, stderr, cliOptions())
	}
	fmt.Fprintf(stderr, "%s help: unknown command %q\nRun '%s help' for a list of commands.\n", command, args[0], command)
	return exitUsage
}

// asksForHelp reports a help flag among a command's own arguments, before any -- that hands the rest on.
func asksForHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if cli.AsksForHelp(arg) {
			return true
		}
	}
	return false
}

package entry

import (
	"slices"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
)

// sessionRoutes start a session's own process: lane-shim runs whatever program follows its `--`, and
// lane-pane whatever session its file names. The worker starts each with no delegate credential in its
// environment, so one that holds one is a delegate's, and is refused.
var sessionRoutes = []string{"lane-shim", "lane-pane"}

// assistantShell is what an assistant's session may run of this binary: every command of the CLI but those
// that act on the machine rather than through the API, and none of the routes the binary answers itself,
// which the worker and an agent CLI start and which the assistant's rules deny by name (ADR-0024).
func assistantShell() (commands, named []string) {
	for _, command := range cli.Commands() {
		if !slices.Contains(authority.MachineLocalCommands, command) {
			commands = append(commands, command)
		}
	}
	for _, group := range routes(commandCLI) {
		for _, r := range group.routes {
			named = append(named, r.Name)
		}
	}
	return commands, named
}

// controlEnv is what an assistant's ropeshift reads to reach the control plane where this daemon listens:
// its run directory, from the daemon's own paths, which a session's environment would not lead to under a
// user service whose XDG_RUNTIME_DIR the session does not carry.
func controlEnv(paths api.Paths) []string {
	return []string{api.EnvRunDir + "=" + paths.RunDir}
}

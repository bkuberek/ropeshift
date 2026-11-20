package cli

import "ropeshift.com/ropeshift/internal/api"

// workerAcceptSetup accepts a piece of the member's own agent CLI setup that leaked into a worker's lanes, for that
// worker and CLI version, so the leak is not raised again there. Needs you names the version, the kind, and the name.
func (c *invocation) workerAcceptSetup(args []string) int {
	set := c.flags()
	version := set.String("version", "", "the agent CLI version the lanes ran")
	kind := set.String("kind", "", "the piece's kind: mcp_server, plugin, agent, skill, or command")
	name := set.String("name", "", "the piece's name")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("this takes one worker")
	}
	if *version == "" || *kind == "" || *name == "" {
		return c.usageError("--version, --kind, and --name are required: the Needs you item names each")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	in := api.AcceptPersonalSetup{Version: *version, Kind: *kind, Name: *name}
	err := client.AcceptPersonalSetup(c.ctx, positional[0], in)
	return c.finish(err, in, func() string {
		return "accepted the " + *kind + " " + *name + " for the lanes of " + positional[0] + " on " + *version + "\n"
	})
}

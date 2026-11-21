package cli

import (
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
)

// laneSettings chooses how a lane runs that its worktree's own settings held: with the repository's settings, with the
// lane's own, or not at all. Needs you names the work order, the lane, and the digest of the change, which the choice
// binds to.
func (c *invocation) laneSettings(args []string) int {
	set := c.flags()
	lane := set.String("lane", "", "the lane held on its own settings")
	digest := set.String("digest", "", "the digest of the change, as Needs you names it")
	run := set.String("run", "", "base, lane, or stop")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if *lane == "" || *digest == "" || !slices.Contains(lanes.SettingsChoices, *run) {
		return c.usageError("--lane, --digest, and --run " + strings.Join(lanes.SettingsChoices, "|") + " are required: the Needs you item names each")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	decided, err := client.DecideLaneSettings(c.ctx, id, api.DecideLaneSettings{Lane: *lane, Digest: *digest, Choice: *run})
	return c.finish(err, decided, func() string {
		switch decided.Choice {
		case lanes.SettingsBase:
			return "the lane of " + decided.WorkOrder + " runs with the repository's settings, which its next generation restores\n"
		case lanes.SettingsLane:
			return "the lane of " + decided.WorkOrder + " runs with its own settings, as your decision\n"
		}
		return "stopped the lane of " + decided.WorkOrder + ", which is " + decided.State + "\n"
	})
}

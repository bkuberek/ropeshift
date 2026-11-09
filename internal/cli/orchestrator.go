package cli

import (
	"fmt"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/ledger"
)

func orchestratorLine(row ledger.Orchestrator) string {
	actual := row.Actual
	if actual == "" {
		actual = "no status yet"
	}
	return fmt.Sprintf("%s on %s: asked to be %s, reads %s, generation %d\n", row.ID, row.WorkerID, row.Desired, actual, row.Generation)
}

// workerArgument reads a command's one worker.
func (c *invocation) workerArgument(args []string) (string, bool) {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return "", false
	}
	if len(positional) != 1 {
		c.usageError("this takes one worker")
		return "", false
	}
	return positional[0], true
}

func (c *invocation) orchestratorShow(args []string) int {
	worker, ok := c.workerArgument(args)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	row, err := client.Orchestrator(c.ctx, worker)
	return c.finish(err, row, func() string { return orchestratorLine(row) })
}

// setsOrchestrator is the command of one desired state. Starting one is the only way an agent comes
// to run with no lane dispatched, so this takes a member's session like any other change.
func setsOrchestrator(desired string) func(*invocation, []string) int {
	return func(c *invocation, args []string) int {
		worker, ok := c.workerArgument(args)
		if !ok {
			return ExitUsage
		}
		client := c.client()
		defer client.Close()
		row, err := client.SetOrchestrator(c.ctx, worker, api.SetOrchestrator{Desired: desired})
		return c.finish(err, row, func() string { return orchestratorLine(row) })
	}
}

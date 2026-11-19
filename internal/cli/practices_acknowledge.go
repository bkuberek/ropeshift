package cli

import (
	"fmt"

	"ropeshift.com/ropeshift/internal/api"
)

// practicesAcknowledge records why a repository leaves a practices finding open. The finding stays in every view of the
// result, listed apart from the open ones with the reason. A delegate's call prepares a card a member confirms.
func (c *invocation) practicesAcknowledge(args []string) int {
	set := c.flags()
	reason := set.String("reason", "", "why the finding stays open: at least a character, and at most 2 KiB")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 2 {
		return c.usageError("this takes one repository and one finding ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	ack, err := client.AcknowledgePractices(c.ctx, api.PracticesAcknowledge{Repository: positional[0], Finding: positional[1], Reason: *reason})
	return c.finish(err, ack, func() string {
		return fmt.Sprintf("Acknowledged %s in %s: %s\n", practicesLine(ack.Finding), practicesLine(positional[0]), practicesLine(ack.Reason))
	})
}

// practicesWithdraw removes the acknowledgement of a finding, so it is listed among the open ones again.
func (c *invocation) practicesWithdraw(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 2 {
		return c.usageError("this takes one repository and one finding ID")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	ack, err := client.WithdrawPractices(c.ctx, api.PracticesWithdraw{Repository: positional[0], Finding: positional[1]})
	return c.finish(err, ack, func() string {
		return fmt.Sprintf("Withdrew the acknowledgement of %s in %s.\n", practicesLine(ack.Finding), practicesLine(positional[0]))
	})
}

package cli

import (
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/api"
)

// workCIWait gives a head whose CI has not finished more time before CI asks again: the time named, or one more p85 of
// the repository's CI.
func (c *invocation) workCIWait(args []string) int {
	set := c.flags()
	var by time.Duration
	set.DurationVar(&by, "by", 0, "the time to add to the CI timeout, such as 45m; one more p85 of the repository's CI when left out")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if by < 0 {
		return c.usageError("--by is a time to wait, such as 45m")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	request := api.CIWait{}
	if by > 0 {
		request.By = by.String()
	}
	result, err := client.CIWait(c.ctx, id, request)
	return c.finish(err, result, func() string {
		return fmt.Sprintf("%s\twaits until %s of CI\n", id, time.Duration(result.Watch.TimeoutMs)*time.Millisecond)
	})
}

// workCIRerun records a rerun of the checks of a head whose CI has not finished or failed for a reason no agent can
// fix.
func (c *invocation) workCIRerun(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	result, err := client.CIRerun(c.ctx, id)
	return c.finish(err, result, func() string { return fmt.Sprintf("%s\t%d reruns of this head\n", id, result.Reruns) })
}

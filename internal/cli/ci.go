package cli

import (
	"fmt"
	"strings"
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

// measuresCI prints how long each repository's CI takes: the p50 and p85 of its recent heads' push-to-completed time
// and queue time, or "no history" with how many heads it has and how many it needs.
func (c *invocation) measuresCI(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	view, err := client.CIDurations(c.ctx)
	return c.finish(err, view, func() string { return ciDurationsText(view) })
}

func ciDurationsText(view api.CIDurations) string {
	var out strings.Builder
	span := func(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }
	for _, row := range view.Repositories {
		if !row.History {
			fmt.Fprintf(&out, "%s\tno history\t%d of %d heads\texpects p50 %s p85 %s\n", row.Name, row.Records, row.MinRecords, span(row.NoHistoryP50Ms), span(row.NoHistoryP85Ms))
			continue
		}
		fmt.Fprintf(&out, "%s\t%d heads\tp50 %s\tp85 %s\tqueue p50 %s\tqueue p85 %s\n",
			row.Name, row.Records, span(row.P50Ms), span(row.P85Ms), span(row.QueueP50Ms), span(row.QueueP85Ms))
	}
	return out.String()
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

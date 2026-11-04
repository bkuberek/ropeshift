package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
)

// telemetryQuery reads the worker's local store, which an orchestrator agent can read with --json.
func (c *invocation) telemetryQuery(args []string) int {
	set := c.flags()
	var query api.TelemetryQuery
	var since time.Duration
	set.StringVar(&query.Signal, "signal", "", "span, metric, or log")
	set.StringVar(&query.Name, "name", "", "one span, metric, or event name")
	set.StringVar(&query.Lane, "lane", "", "one lane")
	set.DurationVar(&since, "since", 0, "only what was observed within this long, such as 1h")
	set.IntVar(&query.Limit, "limit", 0, "the most records to print, 100 by default")
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if since < 0 || query.Limit < 0 {
		return c.usageError("--since and --limit are not negative")
	}
	if since > 0 {
		query.Since = time.Now().Add(-since).UnixMilli()
	}
	client := c.client()
	defer client.Close()
	out, err := client.TelemetryRecords(c.ctx, query)
	return c.finish(err, out, func() string {
		var b strings.Builder
		if !out.Available {
			b.WriteString("this control plane reads no telemetry store\n")
		}
		for _, record := range out.Records {
			value := ""
			if record.Value != nil {
				value = fmt.Sprintf("%g", *record.Value)
			}
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", time.UnixMilli(record.ObservedAt).UTC().Format(time.RFC3339), record.Signal, record.Name, record.Lane, value)
		}
		return b.String()
	})
}

// telemetryLanes prints each lane's context, tokens, and cost from the local store.
func (c *invocation) telemetryLanes(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	out, err := client.LaneTelemetry(c.ctx)
	return c.finish(err, out, func() string {
		var b strings.Builder
		if !out.Available {
			b.WriteString("this control plane reads no telemetry store\n")
		}
		for _, lane := range out.Lanes {
			var tokens []string
			for _, kind := range slices.Sorted(maps.Keys(lane.Tokens)) {
				tokens = append(tokens, fmt.Sprintf("%s %d", kind, lane.Tokens[kind]))
			}
			fmt.Fprintf(&b, "%s\tcontext %d\t%s\tcost at least $%.4f\n", lane.Lane, lane.ContextTokens, strings.Join(tokens, ", "), lane.CostUSD)
		}
		return b.String()
	})
}

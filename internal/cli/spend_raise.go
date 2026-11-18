package cli

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// limitChangeText says what a raise or a resume did, with the limit before and after (FR-SPEND-8). A resume also says
// how much it let one more generation spend, and what that figure was taken from.
func limitChangeText(change ledger.SpendLimitChange) string {
	text := fmt.Sprintf("work order %s\n%s\thard limit %s -> %s, charged %s\n", change.WorkOrder, change.Act, dollars(change.BeforeUSD), dollars(change.AfterUSD), dollars(change.ChargedUSD))
	if change.AllowanceUSD > 0 {
		text += fmt.Sprintf("\tone more generation: %s, %s\n", dollars(change.AllowanceUSD), allowanceBasisText(change.AllowanceBasis))
	}
	return text
}

// allowanceBasisText says what a resume sized its one more generation by.
func allowanceBasisText(basis string) string {
	switch basis {
	case ledger.AllowanceMeanGeneration:
		return "the mean charge of the generations that recorded spend"
	case ledger.AllowanceEstimate:
		return "the work order's estimate"
	}
	return basis
}

// projectLimitChangeText says what a raise of the project's limit did, with the limit before and after (FR-SPEND-8).
func projectLimitChangeText(change ledger.ProjectLimitChange) string {
	name := "daily"
	if change.Window == ledger.WindowWeek {
		name = "weekly"
	}
	return fmt.Sprintf("project %s\nraise\t%s limit %s -> %s, spent %s\n", change.Window, name, dollars(change.BeforeUSD), dollars(change.AfterUSD), dollars(change.SpentUSD))
}

// spendRaise lifts a work order's hard limit by a percentage of it, or to an amount, and wakes the lane the limit
// parked. With --window it lifts the project's daily or weekly limit for the day or week it is made in instead, and wakes
// the lanes the project's limits parked. Only a member raises a limit: an orchestrator holds no such tool, and a
// delegate's call is a card.
func (c *invocation) spendRaise(args []string) int {
	set := c.flags()
	by := set.Float64("by", 0, "raise the hard limit by this percentage of it, such as 50")
	to := set.Float64("to", 0, "raise the hard limit to this amount in US dollars")
	window := set.String("window", "", "raise the project's daily or weekly limit, `day` or `week`, in place of a work order's")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if (*by > 0) == (*to > 0) || *by < 0 || *to < 0 {
		return c.usageError("name one of --by, a percentage above 0, and --to, an amount above 0")
	}
	if *window != "" {
		return c.spendRaiseProject(*window, *by, *to, positional)
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
	change, err := client.RaiseSpend(c.ctx, id, api.RaiseSpend{ByPercent: *by, ToUSD: *to})
	return c.finish(err, change, func() string { return limitChangeText(change) })
}

// spendRaiseProject lifts the project's daily or weekly limit. A work order beside it is a mistake, because the limit it
// would lift is one or the other.
func (c *invocation) spendRaiseProject(window string, by, to float64, positional []string) int {
	if !ledger.ValidWindow(window) {
		return c.usageError("--window names day or week, and not %q", window)
	}
	if len(positional) != 0 {
		return c.usageError("--window raises the project's limit, so it takes no work order, and %q is one", positional[0])
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	change, err := client.RaiseProjectSpend(c.ctx, api.RaiseProjectSpend{Window: window, ByPercent: by, ToUSD: to})
	return c.finish(err, change, func() string { return projectLimitChangeText(change) })
}

// spendStopEndedText says what blocking a spend stop or sending it to replanning did.
func spendStopEndedText(ended api.SpendStopEnded) string {
	return fmt.Sprintf("work order %s\n%s\treason %s\n", ended.WorkOrder, ended.State, ended.Reason)
}

// spendBlock ends a spend stop by blocking its work order with the reason `spend_limit`, so it waits for a member to
// resolve or replan it. Only a member does: a delegate's call is a card.
func (c *invocation) spendBlock(args []string) int {
	return c.endSpendStop(args, (*api.ProjectClient).BlockSpend)
}

// spendReplan ends a spend stop by sending its work order to planning, where its contract is reviewed again.
func (c *invocation) spendReplan(args []string) int {
	return c.endSpendStop(args, (*api.ProjectClient).ReplanSpend)
}

func (c *invocation) endSpendStop(args []string, end func(*api.ProjectClient, context.Context, string) (api.SpendStopEnded, error)) int {
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
	ended, err := end(client, c.ctx, id)
	return c.finish(err, ended, func() string { return spendStopEndedText(ended) })
}

// spendResume grants a work order one more generation's allowance, which lets the lane its hard limit parked go on.
func (c *invocation) spendResume(args []string) int {
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
	change, err := client.ResumeSpend(c.ctx, id)
	return c.finish(err, change, func() string { return limitChangeText(change) })
}

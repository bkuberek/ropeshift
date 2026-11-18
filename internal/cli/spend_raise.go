package cli

import (
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

// spendRaise lifts a work order's hard limit by a percentage of it, or to an amount, and wakes the lane the limit
// parked. Only a member raises a limit: an orchestrator holds no such tool, and a delegate's call is a card.
func (c *invocation) spendRaise(args []string) int {
	set := c.flags()
	by := set.Float64("by", 0, "raise the hard limit by this percentage of it, such as 50")
	to := set.Float64("to", 0, "raise the hard limit to this amount in US dollars")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if (*by > 0) == (*to > 0) || *by < 0 || *to < 0 {
		return c.usageError("name one of --by, a percentage above 0, and --to, an amount above 0")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	change, err := client.RaiseSpend(c.ctx, id, api.RaiseSpend{ByPercent: *by, ToUSD: *to})
	return c.finish(err, change, func() string { return limitChangeText(change) })
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

package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/measures"
)

// measuresCommand prints the product measures of one window, the north star first and its
// counterweights beside it (FR-OBS-7).
func (c *invocation) measuresCommand(args []string) int {
	set := c.flags()
	window := set.String("window", api.DefaultMeasureWindow, "how far back, such as 24h or 30d")
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if _, err := api.ParseWindow(*window); err != nil {
		return c.usageError("%v", err)
	}
	client := c.client()
	defer client.Close()
	report, err := client.Measures(c.ctx, *window)
	return c.finish(err, report, func() string { return measuresText(report) })
}

func rateText(r measures.Ratio, asShare bool) string {
	if r.Value == nil {
		return fmt.Sprintf("no data (%d of %d)", r.Numerator, r.Denominator)
	}
	if asShare {
		return fmt.Sprintf("%.1f%% (%d of %d)", *r.Value*100, r.Numerator, r.Denominator)
	}
	return fmt.Sprintf("%.2f (%d over %d)", *r.Value, r.Numerator, r.Denominator)
}

func durationsText(d measures.Durations) string {
	if d.MedianMs == nil || d.P95Ms == nil {
		return "no data"
	}
	return fmt.Sprintf("median %s, 95th percentile %s, of %d", time.Duration(*d.MedianMs)*time.Millisecond, time.Duration(*d.P95Ms)*time.Millisecond, d.Count)
}

func measuresText(r measures.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "over the last %s\n", r.Window.Name)
	fmt.Fprintf(&b, "interruptions per completed work order\t%s\n", rateText(r.InterruptionsPerCompleted, false))
	fmt.Fprintf(&b, "overturn rate\t%s\n", rateText(r.OverturnRate, true))
	fmt.Fprintf(&b, "rework\t%s\n", rateText(r.Rework, true))
	fmt.Fprintf(&b, "autonomous decision rate\t%s\n", rateText(r.AutonomousDecisionRate, true))
	fmt.Fprintf(&b, "repeated-question rate\t%s\n", rateText(r.RepeatedQuestionRate, true))
	fmt.Fprintf(&b, "interruptions\t%d: %d batches, %d approvals, %d forced\n", r.Interruptions,
		r.InterruptionsByKind[measures.InterruptionBatch], r.InterruptionsByKind[measures.InterruptionApproval], r.InterruptionsByKind[measures.InterruptionForced])
	fmt.Fprintf(&b, "repeats prevented\t%d\n", r.PreventedRepeats)
	fmt.Fprintf(&b, "work in progress\t%d\n", r.WorkInProgress)
	fmt.Fprintf(&b, "flow time, ready to done\t%s\n", durationsText(r.ReadyToDone))
	fmt.Fprintf(&b, "flow time, intake to done\t%s\n", durationsText(r.IntakeToDone))
	for _, trigger := range slices.Sorted(maps.Keys(r.ParkTime)) {
		fmt.Fprintf(&b, "park time, %s\t%s\n", trigger, durationsText(r.ParkTime[trigger]))
	}
	for _, kind := range slices.Sorted(maps.Keys(r.CostPerOutcome)) {
		cost := r.CostPerOutcome[kind]
		each := "no data"
		if cost.PerDone != nil {
			each = fmt.Sprintf("$%.4f each", *cost.PerDone)
			if cost.LowerBound {
				each = "at least " + each
			}
		}
		fmt.Fprintf(&b, "cost per outcome, %s\t%s, $%.4f over %d done\n", kind, each, cost.TotalUSD, cost.Done)
	}
	return b.String()
}

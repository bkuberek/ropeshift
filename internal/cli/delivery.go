package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
)

// workTimeline prints where one work order's time went, and, once it ended, its delivery record.
func (c *invocation) workTimeline(args []string) int {
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
	view, err := client.WorkTimeline(c.ctx, id)
	return c.finish(err, view, func() string { return timelineText(id, view) })
}

// span says a stretch of milliseconds as a person reads it, to the second.
func span(ms int64) string { return (time.Duration(ms) * time.Millisecond).Round(time.Second).String() }

func timelineText(id string, view api.WorkTimeline) string {
	var out strings.Builder
	t := view.Timeline
	state := string(t.Terminal)
	if state == "" {
		state = "under way"
	}
	fmt.Fprintf(&out, "%s\t%s\t%s\n", id, state, span(t.End-t.Start))
	for _, s := range t.Segments {
		fmt.Fprintf(&out, "segment\t%s\t%s\t%s\tround %d\t%s\t%s\n", s.Kind, s.State, orDash(s.Cause), s.Round, span(s.To-s.From), orDash(s.Lane))
	}
	if r := view.Record; r != nil {
		fmt.Fprintf(&out, "elapsed\t%s\ndelivery\t%s\nlane\t%s\nperson\t%s\n", span(r.ElapsedMs), span(r.DeliveryMs), span(r.LaneMs), span(r.PersonMs))
		bound := ""
		if r.Usage.CostLowerBound {
			bound = "at least "
		}
		fmt.Fprintf(&out, "cost\t%s$%.2f, estimated, at list price\ntokens\t%d in\t%d out\n", bound, r.Usage.CostUSD, r.Usage.InputTokens, r.Usage.OutputTokens)
	}
	return out.String()
}

// estimateText prints a work order's current estimate: its label, its time and cost bands, and under them the
// explanation it was made with (FR-EST-14). An open band reads as more than its longest record, and a cost
// band with a lower-bound cost as at least. Work with no estimate prints nothing.
func estimateText(e *delivery.Estimate) string {
	if e == nil {
		return ""
	}
	if e.Status == delivery.NoHistory {
		return "estimate\tno history\n\t" + e.Explanation + "\n"
	}
	open := ""
	if e.P85Open {
		open = "more than "
	}
	bound := ""
	if e.CostAtLeast {
		bound = "at least "
	}
	return fmt.Sprintf("estimate\t%s\tdelivery p50 %s p85 %s%s\tcost %sp50 $%.2f p85 %s$%.2f\n\t%s\n",
		e.Confidence, span(e.DeliveryP50Ms), open, span(e.DeliveryP85Ms), bound, e.CostP50USD, open, e.CostP85USD, e.Explanation)
}

func orDash(text string) string {
	if text == "" {
		return "-"
	}
	return text
}

// measuresDelivery prints look back over a window, by a grouping (FR-EST-5).
func (c *invocation) measuresDelivery(args []string) int {
	set := c.flags()
	window := set.String("window", api.DefaultDeliveryWindow, "how far back, such as 24h or 30d")
	groupings := append(slices.Clone(api.DeliveryGroupings), delivery.ByProject)
	by := set.String("by", delivery.ByKind, "group by "+strings.Join(groupings, ", "))
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if _, err := api.ParseWindow(*window); err != nil {
		return c.usageError("%v", err)
	}
	if !slices.Contains(groupings, *by) {
		return c.usageError("--by is one of %s", strings.Join(groupings, ", "))
	}
	if *by == delivery.ByProject {
		report, err := c.deliveryByProject(*window)
		return c.finish(err, report, func() string { return lookBackText(report) })
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	report, err := client.Delivery(c.ctx, *window, *by)
	return c.finish(err, report, func() string { return lookBackText(report) })
}

// deliveryByProject reads every active project's look back apart, each project one group, through the API
// as the installation's other views read every project.
func (c *invocation) deliveryByProject(window string) (delivery.LookBack, error) {
	client := c.client()
	defer client.Close()
	projects, err := client.Projects(c.ctx)
	if err != nil {
		return delivery.LookBack{}, err
	}
	out := delivery.LookBack{By: delivery.ByProject, Groups: []delivery.Group{}}
	for _, project := range projects {
		if project.State != ledger.ProjectActive {
			continue
		}
		report, err := client.Project(project.Name).Delivery(c.ctx, window, delivery.ByKind)
		if err != nil {
			return delivery.LookBack{}, err
		}
		out.From, out.To = report.From, report.To
		group := report.Groups[0]
		group.Key = project.Name
		out.Groups = append(out.Groups, group)
	}
	return out, nil
}

func quantilesText(q *delivery.Quantiles) string {
	if q == nil {
		return "no data"
	}
	return fmt.Sprintf("p50 %s p85 %s", span(q.P50), span(q.P85))
}

func lookBackText(report delivery.LookBack) string {
	var out strings.Builder
	for _, g := range report.Groups {
		label := "all"
		if g.Key != "" || len(out.String()) > 0 {
			label = report.By + "\t" + orDash(g.Key)
		}
		cost := "no data"
		if g.Cost != nil {
			bound := ""
			if g.CostLowerBound {
				bound = "at least "
			}
			cost = fmt.Sprintf("%sp50 $%.2f p85 $%.2f", bound, g.Cost.P50, g.Cost.P85)
		}
		fmt.Fprintf(&out, "%s\tdone %d\tfailed %d\tcanceled %d\tper week %.1f\telapsed %s\tdelivery %s\tcost %s\n",
			label, g.Done, g.Failed, g.Canceled, g.PerWeek, quantilesText(g.Elapsed), quantilesText(g.Delivery), cost)
		if len(g.Share) > 0 {
			kinds := make([]string, 0, len(g.Share))
			for kind := range g.Share {
				kinds = append(kinds, string(kind))
			}
			slices.Sort(kinds)
			parts := make([]string, 0, len(kinds))
			for _, kind := range kinds {
				parts = append(parts, fmt.Sprintf("%s %.1f%%", kind, g.Share[delivery.Kind(kind)]*100))
			}
			fmt.Fprintf(&out, "share\t%s\t%s\n", strings.ReplaceAll(label, "\t", " "), strings.Join(parts, "\t"))
		}
	}
	if len(report.Groups) > 0 && report.Groups[0].Key == "" {
		for _, w := range report.Groups[0].Trend {
			fmt.Fprintf(&out, "week\t%s\tdone %d\tdelivery %s\n", time.UnixMilli(w.From).UTC().Format(time.DateOnly), w.Done, quantilesText(w.Delivery))
		}
	}
	return out.String()
}

package cli

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
)

// laneOutline prints the plan a planning lane has committed to its branch, which the web UI's planning pane
// shows beside the session.
func (c *invocation) laneOutline(args []string) int {
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
	outline, err := client.Outline(c.ctx, id)
	return c.finish(err, outline, func() string { return outlineText(outline) })
}

// outlineText is one line for each thing the branch holds, led by what it is, with tabs between the parts.
func outlineText(outline api.PlanOutline) string {
	if outline.Unread != "" {
		return fmt.Sprintf("unread\t%s\n", outline.Unread)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "commit\t%s\n", outline.Commit)
	for _, document := range outline.Documents {
		fmt.Fprintf(&text, "%s\t%s\t%s\n", document.Status, document.Path, document.Kind)
	}
	for _, milestone := range outline.Milestones {
		fmt.Fprintf(&text, "milestone\t%s\t%d\t%s\n", milestone.ID, milestone.Priority, milestone.Title)
	}
	for _, contract := range outline.Contracts {
		fmt.Fprintf(&text, "contract\t%s\t%s\t%s\t%s\n", contract.ID, plural(contract.Criteria, "criterion", "criteria"), plural(contract.WorkOrders, "work order", "work orders"), contract.Title)
	}
	for _, decision := range outline.Decisions {
		fmt.Fprintf(&text, "decision\t%s\t%s\n", decision.ID, decision.Question)
	}
	for _, question := range outline.Questions {
		blocking := "open"
		if question.Blocking {
			blocking = "blocking"
		}
		fmt.Fprintf(&text, "question\t%s\t%s\t%s\n", question.ID, blocking, question.Text)
	}
	switch {
	case outline.Approval != nil:
		fmt.Fprintf(&text, "approval\t%s\t%s\tversion %d\n", outline.Approval.ID, outline.Approval.State, outline.Approval.Version)
	case outline.NotSubmitted != "":
		fmt.Fprintf(&text, "not submitted\t%s\n", outline.NotSubmitted)
	}
	return text.String()
}

func plural(count int, one, many string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", count, many)
}

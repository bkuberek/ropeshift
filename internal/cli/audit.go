package cli

import (
	"fmt"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
)

// audit prints a log's events, newest first: the project's, or the installation's with --installation.
// The web UI's Audit reads the same route, with the same filters.
func (c *invocation) audit(args []string) int {
	set, installation := c.agentFlags()
	actor := set.String("actor", "", "only events by this kind of actor: member, worker, lane, orchestrator, or system")
	kind := set.String("kind", "", "only events whose type starts with this, such as project. or work_order.accepted")
	subject := set.String("subject", "", "only events about this kind of record, such as work_order or project")
	before := set.Int64("before", 0, "start below this revision, which a page's next names")
	limit := set.Int("limit", api.AuditDefaultLimit, fmt.Sprintf("how many events, at most %d", api.AuditMaxLimit))
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	scope, code := c.readScope(*installation)
	if code != ExitOK {
		return code
	}
	defer scope.close()
	query := api.AuditQuery{Actor: *actor, Kind: *kind, Subject: *subject, Before: *before, Limit: *limit}
	var page api.AuditPage
	var err error
	if scope.installation {
		page, err = scope.client.InstallationAudit(c.ctx, query)
	} else {
		page, err = scope.project.Audit(c.ctx, query)
	}
	return c.finish(err, page, func() string { return auditText(page) })
}

func auditText(page api.AuditPage) string {
	var b strings.Builder
	if len(page.Events) == 0 {
		b.WriteString("no event matches\n")
	}
	for _, event := range page.Events {
		at := time.UnixMilli(event.RecordedAt).UTC().Format(time.RFC3339)
		fmt.Fprintf(&b, "%d\t%s\t%s %s\t%s\t%s %s\n", event.Rev, at, event.ActorKind, event.ActorID, event.Type, event.SubjectKind, event.SubjectID)
	}
	switch {
	case page.Scanned:
		fmt.Fprintf(&b, "stopped after %d events with older ones unread; go on with --before %d\n", api.AuditScanLimit, page.Next)
	case page.Next > 0:
		fmt.Fprintf(&b, "older events: --before %d\n", page.Next)
	}
	return b.String()
}

// memberList prints each member that holds a live session, with when each session began and ends.
func (c *invocation) memberList(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	client := c.client()
	defer client.Close()
	list, err := client.Members(c.ctx)
	return c.finish(err, list, func() string {
		var b strings.Builder
		fmt.Fprintf(&b, "%d enrolled\n", list.Enrolled)
		for _, member := range list.Members {
			fmt.Fprintf(&b, "%s\t%d live sessions\n", member.ID, len(member.Sessions))
			for _, session := range member.Sessions {
				fmt.Fprintf(&b, "    since %s, until %s\n", time.UnixMilli(session.CreatedAt).UTC().Format(time.RFC3339), time.UnixMilli(session.ExpiresAt).UTC().Format(time.RFC3339))
			}
		}
		return b.String()
	})
}

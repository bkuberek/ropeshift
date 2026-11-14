package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
)

// shownOnce is what a new credential's answer says of its token.
const shownOnce = "This credential is shown once. Store it now; ropeshift keeps only its digest."

// parseLife reads how long a credential lasts: a Go duration such as 8h or 90m, or a number of days such
// as 7d.
func parseLife(text string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(text, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q is no number of days", text)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(text)
}

func moment(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

// delegateCreate makes a delegate credential for an agent outside ropeshift, bound to one project, and
// prints it once with the line a shell evaluates.
func (c *invocation) delegateCreate(args []string) int {
	set := c.flags()
	project := set.String("project", "", "the active project the delegate acts in, by name")
	label := set.String("label", "", "what the delegate is, 1 to 64 characters")
	expires := set.String("expires", "", "how long it lasts: a duration such as 8h, or days such as 7d, from 1m to 30d")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if *project == "" || *label == "" || *expires == "" {
		return c.usageError("--project, --label, and --expires are required")
	}
	life, err := parseLife(*expires)
	if err != nil {
		return c.usageError("--expires: %v", err)
	}
	client := c.client()
	defer client.Close()
	created, err := client.CreateDelegate(c.ctx, api.CreateDelegate{Project: *project, Label: *label, ExpiresInMs: life.Milliseconds()})
	return c.finish(err, created, func() string {
		return fmt.Sprintf("delegate %s (%s) acts in project %s until %s\n%s\nexport %s=%s\n",
			created.ID, plain(created.Label), created.Project, moment(created.ExpiresAt), shownOnce, authority.EnvDelegateToken, created.Token)
	})
}

// delegateList lists the calling member's delegate credentials, oldest first, each with its state.
func (c *invocation) delegateList(args []string) int {
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
	list, err := client.Delegates(c.ctx)
	return c.finish(err, list, func() string {
		var b strings.Builder
		for _, d := range list.Delegates {
			used := "never used"
			if d.LastUsedAt != nil {
				used = "last used " + moment(*d.LastUsedAt)
			}
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\tuntil %s\t%s\n", d.ID, d.State, d.Project, plain(d.Label), moment(d.ExpiresAt), used)
		}
		return b.String()
	})
}

// delegateRevoke ends a delegate credential for good.
func (c *invocation) delegateRevoke(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	revoked, err := client.RevokeDelegate(c.ctx, id)
	return c.finish(err, revoked, func() string { return fmt.Sprintf("revoked %s (%s)\n", revoked.ID, plain(revoked.Label)) })
}

// delegateRemove deletes the record of a credential that no longer acts.
func (c *invocation) delegateRemove(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	err := client.RemoveDelegate(c.ctx, id)
	return c.finish(err, struct {
		Removed string `json:"removed"`
	}{id}, func() string { return fmt.Sprintf("removed %s\n", id) })
}

func (c *invocation) confirm(args []string) int { return c.decideCard(args, true) }

func (c *invocation) decline(args []string) int { return c.decideCard(args, false) }

// decideCard reads a card's digest and decides it, so a card whose subject moved since it was read is void
// rather than run. A confirmation runs the card's call as this member.
func (c *invocation) decideCard(args []string, allow bool) int {
	set := c.flags()
	var reason *string
	if !allow {
		reason = set.String("reason", "", "why, which the card keeps")
	}
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
	card, err := client.Approval(c.ctx, id)
	if err != nil {
		return c.failure(err)
	}
	if card.Kind != authority.KindConfirm {
		return c.usageError("%s is a %s request, not a card: decide it with ropeshift approval approve or deny", id, card.Kind)
	}
	decision := api.DecideApproval{Digest: card.SubjectDigest, Allow: allow}
	if reason != nil {
		decision.Reason = *reason
	}
	decided, err := client.DecideApproval(c.ctx, id, decision)
	if err == nil && decided.State == authority.ApprovalFailed {
		c.finish(nil, decided, func() string { return fmt.Sprintf("failed %s: %s: %s\n", decided.ID, decided.Summary, decided.Reason) })
		return ExitError
	}
	return c.finish(err, decided, func() string {
		return fmt.Sprintf("%s %s: %s\n", cardOutcome(decided.State), decided.ID, decided.Summary)
	})
}

// cardOutcome says what became of a card in the words Needs you uses.
func cardOutcome(state authority.ApprovalState) string {
	switch state {
	case authority.ApprovalUsed:
		return "confirmed"
	case authority.ApprovalDenied:
		return "declined"
	case authority.ApprovalApproved:
		return "running"
	case authority.ApprovalPending:
		return "open"
	}
	return string(state)
}

// operationList prints the class table: what each operation is, and what a delegate's call to it does.
func (c *invocation) operationList(args []string) int {
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
	list, err := client.Operations(c.ctx)
	return c.finish(err, list, func() string {
		var b strings.Builder
		for _, op := range list.Operations {
			exempt := "-"
			switch {
			case op.Exemptable:
				exempt = "exemptable"
			case op.NeverReason != "":
				exempt = "never exempt"
			}
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", op.ID, op.Class, exempt, op.Label)
		}
		return b.String()
	})
}

// prepared explains a call that became a card: it ran nothing, and a member's confirmation runs it.
func (c *invocation) prepared(card authority.Approval) int {
	if c.asJSON {
		c.finish(nil, card, nil)
		return ExitPrepared
	}
	fmt.Fprintf(c.stdout, "Prepared card %s: %s. It runs only when a member confirms it in Needs you or with ropeshift confirm %s.\n", card.ID, card.Summary, card.ID)
	return ExitPrepared
}

package cli

import (
	"fmt"
	"strings"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/authority"
)

// approvalList lists requests, the oldest first, because the oldest has waited longest.
func (c *invocation) approvalList(args []string) int {
	set := c.flags()
	state := set.String("state", "", "only requests in this state")
	if _, ok := c.parse(set, args); !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	rows, err := client.Approvals(c.ctx, authority.ApprovalState(*state))
	return c.finish(err, api.ApprovalList{Approvals: rows}, func() string {
		var out strings.Builder
		for _, row := range rows {
			fmt.Fprintf(&out, "%s  %s  %s  %s\n", row.ID, row.State, row.Lane, row.Summary)
		}
		return out.String()
	})
}

// approvalShow prints what a member decides on, with the digest a decision names.
func (c *invocation) approvalShow(args []string) int {
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
	row, err := client.Approval(c.ctx, id)
	return c.finish(err, row, func() string {
		return fmt.Sprintf("%s\nstate: %s\nlane: %s\nrequested by: %s\ndigest: %s\n", row.Summary, row.State, row.Lane, row.RequestedBy, row.SubjectDigest)
	})
}

func (c *invocation) approvalApprove(args []string) int { return c.approvalDecide(args, true) }

func (c *invocation) approvalDeny(args []string) int { return c.approvalDecide(args, false) }

// approvalDecide names the digest a member read, so a request that is not what they read is refused.
func (c *invocation) approvalDecide(args []string, allow bool) int {
	set := c.flags()
	digest := set.String("digest", "", "the digest `approval show` printed")
	reason := set.String("reason", "", "why, which a lane reads")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if *digest == "" {
		return c.usageError("--digest is required: it says what you read")
	}
	client := c.client()
	defer client.Close()
	row, err := client.DecideApproval(c.ctx, id, api.DecideApproval{Digest: *digest, Allow: allow, Reason: *reason})
	return c.finish(err, row, func() string { return fmt.Sprintf("%s is %s\n", row.ID, row.State) })
}

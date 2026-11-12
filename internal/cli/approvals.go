package cli

import (
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
)

// approvalList lists requests, the oldest first, because the oldest has waited longest.
func (c *invocation) approvalList(args []string) int {
	set := c.flags()
	state := set.String("state", "", "only requests in this state")
	if _, ok := c.parse(set, args); !ok {
		return ExitUsage
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
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
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.Approval(c.ctx, id)
	return c.finish(err, row, func() string {
		var out strings.Builder
		fmt.Fprintf(&out, "%s\nstate: %s\n%s\nrequested by: %s\ndigest: %s\n", row.Summary, row.State, approvalSubject(row), row.RequestedBy, row.SubjectDigest)
		if adoption := api.PlanDetailOf(row).Adoption; adoption != nil {
			writeAdoption(&out, *adoption, "")
		}
		return out.String()
	})
}

// approvalSubject says what a request is about: the plan version a plan approval covers, which no lane
// asked for, the recovery act an orchestrator asked for, with why and the limit of the policy it went past,
// or the lane that asked.
func approvalSubject(row authority.Approval) string {
	if row.Kind == authority.KindRecovery {
		detail := authority.RecoveryDetailOf(row)
		subject := fmt.Sprintf("recovery: %s of work order %s", row.Subject, row.WorkOrder)
		if row.Lane != "" {
			subject += ", lane " + row.Lane
		}
		return fmt.Sprintf("%s\nreason: %s\noutside recovery policy version %d: %s: %s", subject, detail.Reason, detail.PolicyVersion, detail.Check, detail.Limit)
	}
	if row.Kind != authority.KindPlan {
		return "lane: " + row.Lane
	}
	plan := row.Plan
	if title := api.PlanDetailOf(row).Title; title != "" {
		plan = fmt.Sprintf("%s (%s)", title, row.Plan)
	}
	return fmt.Sprintf("plan: %s, version %d", plan, row.PlanVersion)
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
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	row, err := client.DecideApproval(c.ctx, id, api.DecideApproval{Digest: *digest, Allow: allow, Reason: *reason})
	return c.finish(err, row, func() string { return fmt.Sprintf("%s is %s\n", row.ID, row.State) })
}

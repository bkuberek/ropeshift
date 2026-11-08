package cli

import (
	"context"
	"fmt"
	"strings"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/planning"
)

func (c *invocation) planList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	plans, err := client.Plans(c.ctx)
	if plans == nil {
		plans = []planning.Plan{}
	}
	return c.finish(err, api.PlanList{Plans: plans}, func() string {
		var out strings.Builder
		for _, plan := range plans {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", plan.ID, plan.Status, plan.Title)
		}
		return out.String()
	})
}

func (c *invocation) planShow(args []string) int {
	set := c.flags()
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one plan ID")
	}
	client := c.client()
	defer client.Close()
	view, err := client.Plan(c.ctx, positional[0])
	return c.finish(err, view, func() string {
		var out strings.Builder
		fmt.Fprintf(&out, "%s\t%s\t%s\n", view.Plan.ID, view.Plan.Status, view.Plan.Title)
		if view.Goal != nil {
			fmt.Fprintf(&out, "goal\t%s\t%s\n", view.Goal.ID, view.Goal.Title)
		}
		for _, contract := range view.Contracts {
			fmt.Fprintf(&out, "contract\t%s@%d\t%s\n", contract.ID, contract.Version, contract.Title)
		}
		for _, order := range view.WorkOrders {
			out.WriteString("work\t" + orderLine(order))
		}
		return out.String()
	})
}

// planReview starts a contract review as a lane, which is ready for a member to claim. Work orders
// the review reads stay where they are until a member accepts it.
func (c *invocation) planReview(args []string) int {
	set := c.flags()
	id := set.String("id", "", "the review's ID, for a retry")
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one plan ID")
	}
	review := *id
	if review == "" {
		generated, err := newID()
		if err != nil {
			return c.failure(err)
		}
		review = generated
	}
	client := c.client()
	defer client.Close()
	row, err := client.ReviewPlan(c.ctx, positional[0], api.ReviewPlan{ID: review})
	return c.finish(err, row, func() string { return orderLine(row) })
}

func (c *invocation) requirementList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	requirements, err := client.Requirements(c.ctx)
	if requirements == nil {
		requirements = []planning.Requirement{}
	}
	return c.finish(err, api.RequirementList{Requirements: requirements}, func() string {
		var out strings.Builder
		for _, requirement := range requirements {
			fmt.Fprintf(&out, "%s\tv%d\t%s\n", requirement.Key, requirement.Version, requirement.Text)
		}
		return out.String()
	})
}

func (c *invocation) requirementGet(args []string) int {
	set := c.flags()
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("takes one requirement key")
	}
	client := c.client()
	defer client.Close()
	requirement, err := client.Requirement(c.ctx, positional[0])
	return c.finish(err, requirement, func() string {
		return fmt.Sprintf("%s\tv%d\t%s\n", requirement.Key, requirement.Version, requirement.Text)
	})
}

func (c *invocation) workResolve(args []string) int {
	return c.unblock(args, (*api.Client).Resolve, "ready")
}

func (c *invocation) workReplan(args []string) int {
	return c.unblock(args, (*api.Client).Replan, "planning")
}

func (c *invocation) unblock(args []string, move func(*api.Client, context.Context, string, api.Unblock) error, to string) int {
	set := c.flags()
	var request api.Unblock
	set.StringVar(&request.Reason, "reason", "", "what changed")
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if request.Reason == "" {
		return c.usageError("--reason is required")
	}
	client := c.client()
	defer client.Close()
	err := move(client, c.ctx, id, request)
	return c.finish(err, struct {
		Moved string `json:"moved"`
		State string `json:"state"`
	}{id, to}, func() string { return id + "\t" + to + "\n" })
}

func (c *invocation) workSendBack(args []string) int {
	set := c.flags()
	var request api.SendBack
	set.StringVar(&request.Reason, "reason", "", "why the contract must change")
	positional, ok := parse(set, args)
	if !ok {
		return ExitUsage
	}
	id, ok := c.oneID(positional)
	if !ok {
		return ExitUsage
	}
	if request.Reason == "" {
		return c.usageError("--reason is required")
	}
	client := c.client()
	defer client.Close()
	err := client.SendBack(c.ctx, id, request)
	return c.finish(err, struct {
		SentBack string `json:"sent_back"`
	}{id}, func() string { return id + "\tplanning\n" })
}

func (c *invocation) documentList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	documents, err := client.Documents(c.ctx)
	if documents == nil {
		documents = []api.Document{}
	}
	return c.finish(err, api.DocumentList{Documents: documents}, func() string {
		var out strings.Builder
		for _, document := range documents {
			fmt.Fprintf(&out, "%s\t%s\t%s\n", document.Path, document.Digest, document.Title)
		}
		return out.String()
	})
}

// documentRef reads the arguments one document command shares.
func (c *invocation) documentRef(args []string) (api.DocumentRef, bool) {
	set := c.flags()
	var ref api.DocumentRef
	set.StringVar(&ref.Instance, "instance", "", "the repository that holds it, by its first commit")
	set.StringVar(&ref.Version, "version", "", "a version from `document versions`")
	positional, ok := parse(set, args)
	if !ok {
		return ref, false
	}
	if len(positional) != 1 {
		c.usageError("takes one document path")
		return ref, false
	}
	ref.Path = positional[0]
	return ref, true
}

func (c *invocation) documentShow(args []string) int {
	ref, ok := c.documentRef(args)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	document, err := client.Document(c.ctx, ref)
	return c.finish(err, document, func() string { return document.Body })
}

func (c *invocation) documentVersions(args []string) int {
	ref, ok := c.documentRef(args)
	if !ok {
		return ExitUsage
	}
	client := c.client()
	defer client.Close()
	versions, err := client.DocumentVersions(c.ctx, ref)
	return c.finish(err, api.DocumentVersions{Versions: versions}, func() string {
		var out strings.Builder
		for _, version := range versions {
			fmt.Fprintf(&out, "%s\t%s\n", version.Version, version.Digest)
		}
		return out.String()
	})
}

package cli

import (
	"flag"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
)

// shortRepository is enough of an identity to read and to name the repository again, because every
// command takes a prefix of seven or more characters.
func shortRepository(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func dashIfEmpty(text string) string {
	if text == "" {
		return "-"
	}
	return text
}

func repositoryLine(view api.RepositoryView) string {
	return fmt.Sprintf("%s\tbase %s\tmerge %s\tstart %s\treview %s\tauthority %s\t%s\n",
		shortRepository(view.ID), dashIfEmpty(view.Base), view.MergeMethod, view.StartFrom, view.Target(), view.Level(), dashIfEmpty(view.Remote))
}

func repositoryText(view api.RepositoryView) string {
	var out strings.Builder
	fmt.Fprintf(&out, "repository\t%s\n", view.ID)
	fmt.Fprintf(&out, "remote\t%s\n", dashIfEmpty(view.Remote))
	fmt.Fprintf(&out, "default branch\t%s\n", dashIfEmpty(view.DefaultBranch))
	if view.BaseBranch == "" {
		fmt.Fprintf(&out, "base\t%s\t(the default branch)\n", dashIfEmpty(view.Base))
	} else {
		fmt.Fprintf(&out, "base\t%s\n", view.Base)
	}
	fmt.Fprintf(&out, "forge binding\t%s\n", dashIfEmpty(view.ForgeBinding))
	fmt.Fprintf(&out, "merge method\t%s\n", view.MergeMethod)
	fmt.Fprintf(&out, "review target\t%s\n", view.Target())
	fmt.Fprintf(&out, "start from\t%s\n", view.StartFrom)
	fmt.Fprintf(&out, "merge authority\t%s\n", view.Level())
	fmt.Fprintf(&out, "review agreement, last 30 days\t%s\n", agreementText(view.ReviewAgreement.Last30Days))
	fmt.Fprintf(&out, "review agreement, all time\t%s\n", agreementText(view.ReviewAgreement.AllTime))
	return out.String()
}

// agreementText says how a person's decisions stood against the review agent's verdicts. It names each
// figure in words, because a rate alone does not say which way the person disagreed.
func agreementText(a measures.Agreement) string {
	if a.Decisions == 0 {
		return "no decisions after a review yet"
	}
	agreed := "nothing to compare, because the review could not judge"
	if a.AgreementRate.Value != nil {
		agreed = rateText(a.AgreementRate, true)
	}
	return fmt.Sprintf("%s after a review; you agreed with %s; approvals you overturned %d (later defects %d); failures you overrode %d; the review could not judge %d",
		api.Count(a.Decisions, "decision", "decisions"), agreed, a.ApprovalsOverturned, a.LaterDefects, a.FailuresOverridden, a.Unknown)
}

func (c *invocation) repositoryList(args []string) int {
	client, ok, code := c.noArguments(args)
	if !ok {
		return code
	}
	defer client.Close()
	list, err := client.Repositories(c.ctx)
	if list.Repositories == nil {
		list.Repositories = []api.RepositoryView{}
	}
	return c.finish(err, list, func() string {
		var out strings.Builder
		for _, view := range list.Repositories {
			out.WriteString(repositoryLine(view))
		}
		return out.String()
	})
}

func (c *invocation) repositoryShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("this takes one repository")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	view, err := client.Repository(c.ctx, positional[0])
	return c.finish(err, view, func() string { return repositoryText(view) })
}

// repositorySet changes what a member names and leaves the rest. A flag given with an empty value is
// a change too: an empty --base follows the default branch again.
func (c *invocation) repositorySet(args []string) int {
	set := c.flags()
	base := set.String("base", "", "the branch lanes start from, or empty to follow the default branch")
	method := set.String("merge-method", "", "how a pull request lands: merge, squash, or rebase")
	start := set.String("start-from", "", "what dependent work starts from: merged or accepted")
	target := set.String("review-target", "", "where accepted work is reviewed and lands: branch or pull_request")
	authority := set.String("merge-authority", "", "how much of each change you decide after the review: person_merges, person_approves, or review_approves")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("this takes one repository")
	}
	var change api.RepositoryChange
	given := map[string]bool{}
	set.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if given["base"] {
		change.Base = base
	}
	if given["merge-method"] {
		value := ledger.MergeMethod(*method)
		change.MergeMethod = &value
	}
	if given["start-from"] {
		value := ledger.StartFrom(*start)
		change.StartFrom = &value
	}
	if given["review-target"] {
		value := ledger.ReviewTarget(*target)
		change.ReviewTarget = &value
	}
	if given["merge-authority"] {
		value := ledger.MergeAuthority(*authority)
		change.MergeAuthority = &value
	}
	if len(given) == 0 {
		return c.usageError("name a setting to change: --base, --merge-method, --start-from, --review-target, or --merge-authority")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	view, err := client.SetRepository(c.ctx, positional[0], change)
	return c.finish(err, view, func() string { return repositoryText(view) })
}

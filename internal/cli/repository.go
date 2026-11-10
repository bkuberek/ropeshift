package cli

import (
	"flag"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
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

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func repositoryLine(view api.RepositoryView) string {
	return fmt.Sprintf("%s\tbase %s\tmerge %s\tstart %s\tintegration %s\tauthority %s\t%s\n",
		shortRepository(view.ID), dashIfEmpty(view.Base), view.MergeMethod, view.StartFrom, onOff(view.Integration), view.Level(), dashIfEmpty(view.Remote))
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
	fmt.Fprintf(&out, "integration\t%s\n", onOff(view.Integration))
	fmt.Fprintf(&out, "start from\t%s\n", view.StartFrom)
	fmt.Fprintf(&out, "merge authority\t%s\n", view.Level())
	return out.String()
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
	client := c.client()
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
	integration := set.String("integration", "", "on or off: whether ropeshift pushes accepted work and opens its pull request")
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
	if given["integration"] {
		switch *integration {
		case "on", "off":
			value := *integration == "on"
			change.Integration = &value
		default:
			return c.usageError("--integration takes on or off, not %q", *integration)
		}
	}
	if given["merge-authority"] {
		value := ledger.MergeAuthority(*authority)
		change.MergeAuthority = &value
	}
	if len(given) == 0 {
		return c.usageError("name a setting to change: --base, --merge-method, --start-from, --integration, or --merge-authority")
	}
	client := c.client()
	defer client.Close()
	view, err := client.SetRepository(c.ctx, positional[0], change)
	return c.finish(err, view, func() string { return repositoryText(view) })
}

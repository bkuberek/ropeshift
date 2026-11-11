package cli

import (
	"cmp"
	"flag"
	"fmt"
	"path/filepath"
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
	name := dashIfEmpty(view.Name)
	if view.Removed() {
		name += " (removed)"
	}
	if view.Planning {
		name += " (planning)"
	}
	return fmt.Sprintf("%s\t%s\tbase %s\tmerge %s\tstart %s\treview %s\tauthority %s\t%s\n",
		name, shortRepository(view.ID), dashIfEmpty(view.Base), view.MergeMethod, view.StartFrom, view.Target(), view.Level(), dashIfEmpty(view.Remote))
}

func repositoryText(view api.RepositoryView) string {
	var out strings.Builder
	fmt.Fprintf(&out, "repository\t%s\n", view.ID)
	fmt.Fprintf(&out, "name\t%s\n", dashIfEmpty(view.Name))
	fmt.Fprintf(&out, "state\t%s\n", cmp.Or(view.State, ledger.RepositoryActive))
	fmt.Fprintf(&out, "planning repository\t%s\n", planningText(view))
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

// planningText says whether the project plans in this repository, and why.
func planningText(view api.RepositoryView) string {
	switch {
	case view.Planning:
		return "yes"
	case view.PlanOfRecord:
		return "yes\t(the project's only repository)"
	}
	return "no"
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
	name := set.String("name", "", "what commands and a work order's repo call the repository, unique in its project")
	base := set.String("base", "", "the branch lanes start from, or empty to follow the default branch")
	method := set.String("merge-method", "", "how a pull request lands: merge, squash, or rebase")
	start := set.String("start-from", "", "what dependent work starts from: merged or accepted")
	target := set.String("review-target", "", "where accepted work is reviewed and lands: branch or pull_request")
	authority := set.String("merge-authority", "", "how much of each change you decide after the review: person_merges, person_approves, or review_approves")
	planning := set.Bool("planning", false, "make this the project's planning repository, which intake reads plan files from; --planning=false stops it")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) == 0 {
		return c.usageError("name a repository to change")
	}
	var change api.RepositoryChange
	given := map[string]bool{}
	set.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if given["name"] {
		if len(positional) > 1 {
			return c.usageError("--name renames one repository, and this names %d", len(positional))
		}
		change.Name = name
	}
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
	if given["planning"] {
		if *planning && len(positional) > 1 {
			return c.usageError("a project has one planning repository, and this names %d", len(positional))
		}
		change.Planning = planning
	}
	if len(given) == 0 {
		return c.usageError("name a setting to change: --name, --base, --merge-method, --start-from, --review-target, --merge-authority, or --planning")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	if len(positional) == 1 {
		view, err := client.SetRepository(c.ctx, positional[0], change)
		return c.finish(err, view, func() string { return repositoryText(view) })
	}
	return c.setEach(client, positional, change)
}

// setEach applies one change to several repositories, one event each (FR-REPO-12). Every name must
// name a repository of the project before any changes, and a change one repository refuses leaves the
// others changed and says which refused.
func (c *invocation) setEach(client *api.ProjectClient, names []string, change api.RepositoryChange) int {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		view, err := client.Repository(c.ctx, name)
		if err != nil {
			return c.failure(err)
		}
		ids = append(ids, view.ID)
	}
	list := api.RepositoryList{Repositories: []api.RepositoryView{}}
	code := ExitOK
	for _, id := range ids {
		view, err := client.SetRepository(c.ctx, id, change)
		if err != nil {
			if failed := c.failure(err); code == ExitOK {
				code = failed
			}
			continue
		}
		list.Repositories = append(list.Repositories, view)
	}
	printed := c.finish(nil, list, func() string {
		texts := make([]string, 0, len(list.Repositories))
		for _, view := range list.Repositories {
			texts = append(texts, repositoryText(view))
		}
		return strings.Join(texts, "\n")
	})
	return cmp.Or(code, printed)
}

// repositoryAdd registers, in the project it resolves, the git clones at or below paths on this machine,
// through the daemon whose worker runs here (ADR-0022). It exits 4 when anything was refused, after the
// rest were registered.
func (c *invocation) repositoryAdd(args []string) int {
	set := c.flags()
	depth := set.Int("depth", 1, "how many levels below a path that is not a clone to look for clones")
	dryRun := set.Bool("dry-run", false, "say what would be registered and refused, and record nothing")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) == 0 {
		return c.usageError("name a path to register: a clone, or a folder of clones")
	}
	if *depth < 1 {
		return c.usageError("--depth takes 1 or more, not %d", *depth)
	}
	paths := make([]string, 0, len(positional))
	for _, path := range positional {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return c.failure(err)
		}
		paths = append(paths, absolute)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	report, err := client.AddRepositories(c.ctx, api.RepositoryAdd{Paths: paths, Depth: *depth, DryRun: *dryRun})
	if report.Repositories == nil {
		report.Repositories = []api.RepositoryAdded{}
	}
	code := c.finish(err, report, func() string { return addedText(report) })
	if code == ExitOK && report.Refused() > 0 {
		return ExitConflict
	}
	return code
}

// addedText prints one line for each clone and each path: what became of it, the repository's name and
// identity, and where it is.
func addedText(report api.RepositoryAddReport) string {
	var out strings.Builder
	for _, added := range report.Repositories {
		if added.Outcome == ledger.RegistrationRefused {
			fmt.Fprintf(&out, "refused\t%s\t%s\n", added.Path, added.Reason)
			continue
		}
		var name, id string
		if added.Repository != nil {
			name, id = added.Repository.Name, shortRepository(added.Repository.ID)
		}
		word := "registered"
		switch {
		case added.Outcome == ledger.AlreadyRegistered:
			word = "already registered"
		case report.DryRun:
			word = "would register"
		}
		fmt.Fprintf(&out, "%s\t%s\t%s\t%s", word, name, id, added.Path)
		if added.Checkout != "" && added.Checkout != added.Path {
			fmt.Fprintf(&out, "\t(%s; this worker keeps %s)", dashIfEmpty(added.Reason), added.Checkout)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// repositoryRemove takes a repository out of its project once no open work targets it (ADR-0021). Its
// record stays as history, and another project may then register it.
func (c *invocation) repositoryRemove(args []string) int {
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
	view, err := client.RemoveRepository(c.ctx, positional[0])
	return c.finish(err, view, func() string { return repositoryText(view) })
}

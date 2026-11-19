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
	out.WriteString(forgeText(view))
	fmt.Fprintf(&out, "start from\t%s\n", view.StartFrom)
	fmt.Fprintf(&out, "merge authority\t%s\n", view.Level())
	for _, key := range ledger.CIKeys {
		fmt.Fprintf(&out, "ci.%s\t%s\n", key, view.CIEffective.Text(key))
	}
	fmt.Fprintf(&out, "review agreement, last 30 days\t%s\n", agreementText(view.ReviewAgreement.Last30Days))
	fmt.Fprintf(&out, "review agreement, all time\t%s\n", agreementText(view.ReviewAgreement.AllTime))
	return out.String()
}

// forgeText says what each worker that keeps a checkout of the repository last found of its `gh` sign-in for
// it, one line each, with a line after it for a worker whose checkout is a workspace that does not ignore its folder
// for lane worktrees, and that none has spoken yet where the review target needs the forge.
func forgeText(view api.RepositoryView) string {
	var out strings.Builder
	for _, health := range view.Forge {
		line := fmt.Sprintf("forge\t%s\t%s", cmp.Or(health.WorkerName, health.Worker), health.State)
		if health.Detail != "" {
			line += "\t" + strings.Join(strings.Fields(health.Detail), " ")
		}
		out.WriteString(line + "\n")
		if health.UnignoredWorkspace != "" {
			fmt.Fprintf(&out, "workspace\t%s\t%s\t%s\n", cmp.Or(health.WorkerName, health.Worker), health.UnignoredWorkspace, ledger.UnignoredWorkspaceNote)
		}
	}
	if len(view.Forge) == 0 && view.Target() == ledger.ReviewOnPullRequest {
		out.WriteString("forge\t-\tno worker has reported gh for this repository yet\n")
	}
	return out.String()
}

// forgeWarning is what a member hears when a change leaves a repository reviewing through pull requests and no
// worker has said its `gh` works there. The control plane cannot see a worker's `gh` when the setting changes,
// so the change stands, and the work that needs the forge waits with the reason until a worker reports one.
func forgeWarning(view api.RepositoryView) string {
	if view.Target() != ledger.ReviewOnPullRequest || view.ForgeReady() {
		return ""
	}
	return fmt.Sprintf("ropeshift repository set: warning: no worker has reported a working gh sign-in for %s yet. A review waits as forge_unavailable or pull_request_pending until one does; repository show says what each worker found\n",
		cmp.Or(view.ForgeBinding, cmp.Or(view.Name, view.ID)))
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

// splitCIEdits takes the `ci.<key>=<value>` words out of a command's arguments, and says which word is no such
// setting, such as one with no value. The rest are the repositories to change.
func splitCIEdits(words []string) (rest []string, edits map[string]string, bad string) {
	for _, word := range words {
		setting, isCI := strings.CutPrefix(word, "ci.")
		if !isCI {
			rest = append(rest, word)
			continue
		}
		key, value, found := strings.Cut(setting, "=")
		if !found || key == "" {
			return nil, nil, word
		}
		if edits == nil {
			edits = map[string]string{}
		}
		edits[key] = value
	}
	return rest, edits, ""
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
	binding := set.String("forge-binding", "", "the repository on its forge as HOST/OWNER/REPO, which pull_request needs; empty takes it from the remote")
	authority := set.String("merge-authority", "", "how much of each change you decide after the review: person_merges, person_approves, or review_approves")
	planning := set.Bool("planning", false, "make this the project's planning repository, which intake reads plan files from; --planning=false stops it")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	positional, edits, bad := splitCIEdits(positional)
	if bad != "" {
		return c.usageError("%s is no CI setting: write ci.<key>=<value>, such as ci.ci_steady_interval=2m", bad)
	}
	if len(positional) == 0 {
		return c.usageError("name a repository to change")
	}
	var change api.RepositoryChange
	change.CI = edits
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
	if given["forge-binding"] {
		change.ForgeBinding = binding
	}
	if given["planning"] {
		if *planning && len(positional) > 1 {
			return c.usageError("a project has one planning repository, and this names %d", len(positional))
		}
		change.Planning = planning
	}
	if len(given) == 0 && len(edits) == 0 {
		return c.usageError("name a setting to change: --name, --base, --merge-method, --start-from, --review-target, --forge-binding, --merge-authority, --planning, or ci.<key>=<value>")
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	if len(positional) == 1 {
		view, err := client.SetRepository(c.ctx, positional[0], change)
		if err == nil && (change.ReviewTarget != nil || change.ForgeBinding != nil) {
			fmt.Fprint(c.stderr, forgeWarning(view))
		}
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
		if change.ReviewTarget != nil || change.ForgeBinding != nil {
			fmt.Fprint(c.stderr, forgeWarning(view))
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
	manifest := set.String("manifest", "", "register what a repository manifest lists, from the clones beside the planning repository's")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if *manifest != "" {
		if len(positional) != 0 {
			return c.usageError("name paths or --manifest, not both")
		}
		return c.addFromManifest(*manifest, *dryRun)
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

// addFromManifest registers what a repository manifest on this machine lists, as plan approval does for an
// approved one (ADR-0022): each repository from the clone at its path beside the planning repository's
// clone on this worker's machine. A repository with no clone there is refused with why, and exits 4.
func (c *invocation) addFromManifest(path string, dryRun bool) int {
	text, err := c.readLocal(path)
	if err != nil {
		return c.failure(err)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	report, err := client.AddRepositories(c.ctx, api.RepositoryAdd{Manifest: string(text), DryRun: dryRun})
	if report.Repositories == nil {
		report.Repositories = []api.RepositoryAdded{}
	}
	code := c.finish(err, report, func() string { return manifestText(report.Repositories, report.DryRun, "") })
	if code == ExitOK && report.Refused() > 0 {
		return ExitConflict
	}
	return code
}

// manifestText prints one line for each repository a manifest lists: what became of it, or would, its name
// and the path of its clone as the manifest writes them, where that path leads on the worker's machine, the
// remote the manifest names, and why it was refused, each line after an indent.
func manifestText(added []api.RepositoryAdded, wouldBe bool, indent string) string {
	return registrationText(added, wouldBe, indent, "")
}

// plannedSuffix says what a version's approval does for a repository it plans, in a preview.
const plannedSuffix = ", so approving creates nothing; the approval of a version that gives it work creates it"

// registrationText is manifestText with, under each repository a plan version creates, a line for each thing its creation
// did or would do, and for a creation that failed, the commands left to run and the command that retries it for the plan
// named. Every word a worker or a forge said passes through plainLine.
func registrationText(added []api.RepositoryAdded, wouldBe bool, indent, plan string) string {
	var out strings.Builder
	for _, one := range added {
		word := "registered"
		switch {
		case one.Outcome == ledger.RegistrationRefused:
			word = "refused"
		case one.Outcome == ledger.AlreadyRegistered:
			word = "already registered"
		case one.Outcome == ledger.NotAttempted:
			word = "not attempted"
		case one.Outcome == ledger.Planned:
			word = "planned"
		case one.Outcome == ledger.Skipped:
			word = "skipped"
		case one.Outcome == ledger.CreateFailed:
			word = "create failed"
		case one.Outcome == ledger.Created && wouldBe:
			word = "would create"
		case one.Outcome == ledger.Created:
			word = "created"
		case wouldBe:
			word = "would register"
		}
		remote := one.Remote
		if remote == "" && one.Creation != nil {
			remote = one.Creation.URL
		}
		fmt.Fprintf(&out, "%s%s\t%s\t%s\t%s\t%s", indent, word, dashIfEmpty(plainLine(one.Name)), dashIfEmpty(plainPath(one.ManifestPath)),
			dashIfEmpty(plainPath(one.Path)), dashIfEmpty(plainPath(remote)))
		reason := plainLine(one.Reason)
		if one.Outcome == ledger.Planned && wouldBe && reason != "" {
			reason += plannedSuffix
		}
		if reason != "" {
			fmt.Fprintf(&out, "\t%s", reason)
		}
		out.WriteString("\n")
		for _, line := range creationLines(one, wouldBe, plan) {
			fmt.Fprintf(&out, "%s  %s\n", indent, line)
		}
	}
	return out.String()
}

// creationLines says what one repository's creation did, or would: what it creates or created, how far it came, what is
// left to run, the line to add to the workspace Makefile, and how to retry it.
func creationLines(one api.RepositoryAdded, wouldBe bool, plan string) []string {
	creation := one.Creation
	if creation == nil {
		return nil
	}
	var lines []string
	made := forgeRepository(creation)
	switch {
	case one.Outcome == ledger.Created && wouldBe:
		line := "creates " + made
		if creation.Login != "" {
			line += ", with gh signed in as " + plainLine(creation.Login)
		}
		lines = append(lines, line)
	case one.Outcome == ledger.Created:
		line := "created " + made
		if progress := creationProgress(creation); progress != "" {
			line += ": " + progress
		}
		lines = append(lines, line)
	case one.Outcome == ledger.CreateFailed:
		if progress := creationProgress(creation); progress != "" {
			lines = append(lines, progress)
		}
	}
	if one.Outcome == ledger.RegistrationRefused && creation.Remedy != "" && !strings.Contains(one.Reason, creation.Remedy) {
		lines = append(lines, "remedy: "+plainLine(creation.Remedy))
	}
	if one.Outcome == ledger.CreateFailed {
		for _, command := range creation.Remaining {
			lines = append(lines, "remaining: "+plainLine(command))
		}
		if plan != "" {
			lines = append(lines, fmt.Sprintf("retry with: ropeshift plan retry %s %s", plainLine(plan), plainLine(one.Name)))
		}
	}
	if creation.Makefile != "" && one.Outcome == ledger.Created && !wouldBe {
		lines = append(lines, "add to REPOSITORIES in the workspace Makefile: "+plainLine(creation.Makefile))
	}
	return lines
}

// forgeRepository names the repository a creation makes on its forge, with its visibility and the branch it starts on.
func forgeRepository(creation *ledger.CreationOutcome) string {
	host := creation.Forge
	if host == ledger.ForgeGitHub {
		host = "github.com"
	}
	text := fmt.Sprintf("%s/%s/%s, %s", plainLine(host), plainLine(creation.Owner), plainLine(creation.ForgeName), plainLine(creation.Visibility))
	if creation.DefaultBranch != "" {
		text += ", on branch " + plainLine(creation.DefaultBranch)
	}
	return text
}

// creationProgress says how far a creation came: its first commit, and each step with how it ended and what the tool said
// of one that failed.
func creationProgress(creation *ledger.CreationOutcome) string {
	var parts []string
	switch {
	case creation.CommitError != "":
		parts = append(parts, "first commit failed: "+plainLine(creation.CommitError))
	case creation.Commit != "":
		parts = append(parts, "first commit "+plainLine(creationCommit(creation.Commit)))
	}
	for _, step := range creation.Steps {
		text := plainLine(step.Step) + " " + plainLine(step.Outcome)
		if step.Detail != "" && step.Outcome == "failed" {
			text += ": " + plainLine(step.Detail)
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, ", ")
}

// creationCommit is the first seven characters of a commit's ID.
func creationCommit(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// addedText prints one line for each clone and each path: what became of it, the repository's name and
// identity, and where it is.
func addedText(report api.RepositoryAddReport) string {
	var out strings.Builder
	for _, added := range report.Repositories {
		if added.Outcome == ledger.RegistrationRefused {
			fmt.Fprintf(&out, "refused\t%s\t%s\n", plainPath(added.Path), plainLine(added.Reason))
			continue
		}
		var name, id string
		if added.Repository != nil {
			name, id = plainLine(added.Repository.Name), shortRepository(added.Repository.ID)
		}
		word := "registered"
		switch {
		case added.Outcome == ledger.AlreadyRegistered:
			word = "already registered"
		case report.DryRun:
			word = "would register"
		}
		fmt.Fprintf(&out, "%s\t%s\t%s\t%s", word, name, id, plainPath(added.Path))
		switch {
		case added.Checkout != "" && added.Checkout != added.Path:
			fmt.Fprintf(&out, "\t(%s; this worker keeps %s)", dashIfEmpty(plainLine(added.Reason)), plainPath(added.Checkout))
		case added.Reason != "":
			fmt.Fprintf(&out, "\t(%s)", plainLine(added.Reason))
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

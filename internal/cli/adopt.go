package cli

import (
	"bufio"
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
)

// adoptCriterion is the one criterion an adopt session's work order holds.
const adoptCriterion = "The project's existing documents are onboarded into the repository file format with their prose and IDs kept, " +
	"and the plan of what remains is written to the repository as files, with the person in this session."

// adoptStatement is what an adopt session's work order says it is for.
func adoptStatement(project string) string {
	return fmt.Sprintf("Adopt the existing project %s: onboard the documents it already holds into the repository file format, "+
		"mark what the code already does with its evidence, continue the in-flight branches the person keeps, and plan what remains.", project)
}

// adopt brings an existing project into ropeshift from the repository it runs in (ADR-0023). It makes the calls
// a member makes by hand, each one an audited member action: it finds or creates the project, registers the
// repository and every clone under the given paths, settles the planning repository, and starts the adopt
// session, a planning session whose lane onboards what the project holds, then attaches the terminal.
func (c *invocation) adopt(args []string) int {
	set := c.flags()
	planningName := set.String("planning", "", "the repository that holds the plan, for a project of several repositories")
	worker := set.String("worker", "", "the worker that runs the session, by default the one worker online")
	noAttach := set.Bool("no-attach", false, "start the session and leave your terminal alone")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	root, identity, code := c.repositoryRunIn()
	if code != ExitOK {
		return code
	}
	paths := []string{root}
	for _, path := range positional {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return c.failure(err)
		}
		paths = append(paths, absolute)
	}
	client := c.client()
	defer client.Close()
	name, code := c.adoptingProject(client, root, identity)
	if code != ExitOK {
		return code
	}
	project := client.Project(name)
	if code := c.register(project, root, paths); code != ExitOK {
		return code
	}
	repository, code := c.adoptedPlanningRepository(project, *planningName)
	if code != ExitOK {
		return code
	}
	chosenWorker, code := c.chooseWorker(project, *worker)
	if code != ExitOK {
		return code
	}
	started, code := c.openSession(project, sessionDraft{
		title: "Adopt " + name, statement: adoptStatement(name), criterion: adoptCriterion, adopt: true,
		repository: repository, worker: chosenWorker,
	})
	if code != ExitOK {
		return code
	}
	started.Project = name
	return c.joinSession(project, started, *noAttach)
}

// repositoryRunIn is the top of the repository the command runs in and its identity, as the repository index
// names it. Adoption starts from a repository with a commit, so anywhere else is a usage error.
func (c *invocation) repositoryRunIn() (root, identity string, code int) {
	if c.repositoryRoot != nil {
		found, err := c.repositoryRoot(c.ctx)
		if err != nil {
			return "", "", c.failure(err)
		}
		root = found
	}
	if c.repositoryHere != nil && root != "" {
		found, err := c.repositoryHere(c.ctx)
		if err != nil {
			return "", "", c.failure(err)
		}
		identity = found
	}
	if root == "" || identity == "" {
		return "", "", c.usageError("run adopt in a git repository with a commit, the one the project's work lands in")
	}
	return root, identity, ExitOK
}

// adoptingProject is the project that adopts the repository: the one that holds it, or else the one --project
// or ROPESHIFT_PROJECT names, or else a project named after the repository's directory. A project it names
// that does not exist yet is created. A project that already has an approved plan is refused, because it plans
// its next work with a planning session.
func (c *invocation) adoptingProject(client *api.Client, root, identity string) (string, int) {
	projects, err := client.Projects(c.ctx)
	if err != nil {
		return "", c.failure(err)
	}
	named := cmp.Or(c.project, c.envProject)
	for _, p := range projects {
		if !slices.Contains(p.Repositories, identity) {
			continue
		}
		if named != "" && named != p.Name {
			return "", c.refuse(ExitConflict, "project %s holds this repository; adopt it there, with --project %s or none", p.Name, p.Name)
		}
		return p.Name, c.refusePlanned(client.Project(p.Name), p.Name)
	}
	if named == "" {
		named = projectNameOf(filepath.Base(root))
		if named == "" {
			return "", c.usageError("the directory %q makes no project name; name the project with --project", filepath.Base(root))
		}
	}
	if slices.ContainsFunc(projects, func(p api.Project) bool { return p.Name == named }) {
		return named, c.refusePlanned(client.Project(named), named)
	}
	created, err := client.CreateProject(c.ctx, api.CreateProject{Name: named})
	if err != nil {
		return "", c.failure(err)
	}
	c.say("Created project %s.\n", created.Name)
	return created.Name, ExitOK
}

// refusePlanned refuses a project with an approved plan before anything is registered (FR-ADOPT-10). The
// control plane refuses the session's work as well.
func (c *invocation) refusePlanned(client *api.ProjectClient, name string) int {
	approved, err := client.Approvals(c.ctx, authority.ApprovalApproved)
	if err != nil {
		return c.failure(err)
	}
	if slices.ContainsFunc(approved, func(a authority.Approval) bool { return a.Kind == authority.KindPlan }) {
		return c.refuse(ExitConflict, "project %s already has an approved plan, so there is nothing to adopt; plan its next work with '%s plan start --project %s'", name, program, name)
	}
	return ExitOK
}

// projectNameOf makes a project name of a directory's name: lowercase letters and digits, with a hyphen for
// each run of anything else. It is empty when nothing is left.
func projectNameOf(directory string) string {
	var out strings.Builder
	gap := false
	for _, r := range strings.ToLower(directory) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if gap && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(r)
			gap = false
			continue
		}
		gap = true
	}
	name := out.String()
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// register registers the repository and every clone under the given paths, as `repository add` does, and
// says what became of each. A clone refused is said and left out; the repository the command runs in must
// register, because the session plans its work.
func (c *invocation) register(client *api.ProjectClient, root string, paths []string) int {
	report, err := client.AddRepositories(c.ctx, api.RepositoryAdd{Paths: paths, Depth: 1})
	if err != nil {
		return c.failure(err)
	}
	c.say("%s", addedText(report))
	for _, added := range report.Repositories {
		if added.Path == root && added.Outcome == ledger.RegistrationRefused {
			return c.refuse(ExitConflict, "this repository was not registered: %s", added.Reason)
		}
	}
	return ExitOK
}

// adoptedPlanningRepository settles where the plan lives (ADR-0022): the repository --planning names, or the
// one the project already plans in, or its only repository. A project of several that names none asks the
// person on the terminal, and with nobody to ask says how to name one.
func (c *invocation) adoptedPlanningRepository(client *api.ProjectClient, named string) (string, int) {
	if named != "" {
		view, err := client.Repository(c.ctx, named)
		if err != nil {
			return "", c.failure(err)
		}
		return c.planIn(client, view)
	}
	list, err := client.Repositories(c.ctx)
	if err != nil {
		return "", c.failure(err)
	}
	var names []string
	byName := map[string]api.RepositoryView{}
	for _, view := range list.Repositories {
		if view.Removed() {
			continue
		}
		if view.Planning || view.PlanOfRecord {
			return view.ID, ExitOK
		}
		names = append(names, view.Name)
		byName[view.Name] = view
	}
	slices.SortFunc(names, func(a, b string) int { return cmp.Compare(strings.ToLower(a), strings.ToLower(b)) })
	listed := strings.Join(names, ", ")
	if c.terminal == nil || c.asJSON {
		return "", c.usageError("the project holds %d repositories and names none to plan in; pass --planning with one of: %s", len(names), listed)
	}
	fmt.Fprintf(c.stdout, "This project holds several repositories. Which repository holds the plan? (%s): ", listed)
	answer, err := bufio.NewReader(c.terminal).ReadString('\n')
	if err != nil && answer == "" {
		return "", c.usageError("no repository was named to plan in; pass --planning with one of: %s", listed)
	}
	view, held := byName[strings.TrimSpace(answer)]
	if !held {
		return "", c.usageError("%q is not a repository of the project; pass --planning with one of: %s", strings.TrimSpace(answer), listed)
	}
	return c.planIn(client, view)
}

// planIn makes a repository the project's planning repository, unless it is already.
func (c *invocation) planIn(client *api.ProjectClient, view api.RepositoryView) (string, int) {
	if view.Planning {
		return view.ID, ExitOK
	}
	on := true
	if _, err := client.SetRepository(c.ctx, view.ID, api.RepositoryChange{Planning: &on}); err != nil {
		return "", c.failure(err)
	}
	c.say("Planning in repository %s.\n", cmp.Or(view.Name, view.ID))
	return view.ID, ExitOK
}

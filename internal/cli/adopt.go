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
	"ropeshift.com/ropeshift/internal/planning"
)

// adoptCriterion is the one criterion an adopt session's work order holds.
const adoptCriterion = "The project's existing documents are onboarded into the repository file format with their prose and IDs kept, " +
	"and the plan of what remains is written to the repository as files, with the person in this session."

// adoptStatement is what an adopt session's work order says it is for.
func adoptStatement(project string) string {
	return fmt.Sprintf("Adopt the existing project %s: onboard the documents it already holds into the repository file format, "+
		"mark what the code already does with its evidence, continue the in-flight branches the person keeps, and plan what remains.", project)
}

// adopt brings an existing project into ropeshift from the repository it runs in (ADR-0023). It reads the layout
// around the repository first (FR-ADOPT-12), and with --dry-run prints what it found and what adopting would
// do, and records nothing. Otherwise it makes the calls a member makes by hand, each one an audited member
// action: it finds or creates the project, registers the repository and every clone it detected, settles the
// planning repository, and starts the adopt session, a planning session whose lane onboards what the project
// holds, then attaches the terminal. With --restructure the session's work carries the plan's digest and the
// choices, so the adoption branch restructures as the plan listed.
func (c *invocation) adopt(args []string) int {
	set := c.flags()
	planningName := set.String("planning", "", "the repository that holds the plan, by its clone's folder name")
	designName := set.String("design", "", "the design repository, by its clone's folder name")
	var leaveOut repeated
	set.Var(&leaveOut, "leave-out", "a code tree of the design repository that a consolidation removes, relative to its top, repeatable")
	depth := set.Int("depth", 1, "how many levels below each folder to look for clones")
	dryRun := set.Bool("dry-run", false, "print the layout, the shape, and every change and move, and record nothing")
	restructure := set.Bool("restructure", false, "restructure the planning repository as the plan lists, on the adoption branch")
	worker := set.String("worker", "", "the worker that runs the session, by default the one worker online")
	noAttach := set.Bool("no-attach", false, "start the session and leave your terminal alone")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if *depth < 1 {
		return c.usageError("--depth is at least 1")
	}
	root, identity, code := c.repositoryRunIn()
	if code != ExitOK {
		return code
	}
	request := api.AdoptionPlanRequest{Root: root, Depth: *depth, Planning: *planningName, Design: *designName, LeaveOut: leaveOut}
	for _, path := range positional {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return c.failure(err)
		}
		request.Paths = append(request.Paths, absolute)
	}
	client := c.client()
	defer client.Close()
	name, exists, code := c.adoptingProjectName(client, root, identity)
	if code != ExitOK {
		return code
	}
	plan, code := c.adoptionPlan(client, name, exists, request)
	if code != ExitOK {
		return code
	}
	if *restructure && !plan.Shape.Restructures() {
		return c.usageError("--restructure has nothing to do in the %s shape: %s", plan.Shape, shapeSummary(plan.Shape))
	}
	if *restructure && plan.Planning.Undecided {
		return c.usageError("--restructure needs the repository that holds the plan, and this layout settles none; name it with --planning NAME")
	}
	if *dryRun {
		return c.finish(nil, plan, func() string { return adoptionPlanText(plan) })
	}
	if !exists {
		if name, code = c.createAdoptingProject(client, name, root); code != ExitOK {
			return code
		}
	}
	switch {
	case *restructure:
		c.say("Restructuring as the %s shape, from the plan with digest %s.\n", plan.Shape, plan.Digest)
	case plan.Shape.Restructures():
		c.say("The layout reads as the %s shape. '%s adopt --dry-run' lists what --restructure would change.\n", plan.Shape, program)
	}
	project := client.Project(name)
	report, code := c.register(project, root, registrablePaths(plan))
	if code != ExitOK {
		return code
	}
	repository, code := c.adoptedPlanningRepository(project, *planningName, registeredAt(report, plan.Planning.Path))
	if code != ExitOK {
		return code
	}
	chosenWorker, code := c.chooseWorker(project, *worker)
	if code != ExitOK {
		return code
	}
	draft := sessionDraft{
		title: "Adopt " + name, statement: adoptStatement(name), criterion: adoptCriterion, adopt: true,
		repository: repository, worker: chosenWorker,
	}
	if *restructure {
		draft.restructure = restructureOf(plan)
	}
	started, code := c.openSession(project, draft)
	if code != ExitOK {
		return code
	}
	started.Project = name
	return c.joinSession(project, started, *noAttach)
}

// adoptionPlan reads the plan of the adoption from the daemon's worker. A project that does not exist yet has no
// route of its own, so its plan is read with no record of any project.
func (c *invocation) adoptionPlan(client *api.Client, project string, exists bool, request api.AdoptionPlanRequest) (planning.AdoptionPlan, int) {
	var plan planning.AdoptionPlan
	var err error
	if exists {
		plan, err = client.Project(project).AdoptionPlan(c.ctx, request)
	} else {
		plan, err = client.AdoptionPlan(c.ctx, request)
	}
	if err != nil {
		return plan, c.failure(err)
	}
	return plan, ExitOK
}

// registrablePaths are the clones and paths a registration is asked for: every one the plan lists except a
// repository it leaves out of the registration.
func registrablePaths(plan planning.AdoptionPlan) []string {
	var paths []string
	for _, repository := range plan.Repositories {
		if repository.Outcome != planning.OutcomeNotRegistered {
			paths = append(paths, repository.Path)
		}
	}
	return paths
}

// registeredAt is the record of the clone a registration reported at a path, or empty when it reported none.
func registeredAt(report api.RepositoryAddReport, path string) string {
	for _, added := range report.Repositories {
		if added.Path == path && added.Repository != nil && added.Outcome != ledger.RegistrationRefused {
			return added.Repository.ID
		}
	}
	return ""
}

// restructureOf is what the adopt work carries of the plan a member chose to restructure by.
func restructureOf(plan planning.AdoptionPlan) *planning.Restructure {
	chosen := &planning.Restructure{Shape: plan.Shape, Digest: plan.Digest}
	if plan.Shape != planning.ShapeConsolidate || plan.Design == nil {
		return chosen
	}
	chosen.DesignPath, chosen.DesignRoot = plan.Design.Path, plan.Design.Root
	for _, tree := range plan.CodeTrees {
		if tree.LeftOut {
			chosen.LeaveOut = append(chosen.LeaveOut, tree.Path)
		}
	}
	return chosen
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

// adoptingProjectName is the project that adopts the repository: the one that holds it, or else the one --project
// or ROPESHIFT_PROJECT names, or else a project named after the repository's directory. It says whether the
// installation holds the project, and creates nothing. A project that already has an approved plan is refused,
// because it plans its next work with a planning session.
func (c *invocation) adoptingProjectName(client *api.Client, root, identity string) (string, bool, int) {
	projects, err := client.Projects(c.ctx)
	if err != nil {
		return "", false, c.failure(err)
	}
	named := cmp.Or(c.project, c.envProject)
	for _, p := range projects {
		if !slices.Contains(p.Repositories, identity) {
			continue
		}
		if named != "" && named != p.Name {
			return "", false, c.refuse(ExitConflict, "project %s holds this repository; adopt it there, with --project %s or none", p.Name, p.Name)
		}
		return p.Name, true, c.refusePlanned(client.Project(p.Name), p.Name)
	}
	if named == "" {
		named = projectNameOf(filepath.Base(root))
		if named == "" {
			return "", false, c.usageError("the directory %q makes no project name; name the project with --project", filepath.Base(root))
		}
	}
	if slices.ContainsFunc(projects, func(p api.Project) bool { return p.Name == named }) {
		return named, true, c.refusePlanned(client.Project(named), named)
	}
	return named, false, ExitOK
}

// createAdoptingProject creates the project the installation does not hold, shown by the repository's
// directory's name until a rename gives it another.
func (c *invocation) createAdoptingProject(client *api.Client, name, root string) (string, int) {
	created, err := client.CreateProject(c.ctx, api.CreateProject{Name: name, DisplayName: displayNameOf(filepath.Base(root))})
	if err != nil {
		return "", c.failure(err)
	}
	c.say("Created project %s.\n", created.Name)
	return created.Name, ExitOK
}

// displayNameOf is a directory's name as a project's display name, or none when the directory's name is no display
// name, such as one too long to show or one with a character that shows as nothing. The project is then shown by
// its name, and a rename gives it a display name.
func displayNameOf(directory string) string {
	directory = strings.TrimSpace(directory)
	if ledger.ValidateDisplayName(directory) != nil {
		return ""
	}
	return directory
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

// register registers the repository and every clone the detection found, as `repository add` does, and says
// what became of each. A clone refused is said and left out; the repository the command runs in must register,
// because the session plans its work.
func (c *invocation) register(client *api.ProjectClient, root string, paths []string) (api.RepositoryAddReport, int) {
	report, err := client.AddRepositories(c.ctx, api.RepositoryAdd{Paths: paths, Depth: 1})
	if err != nil {
		return report, c.failure(err)
	}
	c.say("%s", addedText(report))
	for _, added := range report.Repositories {
		if added.Path == root && added.Outcome == ledger.RegistrationRefused {
			return report, c.refuse(ExitConflict, "this repository was not registered: %s", added.Reason)
		}
	}
	return report, ExitOK
}

// adoptedPlanningRepository settles where the plan lives (ADR-0022): the repository --planning names, or the
// one the project already plans in, or its only repository, or else the one the layout settled. When the layout
// settles none, a project of several asks the person on the terminal, and with nobody to ask says how to name one.
func (c *invocation) adoptedPlanningRepository(client *api.ProjectClient, named, detected string) (string, int) {
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
	byID, byName := map[string]api.RepositoryView{}, map[string]api.RepositoryView{}
	for _, view := range list.Repositories {
		if view.Removed() {
			continue
		}
		if view.Planning || view.PlanOfRecord {
			return view.ID, ExitOK
		}
		names = append(names, view.Name)
		byID[view.ID], byName[view.Name] = view, view
	}
	if view, held := byID[detected]; held {
		return c.planIn(client, view)
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

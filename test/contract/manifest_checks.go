package contract

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ManifestChecks hold what intake reads of a repository manifest (ADR-0022, FR-REPO-8): a document of its
// plan, read only from the planning repository, whose keys beyond the format's have no effect and are
// reported, and which reports the repositories of the project it does not list without removing any.
var ManifestChecks = []Check{
	{"RM1", "a manifest is read only from the planning repository, as a document of its plan with versions", checkManifestIsAPlanDocument},
	{"RM2", "a manifest that tries to set a member's settings changes none, and intake reports each key it ignored", checkManifestSetsNoSetting},
	{"RM3", "a repository of the project the manifest does not list is reported and kept", checkManifestReportsUnlistedRepositories},
	{"RM4", "approving a plan registers, as its approver, each repository its manifest adds from the clone at its path beside the planning repository, with the safe settings; one with no clone is not registered and a later approval registers it", checkApprovalRegistersTheManifest},
	{"RM5", "a member registers what a manifest lists by hand, by its names, and a clone whose remote is not the manifest's is not registered", checkManifestRegisteredByHand},
	{"RM7", "the first registration from a manifest in a project of one repository names that repository the planning repository, in its transaction, so a later registration still reads paths beside its clone", checkManifestRegistrationNamesThePlanningRepository},
	{"RM8", "approving a plan registers only what its own version's manifest lists, and never brings back a repository a member removed, under its name or its remote, which only a member's registration by hand does", checkApprovalLeavesRemovedRepositoriesOut},
	{"RM9", "a manifest name the project holds for another repository, by remote or by the clone at its path, is a name collision and registers nothing; the same repository under its name reads as registered", checkManifestNameCollision},
	{"RM10", "approving a plan records on the approval what it registered, an outcome for each repository its version's manifest lists with its path, where that led, and its remote, also when one fails and the others register; a dry run records nothing", checkApprovalRecordsWhatItRegistered},
	{"RM11", "a manifest name a member removed stays removed whatever clone, history, or remote the entry names, and a plan approval says so", checkRemovedNameStaysRemoved},
	{"RM12", "deciding a plan approval again changes nothing a first decision registered: the recorded outcomes and the repositories stay, and the approval keeps one record of what it registered", checkRepeatedDecisionKeepsTheRegistration},
	{"RM6", "a manifest registers only a clone inside the directory that holds the planning repository's clone, symbolic links followed: a path or a link that leads out of it, the planning clone itself, and the installation's own directories register nothing, each refused naming where it leads", checkManifestPathsStayBesideThePlanningClone},
}

const manifestPlan = "platform-v1"

// manifestText is a manifest of the plan, listing each repository by name with a path beside the planning
// repository's clone, and holding extra front matter as written.
func manifestText(extra string, names ...string) string {
	out := "+++\nkind = \"repositories\"\nid = \"platform\"\ntitle = \"The platform\"\nplan = \"" + manifestPlan + "\"\n" + extra
	for _, name := range names {
		out += "\n[[repository]]\nname = \"" + name + "\"\nremote = \"git@github.com:acme/" + name + ".git\"\npath = \"../" + name + "\"\n"
	}
	return out + "+++\n\nThe repositories of the platform.\n"
}

// manifestProject is a project of an api and a web repository whose planning repository is the one holding
// the source's files, with a plan that lists its manifest. It returns the source with nothing read yet.
func (r *Rig) manifestProject(s ports.Store) (*fake.WorkSource, error) {
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	for id, directory := range map[string]string{"repo-api": "api", "repo-web": "web"} {
		if _, err := r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: id, DefaultBranch: "main", Directory: directory}, system); err != nil {
			return nil, err
		}
	}
	files := fake.NewWorkSource()
	files.SetRepository("", "main")
	files.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \""+manifestPlan+"\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nOne plan for every repository.\n"))
	return files, nil
}

// readsAsPlanning reads the source once, which records its plan and its repository, and makes that
// repository the planning repository.
func (r *Rig) readsAsPlanning(s ports.Store, files *fake.WorkSource) error {
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	yes := true
	_, err := r.change(s, "memory", control.RepositoryChange{Planning: &yes}, r.Actor)
	return err
}

// problemOf is the one problem a report holds for a path.
func problemOf(report control.IntakeReport, path string) (planning.SourceProblem, bool) {
	at := slices.IndexFunc(report.Problems, func(p planning.SourceProblem) bool { return p.Path == path })
	if at < 0 {
		return planning.SourceProblem{}, false
	}
	return report.Problems[at], true
}

func checkManifestIsAPlanDocument(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText("", "api", "web")))
	// While another repository plans, the manifest is a misplaced plan file.
	yes := true
	if _, err := r.change(s, "repo-api", control.RepositoryChange{Planning: &yes}, r.Actor); err != nil {
		return err
	}
	report, err := r.intake(s, files)
	if err != nil {
		return err
	}
	if problem, found := problemOf(report, "repositories.md"); !found || !strings.Contains(problem.Detail, "planning repository") || report.Records != 0 {
		return Violatedf("a manifest outside the planning repository reads %+v", report)
	}
	if _, err := r.change(s, "memory", control.RepositoryChange{Planning: &yes}, r.Actor); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText("", "api", "web")))
	if report, err = r.intake(s, files); err != nil {
		return err
	}
	if problem, found := problemOf(report, "repositories.md"); found {
		return Violatedf("a manifest that lists every repository is a problem: %+v", problem)
	}
	if err := r.manifestVersion(s, 1, "api", "web"); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText("", "api", "web", "billing")))
	if _, err = r.intake(s, files); err != nil {
		return err
	}
	if err := r.manifestVersion(s, 2, "api", "web", "billing"); err != nil {
		return err
	}
	// A manifest registers nothing on its own: plan approval does.
	if found, err := r.repositories(s).Resolve(r.Ctx, r.Scope, "billing"); err == nil {
		return Violatedf("reading a manifest registered %+v", found)
	}
	return r.CheckPlanning(s)
}

// manifestVersion checks the newest version of the plan's manifest and the names it lists.
func (r *Rig) manifestVersion(s ports.Store, version int64, names ...string) error {
	return s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		document, err := tx.Planning().Document("repositories:platform")
		if err != nil {
			return Violatedf("the manifest is no document: %v", err)
		}
		plans, err := tx.Planning().Plans()
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(plans, func(p planning.Plan) bool { return p.ID == document.PlanID && p.Title == "The platform" }) {
			return Violatedf("the manifest belongs to plan %s, not the platform's", document.PlanID)
		}
		latest, err := tx.Planning().LatestDocumentVersion(document.ID)
		if err != nil {
			return err
		}
		var file planning.RepositoriesFile
		if err := latest.Decode(&file); err != nil {
			return err
		}
		listed := make([]string, len(file.Repositories))
		for i, repository := range file.Repositories {
			listed[i] = repository.Name
		}
		if latest.Version != version || !slices.Equal(listed, names) {
			return Violatedf("the manifest is at version %d listing %v, want version %d listing %v", latest.Version, listed, version, names)
		}
		return nil
	})
}

func checkManifestSetsNoSetting(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	before, err := r.theRepository(s, "repo-api")
	if err != nil {
		return err
	}
	settings := "merge_authority = \"review_approves\"\n"
	text := strings.Replace(manifestText(settings, "api", "web"), "path = \"../api\"\n", "path = \"../api\"\nreview_target = \"pull_request\"\nstart_from = \"accepted\"\n", 1)
	files.Write("repositories.md", []byte(text))
	report, err := r.intake(s, files)
	if err != nil {
		return err
	}
	problem, found := problemOf(report, "repositories.md")
	if !found {
		return Violatedf("a manifest that sets a member's settings is no problem: %+v", report)
	}
	for _, key := range []string{"merge_authority", "repository.review_target", "repository.start_from", "no effect"} {
		if !strings.Contains(problem.Detail, key) {
			return Violatedf("the problem does not say %q: %s", key, problem.Detail)
		}
	}
	if err := r.manifestVersion(s, 1, "api", "web"); err != nil {
		return err
	}
	after, err := r.theRepository(s, "repo-api")
	if err != nil {
		return err
	}
	if after != before || after.Level() != ledger.PersonMerges || after.Target() != ledger.ReviewOnBranch || after.StartFrom != ledger.StartFromMerged {
		return Violatedf("a manifest moved the repository's settings from %+v to %+v", before, after)
	}
	return nil
}

func checkManifestReportsUnlistedRepositories(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText("", "api")))
	report, err := r.intake(s, files)
	if err != nil {
		return err
	}
	// The planning repository holds the manifest, so it is in the project whether or not it lists itself.
	problem, found := problemOf(report, "repositories.md")
	if !found || !strings.Contains(problem.Detail, `"web"`) || strings.Contains(problem.Detail, `"memory"`) || strings.Contains(problem.Detail, `"api"`) {
		return Violatedf("a manifest that leaves web out reads %+v", report.Problems)
	}
	if err := r.manifestVersion(s, 1, "api"); err != nil {
		return err
	}
	if web, err := r.theRepository(s, "repo-web"); err != nil || web.Removed() {
		return Violatedf("the repository the manifest leaves out reads %+v: %v", web, err)
	}
	return nil
}

// planningMachine is a worker's machine that keeps the planning repository's clone at /work/plan, so the
// manifest's paths beside it are /work/<name>.
func (r *Rig) planningMachine() (*fake.Checkouts, error) {
	kept := fake.NewCheckouts()
	return kept, kept.Keep(r.Ctx, r.Scope, "memory", "/work/plan")
}

// cloneOf is a clone on the machine at /work/<directory>, whose remote is the platform's repository named.
func cloneOf(directory, name string) ports.Clone {
	return ports.Clone{Path: "/work/" + directory, ID: "repo-" + name, Remote: "git@github.com:acme/" + name + ".git", DefaultBranch: "main", Directory: directory}
}

// outcomeOf finds what became of one repository a manifest names.
func outcomeOf(added []control.Added, path string) (control.Added, bool) {
	at := slices.IndexFunc(added, func(a control.Added) bool { return a.Path == path })
	if at < 0 {
		return control.Added{}, false
	}
	return added[at], true
}

func checkApprovalRegistersTheManifest(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText("", "api", "web", "billing", "search")))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	// The clone's directory is not the manifest's name, which names the repository.
	kept.Place(cloneOf("billing", "billing"))
	repositories := r.repositories(s)
	request, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if _, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, false, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a tick registering what a manifest adds: %v", err)
	}
	// A delegate holds the consent to register a repository by hand, and still registers nothing from a
	// manifest, approved or given whole.
	delegate := ledger.Actor{Kind: ledger.ActorDelegate, ID: "member-1/assistant"}
	if _, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, true, delegate); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate registering what an approved manifest adds: %v", err)
	}
	if _, err := repositories.RegisterManifest(r.Ctx, r.Scope, planning.RepositoriesFile{ID: "platform"}, kept, true, delegate); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate registering from a manifest: %v", err)
	}
	// Until a member approves the plan, its manifest registers nothing.
	if added, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, false, r.member()); err != nil || len(added) != 0 {
		return Violatedf("an unapproved manifest registered %+v: %v", added, err)
	}
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	added, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, false, r.member())
	if err != nil {
		return err
	}
	if len(added) != 4 {
		return Violatedf("approval registered %+v, want an outcome for each of four repositories", added)
	}
	for _, name := range []string{"api", "web"} {
		if held, ok := outcomeOf(added, "/work/"+name); !ok || held.Outcome != ledger.AlreadyRegistered || held.Repository.Name != name {
			return Violatedf("%s, which the project holds, reads %+v", name, held)
		}
	}
	billing, ok := outcomeOf(added, "/work/billing")
	if !ok || billing.Outcome != ledger.Registered || billing.Checkout != "/work/billing" {
		return Violatedf("billing reads %+v", billing)
	}
	record, err := r.theRepository(s, "repo-billing")
	if err != nil {
		return err
	}
	if record.Name != "billing" || record.Target() != ledger.ReviewOnBranch || record.Level() != ledger.PersonMerges || record.StartFrom != ledger.StartFromMerged || record.MergeMethod != ledger.MergeCommit {
		return Violatedf("billing is registered as %+v", record)
	}
	events, err := r.repositoryEvents(s, "repo-billing")
	if err != nil || len(events) == 0 || events[0].Type != ledger.EventRepositoryRegistered || events[0].Actor != r.member() {
		return Violatedf("billing's registration is recorded as %+v: %v", events, err)
	}
	// The event names the approval that registered it, and the plan version that approval covers.
	var cause struct {
		Name        string `json:"name"`
		Approval    string `json:"approval"`
		Plan        string `json:"plan"`
		PlanVersion int64  `json:"plan_version"`
	}
	if err := json.Unmarshal(events[0].Data, &cause); err != nil || cause.Name != "billing" || cause.Approval != request.ID || cause.Plan != request.Plan || cause.PlanVersion != request.PlanVersion {
		return Violatedf("billing's registration event reads %s, want approval %s of plan %s version %d", events[0].Data, request.ID, request.Plan, request.PlanVersion)
	}
	if search, ok := outcomeOf(added, "/work/search"); !ok || search.Outcome != ledger.RegistrationRefused || !strings.Contains(search.Reason, "/work/search") {
		return Violatedf("search, with no clone, reads %+v", search)
	}
	if held, err := kept.Of(r.Ctx, r.Scope); err != nil || held["repo-billing"] != "/work/billing" {
		return Violatedf("the worker keeps %v: %v", held, err)
	}
	// Once its clone is there, a later plan approval registers it, and what is registered stays as it is. The
	// approval that registered the rest keeps its record, so the later one is of a new version of the plan.
	kept.Place(cloneOf("search", "search"))
	files.Write("repositories.md", []byte(strings.Replace(manifestText("", "api", "web", "billing", "search"), "The repositories of the platform.", "The repositories of the platform, with search.", 1)))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	later, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if _, err := r.approve(s, later, true, r.member()); err != nil {
		return err
	}
	added, err = repositories.RegisterApproved(r.Ctx, r.Scope, later.ID, kept, false, r.member())
	if err != nil {
		return err
	}
	if search, ok := outcomeOf(added, "/work/search"); !ok || search.Outcome != ledger.Registered || search.Repository.Name != "search" {
		return Violatedf("search, once cloned, reads %+v", search)
	}
	if again, ok := outcomeOf(added, "/work/billing"); !ok || again.Outcome != ledger.AlreadyRegistered {
		return Violatedf("billing, registered before, reads %+v", again)
	}
	return r.CheckProjection(s)
}

func checkManifestRegisteredByHand(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "billing", Remote: "git@github.com:acme/billing.git", Path: "../billing"},
		{Name: "ledger", Remote: "git@github.com:acme/ledger.git", Path: "../ledger"},
		// A remote either side leaves empty is not compared, so the clone at the path is taken.
		{Name: "docs", Remote: "git@github.com:acme/docs.git", Path: "../docs"},
		{Name: "wiki", Path: "../wiki"},
	}}
	repositories := r.repositories(s)
	// A machine that keeps no clone of the planning repository has nothing to read the paths from.
	if added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, fake.NewCheckouts(), false, r.member()); err != nil || len(added) != len(manifest.Repositories) ||
		added[0].Outcome != ledger.RegistrationRefused || !strings.Contains(added[0].Reason, "planning repository") {
		return Violatedf("a manifest on a machine with no planning clone reads %+v: %v", added, err)
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	kept.Place(cloneOf("billing", "billing"))
	kept.Place(ports.Clone{Path: "/work/docs", ID: "repo-docs", DefaultBranch: "main"})
	kept.Place(cloneOf("wiki", "wiki"))
	ledgerClone := cloneOf("ledger", "ledger")
	ledgerClone.Remote = "git@github.com:someone-else/ledger.git"
	kept.Place(ledgerClone)
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	dry, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, true, r.member())
	if err != nil {
		return err
	}
	if billing, ok := outcomeOf(dry, "/work/billing"); !ok || billing.Outcome != ledger.Registered {
		return Violatedf("a dry run says billing would be %+v", billing)
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a dry run moved the ledger from %d to %d: %v", before, after, err)
	}
	added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	if billing, ok := outcomeOf(added, "/work/billing"); !ok || billing.Outcome != ledger.Registered || billing.Repository.Name != "billing" {
		return Violatedf("billing reads %+v", billing)
	}
	foreign, ok := outcomeOf(added, "/work/ledger")
	if !ok || foreign.Outcome != ledger.RegistrationRefused || !strings.Contains(foreign.Reason, "someone-else") {
		return Violatedf("a clone of another remote reads %+v", foreign)
	}
	if found, err := repositories.Resolve(r.Ctx, r.Scope, "ledger"); err == nil {
		return Violatedf("a clone of another remote was registered as %+v", found)
	}
	for _, name := range []string{"docs", "wiki"} {
		if taken, ok := outcomeNamed(added, name); !ok || taken.Outcome != ledger.Registered {
			return Violatedf("%s, with a remote on one side only, reads %+v", name, taken)
		}
	}
	return nil
}

// outcomeNamed finds what became of the repository a manifest names.
func outcomeNamed(added []control.Added, name string) (control.Added, bool) {
	at := slices.IndexFunc(added, func(a control.Added) bool { return a.Name == name })
	if at < 0 {
		return control.Added{}, false
	}
	return added[at], true
}

func checkManifestPathsStayBesideThePlanningClone(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	// A manifest whose path climbs out of the directory that holds the planning clone is no document.
	files.Write("repositories.md", []byte(strings.Replace(manifestText("", "api", "web"), `path = "../web"`, `path = "../../home/user/web"`, 1)))
	report, err := r.intake(s, files)
	if err != nil {
		return err
	}
	if problem, found := problemOf(report, "repositories.md"); !found || !strings.Contains(problem.Detail, "inside the directory that holds that clone") {
		return Violatedf("a manifest path that climbs out of the planning clone's directory reads %+v", report.Problems)
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	kept.Place(cloneOf("billing", "billing"))
	kept.Place(ports.Clone{Path: "/home/user/secret", ID: "repo-secret", DefaultBranch: "main"})
	// Another project's clone on the same machine, which that project's checkout names.
	kept.Place(ports.Clone{Path: "/other/api", ID: "repo-theirs", DefaultBranch: "main"})
	if err := kept.Keep(r.Ctx, "workspace-b", "repo-theirs", "/other/api"); err != nil {
		return err
	}
	kept.Place(ports.Clone{Path: "/data/elsewhere", ID: "repo-escape", DefaultBranch: "main"})
	kept.Link("/work/escape", "/data/elsewhere")
	kept.Place(ports.Clone{Path: "/work/real-search", ID: "repo-search", DefaultBranch: "main"})
	kept.Link("/work/search", "/work/real-search")
	kept.Reserve("/work/state")
	kept.Place(ports.Clone{Path: "/work/state/lanes/one", ID: "repo-lane", DefaultBranch: "main"})
	// A manifest given whole skips intake's check, so registration holds every path to the same bound.
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "secret", Path: "../../home/user/secret"},
		{Name: "theirs", Path: "../../other/api"},
		{Name: "escape", Path: "../escape"},
		{Name: "search", Path: "../search"},
		{Name: "itself", Path: "../plan"},
		{Name: "lane", Path: "../state/lanes/one"},
		{Name: "billing", Remote: "git@github.com:acme/billing.git", Path: "../billing"},
	}}
	added, err := r.repositories(s).RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	for name, why := range map[string]string{
		"secret": "/home/user/secret, outside /work",
		"theirs": "/other/api, outside /work",
		"escape": "resolves to /data/elsewhere, outside /work",
		"itself": "the planning repository's own clone",
		"lane":   "this installation keeps for itself",
	} {
		refused, ok := outcomeNamed(added, name)
		if !ok || refused.Outcome != ledger.RegistrationRefused || !strings.Contains(refused.Reason, why) {
			return Violatedf("%s reads %+v, want a refusal saying %q", name, refused, why)
		}
	}
	search, ok := outcomeNamed(added, "search")
	if !ok || search.Outcome != ledger.Registered || search.Repository.Name != "search" || search.Path != "/work/real-search" || search.Checkout != "/work/real-search" {
		return Violatedf("a link inside the directory reads %+v, want the clone it leads to registered", search)
	}
	if billing, ok := outcomeNamed(added, "billing"); !ok || billing.Outcome != ledger.Registered {
		return Violatedf("billing reads %+v", billing)
	}
	for _, id := range []string{"repo-secret", "repo-theirs", "repo-escape", "repo-lane"} {
		if record, err := r.theRepository(s, id); err == nil {
			return Violatedf("a path outside the bound registered %+v", record)
		}
	}
	held, err := kept.Of(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	for _, path := range held {
		if !strings.HasPrefix(path, "/work/") || strings.HasPrefix(path, "/work/state/") {
			return Violatedf("the worker keeps a checkout outside the bound: %v", held)
		}
	}
	return r.CheckProjection(s)
}

func checkManifestRegistrationNamesThePlanningRepository(r *Rig) error {
	s := r.Store()
	// The project's one repository plans in it with no member naming it (ADR-0022).
	if _, err := r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: "repo-plan", DefaultBranch: "main", Directory: "plan"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}); err != nil {
		return err
	}
	kept := fake.NewCheckouts()
	if err := kept.Keep(r.Ctx, r.Scope, "repo-plan", "/work/plan"); err != nil {
		return err
	}
	kept.Place(cloneOf("billing", "billing"))
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "billing", Path: "../billing"}, {Name: "search", Path: "../search"},
	}}
	repositories := r.repositories(s)
	if _, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, true, r.member()); err != nil {
		return err
	}
	if record, err := r.theRepository(s, "repo-plan"); err != nil || record.Planning {
		return Violatedf("a dry run named the planning repository: %+v: %v", record, err)
	}
	added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	if billing, ok := outcomeNamed(added, "billing"); !ok || billing.Outcome != ledger.Registered {
		return Violatedf("billing reads %+v", billing)
	}
	planner, err := r.theRepository(s, "repo-plan")
	if err != nil || !planner.Planning {
		return Violatedf("after a second repository joined, the one that planned reads %+v: %v", planner, err)
	}
	events, err := r.repositoryEvents(s, "repo-plan")
	if err != nil {
		return err
	}
	if last := events[len(events)-1]; last.Type != ledger.EventRepositoryChanged || last.Actor != r.member() {
		return Violatedf("naming the planning repository is recorded as %+v", last)
	}
	kept.Place(cloneOf("search", "search"))
	added, err = repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	if search, ok := outcomeNamed(added, "search"); !ok || search.Outcome != ledger.Registered || search.Repository.Name != "search" {
		return Violatedf("a second registration reads %+v", added)
	}
	return r.CheckProjection(s)
}

// otherPlan is a second plan of the project, whose manifest lists the repositories named.
func otherPlan(files *fake.WorkSource, names ...string) {
	files.Write("other.md", []byte("+++\nkind = \"plan\"\nid = \"other-v1\"\ntitle = \"The other work\"\ndocuments = [\"repositories:other\"]\n+++\n\nAnother plan.\n"))
	text := "+++\nkind = \"repositories\"\nid = \"other\"\ntitle = \"The other repositories\"\nplan = \"other-v1\"\n"
	for _, name := range names {
		text += "\n[[repository]]\nname = \"" + name + "\"\npath = \"../" + name + "\"\n"
	}
	files.Write("other-repositories.md", []byte(text+"+++\n\nWhat the other plan uses.\n"))
}

// removedBilling is a project whose platform plan's manifest lists billing, which an approval registered
// and a member then removed, with the worker's machine and the approval of a plan by its title.
type removedBilling struct {
	s            ports.Store
	files        *fake.WorkSource
	kept         *fake.Checkouts
	repositories *control.Repositories
	approved     func(title string) ([]control.Added, error)
}

func (r *Rig) removedBilling() (removedBilling, error) {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return removedBilling{}, err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return removedBilling{}, err
	}
	files.Write("repositories.md", []byte(manifestText("", "api", "web", "billing")))
	if _, err := r.intake(s, files); err != nil {
		return removedBilling{}, err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return removedBilling{}, err
	}
	kept.Place(cloneOf("billing", "billing"))
	kept.Place(cloneOf("search", "search"))
	repositories := r.repositories(s)
	approved := func(title string) ([]control.Added, error) {
		request, err := r.theOpenPlanApprovalFor(s, title)
		if err != nil {
			return nil, err
		}
		if _, err := r.approve(s, request, true, r.member()); err != nil {
			return nil, err
		}
		return repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, false, r.member())
	}
	if added, err := approved("The platform"); err != nil {
		return removedBilling{}, err
	} else if billing, ok := outcomeNamed(added, "billing"); !ok || billing.Outcome != ledger.Registered {
		return removedBilling{}, Violatedf("billing reads %+v", billing)
	}
	if _, err := repositories.Remove(r.Ctx, r.Scope, "repo-billing", kept, r.member()); err != nil {
		return removedBilling{}, err
	}
	return removedBilling{s: s, files: files, kept: kept, repositories: repositories, approved: approved}, nil
}

func checkApprovalLeavesRemovedRepositoriesOut(r *Rig) error {
	project, err := r.removedBilling()
	if err != nil {
		return err
	}
	s, files, kept, repositories, approved := project.s, project.files, project.kept, project.repositories, project.approved
	// Another plan's approval registers what its own manifest lists, and reads no other plan's.
	otherPlan(files, "search")
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	added, err := approved("The other work")
	if err != nil {
		return err
	}
	if search, ok := outcomeNamed(added, "search"); !ok || search.Outcome != ledger.Registered || len(added) != 1 {
		return Violatedf("the other plan's approval reads %+v, want search alone registered", added)
	}
	if billing, err := r.theRepository(s, "repo-billing"); err != nil || !billing.Removed() {
		return Violatedf("another plan's approval brought billing back: %+v: %v", billing, err)
	}
	// A new version of the plan that lists it still leaves it removed, and says why.
	files.Write("repositories.md", []byte(manifestText("", "api", "web", "billing", "search")))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	if added, err = approved("The platform"); err != nil {
		return err
	}
	billing, ok := outcomeNamed(added, "billing")
	if !ok || billing.Outcome != ledger.RegistrationRefused || !strings.Contains(billing.Reason, "removed") {
		return Violatedf("a removed repository the approved manifest lists reads %+v", billing)
	}
	if record, err := r.theRepository(s, "repo-billing"); err != nil || !record.Removed() {
		return Violatedf("approval brought billing back: %+v: %v", record, err)
	}
	// Nor does a clone of its remote with a history of its own, listed under another name, which would be the
	// same repository again: not by the remote the entry names, nor by the remote the clone has.
	if err := r.removedRemoteStaysRemoved(s, files, kept, approved); err != nil {
		return err
	}
	// A member's registration by hand brings it back.
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{{Name: "billing", Path: "../billing"}}}
	if added, err = repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member()); err != nil {
		return err
	}
	if back, ok := outcomeNamed(added, "billing"); !ok || back.Outcome != ledger.Registered {
		return Violatedf("registering billing by hand reads %+v", back)
	}
	if record, err := r.theRepository(s, "repo-billing"); err != nil || record.Removed() {
		return Violatedf("billing after a member registered it again: %+v: %v", record, err)
	}
	return r.CheckProjection(s)
}

// billingRemote is the remote the platform's billing repository has.
const billingRemote = "git@github.com:acme/billing.git"

// removedRemoteStaysRemoved lists, in a new version of the platform's manifest, three repositories that are
// the removed billing under other names: one a clone with a history of its own whose remote is billing's and
// that the entry names so, one the entry names by billing's remote when no clone is at its path, and one the
// entry names with no remote whose clone has billing's. Approving that version registers none, and says each
// is the removed repository.
func (r *Rig) removedRemoteStaysRemoved(s ports.Store, files *fake.WorkSource, kept *fake.Checkouts, approved func(string) ([]control.Added, error)) error {
	text := manifestText("", "api", "web", "billing", "search", "billing2", "billing3", "billing4")
	text = strings.Replace(text, "acme/billing2.git", "acme/billing.git", 1)
	text = strings.Replace(text, "acme/billing3.git", "acme/billing.git", 1)
	text = strings.Replace(text, "remote = \"git@github.com:acme/billing4.git\"\n", "", 1)
	files.Write("repositories.md", []byte(text))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	for _, name := range []string{"billing2", "billing4"} {
		kept.Place(ports.Clone{Path: "/work/" + name, ID: "repo-" + name + "-fresh", Remote: billingRemote, DefaultBranch: "main", Directory: name})
	}
	added, err := approved("The platform")
	if err != nil {
		return err
	}
	for _, name := range []string{"billing2", "billing3", "billing4"} {
		again, ok := outcomeNamed(added, name)
		if !ok || again.Outcome != ledger.RegistrationRefused || !strings.Contains(again.Reason, "removed") {
			return Violatedf("%s, which is the removed billing under another name, reads %+v", name, again)
		}
		if found, err := r.theRepository(s, "repo-"+name+"-fresh"); err == nil {
			return Violatedf("%s registered %+v", name, found)
		}
	}
	if record, err := r.theRepository(s, "repo-billing"); err != nil || !record.Removed() {
		return Violatedf("approval brought billing back: %+v: %v", record, err)
	}
	return nil
}

func checkRemovedNameStaysRemoved(r *Rig) error {
	project, err := r.removedBilling()
	if err != nil {
		return err
	}
	// The name alone: a clone at billing's path with a history of its own and another remote, which the entry
	// names too, so nothing but the removed repository's name says it is that repository.
	other := "git@github.com:acme/billing-next.git"
	project.kept.Place(ports.Clone{Path: "/work/billing", ID: "repo-billing-fresh", Remote: other, DefaultBranch: "main", Directory: "billing"})
	project.files.Write("repositories.md", []byte(strings.Replace(manifestText("", "api", "web", "billing"), billingRemote, other, 1)))
	if _, err := r.intake(project.s, project.files); err != nil {
		return err
	}
	added, err := project.approved("The platform")
	if err != nil {
		return err
	}
	billing, ok := outcomeNamed(added, "billing")
	if !ok || billing.Outcome != ledger.RegistrationRefused || !strings.Contains(billing.Reason, "removed") {
		return Violatedf("the removed billing's name, with a clone of another history and remote, reads %+v", billing)
	}
	if found, err := r.theRepository(project.s, "repo-billing-fresh"); err == nil {
		return Violatedf("a clone of another history and remote registered under the removed name: %+v", found)
	}
	if record, err := r.theRepository(project.s, "repo-billing"); err != nil || !record.Removed() {
		return Violatedf("approval brought billing back: %+v: %v", record, err)
	}
	// A member's registration by hand takes the clone, which is another repository, under the name.
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{{Name: "billing", Remote: other, Path: "../billing"}}}
	if added, err = project.repositories.RegisterManifest(r.Ctx, r.Scope, manifest, project.kept, false, r.member()); err != nil {
		return err
	}
	if by, ok := outcomeNamed(added, "billing"); !ok || by.Outcome != ledger.Registered || by.Repository.ID != "repo-billing-fresh" {
		return Violatedf("registering the clone by hand reads %+v", by)
	}
	return r.CheckProjection(project.s)
}

func checkManifestNameCollision(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	if _, err := r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: "repo-docs", Remote: "git@github.com:acme/docs.git", DefaultBranch: "main", Directory: "docs"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	// The clone at api's path is another repository than the project's api, and web's is the project's web.
	kept.Place(ports.Clone{Path: "/work/api", ID: "repo-payments", Remote: "git@github.com:acme/payments.git", DefaultBranch: "main"})
	kept.Place(ports.Clone{Path: "/work/web", ID: "repo-web", DefaultBranch: "main"})
	planner, err := r.theRepository(s, "memory")
	if err != nil {
		return err
	}
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "api", Path: "../api"},
		{Name: "web", Path: "../web"},
		{Name: "docs", Remote: "git@github.com:acme/handbook.git", Path: "../docs"},
		{Name: planner.Name, Path: "."},
	}}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	added, err := r.repositories(s).RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	for name, identity := range map[string]string{"api": "github.com/acme/payments", "docs": "github.com/acme/handbook"} {
		collision, ok := outcomeNamed(added, name)
		if !ok || collision.Outcome != ledger.RegistrationRefused || !strings.Contains(collision.Reason, "held by another repository") || !strings.Contains(collision.Reason, identity) {
			return Violatedf("%s, whose name the project holds for another repository, reads %+v", name, collision)
		}
	}
	for _, name := range []string{"web", planner.Name} {
		if same, ok := outcomeNamed(added, name); !ok || same.Outcome != ledger.AlreadyRegistered {
			return Violatedf("%s, the project's own repository, reads %+v", name, same)
		}
	}
	if found, err := r.theRepository(s, "repo-payments"); err == nil {
		return Violatedf("a name collision registered %+v", found)
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a manifest of collisions and held repositories moved the ledger from %d to %d: %v", before, after, err)
	}
	return nil
}

// failingFind is a worker's machine whose git cannot read one path.
type failingFind struct {
	*fake.Checkouts
	at string
}

func (f failingFind) Find(ctx context.Context, paths []string, depth int) ([]ports.Clone, []ports.UnreadPath, error) {
	if slices.Contains(paths, f.at) {
		return nil, nil, errors.New("git cannot read " + f.at)
	}
	return f.Checkouts.Find(ctx, paths, depth)
}

// registrationOf reads what an approval recorded it registered.
func (r *Rig) registrationOf(s ports.Store, approval string) (*ledger.ManifestRegistration, error) {
	var record *ledger.ManifestRegistration
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		record, err = control.RegistrationOf(tx, approval)
		return err
	})
	return record, err
}

func checkApprovalRecordsWhatItRegistered(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText("", "api", "web", "billing", "search", "ledger")))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	machine, err := r.planningMachine()
	if err != nil {
		return err
	}
	machine.Place(cloneOf("billing", "billing"))
	machine.Place(cloneOf("search", "search"))
	kept := failingFind{Checkouts: machine, at: "/work/search"}
	repositories := r.repositories(s)
	request, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	preview, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, true, r.member())
	if err != nil {
		return err
	}
	if billing, ok := outcomeNamed(preview, "billing"); !ok || billing.Outcome != ledger.Registered || billing.Manifest != "../billing" || billing.Path != "/work/billing" {
		return Violatedf("the preview says billing would be %+v", billing)
	}
	if record, err := r.registrationOf(s, request.ID); err != nil || record != nil {
		return Violatedf("a preview recorded %+v: %v", record, err)
	}
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	if _, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, false, r.member()); err != nil {
		return err
	}
	record, err := r.registrationOf(s, request.ID)
	if err != nil || record == nil || len(record.Repositories) != 5 || record.Problem != "" {
		return Violatedf("the approval recorded %+v: %v, want an outcome for each of five repositories", record, err)
	}
	outcome := func(name string) ledger.ManifestOutcome {
		at := slices.IndexFunc(record.Repositories, func(o ledger.ManifestOutcome) bool { return o.Name == name })
		if at < 0 {
			return ledger.ManifestOutcome{}
		}
		return record.Repositories[at]
	}
	billing := outcome("billing")
	if billing.Outcome != ledger.Registered || billing.Path != "../billing" || billing.Resolved != "/work/billing" || billing.Remote != "git@github.com:acme/billing.git" ||
		billing.Repository != "repo-billing" || billing.Checkout != "/work/billing" {
		return Violatedf("the record says billing %+v", billing)
	}
	if search := outcome("search"); search.Outcome != ledger.RegistrationRefused || !strings.Contains(search.Reason, "git cannot read /work/search") {
		return Violatedf("the record says search, which git could not read, %+v", search)
	}
	if missing := outcome("ledger"); missing.Outcome != ledger.RegistrationRefused || !strings.Contains(missing.Reason, "/work/ledger") {
		return Violatedf("the record says ledger, with no clone, %+v", missing)
	}
	for _, name := range []string{"api", "web"} {
		if held := outcome(name); held.Outcome != ledger.AlreadyRegistered || held.Repository != "repo-"+name {
			return Violatedf("the record says %s %+v", name, held)
		}
	}
	if _, err := r.theRepository(s, "repo-billing"); err != nil {
		return Violatedf("billing is not registered beside the failure: %v", err)
	}
	return r.CheckProjection(s)
}

// registrationEvents counts what an approval recorded it registered.
func (r *Rig) registrationEvents(s ports.Store, approval string) (int, error) {
	var recorded int
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		events, err := tx.EventsAbout(ledger.Subject{Kind: authority.SubjectApproval, ID: approval})
		for _, event := range events {
			if event.Type == ledger.EventManifestRegistered {
				recorded++
			}
		}
		return err
	})
	return recorded, err
}

// outcomesRead says what a registration's outcomes tell a member, each in the order the manifest lists them.
func outcomesRead(added []control.Added) []string {
	out := make([]string, len(added))
	for i, one := range added {
		out[i] = one.Name + " " + string(one.Outcome) + " " + one.Path + " " + one.Checkout + " " + one.Repository.ID + " " + one.Reason
	}
	return out
}

func checkRepeatedDecisionKeepsTheRegistration(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText("", "api", "web", "billing", "search")))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	kept.Place(cloneOf("billing", "billing"))
	repositories := r.repositories(s)
	request, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	first, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, false, r.member())
	if err != nil {
		return err
	}
	if billing, ok := outcomeNamed(first, "billing"); !ok || billing.Outcome != ledger.Registered {
		return Violatedf("the first decision reads billing %+v", billing)
	}
	recorded, err := r.registrationOf(s, request.ID)
	if err != nil || recorded == nil {
		return Violatedf("the first decision recorded %+v: %v", recorded, err)
	}
	// The member's word again, as a second press or a command run twice gives it, and a clone for search
	// that was not there the first time, which a registration run again would take.
	kept.Place(cloneOf("search", "search"))
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	second, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, false, r.member())
	if err != nil {
		return err
	}
	if !slices.Equal(outcomesRead(first), outcomesRead(second)) {
		return Violatedf("deciding again reads\n%v\nwhere the first decision read\n%v", outcomesRead(second), outcomesRead(first))
	}
	again, err := r.registrationOf(s, request.ID)
	if err != nil || again == nil || !slices.Equal(again.Repositories, recorded.Repositories) || again.Problem != recorded.Problem {
		return Violatedf("deciding again left the approval recording %+v, where it recorded %+v: %v", again, recorded, err)
	}
	if count, err := r.registrationEvents(s, request.ID); err != nil || count != 1 {
		return Violatedf("the approval holds %d records of what it registered, want one: %v", count, err)
	}
	if found, err := r.theRepository(s, "repo-search"); err == nil {
		return Violatedf("deciding again registered search, whose clone came after: %+v", found)
	}
	return r.CheckProjection(s)
}

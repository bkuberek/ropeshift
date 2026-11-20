package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/gittest"
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
	{"RM13", "a registration that stops partway records each entry it never attempted as not attempted beside the problem that stopped it, and the entries before it keep their outcomes", checkStoppedRegistrationRecordsWhatItNeverAttempted},
	{"RM14", "a manifest path that spells the planning repository's own clone another way, as a disk that ignores case allows, is that clone and registers nothing", checkManifestPathSpelledAsThePlanningClone},
	{"RM15", "a clone is matched to the record whose ID is its first-parent root, else to a record that is not removed whose ID is its lowest root, said to be matched by its earlier identity, else it is a new record under its first-parent root; a removed record is never matched by the lowest root", checkRegistrationMatchesARecordByItsRoots},
	{"RM16", "a clone that shares its first commit with a repository this worker keeps at another path that still holds it is refused, naming that commit, the repository, and its path, when the two remotes differ or neither has one, in one run and across runs, in a dry run too; the same remote, a path that is gone, and the same path with a new remote are not refused", checkClonesThatShareAFirstCommitAreRefused},
	{"RM17", "a manifest entry whose clone shares its first commit with a repository this worker keeps at another path is refused with the same reason, and the first registration stands", checkManifestEntrySharingAFirstCommitIsRefused},
	{"RM19", "a clone is matched to the record this worker already keeps at its path when that record's ID is one of the clone's roots, so a root merged in later does not split one repository into two records", checkANewLowerRootDoesNotSplitTheRecordKeptAtThePath},
	{"RM20", "a clone is not matched by its lowest root to a record whose remote differs from its own unless this worker keeps a clone of that record at another path, which refuses it for sharing that first commit; a clone or a record with no remote is matched as before, and the same path with a moved remote refreshes its record", checkALowestRootDoesNotAttachAnotherRepositoryToARecord},
	{"RM21", "a clone is matched by any root it holds, lowest first, to a record that is not removed and not another repository's, so a lower root merged in later neither splits the record at a second path nor hides a clone that shares its first commit", checkAnyRootMatchesTheRecordAtASecondPath},
	{"RM22", "a path this worker keeps for a record holds nothing of it once another clone replaced the clone there, so a clone of the record's first commit at another path is not refused for sharing it", checkAReplacedCloneHoldsNothing},
	{"RM23", "a path this worker keeps for a record, spelled through a symbolic link, is the clone it leads to: a clone of the record's first commit at another path is refused or is a second clone as for the real path, the same clone registered again keeps the checkout as it was, and a record kept through a link still matches its clone's other roots", checkAKeptPathSpelledThroughALinkIsTheSameClone},
	{"RM18", "on real git repositories, a workspace that merged an unrelated history is its own record whether or not the repository that history came from is kept or removed, unless that repository's record is from before the first-parent rule and kept, which refuses the workspace for sharing its first commit", checkMergedWorkspaceOnRealRepositories},
	{"RM33", "on real git repositories, a shallow clone is refused, in a dry run too, because its first commit is not its repository's, with the reason that says to fetch its whole history, and records nothing; made whole, it registers under its repository's first commit", checkShallowCloneIsRefused},
	{"RM26", "a clone spelled in another case, on a disk that ignores case, is the clone it spells, so it is no clone that shares its first commit, with no remote either", checkASpellingOfAClonesDirectoryIsNotAnotherClone},
	{"RM25", "a clone of one run that has its own record is matched to it, as it is alone, and not to the record of an earlier clone of the run whose history it merged", checkARunMatchesEachCloneAsItWouldAlone},
	{"RM24", "two repositories that merged one history are each their own record, whichever registers first, and neither is matched to the record that history made", checkTwoRepositoriesThatMergedOneHistoryAreEachTheirOwn},
	{"RM6", "a manifest registers only a clone inside the directory that holds the planning repository's clone, symbolic links followed: a path or a link that leads out of it, the planning clone itself, and the installation's own directories register nothing, each refused naming where it leads", checkManifestPathsStayBesideThePlanningClone},
	{"RM34", "approving a planning branch's version before intake records it registers each existing clone its manifest lists, and the preview of that version lists them, with an entry that creates read as planned unless a work order on the branch names it; the approval keeps one record once intake records the commit", checkBranchVersionRegistersItsManifest},
	{"RM35", "an entry with create that no work order of the version names in repo is planned in the preview and in the approval's record, and nothing is created or registered for it; a later version whose work order names it is not planned", checkUntargetedCreationIsPlanned},
	{"RM45", "a contract whose reads name an entry with create, and a work order of another plan that names it in repo, target nothing of the version the ledger holds: the entry stays planned", checkReadsAndAnotherPlanTargetNothingHeld},
	{"RM46", "a contract whose reads name an entry with create targets nothing of the version a planning branch holds: the request carries no target for it and the entry stays planned", checkReadsTargetNothingOnABranch},
	{"RM47", "a work order of a contract of another plan on the branch is no target of this plan's version: the request carries no target for it and the entry stays planned", checkAnotherPlansWorkTargetsNothingOnABranch},
	{"RM48", "an entry with create that a member registers by hand with the manifest, with no plan approval, is never planned: it registers from the clone at its path, or is refused for having none", checkHandRegisteredCreateEntryIsNeverPlanned},
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
	if _, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake-tick"}); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a tick registering what a manifest adds: %v", err)
	}
	// A delegate holds the consent to register a repository by hand, and still registers nothing from a
	// manifest, approved or given whole.
	delegate := ledger.Actor{Kind: ledger.ActorDelegate, ID: "member-1/assistant"}
	if _, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", true, delegate); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate registering what an approved manifest adds: %v", err)
	}
	if _, err := repositories.RegisterManifest(r.Ctx, r.Scope, planning.RepositoriesFile{ID: "platform"}, kept, true, delegate); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate registering from a manifest: %v", err)
	}
	// Until a member approves the plan, its manifest registers nothing.
	if added, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member()); err != nil || len(added) != 0 {
		return Violatedf("an unapproved manifest registered %+v: %v", added, err)
	}
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	added, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
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
	added, err = repositories.RegisterApproved(r.Ctx, r.Scope, later.ID, kept, nil, "", false, r.member())
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
		return repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
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
	preview, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", true, r.member())
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
	if _, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member()); err != nil {
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
	first, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
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
	second, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
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

// failingKeep is a worker's machine that cannot keep the checkout at one path.
type failingKeep struct {
	*fake.Checkouts
	at string
}

func (f failingKeep) Keep(ctx context.Context, project ledger.WorkspaceID, repository, at string) error {
	if at == f.at {
		return errors.New("the checkouts file cannot be written")
	}
	return f.Checkouts.Keep(ctx, project, repository, at)
}

func checkStoppedRegistrationRecordsWhatItNeverAttempted(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText("", "api", "web", "billing", "search", "ledger", "docs")))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	machine, err := r.planningMachine()
	if err != nil {
		return err
	}
	for _, name := range []string{"billing", "search", "ledger", "docs"} {
		machine.Place(cloneOf(name, name))
	}
	kept := failingKeep{Checkouts: machine, at: "/work/search"}
	repositories := r.repositories(s)
	request, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	added, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
	if err == nil || !strings.Contains(err.Error(), "cannot be written") {
		return Violatedf("a run whose checkout could not be kept ended with %v", err)
	}
	record, err := r.registrationOf(s, request.ID)
	if err != nil || record == nil || !strings.Contains(record.Problem, "cannot be written") {
		return Violatedf("the approval recorded %+v: %v, want the problem that stopped it", record, err)
	}
	if len(record.Repositories) != 6 || len(added) != 6 {
		return Violatedf("the approval recorded %d outcomes and returned %d, want one for each of the six repositories the manifest lists: %+v", len(record.Repositories), len(added), record.Repositories)
	}
	outcome := func(name string) ledger.ManifestOutcome {
		at := slices.IndexFunc(record.Repositories, func(o ledger.ManifestOutcome) bool { return o.Name == name })
		if at < 0 {
			return ledger.ManifestOutcome{}
		}
		return record.Repositories[at]
	}
	if billing := outcome("billing"); billing.Outcome != ledger.Registered || billing.Checkout != "/work/billing" {
		return Violatedf("billing, registered before the run stopped, reads %+v", billing)
	}
	for name, path := range map[string]string{"ledger": "../ledger", "docs": "../docs"} {
		left := outcome(name)
		if left.Outcome != "not_attempted" || left.Path != path || left.Remote != "git@github.com:acme/"+name+".git" || !strings.Contains(left.Reason, "search") {
			return Violatedf("%s, which the run never reached, reads %+v", name, left)
		}
		if found, err := r.theRepository(s, "repo-"+name); err == nil {
			return Violatedf("%s was registered though the run never reached it: %+v", name, found)
		}
	}
	return r.CheckProjection(s)
}

func checkManifestPathSpelledAsThePlanningClone(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	// The planning repository's clone is at /work/plan, and the machine reads /work/PLAN as the same directory.
	kept.Place(ports.Clone{Path: "/work/PLAN", ID: "memory", DefaultBranch: "main"})
	kept.Spell("/work/PLAN", "/work/plan")
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{{Name: "again", Path: "../PLAN"}}}
	for _, dryRun := range []bool{true, false} {
		added, err := r.repositories(s).RegisterManifest(r.Ctx, r.Scope, manifest, kept, dryRun, r.member())
		if err != nil {
			return err
		}
		if again, ok := outcomeNamed(added, "again"); !ok || again.Outcome != ledger.RegistrationRefused || !strings.Contains(again.Reason, "the planning repository's own clone") {
			return Violatedf("the planning clone spelled /work/PLAN reads %+v, dry run %v", again, dryRun)
		}
	}
	if held, err := kept.Of(r.Ctx, r.Scope); err != nil || held["memory"] != "/work/plan" {
		return Violatedf("the worker keeps %v as the planning repository's checkout: %v", held, err)
	}
	return r.CheckProjection(s)
}

// rootClone is a clone at /work/<directory> whose first-parent root is root and that holds every root named,
// with no remote when remote is empty.
func rootClone(directory, root, remote string, roots ...string) ports.Clone {
	if len(roots) == 0 {
		roots = []string{root}
	}
	return ports.Clone{Path: "/work/" + directory, ID: root, Roots: roots, Remote: remote, DefaultBranch: "main", Directory: directory}
}

// firstCommit is a root commit the checks give to clones of one template.
func firstCommit(prefix string) string { return prefix + strings.Repeat("0", 40-len(prefix)) }

// sharedRefusal is the reason a clone is refused for sharing its first commit with another.
func sharedRefusal(root, name, path string) string {
	return "shares its first commit `" + root[:8] + "` with repository `" + name + "` at `" + path +
		"`; ropeshift identifies a repository by that commit, so give this clone a remote of its own or a history of its own"
}

// addAt registers the clones at paths by hand on a machine and says what became of the clone at each.
func (r *Rig) addAt(repositories *control.Repositories, kept *fake.Checkouts, dryRun bool, paths ...string) (map[string]control.Added, error) {
	added, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: paths, DryRun: dryRun}, kept, r.member())
	if err != nil {
		return nil, err
	}
	out := map[string]control.Added{}
	for _, one := range added {
		out[one.Path] = one
	}
	if len(out) != len(paths) {
		return nil, Violatedf("registering %v reads %+v", paths, added)
	}
	return out, nil
}

func checkRegistrationMatchesARecordByItsRoots(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	earlier := "matched by its earlier identity"

	// A record stored under the lowest-root rule: its ID is a root the clone holds, and not its first-parent root.
	lowest, own := firstCommit("0a"), firstCommit("0b")
	if err := r.legacy(s, lowest, ""); err != nil {
		return err
	}
	if err := kept.Keep(r.Ctx, r.Scope, lowest, "/work/ws"); err != nil {
		return err
	}
	kept.Place(rootClone("ws", own, "", lowest, own))
	for _, dryRun := range []bool{true, false} {
		got, err := r.addAt(repositories, kept, dryRun, "/work/ws")
		if err != nil {
			return err
		}
		if ws := got["/work/ws"]; ws.Outcome != ledger.AlreadyRegistered || ws.Repository.ID != lowest || ws.Checkout != "/work/ws" || !strings.Contains(ws.Reason, earlier) {
			return Violatedf("a clone whose lowest root names a record reads %+v, dry run %v", ws, dryRun)
		}
	}
	if _, err := r.theRepository(s, own); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a clone matched by its earlier identity made a record under its first-parent root: %v", err)
	}
	if held, err := kept.Of(r.Ctx, r.Scope); err != nil || held[lowest] != "/work/ws" || len(held) != 1 {
		return Violatedf("the worker keeps %v: %v", held, err)
	}

	// The record whose ID is the first-parent root wins over one named by the lowest root.
	older, newer := firstCommit("1a"), firstCommit("1b")
	for _, id := range []string{older, newer} {
		if _, err := r.detect(s, id, "", "main"); err != nil {
			return err
		}
	}
	kept.Place(rootClone("both", newer, "", older, newer))
	got, err := r.addAt(repositories, kept, false, "/work/both")
	if err != nil {
		return err
	}
	if both := got["/work/both"]; both.Outcome != ledger.AlreadyRegistered || both.Repository.ID != newer || strings.Contains(both.Reason, earlier) {
		return Violatedf("a clone whose first-parent root names a record reads %+v", both)
	}

	// With no record of either, it is a new record under its first-parent root.
	fresh, freshOwn := firstCommit("2a"), firstCommit("2b")
	kept.Place(rootClone("fresh", freshOwn, "", fresh, freshOwn))
	if got, err = r.addAt(repositories, kept, false, "/work/fresh"); err != nil {
		return err
	}
	if made := got["/work/fresh"]; made.Outcome != ledger.Registered || made.Repository.ID != freshOwn {
		return Violatedf("a clone with no record reads %+v", made)
	}
	if _, err := r.theRepository(s, fresh); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a new record was made under the lowest root: %v", err)
	}

	// A removed record is never matched by the lowest root, so a workspace that imported a design repository's
	// history does not bring the design repository's record back.
	design, workspace := firstCommit("3a"), firstCommit("3b")
	if _, err := r.detect(s, design, "", "main"); err != nil {
		return err
	}
	if _, err := repositories.Remove(r.Ctx, r.Scope, design, kept, r.member()); err != nil {
		return err
	}
	kept.Place(rootClone("merged", workspace, "", design, workspace))
	if got, err = r.addAt(repositories, kept, false, "/work/merged"); err != nil {
		return err
	}
	if made := got["/work/merged"]; made.Outcome != ledger.Registered || made.Repository.ID != workspace {
		return Violatedf("a clone that merged a removed repository's history reads %+v", made)
	}
	if record, err := r.theRepository(s, design); err != nil || !record.Removed() {
		return Violatedf("the removed repository reads %+v: %v", record, err)
	}
	return r.CheckProjection(s)
}

func checkClonesThatShareAFirstCommitAreRefused(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	remote := func(name string) string { return "git@github.com:acme/" + name + ".git" }
	refused := func(got control.Added, root, name, path string, why string) error {
		if got.Outcome != ledger.RegistrationRefused || got.Reason != sharedRefusal(root, name, path) {
			return Violatedf("%s reads %+v, want a refusal that says %q", why, got, sharedRefusal(root, name, path))
		}
		return nil
	}

	// Two clones of one template with remotes of their own, across runs.
	root := firstCommit("4a")
	kept.Place(rootClone("one", root, remote("one")))
	kept.Place(rootClone("two", root, remote("two")))
	if _, err := r.addAt(repositories, kept, false, "/work/one"); err != nil {
		return err
	}
	for _, dryRun := range []bool{true, false} {
		got, err := r.addAt(repositories, kept, dryRun, "/work/two")
		if err != nil {
			return err
		}
		if err := refused(got["/work/two"], root, "one", "/work/one", "a second template clone with another remote"); err != nil {
			return err
		}
	}
	if record, err := r.theRepository(s, root); err != nil || record.Remote != remote("one") {
		return Violatedf("the template's record reads %+v: %v", record, err)
	}
	if held, err := kept.Of(r.Ctx, r.Scope); err != nil || held[root] != "/work/one" {
		return Violatedf("the worker keeps %v: %v", held, err)
	}

	// Two clones with no remote, across runs.
	bare := firstCommit("4b")
	kept.Place(rootClone("bare-one", bare, ""))
	kept.Place(rootClone("bare-two", bare, ""))
	if _, err := r.addAt(repositories, kept, false, "/work/bare-one"); err != nil {
		return err
	}
	got, err := r.addAt(repositories, kept, false, "/work/bare-two")
	if err != nil {
		return err
	}
	if err := refused(got["/work/bare-two"], bare, "bare-one", "/work/bare-one", "a second clone with no remote"); err != nil {
		return err
	}

	// Within one run the second clone is refused too, and is not "a second clone of" the first, in a dry run as well.
	n := 0
	for _, dryRun := range []bool{true, false} {
		for _, remotes := range [][2]string{{remote("a"), remote("b")}, {"", ""}, {remote("a"), ""}} {
			n++
			within := firstCommit(fmt.Sprintf("5%x", n))
			first, second := fmt.Sprintf("first-%d", n), fmt.Sprintf("second-%d", n)
			kept.Place(rootClone(first, within, remotes[0]))
			kept.Place(rootClone(second, within, remotes[1]))
			got, err := r.addAt(repositories, kept, dryRun, "/work/"+first, "/work/"+second)
			if err != nil {
				return err
			}
			if one := got["/work/"+first]; one.Outcome != ledger.Registered {
				return Violatedf("the first clone of a run reads %+v", one)
			}
			if err := refused(got["/work/"+second], within, first, "/work/"+first, fmt.Sprintf("the second clone of a run with remotes %q, dry run %v", remotes, dryRun)); err != nil {
				return err
			}
		}
	}

	// The same remote is the same repository: a second clone keeps the first checkout and is not refused.
	same := firstCommit("6a")
	kept.Place(rootClone("same-one", same, "git@github.com:acme/same.git"))
	kept.Place(rootClone("same-two", same, "https://github.com/acme/same.git"))
	if _, err := r.addAt(repositories, kept, false, "/work/same-one"); err != nil {
		return err
	}
	if got, err = r.addAt(repositories, kept, false, "/work/same-two"); err != nil {
		return err
	}
	if two := got["/work/same-two"]; two.Outcome != ledger.AlreadyRegistered || two.Checkout != "/work/same-one" {
		return Violatedf("a second clone of one remote reads %+v", two)
	}

	// A checkout that is gone holds nothing, so a clone at another path takes its place.
	gone := firstCommit("6b")
	kept.Place(rootClone("gone-one", gone, remote("gone-one")))
	if _, err := r.addAt(repositories, kept, false, "/work/gone-one"); err != nil {
		return err
	}
	kept.Remove("/work/gone-one")
	kept.Place(rootClone("gone-two", gone, remote("gone-two")))
	if got, err = r.addAt(repositories, kept, false, "/work/gone-two"); err != nil {
		return err
	}
	if two := got["/work/gone-two"]; two.Outcome != ledger.AlreadyRegistered || two.Checkout != "/work/gone-two" {
		return Violatedf("a clone that takes the place of a checkout that is gone reads %+v", two)
	}

	// The same path with a new remote still refreshes the record, because a remote can move.
	moved := firstCommit("6c")
	kept.Place(rootClone("moved", moved, remote("before")))
	if _, err := r.addAt(repositories, kept, false, "/work/moved"); err != nil {
		return err
	}
	kept.Place(rootClone("moved", moved, remote("after")))
	if got, err = r.addAt(repositories, kept, false, "/work/moved"); err != nil {
		return err
	}
	if again := got["/work/moved"]; again.Outcome != ledger.AlreadyRegistered || again.Repository.Remote != remote("after") {
		return Violatedf("the same clone with a new remote reads %+v", again)
	}
	if record, err := r.theRepository(s, moved); err != nil || record.Remote != remote("after") {
		return Violatedf("the record after the remote moved reads %+v: %v", record, err)
	}
	return r.CheckProjection(s)
}

func checkANewLowerRootDoesNotSplitTheRecordKeptAtThePath(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	// The record was registered under the lowest root the clone had then.
	older, lowest, own := firstCommit("81"), firstCommit("80"), firstCommit("82")
	if _, err := r.detect(s, older, "", "main"); err != nil {
		return err
	}
	if err := kept.Keep(r.Ctx, r.Scope, older, "/work/ws"); err != nil {
		return err
	}
	// The clone has since merged a history whose first commit sorts lower still.
	kept.Place(rootClone("ws", own, "", lowest, older, own))
	for _, dryRun := range []bool{true, false} {
		got, err := r.addAt(repositories, kept, dryRun, "/work/ws")
		if err != nil {
			return err
		}
		if ws := got["/work/ws"]; ws.Outcome != ledger.AlreadyRegistered || ws.Repository.ID != older || !strings.Contains(ws.Reason, "matched by its earlier identity") {
			return Violatedf("a clone with a new lower root reads %+v, dry run %v, want the record kept at its path", ws, dryRun)
		}
	}
	for _, id := range []string{lowest, own} {
		if _, err := r.theRepository(s, id); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a second record was made under %s: %v", id, err)
		}
	}
	if held, err := kept.Of(r.Ctx, r.Scope); err != nil || held[older] != "/work/ws" || len(held) != 1 {
		return Violatedf("the worker keeps %v: %v", held, err)
	}
	return r.CheckProjection(s)
}

func checkALowestRootDoesNotAttachAnotherRepositoryToARecord(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	remote := func(name string) string { return "git@github.com:acme/" + name + ".git" }

	// A design repository with a remote, whose clone this worker no longer has, and a workspace with a remote
	// of its own that merged its history: the workspace is a repository of its own, and the record stays as it was.
	design, workspace := firstCommit("90"), firstCommit("91")
	if err := r.legacy(s, design, remote("design")); err != nil {
		return err
	}
	kept.Place(rootClone("workspace", workspace, remote("workspace"), design, workspace))
	got, err := r.addAt(repositories, kept, false, "/work/workspace")
	if err != nil {
		return err
	}
	if made := got["/work/workspace"]; made.Outcome != ledger.Registered || made.Repository.ID != workspace || strings.Contains(made.Reason, "earlier identity") {
		return Violatedf("a workspace with its own remote that merged a design repository's history reads %+v", made)
	}
	if record, err := r.theRepository(s, design); err != nil || record.Removed() || record.Remote != remote("design") {
		return Violatedf("the design repository's record reads %+v: %v", record, err)
	}

	// While this worker keeps the design repository's clone, the workspace shares its first commit and is refused.
	other, merged := firstCommit("92"), firstCommit("93")
	if err := r.legacy(s, other, remote("other")); err != nil {
		return err
	}
	kept.Place(rootClone("other", other, remote("other")))
	if err := kept.Keep(r.Ctx, r.Scope, other, "/work/other"); err != nil {
		return err
	}
	kept.Place(rootClone("merged", merged, remote("merged"), other, merged))
	if got, err = r.addAt(repositories, kept, false, "/work/merged"); err != nil {
		return err
	}
	if refused := got["/work/merged"]; refused.Outcome != ledger.RegistrationRefused || refused.Reason != sharedRefusal(other, "other", "/work/other") {
		return Violatedf("a workspace that merged a kept repository's history reads %+v", refused)
	}
	if record, err := r.theRepository(s, other); err != nil || record.Remote != remote("other") {
		return Violatedf("the kept repository's record reads %+v: %v", record, err)
	}

	// The same path with a moved remote is still its record, which a remote that moved refreshes.
	before, after := firstCommit("94"), firstCommit("95")
	kept.Place(rootClone("moved", before, remote("before")))
	if _, err := r.addAt(repositories, kept, false, "/work/moved"); err != nil {
		return err
	}
	kept.Place(rootClone("moved", after, remote("after"), before, after))
	if got, err = r.addAt(repositories, kept, false, "/work/moved"); err != nil {
		return err
	}
	if again := got["/work/moved"]; again.Outcome != ledger.AlreadyRegistered || again.Repository.ID != before || again.Repository.Remote != remote("after") {
		return Violatedf("the same path with a moved remote reads %+v", again)
	}

	// A clone with no remote, or a record with none, is matched by its lowest root as before.
	for i, shape := range []struct{ record, clone string }{{remote("bare-record"), ""}, {"", remote("bare-clone")}} {
		old, merged := firstCommit(fmt.Sprintf("96%d", i)), firstCommit(fmt.Sprintf("97%d", i))
		if err := r.legacy(s, old, shape.record); err != nil {
			return err
		}
		kept.Place(rootClone(fmt.Sprintf("new-%d", i), merged, shape.clone, old, merged))
		if got, err = r.addAt(repositories, kept, false, fmt.Sprintf("/work/new-%d", i)); err != nil {
			return err
		}
		if matched := got[fmt.Sprintf("/work/new-%d", i)]; matched.Outcome != ledger.AlreadyRegistered || matched.Repository.ID != old {
			return Violatedf("a clone with remote %q matched to a record with remote %q reads %+v", shape.clone, shape.record, matched)
		}
	}
	return r.CheckProjection(s)
}

func checkAnyRootMatchesTheRecordAtASecondPath(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	remote := func(name string) string { return "git@github.com:acme/" + name + ".git" }
	// A record registered under the lowest root its clone had then, with a remote. Its clone has since merged a
	// history whose first commit sorts lower still, so the lowest root of a clone of it names no record.
	lowest, legacy, own := firstCommit("a0"), firstCommit("a1"), firstCommit("a2")
	if err := r.legacy(s, legacy, remote("app")); err != nil {
		return err
	}
	if err := kept.Keep(r.Ctx, r.Scope, legacy, "/work/a"); err != nil {
		return err
	}
	kept.Place(rootClone("a", own, remote("app"), legacy, own))

	// A second clone of the same remote is a second clone of the record, and makes no record of its own.
	kept.Place(rootClone("b", own, remote("app"), lowest, legacy, own))
	got, err := r.addAt(repositories, kept, false, "/work/b")
	if err != nil {
		return err
	}
	if b := got["/work/b"]; b.Outcome != ledger.AlreadyRegistered || b.Repository.ID != legacy || b.Checkout != "/work/a" {
		return Violatedf("a second clone with a lower root merged in reads %+v, want the legacy record kept at /work/a", b)
	}
	// One with another remote shares the record's first commit with the clone this worker keeps, and is refused.
	kept.Place(rootClone("c", own, remote("fork"), lowest, legacy, own))
	if got, err = r.addAt(repositories, kept, false, "/work/c"); err != nil {
		return err
	}
	if c := got["/work/c"]; c.Outcome != ledger.RegistrationRefused || c.Reason != sharedRefusal(legacy, "app", "/work/a") {
		return Violatedf("a fork whose lower root was merged in reads %+v", c)
	}
	for _, id := range []string{lowest, own} {
		if _, err := r.theRepository(s, id); !errors.Is(err, ports.ErrNotFound) {
			return Violatedf("a record was made under %s: %v", id, err)
		}
	}
	// A root that names a record which is another repository's is passed over for the next one.
	other, second := firstCommit("a3"), firstCommit("a4")
	if err := r.legacy(s, other, remote("other")); err != nil {
		return err
	}
	if err := r.legacy(s, second, remote("app-two")); err != nil {
		return err
	}
	kept.Place(rootClone("d", firstCommit("a5"), remote("app-two"), other, second, firstCommit("a5")))
	if got, err = r.addAt(repositories, kept, false, "/work/d"); err != nil {
		return err
	}
	if d := got["/work/d"]; d.Outcome != ledger.AlreadyRegistered || d.Repository.ID != second || !strings.Contains(d.Reason, "matched by its earlier identity") {
		return Violatedf("a clone whose lowest root names another repository's record reads %+v, want the record of its remote", d)
	}
	return r.CheckProjection(s)
}

func checkASpellingOfAClonesDirectoryIsNotAnotherClone(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	root := firstCommit("f0")
	// ~/code/App and ~/Code/app are one directory, and a worker that finds it by both spellings meets one clone.
	kept.Place(rootClone("App", root, ""))
	kept.Place(rootClone("app", root, ""))
	kept.Spell("/work/App", "/work/app")
	got, err := r.addAt(repositories, kept, false, "/work/App", "/work/app")
	if err != nil {
		return err
	}
	if first := got["/work/App"]; first.Outcome != ledger.Registered || first.Repository.ID != root {
		return Violatedf("the first spelling reads %+v", first)
	}
	if second := got["/work/app"]; second.Outcome != ledger.AlreadyRegistered || second.Repository.ID != root {
		return Violatedf("the second spelling of the same directory, with no remote, reads %+v, want a second clone of the first", second)
	}
	// Registered again by the other spelling, it is the checkout kept and no clone that shares its first commit.
	again, err := r.addAt(repositories, kept, false, "/work/app")
	if err != nil {
		return err
	}
	if app := again["/work/app"]; app.Outcome != ledger.AlreadyRegistered || app.Checkout != "/work/App" {
		return Violatedf("the other spelling registered again reads %+v, want the checkout kept as it was", app)
	}
	return r.CheckProjection(s)
}

func checkARunMatchesEachCloneAsItWouldAlone(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	remote := func(name string) string { return "git@github.com:acme/" + name + ".git" }
	// B has a record of its own, with a remote of its own, and C's history merged in. C's first commit sorts
	// above B's, and C is registered first in the run.
	b, c := firstCommit("e1"), firstCommit("e2")
	if _, err := r.detect(s, b, remote("b"), "main"); err != nil {
		return err
	}
	kept := fake.NewCheckouts()
	kept.Place(rootClone("c", c, ""))
	kept.Place(rootClone("b", b, remote("b"), b, c))
	for _, dryRun := range []bool{true, false} {
		got, err := r.addAt(repositories, kept, dryRun, "/work/c", "/work/b")
		if err != nil {
			return err
		}
		if added := got["/work/c"]; added.Outcome != ledger.Registered || added.Repository.ID != c {
			return Violatedf("C reads %+v, dry run %v", added, dryRun)
		}
		if added := got["/work/b"]; added.Outcome != ledger.AlreadyRegistered || added.Repository.ID != b {
			return Violatedf("B, which has a record of its own, reads %+v in a run with C, dry run %v", added, dryRun)
		}
	}
	// The same two clones, with the second a second clone of the first's remote, are one repository.
	d := firstCommit("e3")
	kept.Place(rootClone("d1", d, remote("d")))
	kept.Place(rootClone("d2", d, remote("d")))
	got, err := r.addAt(repositories, kept, true, "/work/d1", "/work/d2")
	if err != nil {
		return err
	}
	if second := got["/work/d2"]; second.Outcome != ledger.AlreadyRegistered || second.Repository.ID != d || !strings.Contains(second.Reason, "a second clone of") {
		return Violatedf("a second clone of the same remote in a dry run reads %+v", second)
	}
	return r.CheckProjection(s)
}

func checkTwoRepositoriesThatMergedOneHistoryAreEachTheirOwn(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	// C's history has a record, with no remote and no clone this worker keeps. A and B each merged it, with
	// remotes of their own or none, and C's root sorts below both of theirs.
	c, a, b := firstCommit("d0"), firstCommit("d1"), firstCommit("d2")
	if _, err := r.detect(s, c, "", "main"); err != nil {
		return err
	}
	kept.Place(rootClone("a", a, "", c, a))
	kept.Place(rootClone("b", b, "git@github.com:acme/b.git", c, b))
	for _, dryRun := range []bool{true, false} {
		got, err := r.addAt(repositories, kept, dryRun, "/work/a", "/work/b")
		if err != nil {
			return err
		}
		for path, root := range map[string]string{"/work/a": a, "/work/b": b} {
			if added := got[path]; added.Outcome != ledger.Registered || added.Repository.ID != root {
				return Violatedf("%s, which merged C's history, reads %+v, dry run %v, want its own record %s", path, added, dryRun, root)
			}
		}
	}
	if record, err := r.theRepository(s, c); err != nil || record.Removed() || record.Remote != "" {
		return Violatedf("C's record reads %+v: %v", record, err)
	}
	return r.CheckProjection(s)
}

func checkAKeptPathSpelledThroughALinkIsTheSameClone(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	// /alias/one is /work/one, as /var is /private/var on a machine that links its temporary directories.
	kept.Link("/alias", "/work")
	remote := func(name string) string { return "git@github.com:acme/" + name + ".git" }
	root := firstCommit("c0")
	kept.Place(rootClone("one", root, remote("one")))
	if _, err := r.addAt(repositories, kept, false, "/work/one"); err != nil {
		return err
	}
	if err := kept.Keep(r.Ctx, r.Scope, root, "/alias/one"); err != nil {
		return err
	}
	kept.Place(rootClone("two", root, remote("two")))
	got, err := r.addAt(repositories, kept, false, "/work/two")
	if err != nil {
		return err
	}
	if two := got["/work/two"]; two.Outcome != ledger.RegistrationRefused || two.Reason != sharedRefusal(root, "one", "/work/one") {
		return Violatedf("a clone sharing the first commit of a clone kept through a link reads %+v", two)
	}
	kept.Place(rootClone("three", root, remote("one")))
	if got, err = r.addAt(repositories, kept, false, "/work/three"); err != nil {
		return err
	}
	if three := got["/work/three"]; three.Outcome != ledger.AlreadyRegistered || three.Checkout != "/alias/one" {
		return Violatedf("a second clone of the remote kept through a link reads %+v, want the checkout kept as it was", three)
	}
	// The same clone, registered again by its real path, is the checkout already kept and moves nothing.
	if got, err = r.addAt(repositories, kept, false, "/work/one"); err != nil {
		return err
	}
	if again := got["/work/one"]; again.Outcome != ledger.AlreadyRegistered || again.Checkout != "/alias/one" {
		return Violatedf("the clone kept through a link, registered again, reads %+v", again)
	}
	if held, err := kept.Of(r.Ctx, r.Scope); err != nil || held[root] != "/alias/one" {
		return Violatedf("the worker keeps %v, want the checkout it kept", held)
	}
	// A record kept through a link is the record of its clone, whose other roots have since changed.
	older, own := firstCommit("c1"), firstCommit("c2")
	if _, err := r.detect(s, older, remote("ws"), "main"); err != nil {
		return err
	}
	if err := kept.Keep(r.Ctx, r.Scope, older, "/alias/ws"); err != nil {
		return err
	}
	kept.Place(rootClone("ws", own, remote("ws"), firstCommit("bf"), older, own))
	if got, err = r.addAt(repositories, kept, false, "/work/ws"); err != nil {
		return err
	}
	if ws := got["/work/ws"]; ws.Outcome != ledger.AlreadyRegistered || ws.Repository.ID != older {
		return Violatedf("a clone whose record is kept through a link reads %+v, want that record", ws)
	}
	if _, err := r.theRepository(s, own); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a second record was made under the first-parent root: %v", err)
	}
	return r.CheckProjection(s)
}

func checkAReplacedCloneHoldsNothing(r *Rig) error {
	s := r.Store()
	repositories := r.repositories(s)
	kept := fake.NewCheckouts()
	root := firstCommit("b0")
	kept.Place(rootClone("one", root, "git@github.com:acme/one.git"))
	if _, err := r.addAt(repositories, kept, false, "/work/one"); err != nil {
		return err
	}
	// Another repository now stands where the record's clone was kept.
	kept.Place(rootClone("one", firstCommit("b1"), "git@github.com:acme/replacement.git"))
	kept.Place(rootClone("two", root, "git@github.com:acme/two.git"))
	got, err := r.addAt(repositories, kept, false, "/work/two")
	if err != nil {
		return err
	}
	if two := got["/work/two"]; two.Outcome != ledger.AlreadyRegistered || two.Repository.ID != root || two.Checkout != "/work/two" {
		return Violatedf("a clone of a record whose kept path another clone replaced reads %+v", two)
	}
	return r.CheckProjection(s)
}

// checkMergedWorkspaceOnRealRepositories reads clones with git itself: a design repository, and a workspace
// that merged the design repository's history, whose first commit sorts lower than the workspace's own.
func checkMergedWorkspaceOnRealRepositories(r *Rig) error {
	workspace, design := gittest.NewUnrelatedPair(r.T)
	own, imported := workspace.Roots("HEAD")[0], design.Roots("HEAD")[0]
	s := r.Store()
	repositories := r.repositories(s)
	machine := &checkouts.File{Path: filepath.Join(r.T.TempDir(), checkouts.FileName), Env: workspace.Env()}
	add := func(dir string) (control.Added, error) {
		added, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{dir}}, machine, r.member())
		if err != nil || len(added) != 1 {
			return control.Added{}, Violatedf("registering %s reads %+v: %v", dir, added, err)
		}
		return added[0], nil
	}
	registered, err := add(design.Dir)
	if err != nil {
		return err
	}
	if registered.Outcome != ledger.Registered || registered.Repository.ID != imported {
		return Violatedf("the design repository reads %+v, want a record under its first commit %s", registered, imported)
	}
	workspace.MergeUnrelated(design)

	// The design repository's record was made under the first-parent rule, so the workspace that merged its
	// history is a repository of its own, and the design repository's record and checkout stay as they were.
	made, err := add(workspace.Dir)
	if err != nil {
		return err
	}
	if made.Outcome != ledger.Registered || made.Repository.ID != own {
		return Violatedf("a workspace that merged a kept repository's history reads %+v, want a record under its first-parent root %s", made, own)
	}
	if record, err := r.theRepository(s, imported); err != nil || record.Removed() || record.Name != registered.Repository.Name {
		return Violatedf("the design repository's record reads %+v: %v", record, err)
	}
	if held, err := machine.Of(r.Ctx, r.Scope); err != nil || held[imported] != registered.Checkout || held[own] != made.Checkout {
		return Violatedf("the worker keeps %v", held)
	}

	// Once the design repository is removed, the workspace is still its own record, and the removed one stays removed.
	if _, err := repositories.Remove(r.Ctx, r.Scope, imported, machine, r.member()); err != nil {
		return err
	}
	again, err := add(workspace.Dir)
	if err != nil {
		return err
	}
	if again.Outcome != ledger.AlreadyRegistered || again.Repository.ID != own {
		return Violatedf("a workspace that merged a removed repository's history reads %+v, want its record under %s", again, own)
	}
	if record, err := r.theRepository(s, imported); err != nil || !record.Removed() {
		return Violatedf("the design repository's record reads %+v: %v", record, err)
	}

	// A design repository whose record is from before the first-parent rule, and is kept, is the repository a
	// workspace that merged its history shares a first commit with, and the workspace is refused.
	older, oldDesign := gittest.NewUnrelatedPair(r.T)
	oldWorkspace, legacyRoot := older, oldDesign.Roots("HEAD")[0]
	if err := r.legacy(s, legacyRoot, ""); err != nil {
		return err
	}
	if err := machine.Keep(r.Ctx, r.Scope, legacyRoot, oldDesign.Dir); err != nil {
		return err
	}
	oldWorkspace.MergeUnrelated(oldDesign)
	shared, err := add(oldWorkspace.Dir)
	if err != nil {
		return err
	}
	legacy, err := r.theRepository(s, legacyRoot)
	if err != nil {
		return err
	}
	// The refusal names the directory the kept path leads to, which is not how a temporary directory is spelled
	// on a machine whose /var is a link to /private/var.
	keptAt, err := filepath.EvalSymlinks(oldDesign.Dir)
	if err != nil {
		return err
	}
	if want := sharedRefusal(legacyRoot, legacy.Name, keptAt); shared.Outcome != ledger.RegistrationRefused || shared.Reason != want {
		return Violatedf("a workspace that merged a kept legacy repository's history reads %+v, want the refusal %q", shared, want)
	}
	return r.CheckProjection(s)
}

// checkShallowCloneIsRefused registers a shallow clone, made with git itself from a repository of two commits:
// its one commit is grafted, so the first commit it holds is not its repository's.
func checkShallowCloneIsRefused(r *Rig) error {
	origin := gittest.New(r.T)
	origin.Commit("README.md", "first\n")
	origin.Commit("NOTES.md", "second\n")
	root := origin.Roots("HEAD")[0]
	shallow := origin.ShallowClone("shallow")
	s := r.Store()
	repositories := r.repositories(s)
	machine := &checkouts.File{Path: filepath.Join(r.T.TempDir(), checkouts.FileName), Env: origin.Env()}
	for _, dryRun := range []bool{true, false} {
		added, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{shallow.Dir}, DryRun: dryRun}, machine, r.member())
		if err != nil {
			return err
		}
		if len(added) != 1 || added[0].Outcome != ledger.RegistrationRefused || added[0].Path != shallow.Dir || added[0].Reason != ledger.ShallowClone {
			return Violatedf("a shallow clone reads %+v, dry run %v, want %s refused with %q", added, dryRun, shallow.Dir, ledger.ShallowClone)
		}
	}
	if listed, err := r.listRepositories(s); err != nil || len(listed) != 0 {
		return Violatedf("a refused shallow clone left records %+v: %v", listed, err)
	}
	if held, err := machine.Of(r.Ctx, r.Scope); err != nil || len(held) != 0 {
		return Violatedf("a refused shallow clone left the checkouts %v: %v", held, err)
	}
	// Made whole, the clone registers under its repository's first commit.
	shallow.Git("fetch", "--quiet", "--unshallow")
	added, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{shallow.Dir}}, machine, r.member())
	if err != nil || len(added) != 1 || added[0].Outcome != ledger.Registered || added[0].Repository.ID != root {
		return Violatedf("the clone made whole reads %+v (%v), want a record under %s", added, err, root)
	}
	return r.CheckProjection(s)
}

func checkManifestEntrySharingAFirstCommitIsRefused(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	root := firstCommit("7a")
	kept.Place(rootClone("billing", root, "git@github.com:acme/billing.git"))
	kept.Place(rootClone("billing-fork", root, "git@github.com:acme/billing-fork.git"))
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "billing", Remote: "git@github.com:acme/billing.git", Path: "../billing"},
		{Name: "billing-fork", Remote: "git@github.com:acme/billing-fork.git", Path: "../billing-fork"},
	}}
	repositories := r.repositories(s)
	for _, dryRun := range []bool{true, false} {
		added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, dryRun, r.member())
		if err != nil {
			return err
		}
		if billing, ok := outcomeNamed(added, "billing"); !ok || billing.Outcome != ledger.Registered {
			return Violatedf("billing reads %+v, dry run %v", billing, dryRun)
		}
		fork, ok := outcomeNamed(added, "billing-fork")
		if !ok || fork.Outcome != ledger.RegistrationRefused || fork.Reason != sharedRefusal(root, "billing", "/work/billing") {
			return Violatedf("a fork that shares billing's first commit reads %+v, dry run %v", fork, dryRun)
		}
	}
	if record, err := r.theRepository(s, root); err != nil || record.Name != "billing" || record.Remote != "git@github.com:acme/billing.git" {
		return Violatedf("the shared first commit's record reads %+v: %v", record, err)
	}
	return r.CheckProjection(s)
}

// createdEntry is a manifest entry for a repository approving creates on GitHub under acme, at a path beside the
// planning repository's clone. Written as the extra front matter of manifestText, it comes before the entries
// that name a remote.
func createdEntry(name string) string {
	return "\n[[repository]]\nname = \"" + name + "\"\npath = \"../" + name + "\"\ncreate = \"github:acme\"\n"
}

// plannedReason is the reason an entry with create reads as planned.
func plannedReason(name string) string {
	return "no work order of this version targets " + name
}

// entryOutcome is what a check expects of one entry of a manifest: its outcome, where its path leads, and a
// text its reason holds.
type entryOutcome struct {
	outcome ledger.RepositoryOutcome
	path    string
	reason  string
	// code is the refusal code the creator gave, and empty for an entry no creator refused.
	code string
}

// outcomesAre checks that a run gave one outcome for each entry named, each as expected.
func outcomesAre(added []control.Added, want map[string]entryOutcome) error {
	if len(added) != len(want) {
		return Violatedf("the run reads %q, want an outcome for each of %d entries", outcomesRead(added), len(want))
	}
	for name, expected := range want {
		got, ok := outcomeNamed(added, name)
		if !ok || got.Outcome != expected.outcome || got.Path != expected.path || !strings.Contains(got.Reason, expected.reason) || got.RefusalCode != expected.code {
			return Violatedf("%s reads %+v, want %s at %q with a reason that says %q and the refusal code %q", name, got, expected.outcome, expected.path, expected.reason, expected.code)
		}
	}
	return nil
}

// recordedAre checks that an approval recorded one outcome for each entry named, each as expected.
func (r *Rig) recordedAre(s ports.Store, approval string, want map[string]entryOutcome) error {
	record, err := r.registrationOf(s, approval)
	if err != nil {
		return err
	}
	if record == nil || len(record.Repositories) != len(want) || record.Problem != "" {
		return Violatedf("the approval recorded %+v, want an outcome for each of %d entries", record, len(want))
	}
	for name, expected := range want {
		at := slices.IndexFunc(record.Repositories, func(o ledger.ManifestOutcome) bool { return o.Name == name })
		if at < 0 {
			return Violatedf("the approval recorded nothing of %s: %+v", name, record.Repositories)
		}
		got := record.Repositories[at]
		if got.Outcome != expected.outcome || got.Resolved != expected.path || !strings.Contains(got.Reason, expected.reason) || got.RefusalCode != expected.code {
			return Violatedf("the approval recorded %s as %+v, want %s at %q with a reason that says %q and the refusal code %q", name, got, expected.outcome, expected.path, expected.reason, expected.code)
		}
	}
	return nil
}

// branchManifestProject is a project whose planning repository's clone this worker's machine keeps at /work/plan,
// with an api repository the project holds and a clone of billing beside it, and whose ledger holds no plan.
func (r *Rig) branchManifestProject(s ports.Store) (*fake.Checkouts, error) {
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	for id, directory := range map[string]string{"memory": "plan", "repo-api": "api"} {
		if _, err := r.repositories(s).Detect(r.Ctx, r.Scope, control.DetectedRepository{ID: id, DefaultBranch: "main", Directory: directory}, system); err != nil {
			return nil, err
		}
	}
	yes := true
	if _, err := r.change(s, "memory", control.RepositoryChange{Planning: &yes}, r.Actor); err != nil {
		return nil, err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return nil, err
	}
	kept.Place(cloneOf("billing", "billing"))
	return kept, nil
}

func checkBranchVersionRegistersItsManifest(r *Rig) error {
	s := r.Store()
	kept, err := r.branchManifestProject(s)
	if err != nil {
		return err
	}
	files := map[string]string{
		"plan.md":         "+++\nkind = \"plan\"\nid = \"" + manifestPlan + "\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nOne plan for every repository.\n",
		"repositories.md": manifestText(createdEntry("web")+createdEntry("docs"), "api", "billing", "ghost"),
		"core.md":         placedContract{ID: "core", Plan: manifestPlan, Orders: []orderIn{{ID: "core-1", Repo: "api"}, {ID: "core-web", Repo: "web"}}}.text(),
	}
	_, pending, err := r.aLanePlanAsksForApproval(s, files)
	if err != nil {
		return err
	}
	if pending.Plan != "" {
		return Violatedf("the branch's version is of plan %s, which the ledger should not hold yet", pending.Plan)
	}
	want := map[string]entryOutcome{
		"api":     {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"billing": {outcome: ledger.Registered, path: "/work/billing"},
		"ghost":   {outcome: ledger.RegistrationRefused, path: "/work/ghost", reason: "no git clone is at /work/ghost"},
		// A work order on the branch names web, so web is not planned; a run with no creator cannot create it.
		"web":  {outcome: ledger.RegistrationRefused, path: "/work/web", reason: "creates no repositories"},
		"docs": {outcome: ledger.Planned, reason: plannedReason("docs")},
	}
	repositories := r.repositories(s)
	preview, err := repositories.RegisterApproved(r.Ctx, r.Scope, pending.ID, kept, nil, "", true, r.member())
	if err != nil {
		return err
	}
	if err := outcomesAre(preview, want); err != nil {
		return Violatedf("the preview of the branch's version: %v", err)
	}
	if record, err := r.registrationOf(s, pending.ID); err != nil || record != nil {
		return Violatedf("a preview recorded %+v: %v", record, err)
	}
	if found, err := repositories.Resolve(r.Ctx, r.Scope, "billing"); err == nil {
		return Violatedf("the preview registered %+v", found)
	}
	if _, err := r.approve(s, pending, true, r.member()); err != nil {
		return err
	}
	added, err := repositories.RegisterApproved(r.Ctx, r.Scope, pending.ID, kept, nil, "", false, r.member())
	if err != nil {
		return err
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("approving the branch's version: %v", err)
	}
	if billing, _ := outcomeNamed(added, "billing"); billing.Checkout != "/work/billing" || billing.Repository.Name != "billing" {
		return Violatedf("billing is registered as %+v", billing)
	}
	if err := r.recordedAre(s, pending.ID, want); err != nil {
		return err
	}
	for _, name := range []string{"web", "docs", "ghost"} {
		if found, err := repositories.Resolve(r.Ctx, r.Scope, name); err == nil {
			return Violatedf("approving registered %s as %+v", name, found)
		}
	}
	// Intake then records the commit, and the approval keeps the one record it made.
	var asked []string
	if _, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(files), &asked)); err != nil {
		return err
	}
	again, err := repositories.RegisterApproved(r.Ctx, r.Scope, pending.ID, kept, nil, "", false, r.member())
	if err != nil {
		return err
	}
	if err := outcomesAre(again, want); err != nil {
		return Violatedf("deciding again once intake recorded the commit: %v", err)
	}
	if recorded, err := r.registrationEvents(s, pending.ID); err != nil || recorded != 1 {
		return Violatedf("the approval recorded what it registered %d times: %v", recorded, err)
	}
	return r.CheckProjection(s)
}

func checkUntargetedCreationIsPlanned(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText(createdEntry("search")+createdEntry("docs"), "api", "billing")))
	files.Write("core.md", []byte(placedContract{ID: "core", Plan: manifestPlan, Orders: []orderIn{{ID: "core-1", Repo: "api"}}}.text()))
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
	want := map[string]entryOutcome{
		"api":     {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"billing": {outcome: ledger.Registered, path: "/work/billing"},
		"search":  {outcome: ledger.Planned, reason: plannedReason("search")},
		"docs":    {outcome: ledger.Planned, reason: plannedReason("docs")},
	}
	preview, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", true, r.member())
	if err != nil {
		return err
	}
	if err := outcomesAre(preview, want); err != nil {
		return Violatedf("the preview: %v", err)
	}
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	added, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
	if err != nil {
		return err
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("approving: %v", err)
	}
	if err := r.recordedAre(s, request.ID, want); err != nil {
		return err
	}
	held, err := kept.Of(r.Ctx, r.Scope)
	if err != nil {
		return err
	}
	for _, name := range []string{"search", "docs"} {
		if found, err := repositories.Resolve(r.Ctx, r.Scope, name); err == nil {
			return Violatedf("approving registered %s, which no work order names, as %+v", name, found)
		}
		for id, path := range held {
			if path == "/work/"+name {
				return Violatedf("approving keeps %s for %s, which no work order names", path, id)
			}
		}
	}
	// A later version gives search work, so its approval does not read search as planned.
	files.Write("core.md", []byte(placedContract{ID: "core", Plan: manifestPlan, Orders: []orderIn{{ID: "core-1", Repo: "api"}, {ID: "core-search", Repo: "search"}}}.text()))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	later, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	want = map[string]entryOutcome{
		"api":     {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"billing": {outcome: ledger.AlreadyRegistered, path: "/work/billing"},
		"search":  {outcome: ledger.RegistrationRefused, path: "/work/search", reason: "creates no repositories"},
		"docs":    {outcome: ledger.Planned, reason: plannedReason("docs")},
	}
	if preview, err = repositories.RegisterApproved(r.Ctx, r.Scope, later.ID, kept, nil, "", true, r.member()); err != nil {
		return err
	}
	if err := outcomesAre(preview, want); err != nil {
		return Violatedf("the preview of the version that gives search work: %v", err)
	}
	if _, err := r.approve(s, later, true, r.member()); err != nil {
		return err
	}
	if added, err = repositories.RegisterApproved(r.Ctx, r.Scope, later.ID, kept, nil, "", false, r.member()); err != nil {
		return err
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("approving the version that gives search work: %v", err)
	}
	return r.CheckProjection(s)
}

// aLanePlanAsksAbout takes a plan lane's branch in a tick and returns the request that asks about the plan titled,
// when the branch holds more than one plan and so raises a request for each.
func (r *Rig) aLanePlanAsksAbout(s ports.Store, files map[string]string, title string) (authority.Approval, error) {
	if _, err := r.planLane(s); err != nil {
		return authority.Approval{}, err
	}
	var asked []string
	if _, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(files), &asked)); err != nil {
		return authority.Approval{}, err
	}
	return r.theOpenPlanApprovalFor(s, title)
}

// carriedTargets reads the repository names a request carries for the work its version holds.
func carriedTargets(request authority.Approval) ([]string, error) {
	var detail planning.PlanDetail
	if err := json.Unmarshal([]byte(request.Detail), &detail); err != nil {
		return nil, Violatedf("the detail of %s is %q: %v", request.ID, request.Detail, err)
	}
	return detail.Targets, nil
}

// anotherPlansWork is a second plan of the project, with a contract whose one work order changes the repository named.
func anotherPlansWork(repo string) map[string]string {
	return map[string]string{
		"other-plan.md": PlanText("other-v1", "Another plan", ""),
		"other.md":      placedContract{ID: "other", Plan: "other-v1", Orders: []orderIn{{ID: "other-1", Repo: repo}}}.text(),
	}
}

func checkReadsAndAnotherPlanTargetNothingHeld(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestText(createdEntry("search")+createdEntry("docs"), "api", "billing")))
	files.Write("core.md", []byte(placedContract{ID: "core", Plan: manifestPlan, Reads: []string{"search"}, Orders: []orderIn{{ID: "core-1", Repo: "api"}}}.text()))
	for path, text := range anotherPlansWork("docs") {
		files.Write(path, []byte(text))
	}
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	kept.Place(cloneOf("billing", "billing"))
	request, err := r.theOpenPlanApprovalFor(s, "The platform")
	if err != nil {
		return err
	}
	want := map[string]entryOutcome{
		"api":     {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"billing": {outcome: ledger.Registered, path: "/work/billing"},
		"search":  {outcome: ledger.Planned, reason: plannedReason("search")},
		"docs":    {outcome: ledger.Planned, reason: plannedReason("docs")},
	}
	repositories := r.repositories(s)
	preview, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", true, r.member())
	if err != nil {
		return err
	}
	if err := outcomesAre(preview, want); err != nil {
		return Violatedf("the preview of a version whose contract reads search, beside another plan's work on docs: %v", err)
	}
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	added, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
	if err != nil {
		return err
	}
	if err := outcomesAre(added, want); err != nil {
		return Violatedf("approving it: %v", err)
	}
	return r.recordedAre(s, request.ID, want)
}

func checkReadsTargetNothingOnABranch(r *Rig) error {
	s := r.Store()
	kept, err := r.branchManifestProject(s)
	if err != nil {
		return err
	}
	files := map[string]string{
		"plan.md":         "+++\nkind = \"plan\"\nid = \"" + manifestPlan + "\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nOne plan for every repository.\n",
		"repositories.md": manifestText(createdEntry("web"), "api", "billing"),
		"core.md":         placedContract{ID: "core", Plan: manifestPlan, Reads: []string{"web"}, Orders: []orderIn{{ID: "core-1", Repo: "api"}}}.text(),
	}
	_, request, err := r.aLanePlanAsksForApproval(s, files)
	if err != nil {
		return err
	}
	return r.branchTargetsOnly(s, request, kept, "api", "web")
}

func checkAnotherPlansWorkTargetsNothingOnABranch(r *Rig) error {
	s := r.Store()
	kept, err := r.branchManifestProject(s)
	if err != nil {
		return err
	}
	files := map[string]string{
		"plan.md":         "+++\nkind = \"plan\"\nid = \"" + manifestPlan + "\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nOne plan for every repository.\n",
		"repositories.md": manifestText(createdEntry("web"), "api", "billing"),
		"core.md":         placedContract{ID: "core", Plan: manifestPlan, Orders: []orderIn{{ID: "core-1", Repo: "api"}}}.text(),
	}
	for path, text := range anotherPlansWork("web") {
		files[path] = text
	}
	request, err := r.aLanePlanAsksAbout(s, files, "The platform")
	if err != nil {
		return err
	}
	return r.branchTargetsOnly(s, request, kept, "api", "web")
}

// branchTargetsOnly checks that the request of a plan a branch holds carries the one target named, and that the
// preview of its version reads the entry named planned, beside billing registering and api held.
func (r *Rig) branchTargetsOnly(s ports.Store, request authority.Approval, kept *fake.Checkouts, target, planned string) error {
	carried, err := carriedTargets(request)
	if err != nil {
		return err
	}
	if !slices.Equal(carried, []string{target}) {
		return Violatedf("the request carries the targets %v, want only %s", carried, target)
	}
	preview, err := r.repositories(s).RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", true, r.member())
	if err != nil {
		return err
	}
	return outcomesAre(preview, map[string]entryOutcome{
		"api":     {outcome: ledger.AlreadyRegistered, path: "/work/api"},
		"billing": {outcome: ledger.Registered, path: "/work/billing"},
		planned:   {outcome: ledger.Planned, reason: plannedReason(planned)},
	})
}

func checkHandRegisteredCreateEntryIsNeverPlanned(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "search", Path: "../search", Create: "github:acme"},
	}}
	repositories := r.repositories(s)
	for _, dryRun := range []bool{true, false} {
		added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, dryRun, r.member())
		if err != nil {
			return err
		}
		search, found := outcomeNamed(added, "search")
		if !found || search.Outcome != ledger.RegistrationRefused || !strings.Contains(search.Reason, "no git clone is at /work/search") {
			return Violatedf("registering an entry with create by hand, with no clone at its path (dry run %v), reads %+v", dryRun, search)
		}
	}
	kept.Place(cloneOf("search", "search"))
	for _, dryRun := range []bool{true, false} {
		added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, dryRun, r.member())
		if err != nil {
			return err
		}
		search, found := outcomeNamed(added, "search")
		if !found || search.Outcome != ledger.Registered {
			return Violatedf("registering an entry with create by hand, with a clone at its path (dry run %v), reads %+v", dryRun, search)
		}
	}
	if held, err := repositories.Resolve(r.Ctx, r.Scope, "search"); err != nil || held.Name != "search" {
		return Violatedf("the clone registered by hand reads %+v: %v", held, err)
	}
	return r.CheckProjection(s)
}

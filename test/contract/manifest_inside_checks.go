package contract

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// ManifestInsideChecks hold what a manifest path inside the planning repository's clone does (FR-REPO-15): it
// registers from the clone there, the planning repository's own ignore rules decide whether the outcome says more,
// and a path that leaves the directory holding the planning clone, through a link inside it, registers nothing.
var ManifestInsideChecks = []Check{
	{"RM27", "a manifest entry at a path inside the planning repository's clone, as repos/api or repos/payments/api, registers from the clone there with no reason when the planning repository ignores that folder", checkManifestPathsInsideThePlanningClone},
	{"RM28", "a manifest entry inside the planning repository's clone that the planning repository does not ignore registers, and its outcome's reason says the repository does not ignore the path, in a dry run, a registration by hand, and again when the repository is held; an entry beside the clone says nothing", checkManifestPathTheClonesRepositoryDoesNotIgnore},
	{"RM29", "approving a plan registers an entry the planning repository does not ignore, previews it in the same words, and records the reason on the approval, which a second decision reads back unchanged", checkApprovalSaysWhatThePlanningRepositoryDoesNotIgnore},
	{"RM30", "intake reports a manifest path the planning repository's clone does not ignore as a problem of the manifest file and records the file all the same; a machine with no planning clone, and a path the clone ignores, say nothing", checkIntakeReportsWhatThePlanningRepositoryDoesNotIgnore},
	{"RM31", "a symbolic link inside the planning clone that leads outside the directory holding it registers nothing and is refused naming where it leads, and one that leads to a clone inside it registers that clone", checkALinkInsideThePlanningCloneStaysInsideTheBound},
	{"RM32", "on real git repositories, a workspace that ignores repos/ registers the clones in repos/api and repos/payments/api with no reason, one in vendor/ledger with the reason until the workspace ignores vendor/, and a link in repos/ to a clone outside the workspace's directory is refused", checkManifestInsideAWorkspaceOnRealGit},
}

// unignoredReason is what a manifest path inside the planning repository's clone says when that repository does
// not ignore it.
func unignoredReason(at string) string {
	return "the planning repository does not ignore `" + at + "`; add it to `.gitignore`, or its files show as untracked content there and can be committed by mistake"
}

// manifestAt is a manifest of the plan whose entries are each a name and the path of its clone.
func manifestAt(entries ...[2]string) string {
	out := "+++\nkind = \"repositories\"\nid = \"platform\"\ntitle = \"The platform\"\nplan = \"" + manifestPlan + "\"\n"
	for _, entry := range entries {
		out += "\n[[repository]]\nname = \"" + entry[0] + "\"\nremote = \"git@github.com:acme/" + entry[0] + ".git\"\npath = \"" + entry[1] + "\"\n"
	}
	return out + "+++\n\nThe repositories of the platform.\n"
}

// insideClone is a clone on the machine at a path inside the planning clone, /work/plan, whose remote is the
// platform's repository named.
func insideClone(at, name string) ports.Clone {
	return ports.Clone{Path: "/work/plan/" + at, ID: "repo-" + name, Remote: "git@github.com:acme/" + name + ".git", DefaultBranch: "main", Directory: path.Base(at)}
}

func entryAt(name, at string) planning.ManifestRepository {
	return planning.ManifestRepository{Name: name, Remote: "git@github.com:acme/" + name + ".git", Path: at}
}

func checkManifestPathsInsideThePlanningClone(r *Rig) error {
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
	kept.Place(insideClone("repos/api", "gateway"))
	kept.Place(insideClone("repos/payments/api", "payments"))
	kept.Ignore("/work/plan", "repos")
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		entryAt("gateway", "repos/api"), entryAt("payments", "repos/payments/api"),
	}}
	for _, dryRun := range []bool{true, false} {
		added, err := r.repositories(s).RegisterManifest(r.Ctx, r.Scope, manifest, kept, dryRun, r.member())
		if err != nil {
			return err
		}
		for name, at := range map[string]string{"gateway": "/work/plan/repos/api", "payments": "/work/plan/repos/payments/api"} {
			one, ok := outcomeNamed(added, name)
			if !ok || one.Outcome != ledger.Registered || one.Path != at || one.Reason != "" || one.Repository.Name != name {
				return Violatedf("%s, at a path inside the planning clone, reads %+v, dry run %v, want it registered from %s with no reason", name, one, dryRun, at)
			}
		}
	}
	held, err := kept.Of(r.Ctx, r.Scope)
	if err != nil || held["repo-gateway"] != "/work/plan/repos/api" || held["repo-payments"] != "/work/plan/repos/payments/api" {
		return Violatedf("the worker keeps %v: %v", held, err)
	}
	return r.CheckProjection(s)
}

func checkManifestPathTheClonesRepositoryDoesNotIgnore(r *Rig) error {
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
	kept.Place(insideClone("repos/api", "gateway"))
	kept.Place(insideClone("vendor/ledger", "ledger"))
	kept.Place(cloneOf("billing", "billing"))
	kept.Ignore("/work/plan", "repos")
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		entryAt("gateway", "repos/api"), entryAt("ledger", "vendor/ledger"), entryAt("billing", "../billing"),
	}}
	repositories := r.repositories(s)
	says := func(added []control.Added, dryRun bool, outcome ledger.RepositoryOutcome, ledgerSays string) error {
		for name, want := range map[string]string{"gateway": "", "ledger": ledgerSays, "billing": ""} {
			one, ok := outcomeNamed(added, name)
			if !ok || one.Reason != want || (name == "ledger" && one.Outcome != outcome) {
				return Violatedf("%s reads %+v, dry run %v, want reason %q", name, one, dryRun, want)
			}
		}
		return nil
	}
	before, err := r.Revision(s, r.Scope)
	if err != nil {
		return err
	}
	dry, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, true, r.member())
	if err != nil {
		return err
	}
	if err := says(dry, true, ledger.Registered, unignoredReason("vendor/ledger")); err != nil {
		return err
	}
	if after, err := r.Revision(s, r.Scope); err != nil || after != before {
		return Violatedf("a dry run moved the ledger from %d to %d: %v", before, after, err)
	}
	added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	if err := says(added, false, ledger.Registered, unignoredReason("vendor/ledger")); err != nil {
		return err
	}
	if record, err := r.theRepository(s, "repo-ledger"); err != nil || record.Name != "ledger" || record.Removed() {
		return Violatedf("a path the planning repository does not ignore did not register: %+v: %v", record, err)
	}
	// The repository the project holds is no safer there, so a second run says it again, and says nothing once
	// the planning repository ignores the folder.
	again, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	if err := says(again, false, ledger.AlreadyRegistered, unignoredReason("vendor/ledger")); err != nil {
		return err
	}
	kept.Ignore("/work/plan", "vendor")
	ignored, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	if err := says(ignored, false, ledger.AlreadyRegistered, ""); err != nil {
		return err
	}
	return r.CheckProjection(s)
}

func checkApprovalSaysWhatThePlanningRepositoryDoesNotIgnore(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	files.Write("repositories.md", []byte(manifestAt([2]string{"gateway", "repos/api"}, [2]string{"ledger", "vendor/ledger"})))
	if _, err := r.intake(s, files); err != nil {
		return err
	}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	kept.Place(insideClone("repos/api", "gateway"))
	kept.Place(insideClone("vendor/ledger", "ledger"))
	kept.Ignore("/work/plan", "repos")
	request, err := r.theOpenPlanApproval(s)
	if err != nil {
		return err
	}
	repositories := r.repositories(s)
	want := unignoredReason("vendor/ledger")
	preview, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", true, r.member())
	if err != nil {
		return err
	}
	if one, ok := outcomeNamed(preview, "ledger"); !ok || one.Outcome != ledger.Registered || one.Reason != want {
		return Violatedf("the preview says ledger would be %+v, want a reason %q", one, want)
	}
	if _, err := r.approve(s, request, true, r.member()); err != nil {
		return err
	}
	added, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
	if err != nil {
		return err
	}
	if one, ok := outcomeNamed(added, "ledger"); !ok || one.Outcome != ledger.Registered || one.Reason != want {
		return Violatedf("approval registered ledger as %+v, want a reason %q", one, want)
	}
	if one, ok := outcomeNamed(added, "gateway"); !ok || one.Outcome != ledger.Registered || one.Reason != "" {
		return Violatedf("approval registered gateway, in a folder the planning repository ignores, as %+v", one)
	}
	record, err := r.registrationOf(s, request.ID)
	if err != nil || record == nil || len(record.Repositories) != 2 {
		return Violatedf("the approval recorded %+v: %v, want an outcome for each of two repositories", record, err)
	}
	for _, one := range record.Repositories {
		if wanted := map[string]string{"gateway": "", "ledger": want}[one.Name]; one.Reason != wanted {
			return Violatedf("the approval recorded %s with the reason %q, want %q", one.Name, one.Reason, wanted)
		}
	}
	// What the approval recorded is what a second decision reads, whatever the planning repository ignores now.
	kept.Ignore("/work/plan", "vendor")
	replayed, err := repositories.RegisterApproved(r.Ctx, r.Scope, request.ID, kept, nil, "", false, r.member())
	if err != nil {
		return err
	}
	if one, ok := outcomeNamed(replayed, "ledger"); !ok || one.Reason != want {
		return Violatedf("a second decision reads ledger as %+v, want the recorded reason %q", one, want)
	}
	return r.CheckProjection(s)
}

func checkIntakeReportsWhatThePlanningRepositoryDoesNotIgnore(r *Rig) error {
	s := r.Store()
	files, err := r.manifestProject(s)
	if err != nil {
		return err
	}
	if err := r.readsAsPlanning(s, files); err != nil {
		return err
	}
	entries := [][2]string{{"api", "../api"}, {"web", "../web"}, {"gateway", "repos/api"}, {"payments", "repos/payments/api"}, {"ledger", "vendor/ledger"}}
	kept, err := r.planningMachine()
	if err != nil {
		return err
	}
	kept.Ignore("/work/plan", "repos")
	poll := func(machine ports.Checkouts, title string) (control.IntakeReport, error) {
		files.Write("repositories.md", []byte(strings.Replace(manifestAt(entries...), "The platform", title, 1)))
		return control.NewIntake(s).Placing(machine).Poll(r.Ctx, r.Scope, files)
	}
	// A machine that keeps no clone of the planning repository can say nothing of what it ignores.
	report, err := poll(fake.NewCheckouts(), "The platform, on a machine with no clone")
	if err != nil {
		return err
	}
	if problem, found := problemOf(report, "repositories.md"); found || report.Records == 0 {
		return Violatedf("a manifest read where the planning clone is not kept reads %+v, with %d records", problem, report.Records)
	}
	report, err = poll(kept, "The platform, read beside its clone")
	if err != nil {
		return err
	}
	problem, found := problemOf(report, "repositories.md")
	if !found || problem.Detail != unignoredReason("vendor/ledger") || report.Records == 0 {
		return Violatedf("a manifest path the planning repository does not ignore reads %+v, with %d records, want the problem %q", problem, report.Records, unignoredReason("vendor/ledger"))
	}
	if err := r.manifestVersion(s, 2, "api", "web", "gateway", "payments", "ledger"); err != nil {
		return err
	}
	kept.Ignore("/work/plan", "vendor")
	report, err = poll(kept, "The platform, once it ignores vendor")
	if err != nil {
		return err
	}
	if problem, found := problemOf(report, "repositories.md"); found {
		return Violatedf("a manifest whose paths the planning repository ignores reads %+v", problem)
	}
	return r.CheckPlanning(s)
}

func checkALinkInsideThePlanningCloneStaysInsideTheBound(r *Rig) error {
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
	kept.Place(insideClone("repos/api", "gateway"))
	kept.Place(ports.Clone{Path: "/data/elsewhere", ID: "repo-escape", DefaultBranch: "main"})
	kept.Link("/work/plan/repos/escape", "/data/elsewhere")
	kept.Link("/work/plan/repos/alias", "/work/plan/repos/api")
	kept.Ignore("/work/plan", "repos")
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "escape", Path: "repos/escape"}, entryAt("gateway", "repos/alias"),
	}}
	for _, dryRun := range []bool{true, false} {
		added, err := r.repositories(s).RegisterManifest(r.Ctx, r.Scope, manifest, kept, dryRun, r.member())
		if err != nil {
			return err
		}
		escape, ok := outcomeNamed(added, "escape")
		if !ok || escape.Outcome != ledger.RegistrationRefused || !strings.Contains(escape.Reason, "/work/plan/repos/escape resolves to /data/elsewhere, outside /work") {
			return Violatedf("a link inside the planning clone that leads outside the bound reads %+v, dry run %v, want a refusal saying where it leads", escape, dryRun)
		}
		gateway, ok := outcomeNamed(added, "gateway")
		if !ok || gateway.Outcome != ledger.Registered || gateway.Path != "/work/plan/repos/api" || gateway.Reason != "" {
			return Violatedf("a link inside the planning clone that leads to a clone inside it reads %+v, dry run %v", gateway, dryRun)
		}
	}
	if record, err := r.theRepository(s, "repo-escape"); err == nil {
		return Violatedf("a link that leads outside the bound registered %+v", record)
	}
	held, err := kept.Of(r.Ctx, r.Scope)
	if err != nil || held["repo-gateway"] != "/work/plan/repos/api" || len(held) != 2 {
		return Violatedf("the worker keeps %v: %v", held, err)
	}
	return r.CheckProjection(s)
}

// cloneOfItsOwn makes a clone with a history of its own, at a path, and returns its remote.
func cloneOfItsOwn(r *Rig, at string) string {
	origin := gittest.New(r.T)
	origin.Commit("README.txt", at+"\n")
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		r.T.Fatal(err)
	}
	origin.Git("clone", "--quiet", origin.Dir, at)
	return origin.Dir
}

func checkManifestInsideAWorkspaceOnRealGit(r *Rig) error {
	workspace := gittest.New(r.T)
	workspace.Commit(".gitignore", "repos/\n")
	outside := filepath.Join(r.T.TempDir(), "elsewhere")
	cloneOfItsOwn(r, outside)
	for _, at := range []string{"repos/api", "repos/payments/api", "vendor/ledger"} {
		cloneOfItsOwn(r, filepath.Join(workspace.Dir, filepath.FromSlash(at)))
	}
	if err := os.Symlink(outside, filepath.Join(workspace.Dir, "repos", "escape")); err != nil {
		return err
	}
	s := r.Store()
	repositories := r.repositories(s)
	machine := &checkouts.File{Path: filepath.Join(r.T.TempDir(), checkouts.FileName), Env: workspace.Env()}
	if added, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{workspace.Dir}}, machine, r.member()); err != nil || len(added) != 1 || added[0].Outcome != ledger.Registered {
		return Violatedf("registering the workspace reads %+v: %v", added, err)
	}
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "api", Path: "repos/api"}, {Name: "payments-api", Path: "repos/payments/api"}, {Name: "ledger", Path: "vendor/ledger"}, {Name: "escape", Path: "repos/escape"},
	}}
	want := func(added []control.Added, outcome ledger.RepositoryOutcome, ledgerSays string) error {
		for name, reason := range map[string]string{"api": "", "payments-api": "", "ledger": ledgerSays} {
			one, ok := outcomeNamed(added, name)
			if !ok || one.Reason != reason || one.Outcome != outcome {
				return Violatedf("%s reads %+v, want the reason %q", name, one, reason)
			}
		}
		if escape, ok := outcomeNamed(added, "escape"); !ok || escape.Outcome != ledger.RegistrationRefused || !strings.Contains(escape.Reason, "outside") {
			return Violatedf("a link to a clone outside the workspace's directory reads %+v", escape)
		}
		return nil
	}
	added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, machine, false, r.member())
	if err != nil {
		return err
	}
	if err := want(added, ledger.Registered, unignoredReason("vendor/ledger")); err != nil {
		return err
	}
	workspace.Commit(".gitignore", "repos/\nvendor/\n")
	again, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, machine, false, r.member())
	if err != nil {
		return err
	}
	if err := want(again, ledger.AlreadyRegistered, ""); err != nil {
		return err
	}
	return r.CheckProjection(s)
}

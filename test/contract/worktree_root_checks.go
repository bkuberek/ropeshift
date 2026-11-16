package contract

import (
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
)

// WorktreeRootChecks hold what a registration says of the worktree root in force for a clone (FR-PROF-3,
// FR-REPO-15): a root that would lie inside the clone is reported in the outcome's reason, and the clone is
// registered all the same.
var WorktreeRootChecks = []Check{
	{"WR1", "a registration by hand reports a worktree root that would lie inside the clone, still registers the clone, and reports nothing for a root outside it", checkAddReportsARootInsideTheClone},
	{"WR2", "a registration from a manifest reports a worktree root that would lie inside the clone, still registers it, and reports nothing for a root outside it", checkManifestReportsARootInsideTheClone},
}

// insideRoot is a user layer whose relative root lands in the clone it is read for.
const insideRoot = "[worktree]\nroot = \"lanes/{project}\"\n"

// rootNote checks the reason of a registration that reports a root inside its clone.
func rootNote(added control.Added, root string) error {
	if added.Outcome != ledger.Registered {
		return Violatedf("a clone whose worktree root lies inside it reads %+v, and it is registered all the same", added)
	}
	for _, want := range []string{"worktree root " + root, "inside", "worktree_inside_clone", "set `[worktree] root` in your profile to a folder outside every clone, or remove it to use the built-in root"} {
		if !strings.Contains(added.Reason, want) {
			return Violatedf("the reason %q does not say %q", added.Reason, want)
		}
	}
	return nil
}

func checkAddReportsARootInsideTheClone(r *Rig) error {
	s := r.Store()
	profiles := fake.NewProfileSource()
	profiles.PutUser(insideRoot)
	repositories := control.NewRepositories(s).Rooted(profiles)
	kept := fake.NewCheckouts()
	kept.Place(cloneOf("api", "api"))
	added, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/api"}, DryRun: true}, kept, r.member())
	if err != nil || len(added) != 1 {
		return Violatedf("a dry run reads %+v: %v", added, err)
	}
	if err := rootNote(added[0], "/work/api/lanes/"+string(r.Scope)); err != nil {
		return err
	}
	if _, err := r.theRepository(s, "repo-api"); err == nil {
		return Violatedf("a dry run recorded the repository")
	}
	added, err = repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/api"}}, kept, r.member())
	if err != nil || len(added) != 1 {
		return Violatedf("a registration reads %+v: %v", added, err)
	}
	if err := rootNote(added[0], "/work/api/lanes/"+string(r.Scope)); err != nil {
		return err
	}
	if record, err := r.theRepository(s, "repo-api"); err != nil || record.Name != "api" {
		return Violatedf("the repository reads %+v: %v", record, err)
	}
	// The same clone is already registered, and its root is still reported.
	again, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/api"}}, kept, r.member())
	if err != nil || len(again) != 1 || again[0].Outcome != ledger.AlreadyRegistered || !strings.Contains(again[0].Reason, "worktree root") {
		return Violatedf("a second registration reads %+v: %v", again, err)
	}
	// With no root of the user's own, the default lies outside every clone and nothing is said.
	profiles.PutUser("")
	kept.Place(cloneOf("web", "web"))
	web, err := repositories.Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/web"}}, kept, r.member())
	if err != nil || len(web) != 1 || web[0].Outcome != ledger.Registered || web[0].Reason != "" {
		return Violatedf("a clone whose root lies outside it reads %+v: %v", web, err)
	}
	// A registration with no profile source says nothing of a root.
	kept.Place(cloneOf("docs", "docs"))
	plain, err := control.NewRepositories(s).Add(r.Ctx, r.Scope, control.RepositoryAdd{Paths: []string{"/work/docs"}}, kept, r.member())
	if err != nil || len(plain) != 1 || plain[0].Reason != "" {
		return Violatedf("a registration with no profile source reads %+v: %v", plain, err)
	}
	return nil
}

func checkManifestReportsARootInsideTheClone(r *Rig) error {
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
	kept.Place(cloneOf("billing", "billing"))
	kept.Place(cloneOf("ledger", "ledger"))
	manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{
		{Name: "billing", Remote: "git@github.com:acme/billing.git", Path: "../billing"},
	}}
	profiles := fake.NewProfileSource()
	profiles.PutUser(insideRoot)
	repositories := control.NewRepositories(s).Rooted(profiles)
	added, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	billing, ok := outcomeOf(added, "/work/billing")
	if !ok {
		return Violatedf("the manifest's billing reads %+v", added)
	}
	if err := rootNote(billing, "/work/billing/lanes/"+string(r.Scope)); err != nil {
		return err
	}
	if record, err := r.theRepository(s, "repo-billing"); err != nil || record.Name != "billing" {
		return Violatedf("the repository reads %+v: %v", record, err)
	}
	// A manifest entry whose repository is registered already still says where its root would land.
	again, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	if billing, ok := outcomeOf(again, "/work/billing"); !ok || billing.Outcome != ledger.AlreadyRegistered || !strings.Contains(billing.Reason, "worktree root") {
		return Violatedf("a manifest entry already registered reads %+v", billing)
	}
	profiles.PutUser("")
	manifest.Repositories = []planning.ManifestRepository{{Name: "ledger", Remote: "git@github.com:acme/ledger.git", Path: "../ledger"}}
	outside, err := repositories.RegisterManifest(r.Ctx, r.Scope, manifest, kept, false, r.member())
	if err != nil {
		return err
	}
	if ledgerOut, ok := outcomeOf(outside, "/work/ledger"); !ok || ledgerOut.Outcome != ledger.Registered || ledgerOut.Reason != "" {
		return Violatedf("a manifest entry whose root lies outside its clone reads %+v", ledgerOut)
	}
	return nil
}

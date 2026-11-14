package contract

import (
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// ManifestChecks hold what intake reads of a repository manifest (ADR-0022, FR-REPO-8): a document of its
// plan, read only from the planning repository, whose keys beyond the format's have no effect and are
// reported, and which reports the repositories of the project it does not list without removing any.
var ManifestChecks = []Check{
	{"RM1", "a manifest is read only from the planning repository, as a document of its plan with versions", checkManifestIsAPlanDocument},
	{"RM2", "a manifest that tries to set a member's settings changes none, and intake reports each key it ignored", checkManifestSetsNoSetting},
	{"RM3", "a repository of the project the manifest does not list is reported and kept", checkManifestReportsUnlistedRepositories},
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

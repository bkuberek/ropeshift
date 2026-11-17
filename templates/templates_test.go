package templates_test

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/apitest"
)

// dataset is one set of data every template is rendered with: the acme example, and the starting data a
// project with no repositories and one with two get.
type dataset struct {
	name string
	data templates.Data
}

var datasets = []dataset{
	{"example", templates.Data{Example: true}},
	{"no repositories", templates.Data{Project: "orchid"}},
	{"two repositories", templates.Data{Project: "orchid", Repositories: []templates.Repository{
		{Name: "orchid-api", Remote: "git@github.com:orchid/orchid-api.git"},
		{Name: "orchid-web", Remote: "git@github.com:orchid/orchid-web.git"},
	}}},
}

// render returns one template's text, and fails the test when it does not render.
func render(t *testing.T, name string, data templates.Data) string {
	t.Helper()
	text, err := templates.Render(name, data)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return text
}

// A reader is the part of the product that reads one kind of template, called as the product calls it.
type reader struct {
	name string
	// applies says which templates the reader holds.
	applies func(name string) bool
	read    func(t *testing.T, text string) error
	// broken is a deliberately broken copy of a template, each of which the reader must refuse.
	broken []breakage
}

// breakage changes one template's rendered example, which must hold old once.
type breakage struct {
	template, old, new string
}

func named(want ...string) func(string) bool {
	return func(name string) bool { return slices.Contains(want, name) }
}

func prefixed(prefix string) func(string) bool {
	return func(name string) bool { return strings.HasPrefix(name, prefix) }
}

func suffixed(suffixes ...string) func(string) bool {
	return func(name string) bool {
		return slices.ContainsFunc(suffixes, func(suffix string) bool { return strings.HasSuffix(name, suffix) })
	}
}

func always(string) bool { return true }

var gitignores = []string{"workspace/gitignore", "monorepo/gitignore", "repository/gitignore"}

var readers = []reader{
	{
		name:    "a file that starts with +++ parses as a plan file",
		applies: always,
		read: func(_ *testing.T, text string) error {
			return readPlanFile(text)
		},
		broken: []breakage{
			{"workspace/plan/goal.md", `kind = "goal"`, `kind = "goals"`},
			{"workspace/plan/plan.md", `"requirements:acme-accounts"`, `"requirements-acme-accounts"`},
			{"workspace/plan/requirements.md", `id = "QA-1"`, `id = "ACC-1"`},
			{"workspace/plan/contract.md", `repo = "acme-web"`, `repo = ["acme-web"]`},
			{"workspace/plan/repositories.md", `path = "repos/acme-api"`, `path = "/repos/acme-api"`},
			{"workspace/plan/repositories.md", `name = "acme-web"`, `name = "acme-api"`},
			{"workspace/docs/decision.md", `status = "proposed"`, `status = "approved"`},
		},
	},
	{
		name:    "the user profile resolves",
		applies: named("user/profile.toml"),
		read: func(_ *testing.T, text string) error {
			_, err := profiles.Resolve(text)
			return err
		},
		broken: []breakage{
			{"user/profile.toml", "[admission]\n", "[admission]\nmax_lanes_per_sign_inn = 3\n"},
			{"user/profile.toml", "push_on_stop = false", `push_on_stop = "never"`},
		},
	},
	{
		name:    "the repository profile resolves over an empty user layer",
		applies: named("repository/profile.toml"),
		read: func(_ *testing.T, text string) error {
			_, err := profiles.ResolveRepository("", text)
			return err
		},
		broken: []breakage{
			{"repository/profile.toml", "[review]\n", "[review]\nmax_review_roundz = 2\n"},
			{"repository/profile.toml", "[hooks]\n", "[hooks]\non_lane_created = \"make deps\"\n"},
		},
	},
	{
		name:    "a policy is accepted by the decode POST /v1/policies runs",
		applies: prefixed("policies/"),
		read:    readPolicy,
		broken: []breakage{
			{"policies/team/decision-policy.toml", `reversible_default = "assume"` + "\n", ""},
			{"policies/team/lane-policy.toml", `on_one_way_decision = "park"`, `on_one_way_decision = "park"` + "\n" + `on_one_way_decisions = "park"`},
			{"policies/solo/delegation-policy.toml", "version = 2\n", "version = 2\nbogus = true\n"},
			{"policies/team/notify-policy.toml", "desktop = false\n", "desktop = \"no\"\n"},
		},
	},
	{
		name:    "the component descriptor is YAML whose entities each have a name and a description",
		applies: named("repository/catalog-info.yaml"),
		read: func(_ *testing.T, text string) error {
			return readCatalog(text)
		},
		broken: []breakage{
			{"repository/catalog-info.yaml", "type: service", "type: [service"},
			{"repository/catalog-info.yaml", "  name: acme-checkout\n", "  name: \"\"\n"},
			{"repository/catalog-info.yaml", "  description: >-\n", "  description: \"\"\n  notes: >-\n"},
			{"repository/catalog-info.yaml", "  name: acme-payments-client\n", "  slug: acme-payments-client\n"},
		},
	},
	{
		name:    "a YAML template is YAML",
		applies: suffixed(".yaml", ".yml"),
		read: func(_ *testing.T, text string) error {
			return readYAML(text)
		},
		broken: []breakage{
			{"pre-commit-config.yaml", "repos:", "repos: ["},
			{"dependabot.yml", "updates:", "updates: ["},
		},
	},
	{
		name:    "a gitignore ignores .env files and keeps .env.example",
		applies: named(gitignores...),
		read: func(_ *testing.T, text string) error {
			return readIgnore(text)
		},
		broken: []breakage{
			{"workspace/gitignore", "\n.env\n", "\n"},
			{"monorepo/gitignore", "\n.env.*\n", "\n.env.local\n"},
			{"repository/gitignore", "\n!.env.example", "\n"},
		},
	},
}

// readPlanFile reads a template as intake reads a repository file. A file that does not start with +++ is
// no plan file, so nothing reads it.
func readPlanFile(text string) error {
	first, _, _ := strings.Cut(text, "\n")
	if first != "+++" {
		return nil
	}
	doc, err := repofiles.ParseFile([]byte(text))
	if err != nil {
		return err
	}
	if doc.Kind == planning.FileRepositories {
		_, err = repofiles.ParseManifest([]byte(text))
	}
	return err
}

// readPolicy publishes the policy to a daemon on a fresh test store, as a member does with
// `ropeshift policy publish`, which reaches the decode POST /v1/policies runs.
func readPolicy(t *testing.T, text string) error {
	t.Helper()
	daemon := apitest.Start(t)
	_, err := daemon.Client.PublishPolicy(t.Context(), text)
	return err
}

// readYAML decodes every document of a YAML file.
func readYAML(text string) error {
	_, err := yamlDocuments(text)
	return err
}

func yamlDocuments(text string) ([]map[string]any, error) {
	decoder := yaml.NewDecoder(strings.NewReader(text))
	var documents []map[string]any
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			return documents, nil
		}
		if err != nil {
			return nil, err
		}
		if document != nil {
			documents = append(documents, document)
		}
	}
}

// readCatalog reads a component descriptor as far as ropeshift does until the descriptor's reader is built:
// YAML, whose every document names its component and says what it is for.
func readCatalog(text string) error {
	documents, err := yamlDocuments(text)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		return errors.New("no document")
	}
	for i, document := range documents {
		metadata, _ := document["metadata"].(map[string]any)
		for _, key := range []string{"name", "description"} {
			if value, _ := metadata[key].(string); strings.TrimSpace(value) == "" {
				return fmt.Errorf("document %d has no metadata.%s", i+1, key)
			}
		}
	}
	return nil
}

// readIgnore holds the three lines that keep a lane environment's files out of the repository.
func readIgnore(text string) error {
	lines := strings.Split(text, "\n")
	for _, want := range []string{".env", ".env.*", "!.env.example"} {
		if !slices.Contains(lines, want) {
			return fmt.Errorf("no line %q", want)
		}
	}
	return nil
}

func TestEveryTemplateIsReadByItsReader(t *testing.T) {
	read := map[string]bool{}
	for _, name := range templates.Names() {
		for _, set := range datasets {
			text := render(t, name, set.data)
			for _, r := range readers {
				if !r.applies(name) || read[r.name+"\x00"+name+"\x00"+text] {
					continue
				}
				read[r.name+"\x00"+name+"\x00"+text] = true
				if err := r.read(t, text); err != nil {
					t.Errorf("%s with the %s data fails %q: %v", name, set.name, r.name, err)
				}
			}
		}
	}
}

func TestEachReaderRefusesABrokenCopy(t *testing.T) {
	for _, r := range readers {
		for _, b := range r.broken {
			t.Run(r.name+" / "+b.template, func(t *testing.T) {
				text := render(t, b.template, templates.Data{Example: true})
				if !r.applies(b.template) {
					t.Fatalf("%q does not hold %s", r.name, b.template)
				}
				if strings.Count(text, b.old) < 1 {
					t.Fatalf("%s holds no %q to break", b.template, b.old)
				}
				broken := strings.Replace(text, b.old, b.new, 1)
				err := r.read(t, broken)
				if err == nil {
					t.Fatalf("the reader accepted a broken copy of %s", b.template)
				}
				t.Logf("refused: %v", err)
			})
		}
	}
}

// Every template that a person writes into a plan holds its front matter, so a reader that skips a file with
// none cannot skip one of them.
func TestThePlanFilesStartWithFrontMatter(t *testing.T) {
	for _, name := range templates.Names() {
		if !strings.HasPrefix(name, "workspace/plan/") && name != "workspace/docs/decision.md" {
			continue
		}
		for _, set := range datasets {
			if first, _, _ := strings.Cut(render(t, name, set.data), "\n"); first != "+++" {
				t.Errorf("%s with the %s data starts with %q, not +++", name, set.name, first)
			}
		}
	}
}

// unlisted names each template in a directory that names does not list, and each file that is neither a
// template nor one of the files the package keeps beside them.
func unlisted(root fs.FS, names []string) []string {
	var found []string
	_ = fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		switch path {
		case "embed.go", "README.md":
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		name, isTemplate := strings.CutSuffix(path, ".template")
		if !isTemplate || !slices.Contains(names, name) {
			found = append(found, path)
		}
		return nil
	})
	return found
}

func TestEveryTemplateInTheDirectoryIsListed(t *testing.T) {
	if found := unlisted(os.DirFS("."), templates.Names()); len(found) > 0 {
		t.Errorf("not in Names(), or not a template: %v", found)
	}
}

func TestAnUnlistedTemplateIsFound(t *testing.T) {
	copyOf := fstest.MapFS{
		"embed.go":                         {Data: []byte("package templates")},
		"README.md":                        {Data: []byte("# Templates")},
		"templates_test.go":                {Data: []byte("package templates_test")},
		"claude.md.template":               {Data: []byte("@AGENTS.md")},
		"workspace/plan/extra.md.template": {Data: []byte("+++\n")},
		"workspace/notes.md":               {Data: []byte("a file that is no template")},
	}
	got := unlisted(copyOf, []string{"claude.md"})
	want := []string{"workspace/notes.md", "workspace/plan/extra.md.template"}
	if !slices.Equal(got, want) {
		t.Errorf("found %v, want %v", got, want)
	}
}

func TestNamesListsEachTemplateByItsPath(t *testing.T) {
	names := templates.Names()
	if !slices.IsSorted(names) {
		t.Errorf("the names are not sorted: %v", names)
	}
	for _, want := range []string{
		"claude.md", "security.md", "codeowners", "pre-commit-config.yaml", "dependabot.yml",
		"monorepo/agents.md", "monorepo/gitignore", "repository/agents.md", "repository/catalog-info.yaml",
		"repository/gitignore", "repository/profile.toml", "user/profile.toml",
		"workspace/agents.md", "workspace/docs/decision.md", "workspace/docs/glossary.md", "workspace/docs/status.md",
		"workspace/gitignore", "workspace/makefile",
		"workspace/plan/architecture.md", "workspace/plan/contract.md", "workspace/plan/experience-design.md",
		"workspace/plan/goal.md", "workspace/plan/milestone.md", "workspace/plan/plan.md",
		"workspace/plan/repositories.md", "workspace/plan/requirements.md", "workspace/plan/shared-contract.md",
		"workspace/plan/spec.md",
		"policies/solo/delegation-policy.toml", "policies/solo/notify-policy.toml",
		"policies/team/decision-policy.toml", "policies/team/delegate-policy.toml",
		"policies/team/delegation-policy.toml", "policies/team/lane-policy.toml", "policies/team/notify-policy.toml",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("Names() lacks %s", want)
		}
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".template") {
			t.Errorf("%s keeps its suffix", name)
		}
	}
}

func TestAnUnknownTemplateIsAnError(t *testing.T) {
	if _, err := templates.Render("workspace/nothing.md", templates.Data{Example: true}); !errors.Is(err, templates.ErrUnknown) {
		t.Errorf("got %v, want ErrUnknown", err)
	}
}

func TestEveryTemplateEndsWithANewlineAndLeavesNothingUnfilled(t *testing.T) {
	for _, name := range templates.Names() {
		for _, set := range datasets {
			text := render(t, name, set.data)
			if !strings.HasSuffix(text, "\n") {
				t.Errorf("%s with the %s data does not end with a newline", name, set.name)
			}
			for _, left := range []string{"{{", "}}", "<no value>"} {
				if strings.Contains(text, left) {
					t.Errorf("%s with the %s data holds %q", name, set.name, left)
				}
			}
		}
	}
}

// The example is the acme project and nothing else is: starting data for another project leaves no trace of it.
func TestStartingDataNamesTheProjectAndNotAcme(t *testing.T) {
	for _, name := range templates.Names() {
		example := render(t, name, templates.Data{Example: true})
		if strings.Contains(example, "orchid") {
			t.Errorf("%s with the example data holds orchid", name)
		}
		for _, set := range datasets[1:] {
			if text := render(t, name, set.data); strings.Contains(text, "acme") || strings.Contains(text, "Acme") {
				t.Errorf("%s with the %s data holds acme", name, set.name)
			}
		}
	}
}

func TestTheExampleIgnoresTheProjectAndRepositoriesItIsGiven(t *testing.T) {
	given := templates.Data{Example: true, Project: "orchid", Repositories: []templates.Repository{{Name: "orchid-api", Remote: "x"}}}
	for _, name := range templates.Names() {
		if render(t, name, given) != render(t, name, templates.Data{Example: true}) {
			t.Errorf("%s with the example data depends on the project it is given", name)
		}
	}
}

func TestTheExampleHoldsTheAcmeRepositories(t *testing.T) {
	manifest, err := repofiles.ParseManifest([]byte(render(t, "workspace/plan/repositories.md", templates.Data{Example: true})))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, repository := range manifest.Repositories {
		got = append(got, repository.Name+" "+repository.Remote+" "+repository.Path)
	}
	want := []string{
		"acme-api git@github.com:acme/acme-api.git repos/acme-api",
		"acme-web git@github.com:acme/acme-web.git repos/acme-web",
	}
	if manifest.ID != "acme" || !slices.Equal(got, want) {
		t.Errorf("the example manifest %s holds %v, want acme with %v", manifest.ID, got, want)
	}
}

func TestStartingDataNamesEachRepositoryInTheManifestAndTheMakefile(t *testing.T) {
	for _, set := range datasets[1:] {
		manifest, err := repofiles.ParseManifest([]byte(render(t, "workspace/plan/repositories.md", set.data)))
		if err != nil {
			t.Fatalf("%s: %v", set.name, err)
		}
		if manifest.ID != "orchid" || len(manifest.Repositories) != len(set.data.Repositories) {
			t.Fatalf("%s: manifest %s holds %d repositories, want orchid with %d", set.name, manifest.ID, len(manifest.Repositories), len(set.data.Repositories))
		}
		makefile := render(t, "workspace/makefile", set.data)
		_, after, _ := strings.Cut(makefile, "REPOSITORIES :=")
		list, _, _ := strings.Cut(after, "\n\n")
		var want []string
		for i, repository := range set.data.Repositories {
			got := manifest.Repositories[i]
			if got.Name != repository.Name || got.Remote != repository.Remote || got.Path != "repos/"+repository.Name {
				t.Errorf("%s: manifest entry %d is %+v", set.name, i, got)
			}
			want = append(want, repository.Name+"="+repository.Remote)
		}
		if fields := strings.Fields(strings.ReplaceAll(list, "\\\n", " ")); !slices.Equal(fields, want) {
			t.Errorf("%s: the Makefile's REPOSITORIES is %q, want %v", set.name, fields, want)
		}
	}
}

func TestTheStartingPlanHoldsOnlyTheFilesNewWrites(t *testing.T) {
	for _, set := range datasets[1:] {
		goal := parsed(t, "workspace/plan/goal.md", set.data)
		plan := parsed(t, "workspace/plan/plan.md", set.data)
		requirements := parsed(t, "workspace/plan/requirements.md", set.data)
		documents := []string{"requirements:" + requirements.Requirements.ID}
		if len(set.data.Repositories) > 0 {
			documents = append(documents, "repositories:orchid")
		}
		if plan.Plan.Goal != goal.Goal.ID || !slices.Equal(plan.Plan.Documents, documents) || len(plan.Plan.Decisions) != 0 {
			t.Errorf("%s: the plan names goal %q and documents %v, with %d draft decisions; want goal %q and %v, with none",
				set.name, plan.Plan.Goal, plan.Plan.Documents, len(plan.Plan.Decisions), goal.Goal.ID, documents)
		}
		if requirements.Requirements.Plan != plan.Plan.ID {
			t.Errorf("%s: the requirements name plan %q, not %q", set.name, requirements.Requirements.Plan, plan.Plan.ID)
		}
		if len(goal.Goal.SuccessMeasures) != 1 {
			t.Errorf("%s: the goal holds %d success measures, want one placeholder", set.name, len(goal.Goal.SuccessMeasures))
		}
		var ids []string
		for _, requirement := range requirements.Requirements.Requirements {
			ids = append(ids, requirement.ID)
		}
		if len(ids) != 2 || ids[1] != "QA-1" {
			t.Errorf("%s: the requirements are %v, want one placeholder and QA-1", set.name, ids)
		}
	}
}

func parsed(t *testing.T, name string, data templates.Data) planning.Document {
	t.Helper()
	doc, err := repofiles.ParseFile([]byte(render(t, name, data)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return doc
}

func TestTheDecisionRecordIsADecisionDocument(t *testing.T) {
	for _, set := range datasets {
		doc := parsed(t, "workspace/docs/decision.md", set.data)
		if doc.Kind != planning.FileDecision || doc.Decision.ID != "adr-0001" || doc.Decision.Status != planning.DecisionProposed {
			t.Errorf("%s: kind %s, id %q, status %q; want a decision adr-0001 that is proposed", set.name, doc.Kind, doc.Decision.ID, doc.Decision.Status)
		}
	}
}

func TestTheStartingComponentIsOneEntityNamedForTheProject(t *testing.T) {
	for _, set := range datasets[1:] {
		documents, err := yamlDocuments(render(t, "repository/catalog-info.yaml", set.data))
		if err != nil {
			t.Fatal(err)
		}
		if len(documents) != 1 || documents[0]["kind"] != "Component" {
			t.Fatalf("%s: %d documents, want one Component", set.name, len(documents))
		}
		if name := documents[0]["metadata"].(map[string]any)["name"]; name != "orchid" {
			t.Errorf("%s: the component is named %v, want orchid", set.name, name)
		}
	}
}

func TestTheCatalogHeaderSaysTheDescriptorIsAcceptedAndRead(t *testing.T) {
	for _, set := range datasets {
		header, _, _ := strings.Cut(render(t, "repository/catalog-info.yaml", set.data), "\n---\n")
		for _, want := range []string{"ADR-0028 is accepted", "step C0"} {
			if !strings.Contains(header, want) {
				t.Errorf("%s: the header does not say %q", set.name, want)
			}
		}
	}
}

// CODEOWNERS holds no rule until the person names the owners, and says to.
func TestCodeownersHoldsNoRule(t *testing.T) {
	for _, set := range datasets {
		text := render(t, "codeowners", set.data)
		rules := 0
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				rules++
			}
		}
		if rules != 0 || !strings.Contains(text, "# *") || !strings.Contains(text, "Name the owners") {
			t.Errorf("%s: %d rules, and the text is %q", set.name, rules, text)
		}
	}
}

func TestThePreCommitConfigurationRunsTheRepositoryCheckInOneLocalHook(t *testing.T) {
	documents, err := yamlDocuments(render(t, "pre-commit-config.yaml", templates.Data{Project: "orchid"}))
	if err != nil || len(documents) != 1 {
		t.Fatalf("%d documents, %v", len(documents), err)
	}
	repos, _ := documents[0]["repos"].([]any)
	if len(repos) != 1 {
		t.Fatalf("%d repos, want one", len(repos))
	}
	repo := repos[0].(map[string]any)
	hooks, _ := repo["hooks"].([]any)
	if repo["repo"] != "local" || len(hooks) != 1 {
		t.Fatalf("repo %v with %d hooks, want one local hook", repo["repo"], len(hooks))
	}
	if entry, _ := hooks[0].(map[string]any)["entry"].(string); !strings.Contains(entry, "scripts/check") {
		t.Errorf("the hook runs %q, not scripts/check", entry)
	}
}

func TestTheDependabotConfigurationUpdatesAPackageManager(t *testing.T) {
	documents, err := yamlDocuments(render(t, "dependabot.yml", templates.Data{Project: "orchid"}))
	if err != nil || len(documents) != 1 {
		t.Fatalf("%d documents, %v", len(documents), err)
	}
	if documents[0]["version"] != 2 {
		t.Errorf("version %v, want 2", documents[0]["version"])
	}
	if updates, _ := documents[0]["updates"].([]any); len(updates) == 0 {
		t.Error("no updates")
	}
}

package planning_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func TestParseDocumentRef(t *testing.T) {
	cases := []struct {
		ref  string
		kind planning.FileKind
		id   string
		ok   bool
	}{
		{"spec:checkout-api", planning.FileSpec, "checkout-api", true},
		{"architecture:payments", planning.FileArchitecture, "payments", true},
		{"experience-design:checkout-flow", planning.FileExperienceDesign, "checkout-flow", true},
		{"requirements:payments", planning.FileRequirements, "payments", true},
		{"milestone:m1", planning.FileMilestone, "m1", true},
		{"repositories:platform", planning.FileRepositories, "platform", true},
		{"decision:adr-0007", planning.FileDecision, "adr-0007", true},
		// An ID may hold a colon, and only the first one separates the kind.
		{"spec:a:b", planning.FileSpec, "a:b", true},
		{"checkout-api", "", "", false},
		{"spec:", "", "", false},
		{":checkout-api", "", "", false},
		// A contract, a plan, and a goal are records and not documents.
		{"contract:checkout-retry", "", "", false},
		{"plan:payments-v2", "", "", false},
		{"goal:reliable", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.ref, func(t *testing.T) {
			kind, id, ok := planning.ParseDocumentRef(c.ref)
			if kind != c.kind || id != c.id || ok != c.ok {
				t.Errorf("%q reads as %q, %q, %v; want %q, %q, %v", c.ref, kind, id, ok, c.kind, c.id, c.ok)
			}
		})
	}
}

func TestEveryDocumentKindHasAHead(t *testing.T) {
	cases := map[planning.FileKind]planning.Document{
		planning.FileRequirements:     {Requirements: &planning.RequirementsFile{ID: "x", Plan: "p"}},
		planning.FileSpec:             {Spec: &planning.SpecFile{ID: "x", Title: "T", Plan: "p"}},
		planning.FileArchitecture:     {Architecture: &planning.ArchitectureFile{ID: "x", Title: "T", Plan: "p"}},
		planning.FileExperienceDesign: {Experience: &planning.ExperienceDesignFile{ID: "x", Title: "T", Plan: "p"}},
		planning.FileMilestone:        {Milestone: &planning.MilestoneFile{ID: "x", Title: "T", Plan: "p", Priority: 3}},
		planning.FileRepositories:     {Repositories: &planning.RepositoriesFile{ID: "x", Plan: "p", Ignored: []string{"merge_authority"}}},
		planning.FileDecision:         {Decision: &planning.DecisionFile{ID: "x", Title: "T", Plan: "p", Status: planning.DecisionAccepted}},
	}
	for kind, doc := range cases {
		t.Run(string(kind), func(t *testing.T) {
			head, ok := doc.Head()
			if !ok || head.Kind != kind || head.ID != "x" || head.Plan != "p" || head.Ref() != string(kind)+":x" {
				t.Fatalf("head %+v, %v", head, ok)
			}
			if !planning.IsDocumentKind(kind) {
				t.Errorf("%s is no document kind", kind)
			}
			var fields map[string]any
			if err := json.Unmarshal(head.Fields, &fields); err != nil || fields["id"] != "x" {
				t.Errorf("fields %s: %v", head.Fields, err)
			}
		})
	}
	for _, record := range []planning.Document{
		{Goal: &planning.GoalFile{ID: "g"}}, {Plan: &planning.PlanFile{ID: "p"}}, {Contract: &planning.ContractFile{ID: "c"}},
	} {
		if head, ok := record.Head(); ok {
			t.Errorf("a record reads as a document: %+v", head)
		}
	}
	if (planning.DocumentHead{ID: "x"}).Name() != "x" || (planning.DocumentHead{ID: "x", Title: "T"}).Name() != "T" {
		t.Error("a document with no title is called by its ID")
	}
}

// ADR-0022: a manifest path is relative to the planning repository's clone and stays inside the directory
// that holds that clone, so a manifest can name a clone beside it or inside it and no other place on the
// machine. Where each path resolves, symbolic links followed, is read again when it is registered.
func TestAManifestPathStaysInTheDirectoryOfThePlanningClone(t *testing.T) {
	for path, ok := range map[string]bool{
		"../api":                       true,
		"../group/api":                 true,
		"vendor/lib":                   true,
		".":                            true,
		"../api/../web":                true,
		"../..api":                     true,
		"..":                           false,
		"../..":                        false,
		"../../home/user/secret":       false,
		"../api/../../outside":         false,
		"vendor/../../../outside":      false,
		"/home/user/secret":            false,
		"..\\outside":                  false,
		"":                             false,
		"../api\x00":                   false,
		"../../../../../../etc/passwd": false,
	} {
		manifest := planning.RepositoriesFile{ID: "platform", Repositories: []planning.ManifestRepository{{Name: "api", Path: path}}}
		_, err := manifest.Normalize()
		if ok && err != nil {
			t.Errorf("path %q is refused: %v", path, err)
		}
		if !ok && !errors.Is(err, planning.ErrBadFile) {
			t.Errorf("path %q reads %v, want a bad file", path, err)
		}
	}
}

func manifestOf(entries ...planning.ManifestRepository) planning.RepositoriesFile {
	return planning.RepositoriesFile{ID: "platform", Repositories: entries}
}

func TestAManifestEntryThatCreatesItsRepositoryKeepsItsThreeKeys(t *testing.T) {
	manifest, err := manifestOf(
		planning.ManifestRepository{Name: "api", Path: "repos/api", Create: "github:acme", Visibility: "public", DefaultBranch: "trunk"},
		planning.ManifestRepository{Name: "web", Path: "../clients/web.git", Create: "github:Acme-Org"},
		planning.ManifestRepository{Name: "docs", Path: "repos/docs", Remote: "git@github.com:acme/docs.git"},
	).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	api, web, docs := manifest.Repositories[0], manifest.Repositories[1], manifest.Repositories[2]
	if api.Create != "github:acme" || api.Visibility != "public" || api.DefaultBranch != "trunk" {
		t.Errorf("the entry reads %+v", api)
	}
	if got := api.ForgeName(); got != "api" {
		t.Errorf("api names its forge repository %q", got)
	}
	if got := web.ForgeName(); got != "web.git" {
		t.Errorf("web names its forge repository %q, want the path's last element", got)
	}
	if got := docs.ForgeName(); got != "" {
		t.Errorf("an entry that creates nothing names a forge repository %q", got)
	}
	if len(manifest.Ignored) != 0 {
		t.Errorf("the keys land in Ignored: %v", manifest.Ignored)
	}
}

func TestAManifestEntryThatCreatesRefusesEachBadCombination(t *testing.T) {
	create := func(change func(*planning.ManifestRepository)) planning.ManifestRepository {
		entry := planning.ManifestRepository{Name: "api", Path: "repos/api", Create: "github:acme"}
		change(&entry)
		return entry
	}
	cases := []struct {
		name    string
		entries []planning.ManifestRepository
		want    string
	}{
		{"another forge", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Create = "gitlab:acme" })},
			"creating a repository on gitlab is not supported yet; ropeshift creates repositories on GitHub"},
		{"no owner", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Create = "github:" })}, `"github:" is not github:<owner>`},
		{"not a forge and owner", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Create = "acme" })}, `"acme" is not github:<owner>`},
		{"a bad owner", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Create = "github:-acme" })}, "is no GitHub account name"},
		{"create with remote", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Remote = "git@github.com:acme/api.git" })},
			"sets both create and remote"},
		{"visibility without create", []planning.ManifestRepository{{Name: "api", Path: "repos/api", Visibility: "public"}},
			"sets visibility without create"},
		{"default branch without create", []planning.ManifestRepository{{Name: "api", Path: "repos/api", DefaultBranch: "main"}},
			"sets default_branch without create"},
		{"a visibility that is neither", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Visibility = "internal" })},
			`visibility "internal", which is neither private nor public`},
		{"a branch git refuses", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.DefaultBranch = "bad branch" })}, "default_branch"},
		{"the path is the planning clone", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Path = "." })},
			"names no folder to create"},
		{"the path cleans to the planning clone", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Path = "repos/.." })},
			"names no folder to create"},
		{"a forge name GitHub refuses", []planning.ManifestRepository{create(func(e *planning.ManifestRepository) { e.Path = "repos/my repo" })},
			"a GitHub repository name has 1 to 100 letters"},
		{"two entries create one repository in different case", []planning.ManifestRepository{
			create(func(*planning.ManifestRepository) {}),
			{Name: "api2", Path: "repos/API", Create: "github:ACME"},
		}, "both create github.com/acme/api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := manifestOf(c.entries...).Normalize()
			if !errors.Is(err, planning.ErrBadFile) {
				t.Fatalf("the manifest reads %v, want a bad file", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the refusal reads %q, want it to hold %q", err, c.want)
			}
			if !strings.Contains(err.Error(), "manifest platform") || !strings.Contains(err.Error(), "api") {
				t.Errorf("the refusal reads %q, want it to name the manifest and the entry", err)
			}
		})
	}
}

func TestEntriesThatCreateDifferentRepositoriesAreAccepted(t *testing.T) {
	_, err := manifestOf(
		planning.ManifestRepository{Name: "api", Path: "repos/api", Create: "github:acme"},
		planning.ManifestRepository{Name: "other-api", Path: "repos/api", Create: "github:other"},
		planning.ManifestRepository{Name: "web", Path: "repos/web", Create: "github:acme"},
	).Normalize()
	if err != nil {
		t.Fatal(err)
	}
}

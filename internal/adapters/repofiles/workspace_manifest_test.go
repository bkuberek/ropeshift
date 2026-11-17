package repofiles

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

// The manifest an adoption writes is a file of the repository file format, and reads back as the repositories it
// was written from: kind repositories, the project's name for its id, no plan, and for each repository its name,
// remote, and path, with an empty description. A reader that finds nothing wrong with it finds no ignored key.
func TestTheManifestAnAdoptionWritesParsesBack(t *testing.T) {
	listed := []planning.WorkspaceRepository{
		{Name: "web", Remote: "git@example.invalid:acme/web.git", Path: "repos/web"},
		{Name: "api", Remote: "https://example.invalid/acme/api.git", Path: "repos/api"},
		{Name: "deep", Remote: "git@example.invalid:acme/deep.git", Path: "repos/group/deep"},
		{Name: "local", Path: "repos/local"},
	}
	doc, err := ParseFile([]byte(planning.RenderRepositoriesFile("shop-app", listed)))
	if err != nil {
		t.Fatalf("the manifest does not parse: %v", err)
	}
	manifest := doc.Repositories
	if doc.Kind != planning.FileRepositories || manifest == nil {
		t.Fatalf("the file reads as kind %q, with manifest %+v", doc.Kind, manifest)
	}
	if manifest.ID != "shop-app" || manifest.Plan != "" || manifest.Title != "" || len(manifest.Ignored) != 0 {
		t.Errorf("the manifest reads id %q, plan %q, title %q, ignored %v", manifest.ID, manifest.Plan, manifest.Title, manifest.Ignored)
	}
	var got []planning.ManifestRepository
	got = append(got, manifest.Repositories...)
	want := []planning.ManifestRepository{
		{Name: "api", Remote: "https://example.invalid/acme/api.git", Path: "repos/api"},
		{Name: "deep", Remote: "git@example.invalid:acme/deep.git", Path: "repos/group/deep"},
		{Name: "local", Path: "repos/local"},
		{Name: "web", Remote: "git@example.invalid:acme/web.git", Path: "repos/web"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("the manifest lists %+v, want %+v", got, want)
	}
	head, ok := doc.Head()
	if !ok || head.Ref() != "repositories:shop-app" {
		t.Errorf("the manifest's reference reads %+v, and a plan lists it in its documents by it", head)
	}
}

// A manifest with no repository is still a manifest, so a workspace that has none yet can write one.
func TestTheManifestOfNoRepositoriesParses(t *testing.T) {
	manifest, err := ParseManifest([]byte(planning.RenderRepositoriesFile("shop", nil)))
	if err != nil || manifest.ID != "shop" || len(manifest.Repositories) != 0 {
		t.Errorf("a manifest with no repository reads %+v: %v", manifest, err)
	}
}

package repofiles

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/test/gittest"
)

// mergedWorkspace is a workspace that merged an unrelated history whose first commit sorts lower than its own.
func mergedWorkspace(t *testing.T) (workspace *gittest.Repo, own, imported string) {
	t.Helper()
	workspace, design := gittest.NewUnrelatedPair(t)
	own, imported = workspace.Roots("HEAD")[0], design.Roots("HEAD")[0]
	workspace.MergeUnrelated(design)
	if got := workspace.Roots("HEAD"); !slices.Equal(got, []string{imported, own}) {
		t.Fatalf("the merged history has roots %v, want %v", got, []string{imported, own})
	}
	return workspace, own, imported
}

func instanceOf(t *testing.T, repo *gittest.Repo, identity string) string {
	t.Helper()
	instance, err := New(Config{Repository: repo.Dir, Env: repo.Env(), Identity: identity}).Instance(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

// FR-REPO-14: merging an unrelated history into a repository never changes its identity.
func TestMergingAnUnrelatedHistoryKeepsTheFirstParentRoot(t *testing.T) {
	workspace, own, imported := mergedWorkspace(t)
	if got := instanceOf(t, workspace, ""); got != own {
		t.Errorf("the instance is %s, want the first-parent root %s and not the lowest root %s", got, own, imported)
	}
}

func TestARepositoryWithOneHistoryIsNamedByItsFirstCommit(t *testing.T) {
	repo := gittest.New(t)
	root := repo.Commit("README.md", "one\n")
	repo.Commit("README.md", "two\n")
	if got := instanceOf(t, repo, ""); got != root {
		t.Errorf("the instance is %s, want %s", got, root)
	}
}

func TestRootsListsEveryFirstCommitSorted(t *testing.T) {
	workspace, own, imported := mergedWorkspace(t)
	got, err := New(Config{Repository: workspace.Dir, Env: workspace.Env()}).Roots(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{imported, own}; !slices.Equal(got, want) {
		t.Errorf("the roots are %v, want %v", got, want)
	}
}

// A record stored under the lowest-root rule keeps its ID, so every external key intake made under it stays.
func TestAnIdentityAmongTheRootsIsTheInstance(t *testing.T) {
	workspace, own, imported := mergedWorkspace(t)
	if got := instanceOf(t, workspace, imported); got != imported {
		t.Errorf("with the lowest root as the identity the instance is %s, want %s", got, imported)
	}
	if got := instanceOf(t, workspace, own); got != own {
		t.Errorf("with the first-parent root as the identity the instance is %s, want %s", got, own)
	}
}

func TestAnIdentityThatIsNoRootOfTheRepositoryIsIgnored(t *testing.T) {
	workspace, own, _ := mergedWorkspace(t)
	if got := instanceOf(t, workspace, "0123456789abcdef0123456789abcdef01234567"); got != own {
		t.Errorf("the instance is %s, want the first-parent root %s", got, own)
	}
}

func TestTheDescriptionNamesTheRepositoryByTheInstance(t *testing.T) {
	workspace, _, imported := mergedWorkspace(t)
	description, err := New(Config{Repository: workspace.Dir, Env: workspace.Env(), Identity: imported}).Describe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if description.Instance != imported {
		t.Errorf("the description names %s, want %s", description.Instance, imported)
	}
}

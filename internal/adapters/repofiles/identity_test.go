package repofiles

import (
	"context"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
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

// A repository read from a path no checkout is kept for is named by the record the project holds for it, in the
// order registration matches a clone.
func TestARepositoryWithNoIdentityIsNamedByTheRecordTheProjectHolds(t *testing.T) {
	workspace, own, imported := mergedWorkspace(t)
	records := func(held ...ledger.Repository) func(context.Context, string) (ledger.Repository, bool, error) {
		return func(_ context.Context, id string) (ledger.Repository, bool, error) {
			for _, record := range held {
				if record.ID == id {
					return record, true, nil
				}
			}
			return ledger.Repository{}, false, nil
		}
	}
	removed := ledger.NewRepository(imported)
	removed.State = ledger.RepositoryRemoved
	for name, tt := range map[string]struct {
		held []ledger.Repository
		want string
	}{
		"a record under the lowest root":           {[]ledger.Repository{ledger.NewRepository(imported)}, imported},
		"a record under each root names the first": {[]ledger.Repository{ledger.NewRepository(imported), ledger.NewRepository(own)}, own},
		"no record":                                   {nil, own},
		"a removed record under the lowest root":      {[]ledger.Repository{removed}, own},
		"a removed record is not matched by its root": {[]ledger.Repository{removed, ledger.NewRepository(own)}, own},
	} {
		t.Run(name, func(t *testing.T) {
			source := New(Config{Repository: workspace.Dir, Env: workspace.Env(), Records: records(tt.held...)})
			if got, err := source.Instance(t.Context()); err != nil || got != tt.want {
				t.Errorf("the instance is %s (%v), want %s", got, err, tt.want)
			}
		})
	}
}

// A record with another remote than the repository's is another repository, so the repository is not read under
// its ID unless this worker keeps a clone of that record elsewhere, which registration refuses as sharing a first
// commit.
func TestARecordWithAnotherRemoteIsNotNamedByTheLowestRoot(t *testing.T) {
	workspace, own, imported := mergedWorkspace(t)
	workspace.Git("remote", "add", "origin", "git@example.invalid:acme/workspace.git")
	record := func(remote string) func(context.Context, string) (ledger.Repository, bool, error) {
		held := ledger.NewRepository(imported)
		held.Remote = remote
		return func(_ context.Context, id string) (ledger.Repository, bool, error) {
			return held, id == imported, nil
		}
	}
	elsewhere := func(kept bool) func(context.Context, string) (bool, error) {
		return func(context.Context, string) (bool, error) { return kept, nil }
	}
	for name, tt := range map[string]struct {
		remote string
		kept   func(context.Context, string) (bool, error)
		want   string
	}{
		"another remote and no clone kept":              {"git@example.invalid:acme/design.git", nil, own},
		"another remote and a clone not kept elsewhere": {"git@example.invalid:acme/design.git", elsewhere(false), own},
		"another remote and a clone kept elsewhere":     {"git@example.invalid:acme/design.git", elsewhere(true), imported},
		"the same remote in another spelling":           {"https://example.invalid/acme/workspace", nil, imported},
		"a record with no remote":                       {"", nil, imported},
	} {
		t.Run(name, func(t *testing.T) {
			source := New(Config{Repository: workspace.Dir, Env: workspace.Env(), Records: record(tt.remote), KeptElsewhere: tt.kept})
			if got, err := source.Instance(t.Context()); err != nil || got != tt.want {
				t.Errorf("the instance is %s (%v), want %s", got, err, tt.want)
			}
		})
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

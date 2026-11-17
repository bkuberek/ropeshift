package repofiles

import (
	"context"
	"errors"
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

// legacy is a record made before a repository's identity was its first-parent root, which a clone matches by any
// of its roots.
func legacyRecord(id string) ledger.Repository {
	record := ledger.NewRepository(id)
	record.IdentityLegacy = true
	return record
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
	removed := legacyRecord(imported)
	removed.State = ledger.RepositoryRemoved
	for name, tt := range map[string]struct {
		held []ledger.Repository
		want string
	}{
		"a legacy record under the lowest root":     {[]ledger.Repository{legacyRecord(imported)}, imported},
		"a record made since under the lowest root": {[]ledger.Repository{ledger.NewRepository(imported)}, own},
		"a record under each root names the first":  {[]ledger.Repository{legacyRecord(imported), ledger.NewRepository(own)}, own},
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
		held := legacyRecord(imported)
		held.Remote = remote
		return func(_ context.Context, id string) (ledger.Repository, bool, error) {
			return held, id == imported, nil
		}
	}
	for name, tt := range map[string]struct {
		remote string
		kept   func(context.Context, string) (Kept, bool, error)
		want   string
	}{
		"another remote and no clone kept":              {"git@example.invalid:acme/design.git", nil, own},
		"another remote and a clone not kept elsewhere": {"git@example.invalid:acme/design.git", keptAt("", ""), own},
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

// keptAt is the clone this worker keeps of a record at a path with a remote, or none when the path is empty.
func keptAt(path, remote string) func(context.Context, string) (Kept, bool, error) {
	return func(context.Context, string) (Kept, bool, error) {
		return Kept{Path: path, Remote: remote}, path != "", nil
	}
}

// A repository that shares its first commit with a clone this worker keeps at another path, with another remote
// or none, is not read: registration refuses it for that reason, and reading it under the record's ID would put
// two repositories under one ID. Two clones of one remote are one repository.
func TestARepositoryThatSharesAFirstCommitWithAKeptCloneIsNotRead(t *testing.T) {
	workspace, _, imported := mergedWorkspace(t)
	records := func(remote string) func(context.Context, string) (ledger.Repository, bool, error) {
		held := legacyRecord(imported)
		held.Name, held.Remote = "design", remote
		return func(_ context.Context, id string) (ledger.Repository, bool, error) { return held, id == imported, nil }
	}
	const design, workspaceRemote = "git@example.invalid:acme/design.git", "git@example.invalid:acme/workspace.git"
	for name, tt := range map[string]struct {
		own, record, kept string
		refused           bool
	}{
		"another remote, kept elsewhere":          {workspaceRemote, design, design, true},
		"the same remote in another spelling":     {workspaceRemote, workspaceRemote, "https://example.invalid/acme/workspace", false},
		"no remote on either, kept elsewhere":     {"", "", "", true},
		"a remote here and none on the kept one":  {workspaceRemote, workspaceRemote, "", true},
		"a record with no remote, a kept remote":  {workspaceRemote, "", design, true},
		"a remote on both and the kept one's own": {workspaceRemote, workspaceRemote, workspaceRemote, false},
	} {
		t.Run(name, func(t *testing.T) {
			if tt.own != "" {
				workspace.Git("remote", "add", "origin", tt.own)
				defer workspace.Git("remote", "remove", "origin")
			}
			source := New(Config{Repository: workspace.Dir, Env: workspace.Env(), Records: records(tt.record), KeptElsewhere: keptAt("/work/design", tt.kept)})
			got, err := source.Instance(t.Context())
			var shared *SharedFirstCommitError
			switch {
			case tt.refused && (!errors.As(err, &shared) || shared.Root != imported || shared.Name != "design" || shared.Path != "/work/design"):
				t.Errorf("the instance is %s (%v), want a refusal naming the design repository", got, err)
			case !tt.refused && (err != nil || got != imported):
				t.Errorf("the instance is %s (%v), want %s", got, err, imported)
			}
		})
	}
}

// A lower root merged in after a record was registered under the lowest root it had leaves the lowest root with
// no record, and the repository is still that record's.
func TestARepositoryIsNamedByAnyRootThatNamesItsRecord(t *testing.T) {
	workspace, design := gittest.NewUnrelatedPair(t)
	own, legacy := workspace.Roots("HEAD")[0], design.Roots("HEAD")[0]
	lower := gittest.NewWithRootBelow(t, own, legacy)
	workspace.MergeUnrelated(design)
	workspace.MergeUnrelated(lower)
	workspace.Git("remote", "add", "origin", "git@example.invalid:acme/app.git")
	record := func(id, remote string) func(context.Context, string) (ledger.Repository, bool, error) {
		held := legacyRecord(id)
		held.Remote = remote
		return func(_ context.Context, asked string) (ledger.Repository, bool, error) { return held, asked == id, nil }
	}
	for name, tt := range map[string]struct {
		records func(context.Context, string) (ledger.Repository, bool, error)
		want    string
	}{
		"a record under a middle root":            {record(legacy, "git@example.invalid:acme/app.git"), legacy},
		"a record under a middle root of another": {record(legacy, "git@example.invalid:acme/other.git"), own},
		"no record": {record("unrelated", ""), own},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := New(Config{Repository: workspace.Dir, Env: workspace.Env(), Records: tt.records}).Instance(t.Context())
			if err != nil || got != tt.want {
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

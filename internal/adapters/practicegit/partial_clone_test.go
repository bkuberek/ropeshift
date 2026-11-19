package practicegit_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/test/gittest"
)

// blobFilteredClone makes a clone of origin beside it that holds every commit and tree and only the blobs of the
// commit it checks out, as `git clone --filter=blob:none` does. Every other blob is missing, and git fetches one from
// origin when a command asks for it.
func blobFilteredClone(t *testing.T, origin *gittest.Repo, name string) *gittest.Repo {
	t.Helper()
	origin.Git("config", "uploadpack.allowFilter", "true")
	dir := filepath.Join(filepath.Dir(origin.Dir), name)
	origin.Git("clone", "--quiet", "--filter=blob:none", "file://"+filepath.ToSlash(origin.Dir), dir)
	return &gittest.Repo{T: t, Dir: dir, Home: origin.Home}
}

// missingObjects lists the objects reachable in the repository that it does not hold. `--missing=print` names them
// and never fetches them.
func missingObjects(t *testing.T, repo *gittest.Repo) []string {
	t.Helper()
	var missing []string
	for line := range strings.SplitSeq(repo.Git("rev-list", "--objects", "--missing=print", "--all"), "\n") {
		if strings.HasPrefix(line, "?") {
			missing = append(missing, line)
		}
	}
	slices.Sort(missing)
	return missing
}

// partialOrigin is a repository whose first commit holds a secret and a .gitignore that its second commit replaces.
func partialOrigin(t *testing.T) (origin *gittest.Repo, first string) {
	t.Helper()
	origin = gittest.New(t)
	origin.Commit(".gitignore", ".env\n")
	first = origin.Commit("config.txt", secretScanTokenA+"\n")
	origin.Commit(".gitignore", "*.log\n")
	origin.Commit("config.txt", "clean\n")
	return origin, first
}

func TestSecretScanOfAPartialCloneIsPartialAndFetchesNothing(t *testing.T) {
	t.Parallel()
	origin, _ := partialOrigin(t)
	clone := blobFilteredClone(t, origin, "partial")
	before := missingObjects(t, clone)
	if len(before) == 0 {
		t.Fatal("the clone is missing no object, so it shows nothing")
	}
	got := secretScan(t, clone, "HEAD", "")
	if !strings.Contains(got.Partial, "partial clone") {
		t.Errorf("the scan of a partial clone says %q, want it partial", got.Partial)
	}
	if after := missingObjects(t, clone); !slices.Equal(before, after) {
		t.Errorf("the scan fetched %d of the %d missing objects", len(before)-len(after), len(before))
	}
}

func TestSecretScanSaysAPartialCloneIsPartialByEitherSetting(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		set     [][]string
		partial bool
	}{
		"the partial clone extension":  {[][]string{{"extensions.partialClone", "origin"}}, true},
		"a promisor remote":            {[][]string{{"remote.origin.promisor", "true"}}, true},
		"a promisor remote set as yes": {[][]string{{"remote.upstream.promisor", "yes"}}, true},
		"a remote that is no promisor": {[][]string{{"remote.origin.promisor", "false"}}, false},
		"neither":                      {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			origin := gittest.New(t)
			origin.Commit("a.txt", "clean\n")
			clone := origin.Clone("clone")
			for _, setting := range tt.set {
				clone.Git("config", setting[0], setting[1])
			}
			got := secretScan(t, clone, "HEAD", "")
			if partial := got.Partial != ""; partial != tt.partial {
				t.Errorf("partial = %v (%q), want %v", partial, got.Partial, tt.partial)
			}
		})
	}
}

func TestSnapshotOfAPartialCloneFetchesNothingAndReadsNoBlob(t *testing.T) {
	t.Parallel()
	origin, first := partialOrigin(t)
	clone := blobFilteredClone(t, origin, "partial")
	before := missingObjects(t, clone)
	if len(before) == 0 {
		t.Fatal("the clone is missing no object, so it shows nothing")
	}
	snapshot := snapshotOf(t, clone, first)
	if !slices.Contains(snapshot.Paths, "config.txt") || !slices.Contains(snapshot.Paths, ".gitignore") {
		t.Errorf("the snapshot lists %v, want the tracked paths", snapshot.Paths)
	}
	if text, ok := snapshot.Read("config.txt"); ok {
		t.Errorf("the snapshot read %q of a blob the clone does not hold", text)
	}
	if len(snapshot.Ignored) != 0 {
		t.Errorf("the snapshot answers %v for the ignore probes, want no answer", snapshot.Ignored)
	}
	if after := missingObjects(t, clone); !slices.Equal(before, after) {
		t.Errorf("the snapshot fetched %d of the %d missing objects", len(before)-len(after), len(before))
	}
}

func TestSnapshotOfAFullCloneStillReadsItsBlobs(t *testing.T) {
	t.Parallel()
	origin, first := partialOrigin(t)
	snapshot, err := practicegit.Snapshots{Env: origin.Env()}.Snapshot(t.Context(), origin.Dir, first)
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := snapshot.Read("config.txt"); !ok || !strings.Contains(text, secretScanTokenA) {
		t.Errorf("the snapshot read %q, %v", text, ok)
	}
	if ignored, ok := snapshot.Ignored[".env"]; !ok || !ignored {
		t.Errorf("the snapshot answers %v for .env, want ignored", snapshot.Ignored)
	}
}

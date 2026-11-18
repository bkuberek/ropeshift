package practicegit_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/gittest"
)

var _ ports.PracticeSnapshots = practicegit.Snapshots{}

// snapshotOf reads the snapshot of a commit or branch of a test repository with the repository's own git
// environment.
func snapshotOf(t *testing.T, repo *gittest.Repo, commit string) practices.Snapshot {
	t.Helper()
	snapshot, err := practicegit.Snapshots{Env: repo.Env()}.Snapshot(t.Context(), repo.Dir, commit)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotIgnoreAnswers(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		probe string
		want  bool
	}{
		{
			name:  "a root gitignore ignores the root env file",
			files: map[string]string{".gitignore": ".env\n", "README.md": "x\n"},
			probe: ".env",
			want:  true,
		},
		{
			name: "a nested gitignore ignores the env file of its folder",
			files: map[string]string{
				"web/.gitignore":   ".env\n",
				"web/package.json": `{"dependencies":{"left-pad":"1.0.0"}}`,
			},
			probe: "web/.env",
			want:  true,
		},
		{
			name: "a nested gitignore does not reach the root",
			files: map[string]string{
				"web/.gitignore":   ".env\n",
				"web/package.json": `{"dependencies":{"left-pad":"1.0.0"}}`,
			},
			probe: ".env",
			want:  false,
		},
		{
			name:  "no gitignore leaves the env file unignored",
			files: map[string]string{".gitignore": "*.log\n", "README.md": "x\n"},
			probe: ".env",
			want:  false,
		},
		{
			name:  "a negation unignores one env file",
			files: map[string]string{".gitignore": ".env*\n!.env.local\n", "README.md": "x\n"},
			probe: ".env.local",
			want:  false,
		},
		{
			name:  "a negation leaves the others ignored",
			files: map[string]string{".gitignore": ".env*\n!.env.local\n", "README.md": "x\n"},
			probe: ".env.production",
			want:  true,
		},
		{
			name: "a folder pattern ignores the env file inside it",
			files: map[string]string{
				".gitignore":       "web/\n",
				"web/package.json": `{"dependencies":{"left-pad":"1.0.0"}}`,
			},
			probe: "web/.env",
			want:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := gittest.New(t)
			for name, content := range tt.files {
				writeFile(t, filepath.Join(repo.Dir, name), content)
			}
			repo.Git("add", "-f", "--", ".")
			repo.Git("commit", "--quiet", "-m", "files")
			snapshot := snapshotOf(t, repo, repo.Git("rev-parse", "HEAD"))
			got, ok := snapshot.Ignored[tt.probe]
			if !ok {
				t.Fatalf("no answer for %s in %v", tt.probe, snapshot.Ignored)
			}
			if got != tt.want {
				t.Fatalf("Ignored[%s] = %v, want %v", tt.probe, got, tt.want)
			}
		})
	}
}

func TestSnapshotAnswersEveryProbe(t *testing.T) {
	repo := gittest.New(t)
	writeFile(t, filepath.Join(repo.Dir, ".gitignore"), ".env\n")
	writeFile(t, filepath.Join(repo.Dir, "web/package.json"), `{"dependencies":{"left-pad":"1.0.0"}}`)
	repo.Git("add", "--", ".")
	repo.Git("commit", "--quiet", "-m", "files")
	snapshot := snapshotOf(t, repo, repo.Git("rev-parse", "HEAD"))
	probes := practices.Probes(snapshot.Paths)
	if len(probes) == 0 {
		t.Fatal("no probes")
	}
	for _, probe := range probes {
		if _, ok := snapshot.Ignored[probe]; !ok {
			t.Errorf("no answer for probe %s", probe)
		}
	}
	if len(snapshot.Ignored) != len(probes) {
		t.Errorf("answers for %d paths, want %d", len(snapshot.Ignored), len(probes))
	}
}

func TestSnapshotIgnoresNoGlobalExcludes(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit(".gitignore", "*.log\n")
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "ignore"), ".env\n")
	writeFile(t, filepath.Join(elsewhere, "gitconfig"), "[core]\n\texcludesFile = "+filepath.Join(elsewhere, "ignore")+"\n")
	writeFile(t, filepath.Join(elsewhere, "git", "ignore"), ".env\n")
	writeFile(t, filepath.Join(repo.Dir, ".git", "info", "exclude"), ".env\n")
	t.Setenv("HOME", elsewhere)
	t.Setenv("XDG_CONFIG_HOME", elsewhere)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(elsewhere, "gitconfig"))
	// The adapter is given no environment, so it runs with this process's, which names the excludes.
	snapshot, err := practicegit.Snapshots{}.Snapshot(t.Context(), repo.Dir, repo.Git("rev-parse", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	if ignored, ok := snapshot.Ignored[".env"]; !ok || ignored {
		t.Fatalf("Ignored[.env] = %v (answered %v), want false: a global or info/exclude rule counted", ignored, ok)
	}
}

func TestSnapshotListsATrackedEnvFile(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "x\n")
	repo.Commit(".env", "TOKEN=abc\n")
	repo.Commit("docs/my notesé.md", "x\n")
	snapshot := snapshotOf(t, repo, repo.Git("rev-parse", "HEAD"))
	for _, want := range []string{".env", "README.md", "docs/my notesé.md"} {
		if !slices.Contains(snapshot.Paths, want) {
			t.Errorf("Paths = %v, want it to hold %q", snapshot.Paths, want)
		}
	}
}

func TestSnapshotReadsTheCommittedGitignoreNotTheWorkingTree(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit(".gitignore", ".env\n")
	commit := repo.Commit("README.md", "x\n")
	writeFile(t, filepath.Join(repo.Dir, ".gitignore"), "# nothing is ignored now\n")
	writeFile(t, filepath.Join(repo.Dir, ".env"), "TOKEN=abc\n")
	snapshot := snapshotOf(t, repo, commit)
	if !snapshot.Ignored[".env"] {
		t.Fatal("the uncommitted edit of .gitignore counted; want the committed one")
	}

	repo.Git("add", "--", ".gitignore")
	repo.Git("commit", "--quiet", "-m", "stop ignoring")
	if snapshotOf(t, repo, commit).Ignored[".env"] != true {
		t.Fatal("a later commit changed the answer for an earlier one")
	}
	if snapshotOf(t, repo, repo.Git("rev-parse", "HEAD")).Ignored[".env"] {
		t.Fatal("the new commit's .gitignore was not read")
	}
}

func TestSnapshotResolvesTheBaseBranch(t *testing.T) {
	t.Run("origin's branch is preferred", func(t *testing.T) {
		upstream := gittest.New(t)
		upstream.Commit("README.md", "one\n")
		clone := upstream.Clone("clone")
		remoteTip := upstream.Commit("CHANGELOG.md", "two\n")
		clone.Git("fetch", "--quiet", "origin")
		localTip := clone.Commit("LOCAL.md", "local\n")
		if localTip == remoteTip {
			t.Fatal("the test needs two different tips")
		}
		snapshot := snapshotOf(t, clone, "main")
		if snapshot.Commit != remoteTip {
			t.Fatalf("Commit = %s, want origin's tip %s", snapshot.Commit, remoteTip)
		}
		if !slices.Contains(snapshot.Paths, "CHANGELOG.md") || slices.Contains(snapshot.Paths, "LOCAL.md") {
			t.Fatalf("Paths = %v, want origin's tree", snapshot.Paths)
		}
	})
	t.Run("the local branch is used when the remote has none", func(t *testing.T) {
		repo := gittest.New(t)
		tip := repo.Commit("README.md", "one\n")
		snapshot := snapshotOf(t, repo, "main")
		if snapshot.Commit != tip {
			t.Fatalf("Commit = %s, want %s", snapshot.Commit, tip)
		}
	})
	t.Run("a commit ID names itself", func(t *testing.T) {
		repo := gittest.New(t)
		first := repo.Commit("README.md", "one\n")
		repo.Commit("NEXT.md", "two\n")
		snapshot := snapshotOf(t, repo, first)
		if snapshot.Commit != first {
			t.Fatalf("Commit = %s, want %s", snapshot.Commit, first)
		}
		if slices.Contains(snapshot.Paths, "NEXT.md") {
			t.Fatalf("Paths = %v, want the first commit's tree", snapshot.Paths)
		}
	})
	t.Run("a name nothing holds is not found", func(t *testing.T) {
		repo := gittest.New(t)
		repo.Commit("README.md", "one\n")
		_, err := practicegit.Snapshots{Env: repo.Env()}.Snapshot(t.Context(), repo.Dir, "missing")
		if !errors.Is(err, ports.ErrNotFound) {
			t.Fatalf("err = %v, want ports.ErrNotFound", err)
		}
	})
}

func TestSnapshotReadsFilesWhenAsked(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "hello\n")
	commit := repo.Commit("docs/guide.md", "a guide\n")
	writeFile(t, filepath.Join(repo.Dir, "README.md"), "edited in the working tree\n")
	snapshot := snapshotOf(t, repo, commit)

	got, ok := snapshot.Read("README.md")
	if !ok || got != "hello\n" {
		t.Fatalf("Read(README.md) = %q, %v; want the committed text", got, ok)
	}
	if got, ok := snapshot.Read("docs/guide.md"); !ok || got != "a guide\n" {
		t.Fatalf("Read(docs/guide.md) = %q, %v", got, ok)
	}
	if got, ok := snapshot.Read("missing.md"); ok || got != "" {
		t.Fatalf("Read(missing.md) = %q, %v; want false", got, ok)
	}
	if _, ok := snapshot.Read("docs"); ok {
		t.Fatal("Read of a folder answered true")
	}
}

func TestSnapshotRemovesItsTemporaryRepository(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	repo := gittest.New(t)
	repo.Commit(".gitignore", ".env\n")
	repo.Commit("web/.gitignore", "*.local\n")
	snapshot := snapshotOf(t, repo, "main")
	if !snapshot.Ignored[".env"] {
		t.Fatal("the check did not run")
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the snapshot left %d entries in the temporary directory", len(entries))
	}
}

func TestSnapshotRunsOnlyReadOnlyCommandsOnTheCheckout(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit(".gitignore", ".env\n")
	commit := repo.Commit("README.md", "x\n")
	before := repo.Git("status", "--porcelain=v2", "--branch")
	refs := repo.Git("for-each-ref")
	snapshot := snapshotOf(t, repo, commit)
	_, _ = snapshot.Read("README.md")
	if after := repo.Git("status", "--porcelain=v2", "--branch"); after != before {
		t.Fatalf("status changed:\n%s\nto:\n%s", before, after)
	}
	if after := repo.Git("for-each-ref"); after != refs {
		t.Fatal("the refs changed")
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".git", "index.lock")); err == nil {
		t.Fatal("an index lock was left behind")
	}
}

// treeEntry is one entry of a tree object built by hand.
type treeEntry struct {
	mode, name, id string
}

// hashObject stores an object without the checks git applies to a tree it writes, and returns its ID.
func hashObject(t *testing.T, repo *gittest.Repo, kind string, content []byte) string {
	t.Helper()
	cmd := exec.Command("git", "hash-object", "-t", kind, "-w", "--literally", "--stdin")
	cmd.Dir, cmd.Env, cmd.Stdin = repo.Dir, repo.Env(), bytes.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git hash-object: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// craftedTree stores a tree with exactly these entries in this order, whatever their names.
func craftedTree(t *testing.T, repo *gittest.Repo, entries ...treeEntry) string {
	t.Helper()
	var buf bytes.Buffer
	for _, e := range entries {
		raw, err := hex.DecodeString(e.id)
		if err != nil {
			t.Fatal(err)
		}
		buf.WriteString(e.mode + " " + e.name)
		buf.WriteByte(0)
		buf.Write(raw)
	}
	return hashObject(t, repo, "tree", buf.Bytes())
}

// craftedCommit makes a tree the tip of main and returns the commit.
func craftedCommit(t *testing.T, repo *gittest.Repo, tree string) string {
	t.Helper()
	commit := repo.Git("commit-tree", tree, "-m", "a tree built by hand")
	repo.Git("update-ref", "refs/heads/main", commit)
	return commit
}

// scratchIn points the temporary directory at a folder of its own below a parent the test can inspect.
func scratchIn(t *testing.T) (parent, scratch string) {
	t.Helper()
	parent = t.TempDir()
	scratch = filepath.Join(parent, "tmp")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", scratch)
	return parent, scratch
}

func entriesOf(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSnapshotKeepsAnIgnoreCopyInsideItsTemporaryRepository(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "x\n")
	ignore := hashObject(t, repo, "blob", []byte(".env\n"))
	file := craftedTree(t, repo, treeEntry{"100644", ".gitignore", ignore})
	// x/../../../escaped/.gitignore: git reads each ".." as the name of an entry.
	third := craftedTree(t, repo, treeEntry{"40000", "escaped", file})
	second := craftedTree(t, repo, treeEntry{"40000", "..", third})
	first := craftedTree(t, repo, treeEntry{"40000", "..", second})
	nested := craftedTree(t, repo, treeEntry{"40000", "..", first})
	root := craftedTree(t, repo,
		treeEntry{"100644", ".gitignore", ignore},
		treeEntry{"40000", "x", nested})
	commit := craftedCommit(t, repo, root)
	if !strings.Contains(repo.Git("ls-tree", "-r", "--name-only", commit), "x/../../../escaped/.gitignore") {
		t.Fatal("the crafted tree does not hold the escaping path")
	}
	parent, scratch := scratchIn(t)

	snapshot := snapshotOf(t, repo, commit)

	if got := entriesOf(t, parent); !slices.Equal(got, []string{"tmp"}) {
		t.Fatalf("the parent of the temporary directory holds %v, want only tmp", got)
	}
	if got := entriesOf(t, scratch); len(got) != 0 {
		t.Fatalf("the snapshot left %v in the temporary directory", got)
	}
	if !snapshot.Ignored[".env"] {
		t.Fatal("the root .gitignore was not read")
	}
}

func TestSnapshotSkipsUnsafeIgnorePaths(t *testing.T) {
	for _, folder := range []string{".git", ".GIT", "."} {
		t.Run(folder, func(t *testing.T) {
			repo := gittest.New(t)
			repo.Commit("README.md", "x\n")
			ignore := hashObject(t, repo, "blob", []byte(".env\n"))
			file := craftedTree(t, repo, treeEntry{"100644", ".gitignore", ignore})
			root := craftedTree(t, repo, treeEntry{"40000", folder, file})
			commit := craftedCommit(t, repo, root)
			_, scratch := scratchIn(t)
			snapshot := snapshotOf(t, repo, commit)
			if snapshot.Ignored[".env"] {
				t.Fatalf("a .gitignore below %q counted for the root", folder)
			}
			if got := entriesOf(t, scratch); len(got) != 0 {
				t.Fatalf("the snapshot left %v in the temporary directory", got)
			}
		})
	}
}

func TestSnapshotCopiesOnlyRegularIgnoreFiles(t *testing.T) {
	t.Run("a symlink does not count", func(t *testing.T) {
		repo := gittest.New(t)
		if err := os.Symlink(".env", filepath.Join(repo.Dir, ".gitignore")); err != nil {
			t.Skip("no symlinks here: ", err)
		}
		repo.Git("add", "--", ".gitignore")
		repo.Git("commit", "--quiet", "-m", "a link")
		snapshot := snapshotOf(t, repo, "main")
		if snapshot.Ignored[".env"] {
			t.Fatal("the text of a symlink's target counted as ignore rules")
		}
	})
	t.Run("a gitlink does not fail the snapshot", func(t *testing.T) {
		repo := gittest.New(t)
		first := repo.Commit("README.md", "x\n")
		repo.Git("update-index", "--add", "--cacheinfo", "160000,"+first+",.gitignore")
		repo.Git("commit", "--quiet", "-m", "a gitlink")
		snapshot := snapshotOf(t, repo, "main")
		if snapshot.Ignored[".env"] {
			t.Fatal("a gitlink counted as ignore rules")
		}
		if !slices.Contains(snapshot.Paths, ".gitignore") {
			t.Fatalf("Paths = %v, want the gitlink listed", snapshot.Paths)
		}
	})
	t.Run("an executable file counts", func(t *testing.T) {
		repo := gittest.New(t)
		writeFile(t, filepath.Join(repo.Dir, ".gitignore"), ".env\n")
		if err := os.Chmod(filepath.Join(repo.Dir, ".gitignore"), 0o700); err != nil {
			t.Fatal(err)
		}
		repo.Git("add", "--", ".gitignore")
		repo.Git("commit", "--quiet", "-m", "an executable")
		if !snapshotOf(t, repo, "main").Ignored[".env"] {
			t.Fatal("a .gitignore with the executable bit did not count")
		}
	})
}

func TestSnapshotCapsWhatItReads(t *testing.T) {
	const limit = 1 << 20
	repo := gittest.New(t)
	repo.Commit("exact.txt", strings.Repeat("a", limit))
	commit := repo.Commit("over.txt", strings.Repeat("a", limit+1))
	snapshot := snapshotOf(t, repo, commit)
	if got, ok := snapshot.Read("exact.txt"); !ok || len(got) != limit {
		t.Fatalf("Read of a file of exactly 1 MiB = %d bytes, %v; want it read", len(got), ok)
	}
	if got, ok := snapshot.Read("over.txt"); ok || got != "" {
		t.Fatalf("Read of a file over 1 MiB = %d bytes, %v; want false", len(got), ok)
	}

	big := gittest.New(t)
	big.Commit(".gitignore", ".env\n"+strings.Repeat("#", limit))
	if snapshotOf(t, big, "main").Ignored[".env"] {
		t.Fatal("a .gitignore over 1 MiB counted")
	}
}

func TestSnapshotRefusesNamesThatAreNotRefs(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "x\n")
	for _, name := range []string{"", "-x", "--help", "HEAD"} {
		t.Run("name "+name, func(t *testing.T) {
			_, err := practicegit.Snapshots{Env: repo.Env()}.Snapshot(t.Context(), repo.Dir, name)
			if !errors.Is(err, ports.ErrNotFound) {
				t.Fatalf("err = %v, want ports.ErrNotFound", err)
			}
		})
	}
}

func TestSnapshotDoesNotFollowReplacedObjects(t *testing.T) {
	repo := gittest.New(t)
	commit := repo.Commit("README.md", "committed\n")
	original := repo.Git("rev-parse", commit+":README.md")
	replacement := hashObject(t, repo, "blob", []byte("replaced\n"))
	repo.Git("replace", original, replacement)
	snapshot := snapshotOf(t, repo, commit)
	if got, ok := snapshot.Read("README.md"); !ok || got != "committed\n" {
		t.Fatalf("Read(README.md) = %q, %v; want the committed text", got, ok)
	}
}

func TestSnapshotRemovesItsTemporaryRepositoryOnAnError(t *testing.T) {
	t.Run("a probe git cannot answer", func(t *testing.T) {
		repo := gittest.New(t)
		repo.Commit("README.md", "x\n")
		ignore := hashObject(t, repo, "blob", []byte(".env\n"))
		manifest := hashObject(t, repo, "blob", []byte("{}"))
		outside := craftedTree(t, repo, treeEntry{"100644", "package.json", manifest})
		root := craftedTree(t, repo,
			treeEntry{"100644", ".gitignore", ignore},
			treeEntry{"40000", "..", outside})
		commit := craftedCommit(t, repo, root)
		_, scratch := scratchIn(t)
		snapshot, err := practicegit.Snapshots{Env: repo.Env()}.Snapshot(t.Context(), repo.Dir, commit)
		if err == nil {
			t.Fatalf("a probe check-ignore exited 128 and the snapshot answered %v", snapshot.Ignored)
		}
		if !strings.Contains(err.Error(), "128") {
			t.Fatalf("err = %v, want the exit status 128", err)
		}
		if got := entriesOf(t, scratch); len(got) != 0 {
			t.Fatalf("the failed snapshot left %v in the temporary directory", got)
		}
	})
	t.Run("a blob git cannot read during the copy", func(t *testing.T) {
		repo := gittest.New(t)
		repo.Commit("README.md", "x\n")
		missing := strings.Repeat("ab", 20)
		root := craftedTree(t, repo, treeEntry{"100644", ".gitignore", missing})
		commit := craftedCommit(t, repo, root)
		_, scratch := scratchIn(t)
		if _, err := (practicegit.Snapshots{Env: repo.Env()}).Snapshot(t.Context(), repo.Dir, commit); err == nil {
			t.Fatal("a .gitignore whose blob is missing did not fail the snapshot")
		}
		if got := entriesOf(t, scratch); len(got) != 0 {
			t.Fatalf("the failed snapshot left %v in the temporary directory", got)
		}
	})
}

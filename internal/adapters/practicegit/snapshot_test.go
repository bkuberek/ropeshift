package practicegit_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	snapshot, err := practicegit.Snapshots{}.Snapshot(t.Context(), repo.Dir, "HEAD")
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
	snapshot := snapshotOf(t, repo, "HEAD")
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

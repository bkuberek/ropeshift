package gitcli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

const importSubject = "chore: import the design history into the workspace"

// importRig is a workspace with a lane's worktree, and a design repository whose history shares nothing with it.
type importRig struct {
	t         *testing.T
	workspace *gittest.Repo
	design    *gittest.Repo
	worktrees gitcli.Worktrees
	path      string
	base      string
	tip       string
	root      string
}

func newImportRig(t *testing.T) *importRig {
	t.Helper()
	workspace := gittest.New(t)
	workspace.Commit("README.txt", "the workspace\n")
	workspace.Commit(".gitignore", "repos/\nnode_modules/\n")
	workspace.Commit("workspace-only.md", "# Workspace\n")
	design := gittest.New(t)
	design.Commit("README.txt", "the design\n")
	design.Commit(".gitignore", "repos/\ndist/\n*.log\n")
	design.Commit("docs/vision.md", "# Vision\n")
	design.Commit("AGENTS.md", "# Rules\n")
	design.Commit("poc/go.mod", "module poc\n")
	design.Commit("poc/main.go", "package main\n")
	r := &importRig{
		t: t, workspace: workspace, design: design, worktrees: gitcli.Worktrees{Env: workspace.Env()},
		path: filepath.Join(filepath.Dir(workspace.Dir), "lanes", "adopt"),
	}
	made, err := r.worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: workspace.Dir, Path: r.path, Branch: "lane/adopt"})
	if err != nil {
		t.Fatal(err)
	}
	r.base = made.Head
	r.tip = design.Git("rev-parse", "HEAD")
	r.root = design.Roots("HEAD")[0]
	return r
}

// request is the import the worker asks for, with the files a consolidation adds.
func (r *importRig) request() ports.HistoryImport {
	return ports.HistoryImport{
		Source: r.design.Dir, Commit: r.tip, Root: r.root, LeaveOut: []string{"poc"}, Subject: importSubject,
		Files: []ports.TreeFile{
			{Path: "plan/repositories.md", Text: "+++\nkind = \"repositories\"\n+++\n"},
			{Path: ".gitignore", Lines: []string{"/.ropeshift/worktrees/"}},
		},
	}
}

func (r *importRig) inWorktree(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = r.path, r.workspace.Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *importRig) read(name string) string {
	r.t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.path, name))
	if err != nil {
		r.t.Fatal(err)
	}
	return string(raw)
}

// untouched is what the design repository holds that a read could change: its refs, its index, and its working
// tree, the last as a digest of every file outside git's own directory.
func untouched(t *testing.T, design *gittest.Repo) string {
	t.Helper()
	sum := sha256.New()
	sum.Write([]byte(design.Git("for-each-ref") + "\x00" + design.Git("ls-files", "--stage") + "\x00" + design.Git("status", "--porcelain", "--ignored")))
	err := filepath.WalkDir(design.Dir, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		sum.Write(append([]byte(at+"\x00"), raw...))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// The history arrives in one merge commit with the branch first and the design repository's tip second, and no
// commit is rewritten, so every design commit keeps its ID and a design file's history reads through the merge.
func TestImportHistoryMergesTheDesignTipAsTheSecondParent(t *testing.T) {
	r := newImportRig(t)
	before := untouched(t, r.design)
	made, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request())
	if err != nil {
		t.Fatal(err)
	}
	head := r.inWorktree("rev-parse", "HEAD")
	if made.Hash != head || made.Subject != importSubject || !slices.Equal(made.Parents, []string{r.base, r.tip}) {
		t.Fatalf("the commit reads %+v, HEAD is %s, the branch began at %s and the design tip is %s", made, head, r.base, r.tip)
	}
	if count := r.inWorktree("rev-list", "--count", r.base+"..HEAD", "--first-parent"); count != "1" {
		t.Errorf("the branch holds %s commits of its own beyond its base, want the merge alone", count)
	}
	for _, commit := range strings.Fields(r.design.Git("rev-list", "HEAD")) {
		if _, err := exec.Command("git", "-C", r.path, "merge-base", "--is-ancestor", commit, "HEAD").CombinedOutput(); err != nil {
			t.Errorf("the design commit %s is not reachable from the merge", commit)
		}
	}
	if roots := r.inWorktree("rev-list", "--max-parents=0", "HEAD"); len(strings.Fields(roots)) != 2 {
		t.Errorf("the merged history has roots %q, want the workspace's and the design repository's", roots)
	}
	if first := r.inWorktree("rev-list", "--first-parent", "--max-parents=0", "HEAD"); first != r.workspace.Roots("HEAD")[0] {
		t.Errorf("the first-parent root is %s, so the workspace's identity changed", first)
	}
	vision := strings.Fields(r.inWorktree("log", "--format=%H", "--", "docs/vision.md"))
	if len(vision) != 1 || r.inWorktree("log", "-1", "--format=%H", "--", "docs/vision.md") != r.design.Git("log", "-1", "--format=%H", "--", "docs/vision.md") {
		t.Errorf("git log on a design file reads %v through the merge", vision)
	}
	if author := r.inWorktree("log", "-1", "--format=%an <%ae>|%cn <%ce>"); author != "ropeshift <ropeshift@localhost>|ropeshift <ropeshift@localhost>" {
		t.Errorf("the merge is by %q", author)
	}
	if status := r.inWorktree("status", "--porcelain"); status != "" {
		t.Errorf("the worktree is dirty after the merge:\n%s", status)
	}
	if after := untouched(t, r.design); after != before {
		t.Error("the design repository changed: its refs, index, or working tree differ")
	}
}

// A path both repositories hold keeps the workspace's version, the ignore file becomes the workspace's lines then
// the design repository's lines it lacked, and the files the worker adds arrive in the same commit.
func TestImportHistoryKeepsTheWorkspacesVersionAndUnionsTheIgnoreFile(t *testing.T) {
	r := newImportRig(t)
	if _, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request()); err != nil {
		t.Fatal(err)
	}
	if text := r.read("README.txt"); text != "the workspace\n" {
		t.Errorf("the colliding README reads %q", text)
	}
	if text := r.inWorktree("show", "HEAD^2:README.txt"); text != "the design" {
		t.Errorf("the design's version at the second parent reads %q", text)
	}
	if text := r.read(".gitignore"); text != "repos/\nnode_modules/\ndist/\n*.log\n/.ropeshift/worktrees/\n" {
		t.Errorf("the ignore file reads %q", text)
	}
	if text := r.read("docs/vision.md"); text != "# Vision\n" {
		t.Errorf("the design's own file reads %q", text)
	}
	if text := r.read("workspace-only.md"); text != "# Workspace\n" {
		t.Errorf("the workspace's own file reads %q", text)
	}
	if files := r.inWorktree("diff", "--name-only", "HEAD^1", "HEAD"); !strings.Contains(files, "plan/repositories.md") {
		t.Errorf("the merge commit adds against the workspace:\n%s", files)
	}
}

// A tree left out is absent from the merge commit and present in its second parent, so it stays readable in the
// history.
func TestImportHistoryRemovesALeftOutTreeInTheMergeCommitOnly(t *testing.T) {
	r := newImportRig(t)
	made, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request())
	if err != nil {
		t.Fatal(err)
	}
	if tracked := r.inWorktree("ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(tracked, "poc/") {
		t.Errorf("the merge commit still holds the left-out tree:\n%s", tracked)
	}
	if _, err := os.Stat(filepath.Join(r.path, "poc")); err == nil {
		t.Error("the left-out tree is still on disk")
	}
	if tracked := r.inWorktree("ls-tree", "-r", "--name-only", "HEAD^2"); !strings.Contains(tracked, "poc/go.mod") || !strings.Contains(tracked, "poc/main.go") {
		t.Errorf("the second parent lost the tree:\n%s", tracked)
	}
	// A merge lists what it changed against its first parent, the workspace, which never held the tree.
	if slices.Contains(made.Files, "poc/go.mod") || !slices.Contains(made.Files, "docs/vision.md") || !slices.Contains(made.Files, "plan/repositories.md") {
		t.Errorf("the merge commit's files are %v", made.Files)
	}
}

// A tree left out removes what the design repository brought and never a file the workspace held at a path
// inside it.
func TestImportHistoryNeverRemovesWhatTheWorkspaceHeldInALeftOutTree(t *testing.T) {
	t.Parallel()
	workspace := gittest.New(t)
	workspace.Commit("poc/NOTES.md", "workspace notes\n")
	design := gittest.New(t)
	design.Commit("poc/go.mod", "module poc\n")
	design.Commit("poc/NOTES.md", "design notes\n")
	worktrees := gitcli.Worktrees{Env: workspace.Env()}
	at := filepath.Join(filepath.Dir(workspace.Dir), "lane")
	if _, err := worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: workspace.Dir, Path: at, Branch: "lane/adopt"}); err != nil {
		t.Fatal(err)
	}
	_, err := worktrees.ImportHistory(t.Context(), at, ports.HistoryImport{
		Source: design.Dir, Commit: design.Git("rev-parse", "HEAD"), Root: design.Roots("HEAD")[0], LeaveOut: []string{"poc"}, Subject: importSubject,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(at, "poc", "NOTES.md"))
	if err != nil || string(raw) != "workspace notes\n" {
		t.Errorf("the workspace's file inside the left-out tree reads %q: %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(at, "poc", "go.mod")); err == nil {
		t.Error("the design's file inside the left-out tree stayed")
	}
}

// Commits lists the merge as one commit of the branch with both parents, and not the design repository's own
// commits, which the merge brought in and which are no commits of the branch.
func TestCommitsListsAMergeAsOneCommitWithItsParents(t *testing.T) {
	r := newImportRig(t)
	if _, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request()); err != nil {
		t.Fatal(err)
	}
	got, err := r.worktrees.Commits(t.Context(), r.path, r.base)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Subject != importSubject || !slices.Equal(got[0].Parents, []string{r.base, r.tip}) {
		t.Fatalf("the branch holds %+v beyond its base", got)
	}
	if !slices.Contains(got[0].Files, "plan/repositories.md") || !slices.Contains(got[0].Files, "docs/vision.md") {
		t.Errorf("the merge lists %v", got[0].Files)
	}
}

// A retry finds the branch holding the design tip and makes no second merge.
func TestImportHistoryRepeatedMakesNoSecondMerge(t *testing.T) {
	r := newImportRig(t)
	if _, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request()); err != nil {
		t.Fatal(err)
	}
	head := r.inWorktree("rev-parse", "HEAD")
	again, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request())
	if err != nil || again.Hash != "" {
		t.Fatalf("a repeat made %+v: %v", again, err)
	}
	if now := r.inWorktree("rev-parse", "HEAD"); now != head {
		t.Errorf("a repeat moved the branch from %s to %s", head, now)
	}
}

// A history that begins at another first commit than the plan named is refused with the root it has, and the
// worktree is as it was.
func TestImportHistoryRefusesAnotherRootAndLeavesTheWorktreeAlone(t *testing.T) {
	r := newImportRig(t)
	in := r.request()
	in.Root = strings.Repeat("0", 40)
	_, err := r.worktrees.ImportHistory(t.Context(), r.path, in)
	var wrong *ports.HistoryRootError
	if !errors.As(err, &wrong) || wrong.Got != r.root || wrong.Want != in.Root {
		t.Fatalf("a wrong root gave %v", err)
	}
	if now := r.inWorktree("rev-parse", "HEAD"); now != r.base {
		t.Errorf("the branch moved to %s", now)
	}
	if status := r.inWorktree("status", "--porcelain"); status != "" {
		t.Errorf("the worktree is dirty:\n%s", status)
	}
	if _, err := os.Stat(filepath.Join(r.path, "docs")); err == nil {
		t.Error("the history's files arrived before its root was checked")
	}
}

// A merge a crash left half made, with the files written and no commit, is made again from the start and gives
// one merge commit.
func TestImportHistoryAfterACrashMidMergeMakesOneMerge(t *testing.T) {
	r := newImportRig(t)
	r.inWorktree("fetch", "--quiet", r.design.Dir, r.tip)
	cmd := exec.Command("git", "merge", "--allow-unrelated-histories", "--no-ff", "--no-commit", "FETCH_HEAD")
	cmd.Dir, cmd.Env = r.path, r.workspace.Env()
	_, _ = cmd.CombinedOutput()
	if err := os.WriteFile(filepath.Join(r.path, "plan-left-behind.md"), []byte("a crash wrote this\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	made, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(made.Parents, []string{r.base, r.tip}) {
		t.Errorf("the merge reads %+v", made)
	}
	if count := r.inWorktree("rev-list", "--count", r.base+"..HEAD", "--first-parent"); count != "1" {
		t.Errorf("the branch holds %s commits of its own beyond its base", count)
	}
	if status := r.inWorktree("status", "--porcelain"); status != "" {
		t.Errorf("the worktree is dirty after the merge:\n%s", status)
	}
}

// The merge runs no hook the repository holds.
func TestImportHistoryRunsNoHook(t *testing.T) {
	r := newImportRig(t)
	marker := filepath.Join(t.TempDir(), "ran")
	hooks := filepath.Join(r.workspace.Dir, ".git", "hooks")
	for _, name := range []string{"pre-merge-commit", "commit-msg", "pre-commit", "post-merge", "post-commit"} {
		script := "#!/bin/sh\ntouch " + marker + "\n"
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a hook of the repository ran")
	}
}

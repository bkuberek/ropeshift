package gitcli_test

import (
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

// caseImport is a workspace and a design repository that hold files under spellings that differ only in case, and
// the worktree the design's history was imported into.
type caseImport struct {
	t         *testing.T
	workspace *gittest.Repo
	path      string
	made      ports.Commit
}

// requireCaseSensitive skips a test that needs a workspace to hold two spellings of one path, which a file system
// that ignores case cannot check out.
func requireCaseSensitive(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "A")); err == nil {
		t.Skip("the file system ignores case")
	}
}

func importWithCases(t *testing.T, ours, theirs map[string]string, leaveOut ...string) *caseImport {
	t.Helper()
	workspace := gittest.New(t)
	workspace.Commit("README.txt", "the workspace\n")
	for name, text := range ours {
		workspace.Commit(name, text)
	}
	design := gittest.New(t)
	design.Commit("README.txt", "the design\n")
	for name, text := range theirs {
		design.Commit(name, text)
	}
	worktrees := gitcli.Worktrees{Env: workspace.Env()}
	at := filepath.Join(filepath.Dir(workspace.Dir), "lane")
	if _, err := worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: workspace.Dir, Path: at, Branch: "lane/adopt"}); err != nil {
		t.Fatal(err)
	}
	made, err := worktrees.ImportHistory(t.Context(), at, ports.HistoryImport{
		Source: design.Dir, Commit: design.Git("rev-parse", "HEAD"), Root: design.Roots("HEAD")[0], LeaveOut: leaveOut, Subject: importSubject,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &caseImport{t: t, workspace: workspace, path: at, made: made}
}

func (c *caseImport) git(args ...string) string {
	c.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = c.path, c.workspace.Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (c *caseImport) read(name string) string {
	c.t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.path, name))
	if err != nil {
		c.t.Fatal(err)
	}
	return string(raw)
}

// tracked is what the merge commit holds.
func (c *caseImport) tracked() []string {
	c.t.Helper()
	return strings.Fields(c.git("ls-tree", "-r", "--name-only", "HEAD"))
}

// A path the design repository holds under another case than the workspace does is the workspace's path, so the
// merge keeps the workspace's file and its spelling, brings none of the design's, and leaves the worktree clean
// with the workspace's text on disk, whether the file system folds case or not.
func TestImportHistoryTreatsACaseVariantAsACollision(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		ours, theirs string
		folder       bool
	}{
		"instructions":     {ours: "AGENTS.md", theirs: "agents.md"},
		"a Makefile":       {ours: "Makefile", theirs: "makefile"},
		"the reverse case": {ours: "makefile", theirs: "Makefile"},
		"a folder":         {ours: "Docs/guide.md", theirs: "docs/guide.md", folder: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := importWithCases(t, map[string]string{tt.ours: "workspace text\n"}, map[string]string{tt.theirs: "design text\n"})
			if got := c.tracked(); !slices.Contains(got, tt.ours) || slices.Contains(got, tt.theirs) {
				t.Errorf("the merge commit holds %v, want %s and not %s", got, tt.ours, tt.theirs)
			}
			if text := c.git("show", "HEAD:"+tt.ours); text != "workspace text" {
				t.Errorf("%s in the merge commit reads %q", tt.ours, text)
			}
			if text := c.read(tt.ours); text != "workspace text\n" {
				t.Errorf("%s in the worktree reads %q", tt.ours, text)
			}
			if status := c.git("status", "--porcelain"); status != "" {
				t.Errorf("the worktree is dirty after the merge:\n%s", status)
			}
			if text := c.git("show", "HEAD^2:"+tt.theirs); text != "design text" {
				t.Errorf("the design's version at the second parent reads %q", text)
			}
			if slices.Contains(c.made.Files, tt.theirs) {
				t.Errorf("the merge commit lists %s among its files %v", tt.theirs, c.made.Files)
			}
		})
	}
}

// The ignore file is the union of both files' lines under the workspace's spelling, whichever case each history
// spells it in.
func TestImportHistoryUnionsTheIgnoreFileUnderAnyCase(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct{ ours, theirs string }{
		"the design spells it in capitals":    {ours: ".gitignore", theirs: ".GITIGNORE"},
		"the workspace spells it in capitals": {ours: ".GITIGNORE", theirs: ".gitignore"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := importWithCases(t, map[string]string{tt.ours: "repos/\nnode_modules/\n"}, map[string]string{tt.theirs: "repos/\ndist/\n"})
			if got := c.tracked(); !slices.Contains(got, tt.ours) || slices.Contains(got, tt.theirs) {
				t.Errorf("the merge commit holds %v, want %s and not %s", got, tt.ours, tt.theirs)
			}
			if text := c.git("show", "HEAD:"+tt.ours); text != "repos/\nnode_modules/\ndist/" {
				t.Errorf("the ignore file in the merge commit reads %q", text)
			}
			if text := c.read(tt.ours); text != "repos/\nnode_modules/\ndist/\n" {
				t.Errorf("the ignore file in the worktree reads %q", text)
			}
			if status := c.git("status", "--porcelain"); status != "" {
				t.Errorf("the worktree is dirty after the merge:\n%s", status)
			}
		})
	}
}

// A tree left out removes no file of the workspace, nor one the workspace holds under another case.
func TestImportHistoryKeepsAWorkspaceFileThatALeftOutTreeHoldsUnderAnotherCase(t *testing.T) {
	t.Parallel()
	c := importWithCases(t,
		map[string]string{"Poc/NOTES.md": "workspace notes\n"},
		map[string]string{"poc/NOTES.md": "design notes\n", "poc/go.mod": "module poc\n"},
		"poc")
	if text := c.read("Poc/NOTES.md"); text != "workspace notes\n" {
		t.Errorf("the workspace's file reads %q", text)
	}
	if got := c.tracked(); slices.Contains(got, "poc/go.mod") || slices.Contains(got, "poc/NOTES.md") {
		t.Errorf("the merge commit holds %v", got)
	}
	if status := c.git("status", "--porcelain"); status != "" {
		t.Errorf("the worktree is dirty after the merge:\n%s", status)
	}
}

// A workspace that holds both spellings of a path keeps both: the design's spelling is a variant only of a path the
// workspace does not hold exactly.
func TestImportHistoryKeepsBothSpellingsOfAPathTheWorkspaceHolds(t *testing.T) {
	requireCaseSensitive(t)
	t.Parallel()
	for name, tt := range map[string]struct {
		ours   map[string]string
		theirs string
	}{
		"the design's text differs":            {ours: map[string]string{"AGENTS.md": "upper\n", "agents.md": "lower\n"}, theirs: "design\n"},
		"the design's text is the workspace's": {ours: map[string]string{"AGENTS.md": "upper\n", "agents.md": "lower\n"}, theirs: "lower\n"},
		"the workspace's spellings read alike": {ours: map[string]string{"AGENTS.md": "same\n", "agents.md": "same\n"}, theirs: "design\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := importWithCases(t, tt.ours, map[string]string{"agents.md": tt.theirs})
			got := c.tracked()
			for spelling, text := range tt.ours {
				if !slices.Contains(got, spelling) || c.git("show", "HEAD:"+spelling) != strings.TrimSpace(text) || c.read(spelling) != text {
					t.Errorf("%s is not the workspace's file in the merge commit %v", spelling, got)
				}
			}
			if status := c.git("status", "--porcelain"); status != "" {
				t.Errorf("the worktree is dirty after the merge:\n%s", status)
			}
		})
	}
}

// Of two spellings of the ignore file in the workspace, the design's file is united with the one it spells alike, or
// with the only one it is a variant of, and the other stays as it is.
func TestImportHistoryUnionsTheIgnoreFileWithTheSpellingItMatches(t *testing.T) {
	requireCaseSensitive(t)
	t.Parallel()
	ours := map[string]string{".gitignore": "repos/\nnode_modules/\n", ".GITIGNORE": "other/\n"}
	for name, tt := range map[string]struct {
		theirs map[string]string
		want   map[string]string
	}{
		"the lower-case file": {
			theirs: map[string]string{".gitignore": "repos/\ndist/\n"},
			want:   map[string]string{".gitignore": "repos/\nnode_modules/\ndist/\n", ".GITIGNORE": "other/\n"},
		},
		"the capitals": {
			theirs: map[string]string{".GITIGNORE": "other/\nextra/\n"},
			want:   map[string]string{".gitignore": "repos/\nnode_modules/\n", ".GITIGNORE": "other/\nextra/\n"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := importWithCases(t, ours, tt.theirs)
			for file, text := range tt.want {
				if got := c.read(file); got != text {
					t.Errorf("%s reads %q, want %q", file, got, text)
				}
				if got := c.git("show", "HEAD:"+file); got != strings.TrimSpace(text) {
					t.Errorf("%s in the merge commit reads %q, want %q", file, got, strings.TrimSpace(text))
				}
			}
			if status := c.git("status", "--porcelain"); status != "" {
				t.Errorf("the worktree is dirty after the merge:\n%s", status)
			}
		})
	}
}

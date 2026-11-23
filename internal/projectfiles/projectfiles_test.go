package projectfiles_test

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// named renders each template as its own name, so a test reads which template made each file.
func named(name string, _ projectfiles.Data) (string, error) { return name, nil }

// columns is the table of FR-TPL-1: for each shape, the path of each file in the folder and the template it
// comes from, for a project with no code repositories.
var columns = map[projectfiles.Shape]map[string]string{
	projectfiles.Workspace: {
		"AGENTS.md":            "workspace/agents.md",
		"CLAUDE.md":            "claude.md",
		"Makefile":             "workspace/makefile",
		".gitignore":           "workspace/gitignore",
		"plan/goal.md":         "workspace/plan/goal.md",
		"plan/plan.md":         "workspace/plan/plan.md",
		"plan/requirements.md": "workspace/plan/requirements.md",
		"SECURITY.md":          "security.md",
		"CODEOWNERS":           "codeowners",
	},
	projectfiles.Monorepo: {
		"AGENTS.md":               "monorepo/agents.md",
		"CLAUDE.md":               "claude.md",
		".gitignore":              "monorepo/gitignore",
		"plan/goal.md":            "workspace/plan/goal.md",
		"plan/plan.md":            "workspace/plan/plan.md",
		"plan/requirements.md":    "workspace/plan/requirements.md",
		"catalog-info.yaml":       "repository/catalog-info.yaml",
		"SECURITY.md":             "security.md",
		"CODEOWNERS":              "codeowners",
		".pre-commit-config.yaml": "pre-commit-config.yaml",
	},
	projectfiles.Repository: {
		"AGENTS.md":               "repository/agents.md",
		"CLAUDE.md":               "claude.md",
		".gitignore":              "repository/gitignore",
		"catalog-info.yaml":       "repository/catalog-info.yaml",
		"SECURITY.md":             "security.md",
		"CODEOWNERS":              "codeowners",
		".pre-commit-config.yaml": "pre-commit-config.yaml",
	},
}

// two is a workspace's pair of code repositories.
var two = []projectfiles.CodeRepository{
	{Name: "orchid-api", Remote: "git@example.invalid:orchid/orchid-api.git"},
	{Name: "orchid-web", Remote: "git@example.invalid:orchid/orchid-web.git"},
}

// FR-TPL-1: each shape writes the files of its column, sorted by path, and a workspace writes the manifest
// only when it names a code repository.
func TestEachShapeWritesTheFilesOfItsColumn(t *testing.T) {
	cases := []struct {
		name  string
		shape projectfiles.Shape
		data  projectfiles.Data
		extra map[string]string
	}{
		{"a workspace with no repositories", projectfiles.Workspace, projectfiles.Data{Project: "orchid"}, nil},
		{"a workspace with two repositories", projectfiles.Workspace, projectfiles.Data{Project: "orchid", Repositories: two},
			map[string]string{"plan/repositories.md": "workspace/plan/repositories.md"}},
		{"a monorepo", projectfiles.Monorepo, projectfiles.Data{Project: "orchid"}, nil},
		{"a repository", projectfiles.Repository, projectfiles.Data{Project: "orchid"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := maps.Clone(columns[tc.shape])
			maps.Copy(want, tc.extra)
			files, err := projectfiles.Files(tc.shape, tc.data, named)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			var paths []string
			for _, file := range files {
				got[file.Path] = file.Template
				paths = append(paths, file.Path)
				if file.Text != file.Template {
					t.Errorf("%s holds %q, which template %s did not render", file.Path, file.Text, file.Template)
				}
			}
			if !maps.Equal(got, want) {
				t.Errorf("wrote %v, want %v", got, want)
			}
			if !slices.IsSorted(paths) {
				t.Errorf("the files are not sorted by path: %v", paths)
			}
		})
	}
}

// Every file renders over the data given: the project's name and the workspace's code repositories.
func TestEveryFileRendersOverTheDataGiven(t *testing.T) {
	data := projectfiles.Data{Project: "orchid", Repositories: two}
	var rendered []string
	render := func(name string, got projectfiles.Data) (string, error) {
		if got.Project != data.Project || !slices.Equal(got.Repositories, data.Repositories) {
			t.Errorf("%s rendered over %+v, want %+v", name, got, data)
		}
		rendered = append(rendered, name)
		return "text", nil
	}
	files, err := projectfiles.Files(projectfiles.Workspace, data, render)
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != len(files) {
		t.Errorf("rendered %d templates for %d files", len(rendered), len(files))
	}
}

// A template that does not render fails the whole set, naming the template, so nothing is written from half
// of it.
func TestATemplateThatDoesNotRenderFailsTheSet(t *testing.T) {
	broken := errors.New("broken")
	render := func(name string, _ projectfiles.Data) (string, error) {
		if name == "codeowners" {
			return "", broken
		}
		return name, nil
	}
	files, err := projectfiles.Files(projectfiles.Monorepo, projectfiles.Data{Project: "orchid"}, render)
	if !errors.Is(err, broken) || !strings.Contains(err.Error(), "codeowners") || files != nil {
		t.Errorf("files %v, error %v", files, err)
	}
}

// What no shape can write is refused before anything renders: an unknown shape, code repositories on a shape
// that is not a workspace, a name the ledger refuses, a name given twice, and a repository with no remote.
func TestWhatNoShapeWritesIsRefusedBeforeAnythingRenders(t *testing.T) {
	cases := []struct {
		name         string
		shape        projectfiles.Shape
		repositories []projectfiles.CodeRepository
		says         string
	}{
		{"an unknown shape", "library", nil, `"library"`},
		{"repositories on a monorepo", projectfiles.Monorepo, two[:1], "monorepo"},
		{"repositories on a repository", projectfiles.Repository, two[:1], "repository"},
		{"a name the ledger refuses", projectfiles.Workspace, []projectfiles.CodeRepository{{Name: "-api", Remote: two[0].Remote}}, "dash"},
		{"a name with a slash", projectfiles.Workspace, []projectfiles.CodeRepository{{Name: "a/b", Remote: two[0].Remote}}, "slash"},
		{"a name that runs a command in the Makefile", projectfiles.Workspace, []projectfiles.CodeRepository{{Name: "x;do:;done;touch${IFS}p;#", Remote: two[0].Remote}}, "letters, digits"},
		{"a name with a make variable", projectfiles.Workspace, []projectfiles.CodeRepository{{Name: "a$(b)", Remote: two[0].Remote}}, "letters, digits"},
		{"a name given twice", projectfiles.Workspace, []projectfiles.CodeRepository{two[0], two[0]}, "orchid-api"},
		{"no remote", projectfiles.Workspace, []projectfiles.CodeRepository{{Name: "orchid-api"}}, "remote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := projectfiles.Check(tc.shape, tc.repositories); !errors.Is(err, projectfiles.ErrInvalid) || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("check: %v, want %v saying %q", err, projectfiles.ErrInvalid, tc.says)
			}
			render := func(name string, _ projectfiles.Data) (string, error) {
				t.Errorf("rendered %s", name)
				return "", nil
			}
			files, err := projectfiles.Files(tc.shape, projectfiles.Data{Project: "orchid", Repositories: tc.repositories}, render)
			if !errors.Is(err, projectfiles.ErrInvalid) || files != nil {
				t.Errorf("files %v, error %v", files, err)
			}
		})
	}
	if err := projectfiles.Check(projectfiles.Workspace, []projectfiles.CodeRepository{{Name: "-api", Remote: two[0].Remote}}); !errors.Is(err, ledger.ErrInvalidRepository) {
		t.Errorf("a refused name is %v, want the ledger's reason", err)
	}
	for _, shape := range projectfiles.Shapes() {
		if err := projectfiles.Check(shape, nil); err != nil {
			t.Errorf("%s with no repositories: %v", shape, err)
		}
	}
	if err := projectfiles.Check(projectfiles.Workspace, two); err != nil {
		t.Errorf("a workspace with two repositories: %v", err)
	}
}

// The shapes are the three FR-TPL-1 names, in the order help lists them, and nothing else is one.
func TestTheShapesAreWorkspaceMonorepoAndRepository(t *testing.T) {
	want := []projectfiles.Shape{projectfiles.Workspace, projectfiles.Monorepo, projectfiles.Repository}
	if got := projectfiles.Shapes(); !slices.Equal(got, want) {
		t.Errorf("shapes %v, want %v", got, want)
	}
	for _, shape := range want {
		if !shape.Valid() {
			t.Errorf("%s is not valid", shape)
		}
	}
	for _, shape := range []projectfiles.Shape{"", "Workspace", "library"} {
		if shape.Valid() {
			t.Errorf("%q is valid", shape)
		}
	}
}

// Path names where any shape writes a template, which is the same path for every shape that writes it, and
// is empty for a template no shape writes.
func TestPathNamesWhereAShapeWritesATemplate(t *testing.T) {
	for shape, column := range columns {
		for path, name := range column {
			if got := projectfiles.Path(name); got != path {
				t.Errorf("%s writes %s to %s, and Path says %q", shape, name, path, got)
			}
		}
	}
	if got := projectfiles.Path("workspace/plan/repositories.md"); got != "plan/repositories.md" {
		t.Errorf("the manifest is written to %q", got)
	}
	for _, name := range []string{"dependabot.yml", "workspace/plan/contract.md", "user/profile.toml", "no/such"} {
		if got := projectfiles.Path(name); got != "" {
			t.Errorf("%s is written to %q, and no shape writes it", name, got)
		}
	}
}

// ShapesOf lists the shapes that write a template, in the order of Shapes, for every file of every column, and
// none for a template no shape writes, the manifest included only through a workspace.
func TestShapesOfListsTheShapesThatWriteATemplateInOrder(t *testing.T) {
	want := map[string][]projectfiles.Shape{}
	for _, shape := range projectfiles.Shapes() {
		for _, name := range columns[shape] {
			want[name] = append(want[name], shape)
		}
	}
	want["workspace/plan/repositories.md"] = []projectfiles.Shape{projectfiles.Workspace}
	for name, shapes := range want {
		if got := projectfiles.ShapesOf(name); !slices.Equal(got, shapes) {
			t.Errorf("%s is written by %v, and ShapesOf says %v", name, shapes, got)
		}
	}
	if got := projectfiles.ShapesOf("claude.md"); !slices.Equal(got, projectfiles.Shapes()) {
		t.Errorf("claude.md is written by %v, want every shape in order", got)
	}
	for _, name := range []string{"dependabot.yml", "workspace/plan/contract.md", "user/profile.toml", "no/such"} {
		if got := projectfiles.ShapesOf(name); got == nil || len(got) != 0 {
			t.Errorf("%s is written by %#v, want an empty list that is not nil", name, got)
		}
	}
}

// WritesTo is Path for a template a shape writes, and profile.toml for the user profile, which `init` writes to
// the state directory.
func TestWritesToNamesThePathOfATemplateAndOfTheUserProfile(t *testing.T) {
	for _, column := range columns {
		for path, name := range column {
			if got := projectfiles.WritesTo(name); got != path {
				t.Errorf("%s is written to %q, want %q", name, got, path)
			}
		}
	}
	if got := projectfiles.WritesTo(projectfiles.UserProfile); got != "profile.toml" {
		t.Errorf("the user profile is written to %q", got)
	}
	for _, name := range []string{"dependabot.yml", "workspace/plan/contract.md", "no/such"} {
		if got := projectfiles.WritesTo(name); got != "" {
			t.Errorf("%s is written to %q, and nothing writes it", name, got)
		}
	}
}

// FR-TPL-2: the commands that register what a shape wrote, for a member's session, with the git commands
// first when the folder still needs its first commit.
func TestTheCommandsRegisterWhatEachShapeWrote(t *testing.T) {
	// The first commit adds each file the shape writes, and never a path that was in the folder before.
	git := func(shape projectfiles.Shape, repositories []projectfiles.CodeRepository) []string {
		return []string{
			"git -C /work/orchid init",
			addOf("/work/orchid", shapePaths(shape, repositories)...),
			`git -C /work/orchid commit -m "chore: start orchid"`,
		}
	}
	cases := []struct {
		name         string
		shape        projectfiles.Shape
		repositories []projectfiles.CodeRepository
		firstCommit  bool
		want         []string
	}{
		{"a workspace with repositories and no commit", projectfiles.Workspace, two, true, append(git(projectfiles.Workspace, two),
			"make -C /work/orchid setup", login, "ropeshift project create orchid", "make -C /work/orchid register")},
		{"a workspace with no repositories in a clone", projectfiles.Workspace, nil, false, []string{
			login, "ropeshift project create orchid", "make -C /work/orchid register"}},
		{"a monorepo with no commit", projectfiles.Monorepo, nil, true, append(git(projectfiles.Monorepo, nil),
			login, "ropeshift project create orchid", "ropeshift repository add /work/orchid --project orchid")},
		{"a monorepo in a clone", projectfiles.Monorepo, nil, false, []string{
			login, "ropeshift project create orchid", "ropeshift repository add /work/orchid --project orchid"}},
		{"a repository with no commit", projectfiles.Repository, nil, true, append(git(projectfiles.Repository, nil),
			"ropeshift repository add /work/orchid --project orchid")},
		{"a repository in a clone", projectfiles.Repository, nil, false, []string{
			"ropeshift repository add /work/orchid --project orchid"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := projectfiles.Commands(tc.shape, "/work/orchid", "orchid", tc.repositories, tc.firstCommit)
			if !slices.Equal(got, tc.want) {
				t.Errorf("commands\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// A folder whose path a shell would split or expand is quoted, so each command copies to a terminal whole.
func TestACommandQuotesAPathAShellWouldSplit(t *testing.T) {
	got := projectfiles.Commands(projectfiles.Monorepo, "/work/it's mine", "it-s-mine", nil, true)
	want := []string{
		`git -C '/work/it'\''s mine' init`,
		addOf(`'/work/it'\''s mine'`, shapePaths(projectfiles.Monorepo, nil)...),
		`git -C '/work/it'\''s mine' commit -m "chore: start it-s-mine"`,
		login,
		"ropeshift project create it-s-mine",
		`ropeshift repository add '/work/it'\''s mine' --project it-s-mine`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("commands\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := projectfiles.Commands(projectfiles.Repository, "/work/$HOME", "home", nil, false); got[0] != "ropeshift repository add '/work/$HOME' --project home" {
		t.Errorf("a path a shell would expand: %v", got)
	}
}

// login is the command that opens one session for the commands after it, so a project is made and registered with one
// passphrase.
const login = `eval "$(ropeshift login)"`

// addOf is the command that adds the files at paths in the folder: the paths git would add, which are those it does not
// ignore, read as files and never as pathspecs.
func addOf(folder string, paths ...string) string {
	git := "git -C " + folder + " --literal-pathspecs"
	return git + " ls-files -z --cached --others --exclude-standard -- " + strings.Join(paths, " ") + " | xargs -0 " + git + " add --"
}

// shapePaths are the paths a shape writes, sorted, from the table of FR-TPL-1, with the workspace's manifest when it
// names code repositories.
func shapePaths(shape projectfiles.Shape, repositories []projectfiles.CodeRepository) []string {
	paths := slices.Sorted(maps.Keys(columns[shape]))
	if shape == projectfiles.Workspace && len(repositories) > 0 {
		paths = append(paths, "plan/repositories.md")
		slices.Sort(paths)
	}
	return paths
}

// The first commit a person runs adds the files that were written and no other: a file that was in the folder
// before is not added, and each new code repository adds the files of its own.
func TestTheFirstCommitCommandsAddOnlyTheFilesWritten(t *testing.T) {
	written := []string{"AGENTS.md", "plan/goal.md"}
	got := projectfiles.CommandsStaging(projectfiles.Monorepo, "/work/orchid", "orchid", nil, written, "")
	want := []string{
		"git -C /work/orchid init",
		addOf("/work/orchid", "AGENTS.md", "plan/goal.md"),
		`git -C /work/orchid commit -m "chore: start orchid"`,
		login,
		"ropeshift project create orchid",
		"ropeshift repository add /work/orchid --project orchid",
	}
	if !slices.Equal(got, want) {
		t.Errorf("commands\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	fresh := []projectfiles.CodeRepository{{Name: "api", New: true}}
	got = projectfiles.CommandsStaging(projectfiles.Workspace, "/work/orchid", "orchid", fresh,
		[]string{"AGENTS.md", "repos/api/AGENTS.md", "repos/api/.gitignore"}, "")
	for _, line := range []string{
		addOf("/work/orchid/repos/api", "AGENTS.md", ".gitignore"),
		addOf("/work/orchid", "AGENTS.md"),
	} {
		if !slices.Contains(got, line) {
			t.Errorf("commands lack %q:\n%s", line, strings.Join(got, "\n"))
		}
	}
}

// A path with a character a shell would split is quoted, and the add names each path after --, so it copies to a
// terminal whole and reads each path as a file.
func TestTheAddCommandQuotesEachPath(t *testing.T) {
	got := projectfiles.FirstCommitCommands("/work/orchid", "orchid", []string{"-n.md", "a b.md", "plan/goal.md"}, "")
	if want := addOf("/work/orchid", "-n.md", "'a b.md'", "plan/goal.md"); got[1] != want {
		t.Errorf("the add is %q, want %q", got[1], want)
	}
}

// RepositoryPaths divides a folder's files among its repositories: the folder's own repository holds what is not
// under repos/, and a code repository holds what is under its folder, with that folder taken off.
func TestRepositoryPathsDivideTheFilesAmongTheRepositories(t *testing.T) {
	files := []string{"AGENTS.md", "plan/goal.md", "repos/api/AGENTS.md", "repos/web/.gitignore"}
	if got, want := projectfiles.RepositoryPaths(".", files), []string{"AGENTS.md", "plan/goal.md"}; !slices.Equal(got, want) {
		t.Errorf("the folder's own repository holds %v, want %v", got, want)
	}
	if got, want := projectfiles.RepositoryPaths("repos/api", files), []string{"AGENTS.md"}; !slices.Equal(got, want) {
		t.Errorf("repos/api holds %v, want %v", got, want)
	}
}

// With no file to add the commands add nothing: a pathspec of none would list every file of the folder.
func TestTheFirstCommitCommandsOfNoFileMakeTheRepositoryAndAddNothing(t *testing.T) {
	got := projectfiles.FirstCommitCommands("/work/orchid", "orchid", nil, "")
	if want := []string{"git -C /work/orchid init"}; !slices.Equal(got, want) {
		t.Errorf("commands %v, want %v", got, want)
	}
}

// A copy-pasted init starts the repository on the branch the run chose, and on git's own default when none is named.
func TestTheInitCommandNamesTheBranchTheRunChose(t *testing.T) {
	got := projectfiles.FirstCommitCommands("/work/orchid", "orchid", []string{"AGENTS.md"}, "trunk")
	if want := "git -C /work/orchid init --initial-branch=trunk"; got[0] != want {
		t.Errorf("the init is %q, want %q", got[0], want)
	}
	if got := projectfiles.FirstCommitCommands("/work/orchid", "orchid", nil, "trunk"); got[0] != "git -C /work/orchid init --initial-branch=trunk" || len(got) != 1 {
		t.Errorf("commands of no file are %v", got)
	}
	also := projectfiles.CommandsStaging(projectfiles.Workspace, "/work/orchid", "orchid", []projectfiles.CodeRepository{{Name: "api", New: true}}, []string{"AGENTS.md", "repos/api/AGENTS.md"}, "trunk")
	for _, want := range []string{"git -C /work/orchid/repos/api init --initial-branch=trunk", "git -C /work/orchid init --initial-branch=trunk"} {
		if !slices.Contains(also, want) {
			t.Errorf("commands lack %q:\n%s", want, strings.Join(also, "\n"))
		}
	}
}

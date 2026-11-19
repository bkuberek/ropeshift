package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/templates"
)

// runNew runs one command with the options given, which is how a test stands in for git.
func runNew(opts cli.Options, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, opts)
	return code, stdout.String(), stderr.String()
}

// filesUnder reads every file under dir, by its path in dir with slashes, so a test sees anything written
// besides the shape's files, a .git folder included.
func filesUnder(t *testing.T, dir string) map[string]string {
	t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		found[filepath.ToSlash(rel)] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// startingFiles is what a shape starts its folder with for the project and repositories given, as the
// table and the templates of this binary make it.
func startingFiles(t *testing.T, shape projectfiles.Shape, project string, repositories ...projectfiles.CodeRepository) map[string]string {
	t.Helper()
	render := func(name string, data projectfiles.Data) (string, error) {
		converted := templates.Data{Project: data.Project}
		for _, r := range data.Repositories {
			converted.Repositories = append(converted.Repositories, templates.Repository{Name: r.Name, Remote: r.Remote})
		}
		return templates.Render(name, converted)
	}
	files, err := projectfiles.Files(shape, projectfiles.Data{Project: project, Repositories: repositories}, render)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, file := range files {
		want[file.Path] = file.Text
	}
	return want
}

var (
	orchidAPI = projectfiles.CodeRepository{Name: "orchid-api", Remote: "git@example.invalid:orchid/orchid-api.git"}
	orchidWeb = projectfiles.CodeRepository{Name: "orchid-web", Remote: "git@example.invalid:orchid/orchid-web.git"}
)

// FR-TPL-1: each shape writes exactly its files into an empty folder, or an absent one with absent parents,
// with no daemon, no session, and no git, so no .git appears.
func TestNewWritesEachShapesFilesIntoAnEmptyFolder(t *testing.T) {
	cases := []struct {
		name         string
		shape        projectfiles.Shape
		flags        []string
		repositories []projectfiles.CodeRepository
		exists       bool
	}{
		{"a workspace in an empty folder", projectfiles.Workspace, nil, nil, true},
		{"a workspace with two repositories", projectfiles.Workspace,
			[]string{"--repository", "orchid-api=" + orchidAPI.Remote, "--repository", "orchid-web=" + orchidWeb.Remote},
			[]projectfiles.CodeRepository{orchidAPI, orchidWeb}, false},
		{"a monorepo in an absent folder", projectfiles.Monorepo, nil, nil, false},
		{"a repository in an empty folder", projectfiles.Repository, nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			folder := filepath.Join(t.TempDir(), "absent", "parents", "orchid")
			if tc.exists {
				folder = filepath.Join(t.TempDir(), "orchid")
				if err := os.Mkdir(folder, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			code, stdout, stderr := runNew(cli.Options{}, append([]string{"new", string(tc.shape), folder}, tc.flags...)...)
			if code != cli.ExitOK || stderr != "" {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			want := startingFiles(t, tc.shape, "orchid", tc.repositories...)
			got := filesUnder(t, folder)
			if !maps.Equal(got, want) {
				t.Errorf("wrote %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
			}
			for path := range want {
				if !strings.Contains(stdout, "wrote\t"+path+"\n") {
					t.Errorf("stdout does not list %s as written:\n%s", path, stdout)
				}
			}
		})
	}
}

// A second run writes nothing and lists every file as left, because each exists.
func TestNewASecondRunWritesNothingAndListsEveryFileAsLeft(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	if code, _, stderr := runNew(cli.Options{}, "new", "workspace", folder); code != cli.ExitOK {
		t.Fatalf("first run: exit %d, stderr %q", code, stderr)
	}
	before := filesUnder(t, folder)
	code, stdout, stderr := runNew(cli.Options{}, "new", "workspace", folder)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("second run: exit %d, stderr %q", code, stderr)
	}
	if strings.Contains(stdout, "wrote\t") {
		t.Errorf("the second run wrote:\n%s", stdout)
	}
	for path := range before {
		if !strings.Contains(stdout, "left\t"+path+"\t") {
			t.Errorf("stdout does not list %s as left:\n%s", path, stdout)
		}
	}
	if after := filesUnder(t, folder); !maps.Equal(after, before) {
		t.Error("the second run changed the folder")
	}
}

// A file that exists is never overwritten: a person's own AGENTS.md is kept and listed as left, and the
// shape's other files are written beside it.
func TestNewKeepsAnAgentsFileThatExists(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	mine := "# Our own instructions\n"
	if err := os.WriteFile(filepath.Join(folder, "AGENTS.md"), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runNew(cli.Options{}, "new", "monorepo", folder)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	want := startingFiles(t, projectfiles.Monorepo, "orchid")
	want["AGENTS.md"] = mine
	if got := filesUnder(t, folder); !maps.Equal(got, want) {
		t.Errorf("the folder holds %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	if !strings.Contains(stdout, "left\tAGENTS.md\t") || strings.Contains(stdout, "wrote\tAGENTS.md") {
		t.Errorf("AGENTS.md is not listed as left:\n%s", stdout)
	}
}

// newResult is what `new --json` prints.
type newResult struct {
	Path     string   `json:"path"`
	Project  string   `json:"project"`
	Written  []string `json:"written"`
	Left     []string `json:"left"`
	Commands []string `json:"commands"`
}

// newJSON runs `new` with --json and reads its one document.
func newJSON(t *testing.T, opts cli.Options, args ...string) newResult {
	t.Helper()
	code, stdout, stderr := runNew(opts, append(args, "--json")...)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var result newResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	return result
}

// With --json, `new` prints the folder, the project's name, the files written, the files left, and the
// commands, each list an array even when it is empty.
func TestNewJSONListsTheFilesWrittenAndLeftAndTheCommands(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	paths := slices.Sorted(maps.Keys(startingFiles(t, projectfiles.Repository, "orchid")))
	first := newJSON(t, cli.Options{}, "new", "repository", folder)
	want := newResult{
		Path: folder, Project: "orchid", Written: paths, Left: []string{},
		Commands: projectfiles.Commands(projectfiles.Repository, folder, "orchid", nil, true),
	}
	if !slices.Equal(first.Written, want.Written) || !slices.Equal(first.Left, want.Left) ||
		!slices.Equal(first.Commands, want.Commands) || first.Path != want.Path || first.Project != want.Project {
		t.Errorf("first run %+v, want %+v", first, want)
	}
	second := newJSON(t, cli.Options{}, "new", "repository", folder)
	if !slices.Equal(second.Written, []string{}) || !slices.Equal(second.Left, paths) {
		t.Errorf("second run wrote %v and left %v", second.Written, second.Left)
	}
	_, stdout, _ := runNew(cli.Options{}, "new", "repository", folder, "--json")
	if !strings.Contains(stdout, `"written":[]`) {
		t.Errorf("an empty list is not an empty array: %s", stdout)
	}
}

// The project is the folder's name made a project name, as adopt makes one, in the files and in the
// commands; --project on a repository changes only the name in the commands.
func TestNewNamesTheProjectAfterTheFolder(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "Orchid Shop")
	result := newJSON(t, cli.Options{}, "new", "monorepo", folder)
	if result.Project != "orchid-shop" {
		t.Errorf("project %q", result.Project)
	}
	plan, err := os.ReadFile(filepath.Join(folder, "plan", "plan.md"))
	if err != nil || !strings.Contains(string(plan), `id = "orchid-shop-v1"`) {
		t.Errorf("the plan is not orchid-shop's (%v):\n%s", err, plan)
	}
	if want := projectfiles.Commands(projectfiles.Monorepo, folder, "orchid-shop", nil, true); !slices.Equal(result.Commands, want) {
		t.Errorf("commands %v, want %v", result.Commands, want)
	}

	code := filepath.Join(t.TempDir(), "orchid-api")
	named := newJSON(t, cli.Options{}, "new", "repository", code, "--project", "orchid")
	if want := projectfiles.Commands(projectfiles.Repository, code, "orchid", nil, true); !slices.Equal(named.Commands, want) {
		t.Errorf("commands %v, want %v", named.Commands, want)
	}
	catalog, err := os.ReadFile(filepath.Join(code, "catalog-info.yaml"))
	if err != nil || !strings.Contains(string(catalog), "name: orchid-api") {
		t.Errorf("the catalog does not name the folder's project (%v):\n%s", err, catalog)
	}
}

// What `new` cannot write is a usage error before anything is written: the folder is not even created.
func TestNewRefusesWhatItCannotWriteBeforeWritingAnything(t *testing.T) {
	cases := []struct {
		name string
		args func(folder string) []string
		says string
	}{
		{"no folder", func(string) []string { return []string{"new", "workspace"} }, "folder"},
		{"two folders", func(folder string) []string { return []string{"new", "monorepo", folder, folder + "-2"} }, "unexpected"},
		{"a folder that makes no project name", func(folder string) []string {
			return []string{"new", "monorepo", filepath.Join(filepath.Dir(folder), "___")}
		}, "rename"},
		{"a repository with no remote given", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api"}
		}, "name=remote"},
		{"a repository name the ledger refuses", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "-api=git@example.invalid:o/a.git"}
		}, "dash"},
		{"a repository name that runs a command in the Makefile", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "x;do:;done;touch${IFS}p;#=" + orchidAPI.Remote}
		}, "letters, digits"},
		{"a repository named twice", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api=" + orchidAPI.Remote, "--repository", "orchid-api=" + orchidWeb.Remote}
		}, "twice"},
		{"a repository with an empty remote", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api="}
		}, "remote"},
		{"a remote that ends the manifest's string", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", `orchid-api=https://example.invalid/a".git`}
		}, "remote"},
		{"a remote with a newline", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api=https://example.invalid/a\nb.git"}
		}, "remote"},
		{"a remote with a tab", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api=https://example.invalid/a\tb.git"}
		}, "remote"},
		{"a remote that runs a command in the Makefile", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api=https://example.invalid/$(touch pwned).git"}
		}, "remote"},
		{"a remote that is no git remote", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api=r1"}
		}, "remote"},
		{"a local path as the remote", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api=/srv/repo.git"}
		}, "use a file:// URL"},
		{"a relative path as the remote", func(folder string) []string {
			return []string{"new", "workspace", folder, "--repository", "orchid-api=../repo"}
		}, "use a file:// URL"},
		{"a repository on a monorepo", func(folder string) []string {
			return []string{"new", "monorepo", folder, "--repository", "orchid-api=r1"}
		}, "usage"},
		{"a project on a workspace", func(folder string) []string {
			return []string{"new", "workspace", folder, "--project", "orchid"}
		}, "usage"},
		{"a project that is no project name", func(folder string) []string {
			return []string{"new", "repository", folder, "--project", "Orchid Shop"}
		}, "Orchid Shop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			folder := filepath.Join(t.TempDir(), "orchid")
			args := tc.args(folder)
			code, stdout, stderr := runNew(cli.Options{}, args...)
			if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, tc.says) {
				t.Errorf("exit %d, stdout %q, stderr %q, want exit %d saying %q", code, stdout, stderr, cli.ExitUsage, tc.says)
			}
			for _, path := range args[2:] {
				if !strings.HasPrefix(path, "-") && filepath.IsAbs(path) {
					if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("%s exists after a refusal (%v)", path, err)
					}
				}
			}
		})
	}
}

// FR-TPL-2: the git commands that give the folder a repository of its own are printed when no clone holds
// it, or when the clone that holds it ignores it, and left out when the folder is a clone's top or a clone
// tracks it. The decision is made after the files are written.
func TestNewPrintsTheGitCommandsOnlyForAFolderWithNoRepositoryOfItsOwn(t *testing.T) {
	cases := []struct {
		name    string
		top     func(folder string) string
		ignores bool
		git     bool
	}{
		{"no clone holds it", func(string) string { return "" }, false, true},
		{"it is a clone's top", func(folder string) string { return folder }, false, false},
		{"a clone holds it and ignores it", func(folder string) string { return filepath.Dir(filepath.Dir(folder)) }, true, true},
		{"a clone holds it and tracks it", func(folder string) string { return filepath.Dir(filepath.Dir(folder)) }, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			folder := filepath.Join(root, "repos", "orchid-api")
			var asked []string
			opts := cli.Options{
				CloneTop: func(_ context.Context, dir string) (string, error) {
					if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
						t.Errorf("asked for the clone before the files were written: %v", err)
					}
					asked = append(asked, "top "+dir)
					return tc.top(dir), nil
				},
				CloneIgnores: func(_ context.Context, top, path string) (bool, error) {
					asked = append(asked, "ignores "+top+" "+path)
					return tc.ignores, nil
				},
			}
			result := newJSON(t, opts, "new", "repository", folder)
			if want := projectfiles.Commands(projectfiles.Repository, folder, "orchid-api", nil, tc.git); !slices.Equal(result.Commands, want) {
				t.Errorf("commands %v, want %v", result.Commands, want)
			}
			top := tc.top(folder)
			want := []string{"top " + folder}
			if top != "" && top != folder {
				want = append(want, "ignores "+top+" "+folder)
			}
			if !slices.Equal(asked, want) {
				t.Errorf("asked %v, want %v", asked, want)
			}
		})
	}
	folder := filepath.Join(t.TempDir(), "orchid")
	if result := newJSON(t, cli.Options{}, "new", "monorepo", folder); !slices.Equal(result.Commands, projectfiles.Commands(projectfiles.Monorepo, folder, "orchid", nil, true)) {
		t.Errorf("with no way to ask git, the commands are %v", result.Commands)
	}
}

// The text lists each file, then the commands that register the folder, one to a line.
func TestNewPrintsTheCommandsAfterTheFiles(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	code, stdout, _ := runNew(cli.Options{}, "new", "workspace", folder, "--repository", "orchid-api="+orchidAPI.Remote)
	if code != cli.ExitOK {
		t.Fatalf("exit %d", code)
	}
	commands := projectfiles.Commands(projectfiles.Workspace, folder, "orchid", []projectfiles.CodeRepository{orchidAPI}, true)
	last := strings.LastIndex(stdout, "wrote\t")
	for _, command := range commands {
		at := strings.Index(stdout, "  "+command+"\n")
		if at < last {
			t.Errorf("%q is not printed after the files:\n%s", command, stdout)
		}
	}
}

// `new` reads nothing but the binary and the folder: no socket, no session, and no project.
func TestNewNeedsNoDaemonAndNoSession(t *testing.T) {
	for name, options := range map[string]cli.Options{
		"a dead socket":      {Socket: filepath.Join(t.TempDir(), "no-daemon.sock")},
		"a stale session":    {Socket: filepath.Join(t.TempDir(), "no-daemon.sock"), Session: "stale"},
		"an unnamed project": {Project: "no-such-project"},
	} {
		folder := filepath.Join(t.TempDir(), "orchid")
		if code, _, stderr := runNew(options, "new", "monorepo", folder); code != cli.ExitOK || stderr != "" {
			t.Errorf("%s: exit %d, stderr %q", name, code, stderr)
		}
	}
}

// The three commands sit in the Templates section, take no project, and each takes only its own flags.
func TestNewIsInTheTemplatesSection(t *testing.T) {
	groups := cli.Groups()
	templatesGroup := groups[len(groups)-1]
	var names []string
	for _, command := range templatesGroup.Commands {
		names = append(names, command.Name)
	}
	for _, name := range []string{"new workspace", "new monorepo", "new repository", "template show"} {
		if !slices.Contains(names, name) {
			t.Errorf("the %s section holds %v, not %s", templatesGroup.Title, names, name)
		}
	}
	for shape, flags := range map[string][]string{"workspace": {"-repository"}, "monorepo": nil, "repository": {"-project"}} {
		code, stdout, _ := runNew(cli.Options{}, "new", shape, "-h")
		if code != cli.ExitOK {
			t.Errorf("new %s -h: exit %d", shape, code)
		}
		for _, flag := range []string{"-repository", "-project"} {
			if strings.Contains(stdout, flag) != slices.Contains(flags, flag) {
				t.Errorf("new %s -h, and %s is not among its flags %v:\n%s", shape, flag, flags, stdout)
			}
		}
	}
}

// The commands that make the first commit add the files `new` wrote and not one that was in the folder already.
func TestNewPrintsAnAddThatLeavesOutTheFilesThatWereThere(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result := newJSON(t, cli.Options{}, "new", "monorepo", folder)
	paths := slices.DeleteFunc(slices.Sorted(maps.Keys(startingFiles(t, projectfiles.Monorepo, "orchid"))), func(p string) bool { return p == "AGENTS.md" })
	git := "git -C " + folder + " --literal-pathspecs"
	want := git + " ls-files -z --others --exclude-standard -- " + strings.Join(paths, " ") + " | xargs -0 " + git + " add --"
	if !slices.Contains(result.Commands, want) {
		t.Errorf("the commands lack %q:\n%s", want, strings.Join(result.Commands, "\n"))
	}
}

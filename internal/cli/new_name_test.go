package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// --name names the project in the files and in the commands that register it, and the folder's name stays the default.
func TestNewNameReplacesTheFoldersNameInTheFilesAndTheCommands(t *testing.T) {
	cases := map[string]struct {
		shape    projectfiles.Shape
		register string
	}{
		"workspace": {projectfiles.Workspace, "make -C %s register"},
		"monorepo":  {projectfiles.Monorepo, "ropeshift repository add %s --project harbor"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			folder := filepath.Join(t.TempDir(), "Orchid Shop")
			result := newJSON(t, cli.Options{}, "new", name, folder, "--name", "harbor")
			if result.Project != "harbor" {
				t.Errorf("project %q, want harbor", result.Project)
			}
			want := startingFiles(t, tc.shape, "harbor")
			for path, text := range want {
				if raw, err := os.ReadFile(filepath.Join(folder, path)); err != nil || string(raw) != text {
					t.Errorf("%s is not rendered for harbor (%v)", path, err)
				}
			}
			if !slices.Contains(result.Commands, "ropeshift project create harbor") && tc.shape == projectfiles.Monorepo {
				t.Errorf("commands %v lack the project harbor", result.Commands)
			}
			if registered := strings.Replace(tc.register, "%s", projectfiles.Quote(folder), 1); !slices.Contains(result.Commands, registered) {
				t.Errorf("commands %v lack %q", result.Commands, registered)
			}
			for _, command := range result.Commands {
				if strings.Contains(command, "orchid-shop") {
					t.Errorf("the command %q names the folder's project", command)
				}
			}
		})
	}
}

// A folder whose name makes no project name is fine when the name is given.
func TestNewNameServesAFolderThatMakesNoName(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "!!!")
	result := newJSON(t, cli.Options{}, "new", "monorepo", folder, "--name", "harbor")
	if result.Project != "harbor" {
		t.Errorf("project %q", result.Project)
	}
}

// A name the ledger refuses is a usage error, said before anything is written.
func TestNewNameRefusesAnInvalidNameBeforeWritingAnything(t *testing.T) {
	for _, name := range []string{"Harbor", "har bor", "-harbor", "harbor-", "a--b", strings.Repeat("a", 64)} {
		folder := filepath.Join(t.TempDir(), "orchid")
		code, _, stderr := runNew(cli.Options{}, "new", "monorepo", folder, "--name", name)
		if code != cli.ExitUsage || !strings.Contains(stderr, "--name") {
			t.Errorf("--name %q: exit %d, stderr %q", name, code, stderr)
		}
		requireAbsent(t, folder)
	}
	if code, _, stderr := runNew(cli.Options{}, "new", "monorepo", filepath.Join(t.TempDir(), "x"), "--name", ""); code != cli.ExitUsage || !strings.Contains(stderr, "--name") {
		t.Errorf("an empty --name: exit %d, stderr %q", code, stderr)
	}
}

// On a repository, --name is the name in the files and --project is the project the printed command registers it in:
// each keeps its own meaning, together or apart.
func TestNewRepositoryNameAndProjectKeepTheirOwnMeanings(t *testing.T) {
	catalogName := func(t *testing.T, folder string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(folder, "catalog-info.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	t.Run("both", func(t *testing.T) {
		folder := filepath.Join(t.TempDir(), "code")
		result := newJSON(t, cli.Options{}, "new", "repository", folder, "--name", "harbor-api", "--project", "harbor")
		if !strings.Contains(catalogName(t, folder), "name: harbor-api") {
			t.Error("the catalog does not name harbor-api")
		}
		if want := projectfiles.Commands(projectfiles.Repository, folder, "harbor", nil, true); !slices.Equal(result.Commands, want) {
			t.Errorf("commands %v, want %v", result.Commands, want)
		}
		if result.Project != "harbor-api" {
			t.Errorf("the name is %q", result.Project)
		}
	})
	t.Run("name alone", func(t *testing.T) {
		folder := filepath.Join(t.TempDir(), "code")
		result := newJSON(t, cli.Options{}, "new", "repository", folder, "--name", "harbor-api")
		if !strings.Contains(catalogName(t, folder), "name: harbor-api") {
			t.Error("the catalog does not name harbor-api")
		}
		if want := projectfiles.Commands(projectfiles.Repository, folder, "harbor-api", nil, true); !slices.Equal(result.Commands, want) {
			t.Errorf("commands %v, want %v", result.Commands, want)
		}
	})
	t.Run("project alone", func(t *testing.T) {
		folder := filepath.Join(t.TempDir(), "code")
		newJSON(t, cli.Options{}, "new", "repository", folder, "--project", "harbor")
		if !strings.Contains(catalogName(t, folder), "name: code") {
			t.Error("the catalog does not name the folder's name")
		}
	})
}

// The request a run makes for a first commit and a remote carries the name, beside the project of a repository.
func TestNewNameReachesTheRequestAMachineRuns(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	var p project
	code, _, stderr := runNew(p.options(""), "new", "repository", folder, "--name", "harbor-api", "--project", "harbor", "--first-commit")
	if code != cli.ExitOK || stderr != "" || len(p.requests) != 1 {
		t.Fatalf("exit %d, stderr %q, requests %v", code, stderr, p.requests)
	}
	if got := p.requests[0]; got.Name != "harbor-api" || got.Project != "harbor" {
		t.Errorf("the request names %q and registers in %q", got.Name, got.Project)
	}
	var m project
	if code, _, _ := runNew(m.options(""), "new", "monorepo", folder, "--name", "harbor", "--first-commit"); code != cli.ExitOK || m.requests[0].Name != "harbor" || m.requests[0].Project != "" {
		t.Errorf("a monorepo's request is %+v", m.requests)
	}
}

// With no --name the request carries none, and the folder names the project as before.
func TestNewWithNoNameLeavesTheRequestsNameEmpty(t *testing.T) {
	var p project
	runNew(p.options(""), "new", "monorepo", filepath.Join(t.TempDir(), "orchid"), "--first-commit")
	if len(p.requests) != 1 || p.requests[0].Name != "" {
		t.Errorf("requests %+v", p.requests)
	}
}

// The dry run shows the name it would use, in the text and the document.
func TestNewDryRunShowsTheChosenName(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "orchid")
	if dry := dryJSON(t, dryOptions(t), "new", "monorepo", folder, "--name", "harbor"); dry.Project != "harbor" {
		t.Errorf("the plan names %q", dry.Project)
	} else if !slices.Contains(dry.Commands, "ropeshift project create harbor") {
		t.Errorf("the plan's commands are %v", dry.Commands)
	}
	_, stdout, _ := runNew(dryOptions(t), "new", "monorepo", folder, "--name", "harbor", "--dry-run")
	if !strings.Contains(stdout, "project\tharbor") {
		t.Errorf("the text does not name the project:\n%s", stdout)
	}
	requireAbsent(t, folder)
}

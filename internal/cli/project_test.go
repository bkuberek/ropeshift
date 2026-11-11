package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// acmeRepository is the identity of the repository the project acme holds in these tests.
const acmeRepository = "example.com/acme/invoices"

// projects is an installation with the first project and acme, a work order in each, and acme holding
// one repository.
type projects struct {
	t  *testing.T
	in *apitest.Installation
}

func newProjects(t *testing.T) *projects {
	t.Helper()
	p := &projects{t: t, in: apitest.StartInstallation(t, "acme")}
	_, err := p.in.State.Installation.IndexRepositories(t.Context(), []ledger.IndexedRepository{
		{Identity: acmeRepository, Project: "acme-id", Repository: "r1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.must(cli.Options{}, "work", "create", "--project", ledger.DefaultProjectName, "--id", "wo-home", "--title", "Add the retry budget", "--criterion", "It retries")
	p.must(cli.Options{}, "work", "create", "--project", "acme", "--id", "wo-acme", "--title", "Ship the invoice export", "--criterion", "It exports")
	return p
}

// run runs a command as the installation's member, from options that stand for the shell's environment
// and the repository it runs in.
func (p *projects) run(opts cli.Options, args ...string) (int, string, string) {
	p.t.Helper()
	var stdout, stderr bytes.Buffer
	opts.Socket, opts.Session = p.in.Socket, p.in.Session
	code := cli.Run(args, &stdout, &stderr, opts)
	return code, stdout.String(), stderr.String()
}

func (p *projects) must(opts cli.Options, args ...string) string {
	p.t.Helper()
	code, stdout, stderr := p.run(opts, args...)
	if code != cli.ExitOK {
		p.t.Fatalf("%v: exit %d: %s", args, code, stderr)
	}
	return stdout
}

func inRepository(identity string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return identity, nil }
}

// TestACommandFindsItsProjectInOrder holds FR-PROJ-4: --project, then ROPESHIFT_PROJECT, then the
// project that holds the repository the command runs in, then the only active project.
func TestACommandFindsItsProjectInOrder(t *testing.T) {
	p := newProjects(t)
	tests := []struct {
		name string
		opts cli.Options
		args []string
		want string
	}{
		{"the flag", cli.Options{}, []string{"--project", "acme"}, "wo-acme"},
		{"the variable", cli.Options{Project: "acme"}, nil, "wo-acme"},
		{"the flag over the variable", cli.Options{Project: "acme"}, []string{"--project", ledger.DefaultProjectName}, "wo-home"},
		{"the repository it runs in", cli.Options{RepositoryHere: inRepository(acmeRepository)}, nil, "wo-acme"},
		{"the variable over the repository", cli.Options{Project: ledger.DefaultProjectName, RepositoryHere: inRepository(acmeRepository)}, nil, "wo-home"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout := p.must(tt.opts, append([]string{"work", "list"}, tt.args...)...)
			other := "wo-home"
			if tt.want == "wo-home" {
				other = "wo-acme"
			}
			if !strings.Contains(stdout, tt.want) || strings.Contains(stdout, other) {
				t.Errorf("listed %q, want only %s", stdout, tt.want)
			}
		})
	}
}

// TestACommandWithNoProjectToChooseListsThem holds the end of FR-PROJ-4: with two active projects, a
// repository no project holds, and no name, the command exits 2 and lists every project.
func TestACommandWithNoProjectToChooseListsThem(t *testing.T) {
	p := newProjects(t)
	for name, opts := range map[string]cli.Options{
		"outside any repository":           {},
		"in a repository no project holds": {RepositoryHere: inRepository("example.com/someone/else")},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := p.run(opts, "work", "list")
			if code != cli.ExitUsage || stdout != "" {
				t.Fatalf("exit %d, stdout %q", code, stdout)
			}
			for _, want := range []string{"--project", api.EnvProject, "default (active)", "acme (active)"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr %q lacks %q", stderr, want)
				}
			}
		})
	}
	code, _, stderr := p.run(cli.Options{}, "work", "list", "--json")
	if code != cli.ExitUsage || !strings.Contains(stderr, `"code":"no_project"`) {
		t.Errorf("--json: exit %d, stderr %q", code, stderr)
	}
}

// TestAProjectTheInstallationLacksListsTheOnesItHolds is a name from the flag or the variable that names
// no project.
func TestAProjectTheInstallationLacksListsTheOnesItHolds(t *testing.T) {
	p := newProjects(t)
	for name, opts := range map[string]cli.Options{"the flag": {}, "the variable": {Project: "nowhere"}} {
		t.Run(name, func(t *testing.T) {
			args := []string{"work", "show", "wo-acme"}
			if opts.Project == "" {
				args = append(args, "--project", "nowhere")
			}
			code, _, stderr := p.run(opts, args...)
			if code != cli.ExitUsage || !strings.Contains(stderr, `"nowhere"`) || !strings.Contains(stderr, "acme (active)") {
				t.Errorf("exit %d, stderr %q", code, stderr)
			}
		})
	}
}

// TestTheOnlyActiveProjectNeedsNoName is the common case: one project, and every command finds it.
func TestTheOnlyActiveProjectNeedsNoName(t *testing.T) {
	in := apitest.StartInstallation(t)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"work", "list"}, &stdout, &stderr, cli.Options{Socket: in.Socket, Session: in.Session})
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
}

// TestACommandOnTheInstallationTakesNoProject holds that the installation's own records belong to no
// project: such a command runs with several projects and refuses --project.
func TestACommandOnTheInstallationTakesNoProject(t *testing.T) {
	p := newProjects(t)
	for _, args := range [][]string{{"worker", "list"}, {"project", "list"}, {"health"}, {"telemetry", "lanes"}} {
		if code, _, stderr := p.run(cli.Options{}, args...); code != cli.ExitOK {
			t.Errorf("%v: exit %d: %s", args, code, stderr)
		}
		if code, _, _ := p.run(cli.Options{}, append(args, "--project", "acme")...); code != cli.ExitUsage {
			t.Errorf("%v --project: exit %d", args, code)
		}
	}
}

// TestProjectListAndShow print the registry: each project with its state and repositories, and one
// project's record with its database.
func TestProjectListAndShow(t *testing.T) {
	p := newProjects(t)
	list := p.must(cli.Options{}, "project", "list")
	for _, want := range []string{"default\tactive", "acme\tactive", "1 repository"} {
		if !strings.Contains(list, want) {
			t.Errorf("project list %q lacks %q", list, want)
		}
	}
	show := p.must(cli.Options{}, "project", "show", "acme")
	for _, want := range []string{"id\tacme-id", "database\tprojects/acme-id.db", "state\tactive"} {
		if !strings.Contains(show, want) {
			t.Errorf("project show %q lacks %q", show, want)
		}
	}
	if code, _, _ := p.run(cli.Options{}, "project", "show", "nowhere"); code != cli.ExitUsage {
		t.Errorf("an unknown project: exit %d", code)
	}
}

// TestTheProjectCommandsWalkTheLifecycle holds the lifecycle from the command line: create, rename,
// archive, which makes the project read-only, restore, and remove with its guards.
func TestTheProjectCommandsWalkTheLifecycle(t *testing.T) {
	p := newProjects(t)
	none := cli.Options{}
	if out := p.must(none, "project", "create", "beta", "--display-name", "Beta"); !strings.Contains(out, "created project beta") {
		t.Errorf("create: %q", out)
	}
	if code, _, _ := p.run(none, "project", "create", "beta"); code != cli.ExitConflict {
		t.Errorf("a name another project holds: exit %d", code)
	}
	if code, _, _ := p.run(none, "project", "create", "Not A Slug"); code != cli.ExitUsage {
		t.Errorf("a name that is no slug: exit %d", code)
	}
	p.must(none, "work", "create", "--project", "beta", "--id", "wo-beta", "--title", "Plan the beta", "--criterion", "It plans")
	p.must(none, "project", "rename", "beta", "gamma")
	if out := p.must(none, "work", "list", "--project", "gamma"); !strings.Contains(out, "wo-beta") {
		t.Errorf("gamma lists %q", out)
	}
	p.must(none, "project", "archive", "gamma")
	if code, _, stderr := p.run(none, "work", "create", "--project", "gamma", "--id", "wo-2", "--title", "More", "--criterion", "C"); code != cli.ExitConflict || !strings.Contains(stderr, "archived") {
		t.Errorf("a change to an archived project: exit %d, %s", code, stderr)
	}
	if out := p.must(none, "project", "list"); !strings.Contains(out, "gamma\tarchived") {
		t.Errorf("project list %q", out)
	}
	p.must(none, "project", "restore", "gamma")
	p.must(none, "project", "archive", "gamma")
	for name, args := range map[string][]string{
		"no confirmation":    {"project", "remove", "gamma", "--no-backup"},
		"the wrong name":     {"project", "remove", "gamma", "--confirm", "gama", "--no-backup"},
		"no backup choice":   {"project", "remove", "gamma", "--confirm", "gamma"},
		"two backup choices": {"project", "remove", "gamma", "--confirm", "gamma", "--no-backup", "--backup", "x.db"},
	} {
		if code, _, _ := p.run(none, args...); code != cli.ExitUsage {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	if code, _, _ := p.run(none, "project", "remove", "acme", "--confirm", "acme", "--no-backup"); code != cli.ExitConflict {
		t.Errorf("an active project: exit %d", code)
	}
	backup := filepath.Join(t.TempDir(), "gamma.db")
	if out := p.must(none, "project", "remove", "gamma", "--confirm", "gamma", "--backup", backup); !strings.Contains(out, backup) {
		t.Errorf("remove: %q", out)
	}
	if code, _, _ := p.run(none, "project", "show", "gamma"); code != cli.ExitUsage {
		t.Errorf("a removed project: exit %d", code)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Errorf("the backup: %v", err)
	}
}

// TestABackupImportsIntoAFreshInstallationWithItsID holds FR-PROJ-9 end to end: a project's backup from one
// installation registers in a fresh one with its ID, its name, and its records, and only once.
func TestABackupImportsIntoAFreshInstallationWithItsID(t *testing.T) {
	from := newProjects(t)
	dir := t.TempDir()
	t.Chdir(dir)
	from.must(cli.Options{}, "backup", "--project", "acme", "acme.db")

	to := &projects{t: t, in: apitest.StartInstallation(t)}
	if out := to.must(cli.Options{}, "project", "import", "acme.db"); !strings.Contains(out, "imported project acme, acme-id") {
		t.Errorf("import: %q", out)
	}
	if out := to.must(cli.Options{}, "project", "show", "acme"); !strings.Contains(out, "id\tacme-id") {
		t.Errorf("show: %q", out)
	}
	if out := to.must(cli.Options{}, "work", "show", "wo-acme", "--project", "acme"); !strings.Contains(out, "Ship the invoice export") {
		t.Errorf("work show: %q", out)
	}
	if code, _, stderr := to.run(cli.Options{}, "project", "import", "acme.db", "--name", "acme-again"); code != cli.ExitConflict || !strings.Contains(stderr, "acme-id") {
		t.Errorf("the same ID again: exit %d, %s", code, stderr)
	}
	if out := to.must(cli.Options{}, "project", "list"); !strings.Contains(out, "acme\tactive") || !strings.Contains(out, "default\tactive") {
		t.Errorf("project list %q", out)
	}
}

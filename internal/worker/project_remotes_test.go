package worker_test

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// lateForge is the forge account a test puts behind the worker after it started: with none, the worker has no gh.
type lateForge struct{ target ports.ForgeAccounts }

func (f *lateForge) Account(ctx context.Context, host string) (ports.ForgeAccount, error) {
	if f.target == nil {
		return ports.ForgeAccount{State: ledger.ForgeAccountMissing}, nil
	}
	return f.target.Account(ctx, host)
}

func (f *lateForge) Owners(ctx context.Context, host string) ([]string, error) {
	return f.target.Owners(ctx, host)
}

func (f *lateForge) Exists(ctx context.Context, host, owner, name string) (bool, error) {
	return f.target.Exists(ctx, host, owner, name)
}

func (f *lateForge) Create(ctx context.Context, host string, repository ports.CreateRepository) error {
	return f.target.Create(ctx, host, repository)
}

func (r *templateRig) bare(name string) string {
	r.t.Helper()
	dir := r.folder(name)
	cmd := exec.Command("git", "init", "--bare", "--quiet", dir)
	cmd.Env = r.env
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	return dir
}

func (r *templateRig) heads(bare string) []string {
	r.t.Helper()
	return strings.Fields(r.git(bare, "for-each-ref", "--format=%(objectname)", "refs/heads"))
}

func steps(report ledger.RepositoryReport) string {
	var out []string
	for _, s := range report.Steps {
		out = append(out, s.Step+"="+s.Outcome)
	}
	return strings.Join(out, " ")
}

// A worker adds a URL as origin and pushes the first commit, with real git, and reports each step.
func TestAWorkerAddsAURLAsOriginAndPushesTheFirstCommit(t *testing.T) {
	r := newTemplateRig(t, nil)
	bare := r.bare("remote.git")
	folder := r.folder("harbor")
	id := r.ask(ledger.TemplateWriteRequest{
		Shape: "monorepo", Path: folder, FirstCommit: true, Remote: &ledger.ProjectRemote{URL: "file://" + bare},
	})
	view := r.settle(id)
	if view.State != ledger.TemplateWriteWritten || view.Report == nil {
		t.Fatalf("view = %+v", view)
	}
	own := view.Report.Repositories
	if len(own) != 1 || steps(own[0]) != "origin=done push=done" || own[0].Commit != view.Report.Commit {
		t.Fatalf("repositories = %+v", own)
	}
	if got := r.heads(bare); !slices.Equal(got, []string{view.Report.Commit}) {
		t.Errorf("the remote holds %v, want the first commit %s", got, view.Report.Commit)
	}
	if got := r.git(folder, "remote", "get-url", "origin"); got != "file://"+bare {
		t.Errorf("origin = %q", got)
	}
	if len(view.Report.Remaining) != 0 {
		t.Errorf("remaining = %v", view.Report.Remaining)
	}
}

// A push git refuses leaves the files, the commit, and the origin, and names the push as what remains.
func TestAPushThatFailsKeepsTheFilesTheCommitAndTheOrigin(t *testing.T) {
	r := newTemplateRig(t, nil)
	missing := r.folder("nowhere.git")
	folder := r.folder("harbor")
	view := r.settle(r.ask(ledger.TemplateWriteRequest{
		Shape: "monorepo", Path: folder, FirstCommit: true, Remote: &ledger.ProjectRemote{URL: "file://" + missing},
	}))
	if view.State != ledger.TemplateWriteWritten || view.Report.Commit == "" {
		t.Fatalf("view = %+v", view)
	}
	got := view.Report.Repositories[0]
	if steps(got) != "origin=done push=failed" || got.Steps[1].Detail == "" {
		t.Errorf("steps = %s: %+v", steps(got), got.Steps)
	}
	if len(view.Report.Remaining) != 1 || !strings.Contains(view.Report.Remaining[0], "push --set-upstream origin") {
		t.Errorf("remaining = %v", view.Report.Remaining)
	}
	if _, err := os.Stat(folder + "/AGENTS.md"); err != nil {
		t.Errorf("the files stay: %v", err)
	}
}

// A workspace writes each new code repository at repos/<name> with a first commit of its own, and its own first commit
// holds none of them.
func TestAWorkspaceMakesEachNewCodeRepositoryItsOwnRepository(t *testing.T) {
	r := newTemplateRig(t, nil)
	bare := r.bare("api.git")
	folder := r.folder("harbor")
	view := r.settle(r.ask(ledger.TemplateWriteRequest{
		Shape: "workspace", Path: folder, FirstCommit: true,
		Repositories: []ledger.TemplateWriteRepository{
			{Name: "api", New: true, URL: "file://" + bare},
			{Name: "web", New: true},
			{Name: "legacy", Remote: "git@example.com:acme/legacy.git"},
		},
	}))
	if view.State != ledger.TemplateWriteWritten {
		t.Fatalf("view = %+v", view)
	}
	for _, name := range []string{"api", "web"} {
		if r.git(folder+"/repos/"+name, "rev-list", "--count", "HEAD") != "1" {
			t.Errorf("repos/%s has no first commit of its own", name)
		}
	}
	if _, err := os.Stat(folder + "/repos/legacy"); !os.IsNotExist(err) {
		t.Errorf("an existing repository gets no folder: %v", err)
	}
	if tracked := r.git(folder, "ls-files"); strings.Contains(tracked, "repos/") {
		t.Errorf("the workspace's first commit holds %q", tracked)
	}
	manifest, err := os.ReadFile(folder + "/plan/repositories.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"repository api=file://" + bare, "repository web=", "repository legacy=git@example.com:acme/legacy.git"} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("the manifest lacks %q", want)
		}
	}
	if got := r.heads(bare); len(got) != 1 || got[0] != r.git(folder+"/repos/api", "rev-parse", "HEAD") {
		t.Errorf("the remote of api holds %v", got)
	}
	names := map[string]string{}
	for _, repository := range view.Report.Repositories {
		names[repository.Name] = steps(repository)
	}
	if len(names) != 3 || names["api"] != "origin=done push=done" || names["web"] != "" || names["harbor"] != "" {
		t.Errorf("repositories = %v", names)
	}
}

// A check that fails creates nothing and writes nothing, and its report carries the command the person runs.
func TestAWorkerThatCannotCreateARepositoryWritesNothing(t *testing.T) {
	r := newTemplateRig(t, nil)
	forge := fake.NewForgeAccounts()
	forge.SignIn.State = ledger.ForgeAccountSignedOut
	r.forge.(*lateForge).target = forge
	folder := r.folder("harbor")
	view := r.settle(r.ask(ledger.TemplateWriteRequest{
		Shape: "monorepo", Path: folder, FirstCommit: true,
		Remote: &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "test"}},
	}))
	if view.State != ledger.TemplateWriteRefused || view.Report.Reason != ledger.TemplateRefusedGHSignedOut || view.Report.Remedy != "gh auth login --hostname github.com" {
		t.Fatalf("view = %+v", view)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("a refusal made the folder: %v", err)
	}
	if len(forge.Repositories()) != 0 {
		t.Errorf("a refusal created %+v", forge.Repositories())
	}
}

// A worker with no gh refuses a request to create a repository as gh_missing.
func TestAWorkerWithNoGHRefusesToCreateARepository(t *testing.T) {
	r := newTemplateRig(t, nil)
	view := r.settle(r.ask(ledger.TemplateWriteRequest{
		Shape: "monorepo", Path: r.folder("harbor"), FirstCommit: true,
		Remote: &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "test"}},
	}))
	if view.State != ledger.TemplateWriteRefused || view.Report.Reason != ledger.TemplateRefusedGHMissing {
		t.Fatalf("view = %+v", view)
	}
}

// A forge account read is answered from the worker's gh.
func TestAWorkerAnswersAForgeAccountRead(t *testing.T) {
	r := newTemplateRig(t, nil)
	forge := fake.NewForgeAccounts()
	forge.Orgs = []string{"acme-co"}
	r.forge.(*lateForge).target = forge
	reads := control.NewForgeAccountReads(r.store.Installation(scope), 60_000)
	asked, err := reads.Request(r.t.Context(), r.subject.Auth().WorkerID, ledger.ForgeAccountRequest{Forge: "github"}, member)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Converge(r.t.Context()); err != nil {
		t.Fatal(err)
	}
	view, err := reads.View(r.t.Context(), r.subject.Auth().WorkerID, asked.ID)
	if err != nil || view.State != ledger.ForgeAccountReadReported || view.Report == nil {
		t.Fatalf("view = %+v (%v)", view, err)
	}
	if got := view.Report; got.State != ledger.ForgeAccountOK || got.Login != "test" || !slices.Equal(got.Owners, []string{"test", "acme-co"}) {
		t.Errorf("report = %+v", got)
	}
}

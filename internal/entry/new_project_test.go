package entry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectremote"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/doubles/ghstate"
	"ropeshift.com/ropeshift/test/gittest"
)

// forgeRig is the machine `ropeshift new` runs on in these tests: the fake gh first on PATH, a git configuration of the
// test's own that points https://github.com/ and git@github.com: at the fake's bare repositories, so no push reaches
// GitHub, and no real sign-in.
type forgeRig struct {
	t       *testing.T
	gh      string
	home    string
	root    string
	workers string
}

func newForgeRig(t *testing.T) *forgeRig {
	t.Helper()
	gittest.Require(t)
	r := &forgeRig{t: t, gh: doubles.InstallFakeGH(t), home: t.TempDir(), root: t.TempDir(), workers: t.TempDir()}
	remotes := filepath.Join(r.gh, ghstate.RemotesDir) + "/"
	config := "[user]\n\tname = Test\n\temail = test@example.invalid\n[init]\n\tdefaultBranch = main\n" +
		"[url \"" + remotes + "\"]\n\tinsteadOf = https://github.com/\n\tinsteadOf = git@github.com:\n[protocol \"file\"]\n\tallow = always\n"
	if err := os.WriteFile(filepath.Join(r.home, "gitconfig"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", r.home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(r.home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("PATH", r.gh+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The worker's own gh configuration, which a lane never gets: its environment holds no token and an empty config dir.
	t.Setenv("GH_CONFIG_DIR", r.workers)
	t.Setenv("GH_TOKEN", "")
	return r
}

func (r *forgeRig) forge(change func(*ghstate.State)) {
	r.t.Helper()
	doubles.EditFakeGH(r.t, r.gh, change)
}

func (r *forgeRig) folder(name string) string { return filepath.Join(r.root, name) }

func (r *forgeRig) bare(owner, name string) string {
	return filepath.Join(r.gh, ghstate.RemotesDir, owner, name+".git")
}

func (r *forgeRig) git(dir string, args ...string) string {
	r.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *forgeRig) run(request ledger.TemplateWriteRequest) ledger.TemplateWriteReport {
	r.t.Helper()
	report, err := newProject(context.Background(), request, true)
	if err != nil {
		r.t.Fatal(err)
	}
	return report
}

// repoCreates are the repo create calls the fake gh took.
func (r *forgeRig) repoCreates() []ghstate.Call {
	var out []ghstate.Call
	for _, call := range doubles.FakeGHCalls(r.t, r.gh) {
		if len(call.Args) >= 2 && call.Args[0] == "repo" && call.Args[1] == "create" {
			out = append(out, call)
		}
	}
	return out
}

func stepsOf(report ledger.RepositoryReport) string {
	var out []string
	for _, s := range report.Steps {
		out = append(out, s.Step+"="+s.Outcome)
	}
	return strings.Join(out, " ")
}

func githubOf(owner, visibility string) *ledger.ProjectRemote {
	return &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: owner, Visibility: visibility}}
}

// FR-TPL-6: a repository is created private by default, the first commit is pushed to it, and gh runs with the
// worker's own environment, never a lane's.
func TestNewCreatesAPrivateRepositoryOnGitHubAndPushesTheFirstCommit(t *testing.T) {
	r := newForgeRig(t)
	folder := r.folder("acme")
	report := r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: githubOf("test", "")})
	if report.Outcome != ledger.TemplateOutcomeWritten || len(report.Repositories) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if got := stepsOf(report.Repositories[0]); got != "create=done origin=done push=done" {
		t.Errorf("steps = %s", got)
	}
	state := doubles.FakeGH(t, r.gh)
	if len(state.Repositories) != 1 || state.Repositories[0] != (ghstate.Repository{Owner: "test", Name: "acme", Visibility: "private"}) {
		t.Errorf("repositories = %+v", state.Repositories)
	}
	if got := r.git(r.bare("test", "acme"), "rev-parse", "refs/heads/main"); got != report.Commit {
		t.Errorf("the forge holds %s, want the first commit %s", got, report.Commit)
	}
	if got := r.git(folder, "config", "--get", "remote.origin.url"); got != "https://github.com/test/acme.git" {
		t.Errorf("origin = %q", got)
	}
	creates := r.repoCreates()
	if len(creates) != 1 || !slices.Equal(creates[0].Args, []string{"repo", "create", "test/acme", "--private"}) {
		t.Fatalf("repo create calls = %+v", creates)
	}
	if creates[0].Env["GH_CONFIG_DIR"] != r.workers {
		t.Errorf("repo create ran with GH_CONFIG_DIR %q, want the worker's %q", creates[0].Env["GH_CONFIG_DIR"], r.workers)
	}
	for _, call := range doubles.FakeGHCalls(t, r.gh) {
		for _, arg := range call.Args {
			if arg == "--internal" || arg == "--source" || arg == "--push" || arg == "--clone" {
				t.Errorf("gh ran with %s: %v", arg, call.Args)
			}
		}
	}
}

func TestNewCreatesAPublicRepositoryOnlyWhenAsked(t *testing.T) {
	r := newForgeRig(t)
	r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: r.folder("acme"), FirstCommit: true, Remote: githubOf("test", ledger.VisibilityPublic)})
	state := doubles.FakeGH(t, r.gh)
	if len(state.Repositories) != 1 || state.Repositories[0].Visibility != "public" {
		t.Errorf("repositories = %+v", state.Repositories)
	}
	if creates := r.repoCreates(); len(creates) != 1 || creates[0].Args[3] != "--public" {
		t.Errorf("repo create calls = %+v", creates)
	}
}

func TestNewPushesOverSSHWhenGHUsesSSH(t *testing.T) {
	r := newForgeRig(t)
	r.forge(func(s *ghstate.State) { s.GitProtocol = "ssh" })
	folder := r.folder("acme")
	report := r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: githubOf("test", "")})
	if got := r.git(folder, "config", "--get", "remote.origin.url"); got != "git@github.com:test/acme.git" {
		t.Errorf("origin = %q", got)
	}
	if got := r.git(r.bare("test", "acme"), "rev-parse", "refs/heads/main"); got != report.Commit {
		t.Errorf("the forge holds %s, want %s", got, report.Commit)
	}
}

// Each check refuses before anything is written or created, with the reason and the command for the person.
func TestNewRefusesBeforeItWritesOrCreatesAnything(t *testing.T) {
	cases := map[string]struct {
		change func(*ghstate.State)
		remote *ledger.ProjectRemote
		reason string
		remedy string
	}{
		"signed out":           {func(s *ghstate.State) { s.SignedOut, s.AuthError = true, "token invalid" }, githubOf("test", ""), ledger.TemplateRefusedGHSignedOut, "gh auth login --hostname github.com"},
		"an old gh":            {func(s *ghstate.State) { s.NoJSON = true }, githubOf("test", ""), ledger.TemplateRefusedGHOutdated, ""},
		"a token with no repo": {func(s *ghstate.State) { s.Scopes = []string{"gist"} }, githubOf("test", ""), ledger.TemplateRefusedGHScope, "gh auth refresh --hostname github.com --scopes repo"},
		"organizations hidden": {func(s *ghstate.State) { s.Scopes, s.OrgsForbidden = []string{"repo"}, true }, githubOf("acme-co", ""), ledger.TemplateRefusedGHScope, "gh auth refresh --hostname github.com --scopes read:org"},
		"an owner not offered": {func(s *ghstate.State) { s.Orgs = []string{"other"} }, githubOf("acme-co", ""), ledger.TemplateRefusedOwner, ""},
		"a repository there": {func(s *ghstate.State) {
			s.Repositories = []ghstate.Repository{{Owner: "test", Name: "acme", Visibility: "public"}}
		}, githubOf("test", ""), ledger.TemplateRefusedRemoteExists, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newForgeRig(t)
			r.forge(c.change)
			before := len(doubles.FakeGH(t, r.gh).Repositories)
			report := r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: r.folder("acme"), FirstCommit: true, Remote: c.remote})
			if report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != c.reason || report.Remedy != c.remedy || report.Detail == "" {
				t.Errorf("report = %+v", report)
			}
			if _, err := os.Stat(r.folder("acme")); !os.IsNotExist(err) {
				t.Errorf("a refusal made the folder: %v", err)
			}
			if len(r.repoCreates()) != 0 || len(doubles.FakeGH(t, r.gh).Repositories) != before {
				t.Error("a refusal created a repository")
			}
		})
	}
}

// With no gh on PATH, the request is refused with the install step.
func TestNewRefusesWhenGHIsNotInstalled(t *testing.T) {
	r := newForgeRig(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	report := r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: r.folder("acme"), FirstCommit: true, Remote: githubOf("test", "")})
	if report.Reason != ledger.TemplateRefusedGHMissing || !strings.Contains(report.Detail, "https://cli.github.com/") || report.Remedy != "gh auth login --hostname github.com" {
		t.Errorf("report = %+v", report)
	}
}

// A create gh refuses keeps the local repository and its commit, and names the commands that finish the rest.
func TestNewKeepsTheLocalRepositoryWhenCreateFails(t *testing.T) {
	r := newForgeRig(t)
	r.forge(func(s *ghstate.State) { s.CreateError = "GraphQL: acme-co forbids creating repositories" })
	folder := r.folder("acme")
	report := r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: githubOf("test", "")})
	if report.Outcome != ledger.TemplateOutcomeWritten || report.Commit == "" {
		t.Fatalf("report = %+v", report)
	}
	if got := stepsOf(report.Repositories[0]); got != "create=failed origin=skipped push=skipped" {
		t.Errorf("steps = %s", got)
	}
	if got := r.git(folder, "rev-parse", "HEAD"); got != report.Commit {
		t.Errorf("HEAD = %s", got)
	}
	want := []string{
		"gh repo create test/acme --private",
		"git -C " + folder + " remote add origin https://github.com/test/acme.git",
		"gh auth setup-git",
		"git -C " + folder + " push --set-upstream origin -- main",
	}
	if !slices.Equal(report.Remaining, want) {
		t.Errorf("remaining = %q, want %q", report.Remaining, want)
	}
}

// A push the forge refuses leaves the repository created and origin added, and names the push as what remains.
func TestNewKeepsTheCreatedRepositoryWhenThePushFails(t *testing.T) {
	r := newForgeRig(t)
	r.forge(func(s *ghstate.State) { s.RejectPush = true })
	folder := r.folder("acme")
	report := r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: githubOf("test", "")})
	if got := stepsOf(report.Repositories[0]); got != "create=done origin=done push=failed" {
		t.Errorf("steps = %s", got)
	}
	if detail := report.Repositories[0].Steps[2].Detail; !strings.Contains(detail, "push refused") {
		t.Errorf("the push says %q", detail)
	}
	if state := doubles.FakeGH(t, r.gh); len(state.Repositories) != 1 {
		t.Errorf("a failed push deleted the repository: %+v", state.Repositories)
	}
	if got := r.git(folder, "remote", "get-url", "origin"); got == "" {
		t.Error("origin is gone")
	}
	if len(report.Remaining) != 2 || report.Remaining[0] != "gh auth setup-git" {
		t.Errorf("remaining = %q", report.Remaining)
	}
}

// A crash after gh created the repository and before the journal recorded it is followed by a restart that finds the
// repository there, creates nothing twice, and finishes the origin and the push.
func TestARestartAfterTheCreateCreatesNothingTwice(t *testing.T) {
	r := newForgeRig(t)
	runtime := machineRuntime()
	folder := r.folder("acme")
	job := projectremote.Job{
		Request:         ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: githubOf("test", "")},
		NeedsRepository: true,
	}
	progress := &projectremote.Progress{}
	if refusal, err := projectremote.Start(t.Context(), runtime, job, progress); err != nil || refusal != nil {
		t.Fatalf("refusal = %+v, err = %v", refusal, err)
	}
	// The journal is what the last save kept. The save that would record the create as done is the one the crash loses.
	var journal []byte
	crashed := false
	save := func() error {
		if step := progress.Units["."].Steps["create"]; step != nil && step.State == "done" && !crashed {
			crashed = true
			return errors.New("the worker died")
		}
		var err error
		journal, err = json.Marshal(progress)
		return err
	}
	if _, err := projectremote.Finish(t.Context(), runtime, job, progress, save); err == nil {
		t.Fatal("the run did not crash")
	}
	if len(r.repoCreates()) != 1 {
		t.Fatalf("the first run created %d repositories", len(r.repoCreates()))
	}
	var restored projectremote.Progress
	if err := json.Unmarshal(journal, &restored); err != nil {
		t.Fatal(err)
	}
	if got := restored.Units["."].Steps["create"].State; got != "started" {
		t.Fatalf("the journal holds create as %q", got)
	}
	report, err := projectremote.Finish(t.Context(), runtime, job, &restored, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(report.Repositories[0]); got != "create=done origin=done push=done" {
		t.Errorf("steps = %s", got)
	}
	if len(r.repoCreates()) != 1 {
		t.Errorf("the restart created again: %d repo create calls", len(r.repoCreates()))
	}
	if got := r.git(r.bare("test", "acme"), "rev-parse", "refs/heads/main"); got != report.Commit {
		t.Errorf("the forge holds %s, want %s", got, report.Commit)
	}
}

// A URL remote is added and pushed with the person's own git credentials, and gh is never run.
func TestNewAddsAURLRemoteAndPushesWithNoCallToGH(t *testing.T) {
	r := newForgeRig(t)
	bare := r.bare("acme", "acme")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", "--quiet", "--initial-branch=main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	callsBefore := len(doubles.FakeGHCalls(t, r.gh))
	folder := r.folder("acme")
	report := r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: &ledger.ProjectRemote{URL: "https://github.com/acme/acme.git"}})
	if got := stepsOf(report.Repositories[0]); got != "origin=done push=done" {
		t.Fatalf("steps = %s: %+v", got, report)
	}
	if got := r.git(bare, "rev-parse", "refs/heads/main"); got != report.Commit {
		t.Errorf("the remote holds %s, want %s", got, report.Commit)
	}
	if got := len(doubles.FakeGHCalls(t, r.gh)); got != callsBefore {
		t.Errorf("a URL remote ran gh %d times", got-callsBefore)
	}
}

// A workspace makes each new code repository its own repository with its own commit and remote, and its manifest names each
// remote or none, which the first plan's approval registers.
func TestNewMakesAWorkspaceWithNewCodeRepositoriesEachWithItsOwnRemote(t *testing.T) {
	r := newForgeRig(t)
	r.forge(func(s *ghstate.State) { s.Orgs = []string{"acme-co"} })
	folder := r.folder("orchid")
	report := r.run(ledger.TemplateWriteRequest{
		Shape: "workspace", Path: folder, FirstCommit: true, Remote: githubOf("test", ""),
		Repositories: []ledger.TemplateWriteRepository{
			{Name: "orchid-api", New: true, Create: githubOf("acme-co", "").Create},
			{Name: "orchid-web", New: true},
			{Name: "orchid-old", Remote: "git@example.invalid:orchid/orchid-old.git"},
		},
	})
	if report.Outcome != ledger.TemplateOutcomeWritten || len(report.Repositories) != 3 {
		t.Fatalf("report = %+v", report)
	}
	state := doubles.FakeGH(t, r.gh)
	var made []string
	for _, repository := range state.Repositories {
		made = append(made, repository.Owner+"/"+repository.Name)
	}
	slices.Sort(made)
	if !slices.Equal(made, []string{"acme-co/orchid-api", "test/orchid"}) {
		t.Errorf("created %v", made)
	}
	for _, code := range []string{"orchid-api", "orchid-web"} {
		if got := r.git(filepath.Join(folder, "repos", code), "rev-list", "--count", "HEAD"); got != "1" {
			t.Errorf("repos/%s has %s commits, want its own first", code, got)
		}
	}
	if tracked := r.git(folder, "ls-files"); strings.Contains(tracked, "repos/") {
		t.Errorf("the workspace's first commit holds a code repository: %q", tracked)
	}
	if _, err := os.Stat(filepath.Join(folder, "repos", "orchid-old")); !os.IsNotExist(err) {
		t.Errorf("an existing repository got a folder: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(folder, "plan", "repositories.md"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(raw)
	for _, want := range []string{
		`remote = "https://github.com/acme-co/orchid-api.git"`, `path = "repos/orchid-api"`, `path = "repos/orchid-web"`,
		`remote = "git@example.invalid:orchid/orchid-old.git"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("the manifest lacks %s:\n%s", want, manifest)
		}
	}
	if strings.Count(manifest, "remote = ") != 2 {
		t.Errorf("a repository with no remote has no remote key, and the manifest holds %d:\n%s", strings.Count(manifest, "remote = "), manifest)
	}
	makefile, err := os.ReadFile(filepath.Join(folder, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"orchid-api=https://github.com/acme-co/orchid-api.git", "orchid-web=", "orchid-old=git@example.invalid:orchid/orchid-old.git"} {
		if !strings.Contains(string(makefile), want) {
			t.Errorf("the Makefile lacks %s", want)
		}
	}
	for _, code := range []string{"orchid-api"} {
		if got := r.git(r.bare("acme-co", code), "rev-parse", "refs/heads/main"); got != r.git(filepath.Join(folder, "repos", code), "rev-parse", "HEAD") {
			t.Errorf("the forge holds %s for %s", got, code)
		}
	}
}

// A refused check leaves no folder above the new one, and a request that passes its checks makes them.
func TestNewMakesTheFoldersAboveTheNewOneOnlyAfterItsChecksPass(t *testing.T) {
	r := newForgeRig(t)
	r.forge(func(s *ghstate.State) { s.SignedOut, s.AuthError = true, "token invalid" })
	parent := r.folder("code")
	folder := filepath.Join(parent, "deeper", "acme")
	report := r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: githubOf("test", "")})
	if report.Outcome != ledger.TemplateOutcomeRefused {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("a refused check left the parent folder: %v", err)
	}

	r.forge(func(s *ghstate.State) { s.SignedOut, s.AuthError = false, "" })
	report = r.run(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true, Remote: githubOf("test", "")})
	if report.Outcome != ledger.TemplateOutcomeWritten || report.Commit == "" {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(folder, "AGENTS.md")); err != nil {
		t.Errorf("the files are not in the folder: %v", err)
	}
}

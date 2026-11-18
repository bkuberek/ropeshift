package projectremote_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/projectremote"
)

// render prints what each file renders over, so a test reads the remote a manifest carries.
func render(name string, data projectfiles.Data) (string, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "%s project=%s", name, data.Project)
	for _, r := range data.Repositories {
		fmt.Fprintf(&out, " repo=%s=%s", r.Name, r.Remote)
	}
	return out.String(), nil
}

type machine struct {
	folders *fake.ProjectFolders
	forge   *fake.ForgeAccounts
	runtime projectremote.Runtime
}

func newMachine() *machine {
	m := &machine{folders: fake.NewProjectFolders(), forge: fake.NewForgeAccounts()}
	m.folders.Dir("/srv")
	m.runtime = projectremote.Runtime{Folders: m.folders, Forge: m.forge, Render: render, Machine: "atlas"}
	return m
}

func create(owner string) *ledger.ProjectRemote {
	return &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: owner}}
}

// run does what a worker does with a request: the checks, then the write, the commits, and the remote steps.
func (m *machine) run(t *testing.T, request ledger.TemplateWriteRequest, progress *projectremote.Progress) ledger.TemplateWriteReport {
	t.Helper()
	job := projectremote.Job{Request: request, NeedsRepository: true}
	refusal, err := projectremote.Start(context.Background(), m.runtime, job, progress)
	if err != nil {
		t.Fatal(err)
	}
	if refusal != nil {
		return *refusal
	}
	report, err := projectremote.Finish(context.Background(), m.runtime, job, progress, nil)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func monorepo(remote *ledger.ProjectRemote) ledger.TemplateWriteRequest {
	return ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/acme", FirstCommit: true, Remote: remote}
}

func steps(report ledger.RepositoryReport) string {
	var out []string
	for _, s := range report.Steps {
		out = append(out, s.Step+"="+s.Outcome)
	}
	return strings.Join(out, " ")
}

func TestACreatedRepositoryIsPrivateAndTheCommitIsPushedToIt(t *testing.T) {
	m := newMachine()
	report := m.run(t, monorepo(create("test")), &projectremote.Progress{})
	if report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("report = %+v", report)
	}
	if got := m.forge.Repositories(); len(got) != 1 || got[0] != (fake.Created{Owner: "test", Name: "acme"}) {
		t.Errorf("created %+v, want one private acme under test", got)
	}
	if origin := m.folders.Origin("/srv/acme"); origin != "https://github.com/test/acme.git" {
		t.Errorf("origin = %q", origin)
	}
	pushes := m.folders.Pushes()
	if len(pushes) != 1 || pushes[0].Branch != "main" || pushes[0].CredentialHelper != "/usr/bin/gh" {
		t.Errorf("pushes = %+v: an https push names gh as the credential helper", pushes)
	}
	if len(report.Repositories) != 1 {
		t.Fatalf("repositories = %+v", report.Repositories)
	}
	own := report.Repositories[0]
	if own.Path != "." || own.Commit == "" || own.Remote != "https://github.com/test/acme.git" || steps(own) != "create=done origin=done push=done" {
		t.Errorf("own = %+v (%s)", own, steps(own))
	}
	if len(report.Remaining) != 0 {
		t.Errorf("remaining = %v", report.Remaining)
	}
	if report.Commit == "" || !report.Clone {
		t.Errorf("commit %q, clone %v", report.Commit, report.Clone)
	}
}

func TestACreatedRepositoryOverSSHNamesNoCredentialHelper(t *testing.T) {
	m := newMachine()
	m.forge.SignIn.GitProtocol = "ssh"
	m.run(t, monorepo(create("test")), &projectremote.Progress{})
	if origin := m.folders.Origin("/srv/acme"); origin != "git@github.com:test/acme.git" {
		t.Errorf("origin = %q", origin)
	}
	if pushes := m.folders.Pushes(); len(pushes) != 1 || pushes[0].CredentialHelper != "" {
		t.Errorf("pushes = %+v", pushes)
	}
}

func TestARepositoryIsPublicOnlyWhenAsked(t *testing.T) {
	m := newMachine()
	public := &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "test", Visibility: ledger.VisibilityPublic}}
	m.run(t, monorepo(public), &projectremote.Progress{})
	if got := m.forge.Repositories(); len(got) != 1 || !got[0].Public {
		t.Errorf("created %+v, want one public repository", got)
	}
}

func TestAnOrganizationOwnsARepositoryWhenTheAccountBelongsToIt(t *testing.T) {
	m := newMachine()
	m.forge.Orgs = []string{"acme-co"}
	if report := m.run(t, monorepo(create("acme-co")), &projectremote.Progress{}); report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("report = %+v", report)
	}
	if got := m.forge.Repositories(); len(got) != 1 || got[0].Owner != "acme-co" {
		t.Errorf("created %+v", got)
	}
}

func TestEachCheckRefusesBeforeAnythingIsWrittenOrCreated(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fake.ForgeAccounts)
		remote *ledger.ProjectRemote
		reason string
		remedy string
	}{
		{"missing", func(f *fake.ForgeAccounts) { f.SignIn = ports.ForgeAccount{State: ledger.ForgeAccountMissing} }, create("test"), ledger.TemplateRefusedGHMissing, "gh auth login --hostname github.com"},
		{"outdated", func(f *fake.ForgeAccounts) { f.SignIn.State = ledger.ForgeAccountOutdated }, create("test"), ledger.TemplateRefusedGHOutdated, ""},
		{"signed out", func(f *fake.ForgeAccounts) { f.SignIn.State = ledger.ForgeAccountSignedOut }, create("test"), ledger.TemplateRefusedGHSignedOut, "gh auth login --hostname github.com"},
		{"no repo scope", func(f *fake.ForgeAccounts) { f.SignIn.Scopes = []string{"gist"} }, create("test"), ledger.TemplateRefusedGHScope, "gh auth refresh --hostname github.com --scopes repo"},
		{"public_repo is not enough for a private repository", func(f *fake.ForgeAccounts) { f.SignIn.Scopes = []string{"public_repo"} }, create("test"), ledger.TemplateRefusedGHScope, "gh auth refresh --hostname github.com --scopes repo"},
		{"organizations unreadable", func(f *fake.ForgeAccounts) { f.OrgsScopeMissing = true }, create("acme-co"), ledger.TemplateRefusedGHScope, "gh auth refresh --hostname github.com --scopes read:org"},
		{"owner not offered", func(f *fake.ForgeAccounts) { f.Orgs = []string{"other"} }, create("acme-co"), ledger.TemplateRefusedOwner, ""},
		{"repository exists", func(f *fake.ForgeAccounts) { f.Exist("test", "acme") }, create("test"), ledger.TemplateRefusedRemoteExists, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newMachine()
			c.change(m.forge)
			report := m.run(t, monorepo(c.remote), &projectremote.Progress{})
			if report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != c.reason || report.Remedy != c.remedy {
				t.Errorf("report = %+v, want %s with remedy %q", report, c.reason, c.remedy)
			}
			if report.Detail == "" {
				t.Error("a refusal says why")
			}
			if done := m.folders.Done(); len(done) != 0 {
				t.Errorf("a refusal wrote %v", done)
			}
			created := 0
			for _, r := range m.forge.Repositories() {
				if r.Name == "acme" && c.reason != ledger.TemplateRefusedRemoteExists {
					created++
				}
			}
			if created != 0 {
				t.Errorf("a refusal created %d repositories", created)
			}
		})
	}
}

func TestAFineGrainedTokenReportsNoScopesAndIsNotChecked(t *testing.T) {
	m := newMachine()
	m.forge.SignIn.Scopes = nil
	if report := m.run(t, monorepo(create("test")), &projectremote.Progress{}); report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("report = %+v", report)
	}
}

func TestPublicRepoScopeSufficesForPublicRepositories(t *testing.T) {
	m := newMachine()
	m.forge.SignIn.Scopes = []string{"public_repo"}
	public := &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "test", Visibility: ledger.VisibilityPublic}}
	if report := m.run(t, monorepo(public), &projectremote.Progress{}); report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("report = %+v", report)
	}
}

func TestAURLRemoteIsAddedAndPushedWithNoCallToGitHub(t *testing.T) {
	m := newMachine()
	m.runtime.Forge = nil
	report := m.run(t, monorepo(&ledger.ProjectRemote{URL: "git@example.com:acme/acme.git"}), &projectremote.Progress{})
	if report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("report = %+v", report)
	}
	if origin := m.folders.Origin("/srv/acme"); origin != "git@example.com:acme/acme.git" {
		t.Errorf("origin = %q", origin)
	}
	if pushes := m.folders.Pushes(); len(pushes) != 1 || pushes[0].CredentialHelper != "" {
		t.Errorf("a URL remote pushes with the person's own credentials: %+v", pushes)
	}
	if got := steps(report.Repositories[0]); got != "origin=done push=done" {
		t.Errorf("steps = %s", got)
	}
}

func TestAUnsafeURLRemoteIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	for _, url := range []string{"ext::sh -c touch pwned", "ext::sh%20x", "--upload-pack=x", "https://-host/x"} {
		m := newMachine()
		report := m.run(t, monorepo(&ledger.ProjectRemote{URL: url}), &projectremote.Progress{})
		if report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedInvalid {
			t.Errorf("%q: report = %+v", url, report)
		}
		if len(m.folders.Done()) != 0 {
			t.Errorf("%q: wrote %v", url, m.folders.Done())
		}
	}
}

func TestACreatedRepositoryNeedsANameGitHubAccepts(t *testing.T) {
	long := strings.Repeat("a", 101)
	for _, folder := range []string{"my project", "acme;touch x", "acme'x", "$(id)", "acme/../..", "..", ".", "caf\u00e9", long} {
		m := newMachine()
		request := monorepo(create("test"))
		request.Path = "/srv/" + folder
		report := m.run(t, request, &projectremote.Progress{})
		if report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedInvalid || report.Detail == "" {
			t.Errorf("%q: report = %+v", folder, report)
		}
		if len(m.folders.Done()) != 0 || len(m.forge.Repositories()) != 0 {
			t.Errorf("%q: wrote %v and created %v", folder, m.folders.Done(), m.forge.Repositories())
		}
	}
}

func TestANewCodeRepositoryToCreateNeedsANameGitHubAccepts(t *testing.T) {
	m := newMachine()
	request := ledger.TemplateWriteRequest{
		Shape: "workspace", Path: "/srv/acme", FirstCommit: true,
		Repositories: []ledger.TemplateWriteRepository{{Name: "api web", New: true, Create: &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "test"}}},
	}
	report := m.run(t, request, &projectremote.Progress{})
	if report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedInvalid || !strings.Contains(report.Detail, "api web") {
		t.Errorf("report = %+v", report)
	}
	if len(m.folders.Done()) != 0 || len(m.forge.Repositories()) != 0 {
		t.Errorf("wrote %v and created %v", m.folders.Done(), m.forge.Repositories())
	}
}

func TestANameGitHubAcceptsIsCreated(t *testing.T) {
	for _, folder := range []string{"acme", "Acme.web_2-x", ".github", "a", strings.Repeat("a", 100)} {
		m := newMachine()
		request := monorepo(create("test"))
		request.Path = "/srv/" + folder
		if report := m.run(t, request, &projectremote.Progress{}); report.Outcome != ledger.TemplateOutcomeWritten {
			t.Errorf("%q: report = %+v", folder, report)
		}
	}
}

func TestARemoteNeedsAFolderThatMakesARepositoryOfItsOwn(t *testing.T) {
	m := newMachine()
	job := projectremote.Job{Request: monorepo(&ledger.ProjectRemote{URL: "https://example.com/acme.git"}), NeedsRepository: false}
	refusal, err := projectremote.Start(context.Background(), m.runtime, job, &projectremote.Progress{})
	if err != nil || refusal == nil || refusal.Reason != ledger.TemplateRefusedNoOwnRepository {
		t.Fatalf("refusal = %+v, err = %v", refusal, err)
	}
}

func TestAFailedCreateKeepsTheLocalRepositoryAndNamesTheCommandsLeft(t *testing.T) {
	m := newMachine()
	m.forge.CreateError = errors.New("GraphQL: you cannot create repositories under acme-co")
	report := m.run(t, monorepo(create("test")), &projectremote.Progress{})
	if report.Outcome != ledger.TemplateOutcomeWritten || report.Commit == "" {
		t.Fatalf("report = %+v: the files and the commit stay", report)
	}
	own := report.Repositories[0]
	if got := steps(own); got != "create=failed origin=skipped push=skipped" {
		t.Errorf("steps = %s", got)
	}
	if !strings.Contains(own.Steps[0].Detail, "cannot create repositories") {
		t.Errorf("detail = %q", own.Steps[0].Detail)
	}
	want := []string{
		"gh repo create test/acme --private",
		"git -C /srv/acme remote add origin https://github.com/test/acme.git",
		"gh auth setup-git",
		"git -C /srv/acme push --set-upstream origin main",
	}
	if !slices.Equal(report.Remaining, want) {
		t.Errorf("remaining = %q, want %q", report.Remaining, want)
	}
}

// finishUnchecked runs Finish as a run that passed Start would, so the commands it prints are read for names and
// URLs that Start refuses.
func finishUnchecked(t *testing.T, m *machine, request ledger.TemplateWriteRequest) ledger.TemplateWriteReport {
	t.Helper()
	m.folders.CommitError = errors.New("Author identity unknown")
	progress := &projectremote.Progress{
		Started: true, Commit: true, NeedsRepository: true,
		Account: &projectremote.Account{Login: "test", GitProtocol: "https", Binary: "/usr/bin/gh"},
	}
	report, err := projectremote.Finish(context.Background(), m.runtime, projectremote.Job{Request: request, NeedsRepository: true}, progress, nil)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestTheCommandsLeftQuoteANameAndAURLTheShellWouldReadAsMore(t *testing.T) {
	m := newMachine()
	request := monorepo(create("test"))
	request.Path = "/srv/a;touch pwned"
	want := []string{
		"gh repo create 'test/a;touch pwned' --private",
		"git -C '/srv/a;touch pwned' remote add origin 'https://github.com/test/a;touch pwned.git'",
		"gh auth setup-git",
		"git -C '/srv/a;touch pwned' push --set-upstream origin main",
	}
	if got := finishUnchecked(t, m, request).Remaining; !slices.Equal(got, want) {
		t.Errorf("remaining = %q, want %q", got, want)
	}

	m = newMachine()
	request = monorepo(&ledger.ProjectRemote{URL: "https://example.com/a b$(touch pwned)'.git"})
	want = []string{
		`git -C /srv/acme remote add origin 'https://example.com/a b$(touch pwned)'\''.git'`,
		"git -C /srv/acme push --set-upstream origin main",
	}
	if got := finishUnchecked(t, m, request).Remaining; !slices.Equal(got, want) {
		t.Errorf("remaining = %q, want %q", got, want)
	}
}

func TestAFailedPushKeepsTheCreatedRepositoryAndTheOrigin(t *testing.T) {
	m := newMachine()
	m.folders.FailPush("/srv/acme", errors.New("remote: push refused"))
	report := m.run(t, monorepo(create("test")), &projectremote.Progress{})
	own := report.Repositories[0]
	if got := steps(own); got != "create=done origin=done push=failed" {
		t.Errorf("steps = %s", got)
	}
	if len(m.forge.Repositories()) != 1 || m.folders.Origin("/srv/acme") == "" {
		t.Error("a failed push deletes nothing")
	}
	want := []string{"gh auth setup-git", "git -C /srv/acme push --set-upstream origin main"}
	if !slices.Equal(report.Remaining, want) {
		t.Errorf("remaining = %q, want %q", report.Remaining, want)
	}
}

func TestAFailedCommitSkipsTheRemoteSteps(t *testing.T) {
	m := newMachine()
	m.folders.CommitError = errors.New("Author identity unknown")
	report := m.run(t, monorepo(&ledger.ProjectRemote{URL: "https://example.com/acme.git"}), &projectremote.Progress{})
	if report.CommitError == "" || report.Commit != "" {
		t.Fatalf("report = %+v", report)
	}
	if got := steps(report.Repositories[0]); got != "origin=skipped push=skipped" {
		t.Errorf("steps = %s", got)
	}
	if len(m.folders.Pushes()) != 0 {
		t.Error("nothing is pushed without a commit")
	}
}

func TestAWorkspaceWritesEachNewCodeRepositoryWithItsOwnFirstCommit(t *testing.T) {
	m := newMachine()
	request := ledger.TemplateWriteRequest{
		Shape: "workspace", Path: "/srv/acme", FirstCommit: true, Remote: create("test"),
		Repositories: []ledger.TemplateWriteRepository{
			{Name: "api", New: true, Create: create("test").Create},
			{Name: "web", New: true},
			{Name: "legacy", Remote: "git@example.com:acme/legacy.git"},
			{Name: "tools", New: true, URL: "https://example.com/acme/tools.git"},
		},
	}
	report := m.run(t, request, &projectremote.Progress{})
	if report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("report = %+v", report)
	}
	manifest, _ := m.folders.Text("/srv/acme/plan/repositories.md")
	for _, want := range []string{
		"repo=api=https://github.com/test/api.git", "repo=web=", "repo=legacy=git@example.com:acme/legacy.git", "repo=tools=https://example.com/acme/tools.git",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest %q lacks %q", manifest, want)
		}
	}
	if strings.Contains(manifest, "repo=web=h") || strings.Contains(manifest, "repo=web=g") {
		t.Errorf("a repository with no remote has none: %q", manifest)
	}
	for _, path := range []string{"/srv/acme/repos/api/AGENTS.md", "/srv/acme/repos/web/catalog-info.yaml", "/srv/acme/repos/tools/AGENTS.md"} {
		if _, ok := m.folders.Text(path); !ok {
			t.Errorf("%s is not written", path)
		}
	}
	if m.folders.Inspected("/srv/acme/repos/legacy").Exists {
		t.Error("an existing repository gets no folder")
	}
	var commits []string
	for _, call := range m.folders.Done() {
		if strings.HasPrefix(call, "commit ") {
			commits = append(commits, strings.SplitN(strings.TrimPrefix(call, "commit "), ":", 2)[0])
		}
	}
	if want := []string{"/srv/acme/repos/api", "/srv/acme/repos/web", "/srv/acme/repos/tools", "/srv/acme"}; !slices.Equal(commits, want) {
		t.Errorf("commits = %v, want each new code repository's first, then the workspace's", commits)
	}
	if got := len(m.forge.Repositories()); got != 2 {
		t.Errorf("created %d repositories, want the workspace's and api's", got)
	}
	byName := map[string]ledger.RepositoryReport{}
	for _, r := range report.Repositories {
		byName[r.Name] = r
	}
	if len(report.Repositories) != 4 {
		t.Fatalf("repositories = %+v", report.Repositories)
	}
	if got := steps(byName["acme"]); got != "create=done origin=done push=done" {
		t.Errorf("workspace steps = %s", got)
	}
	if got := steps(byName["api"]); got != "create=done origin=done push=done" {
		t.Errorf("api steps = %s", got)
	}
	if got := steps(byName["tools"]); got != "origin=done push=done" {
		t.Errorf("tools steps = %s", got)
	}
	if got := steps(byName["web"]); got != "" || byName["web"].Commit == "" {
		t.Errorf("web has a commit and no remote steps: %+v", byName["web"])
	}
}

func TestACrashAfterCreateAndBeforePushCreatesNothingTwice(t *testing.T) {
	m := newMachine()
	progress := &projectremote.Progress{}
	job := projectremote.Job{Request: monorepo(create("test")), NeedsRepository: true}
	if refusal, err := projectremote.Start(context.Background(), m.runtime, job, progress); err != nil || refusal != nil {
		t.Fatalf("refusal = %+v, err = %v", refusal, err)
	}
	// The journal as a worker left it: the files written, the commit made, and create started when the process died, after
	// the forge had made the repository.
	own := progress.Units["."]
	if own == nil {
		own = &projectremote.UnitProgress{}
		progress.Units = map[string]*projectremote.UnitProgress{".": own}
	}
	m.folders.Dir("/srv/acme")
	commit, err := m.folders.FirstCommit(context.Background(), "/srv/acme", "chore: start acme", false, "fake:/srv/acme")
	if err != nil {
		t.Fatal(err)
	}
	own.Folder, own.Initialized, own.Committed, own.Commit = "fake:/srv/acme", true, true, commit
	own.Steps = map[string]*projectremote.StepProgress{"create": {State: "started"}}
	m.forge.Exist("test", "acme")
	report, err := projectremote.Finish(context.Background(), m.runtime, job, progress, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.forge.Repositories()) != 0 {
		t.Errorf("a restart created %+v again", m.forge.Repositories())
	}
	if got := steps(report.Repositories[0]); got != "create=done origin=done push=done" {
		t.Errorf("steps = %s", got)
	}
}

func TestACreateThatNeverReachedTheForgeRunsAgainAfterARestart(t *testing.T) {
	m := newMachine()
	progress := &projectremote.Progress{}
	job := projectremote.Job{Request: monorepo(create("test")), NeedsRepository: true}
	if refusal, err := projectremote.Start(context.Background(), m.runtime, job, progress); err != nil || refusal != nil {
		t.Fatalf("refusal = %+v, err = %v", refusal, err)
	}
	m.folders.Dir("/srv/acme")
	commit, err := m.folders.FirstCommit(context.Background(), "/srv/acme", "chore: start acme", false, "fake:/srv/acme")
	if err != nil {
		t.Fatal(err)
	}
	progress.Units = map[string]*projectremote.UnitProgress{".": {
		Folder: "fake:/srv/acme", Initialized: true, Committed: true, Commit: commit,
		Steps: map[string]*projectremote.StepProgress{"create": {State: "started"}},
	}}
	if _, err := projectremote.Finish(context.Background(), m.runtime, job, progress, nil); err != nil {
		t.Fatal(err)
	}
	if len(m.forge.Repositories()) != 1 {
		t.Errorf("created %+v, want the repository made once", m.forge.Repositories())
	}
}

func TestTheFirstCommitIsMadeOnlyWhenAsked(t *testing.T) {
	m := newMachine()
	request := ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/acme"}
	report := m.run(t, request, &projectremote.Progress{})
	if report.Commit != "" || len(report.Repositories) != 0 {
		t.Errorf("report = %+v", report)
	}
	if len(report.Commands) < 3 || report.Commands[0] != "git -C /srv/acme init" {
		t.Errorf("commands = %v: the git commands are the person's", report.Commands)
	}
}

func TestAForgeAccountReadSaysWhoMayOwnARepository(t *testing.T) {
	m := newMachine()
	m.forge.Orgs = []string{"zeta", "acme-co"}
	report, err := projectremote.ReadAccount(context.Background(), m.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != ledger.ForgeAccountOK || report.Login != "test" || report.GitProtocol != "https" || !slices.Equal(report.Owners, []string{"test", "acme-co", "zeta"}) {
		t.Errorf("report = %+v", report)
	}
	m.forge.SignIn.State = ledger.ForgeAccountSignedOut
	if report, _ = projectremote.ReadAccount(context.Background(), m.runtime); report.State != ledger.ForgeAccountSignedOut || report.Remedy != "gh auth login --hostname github.com" {
		t.Errorf("signed out: %+v", report)
	}
	m.forge.SignIn.State, m.forge.SignIn.Scopes = ledger.ForgeAccountOK, []string{"gist"}
	if report, _ = projectremote.ReadAccount(context.Background(), m.runtime); report.State != ledger.ForgeAccountScope {
		t.Errorf("no scope: %+v", report)
	}
	m.forge.SignIn.Scopes, m.forge.OrgsScopeMissing = []string{"public_repo"}, true
	if report, _ = projectremote.ReadAccount(context.Background(), m.runtime); report.State != ledger.ForgeAccountScope || !strings.Contains(report.Remedy, "read:org") {
		t.Errorf("no organization scope: %+v", report)
	}
	m.runtime.Forge = nil
	if report, _ = projectremote.ReadAccount(context.Background(), m.runtime); report.State != ledger.ForgeAccountMissing {
		t.Errorf("no gh: %+v", report)
	}
}

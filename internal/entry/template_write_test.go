package entry

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestAWorkspaceStartedFromTheWebUIIsWrittenByItsWorkerAndRegistersAfterItsDryRun is FR-TPL-5 end to end, as a solo
// daemon and its worker run it. A member's request for a workspace with two code repositories and a first commit
// reaches the worker through desired state. The worker writes the files `ropeshift new` writes, commits exactly
// them, and reports. The member's read says written and registrable, because the worker is the one the daemon runs
// and the folder is a clone. Intake finds no problem in the commit. Creating the project and registering the folder
// are separate acts, and the register route's dry run reports that it would register the workspace.
func TestAWorkspaceStartedFromTheWebUIIsWrittenByItsWorkerAndRegistersAfterItsDryRun(t *testing.T) {
	gittest.Require(t)
	identity := gittest.New(t)
	installation := apitest.StartInstallation(t)
	ctx := t.Context()

	// The worker: its journal, the checkouts it keeps, and the folders it writes, with the git of this test.
	state := t.TempDir()
	kept := &checkouts.File{Path: filepath.Join(state, checkouts.FileName), Env: identity.Env(), Reserved: []string{state}}
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	templates := soloTemplates(state, kept)
	templates.Folders = projectfolders.Folders{Env: identity.Env()}
	socket := api.NewUnixClient(installation.Socket)
	t.Cleanup(socket.Close)
	subject := worker.New(workerlink.New(socket), journal, clock.System{}, worker.Config{
		Name:      "local",
		JoinToken: installation.Client.CreateJoinToken,
		Templates: templates,
		Report:    func(err error) { t.Errorf("the worker reported %v", err) },
	})
	if err := subject.Start(ctx); err != nil {
		t.Fatal(err)
	}
	local := subject.Auth().WorkerID

	// The daemon names that worker as the one it runs, as a solo daemon does.
	config := installation.Config
	config.LocalWorker = func(context.Context) (string, error) { return local, nil }
	listener := filepath.Join(apitest.ShortDir(t), "web.sock")
	bound, err := net.Listen("unix", listener)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpServer.Serve(bound) }()
	t.Cleanup(func() { _ = httpServer.Close() })
	member := api.NewUnixClient(listener)
	t.Cleanup(member.Close)
	member.UseSession(installation.Session)

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "orchid")
	repositories := []ledger.TemplateWriteRepository{
		{Name: "orchid-api", Remote: "git@example.invalid:orchid/orchid-api.git"},
		{Name: "orchid-web", Remote: "git@example.invalid:orchid/orchid-web.git"},
	}
	asked, err := member.RequestTemplateWrite(ctx, local, api.TemplateWrite{Shape: "workspace", Path: folder, Repositories: repositories, FirstCommit: true})
	if err != nil || asked.State != ledger.TemplateWriteOffered {
		t.Fatalf("the request is %+v (%v)", asked, err)
	}
	if err := subject.Converge(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := member.TemplateWriteView(ctx, local, asked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ledger.TemplateWriteWritten || got.Report == nil || !got.Registrable || !got.Report.Clone || got.Report.Commit == "" {
		t.Fatalf("the request reads %+v, want written, registrable, with a first commit", got)
	}

	// The commit holds exactly the files `new workspace` writes for these repositories, and intake reads it with no problem.
	workspace := &gittest.Repo{T: t, Dir: folder, Home: identity.Home}
	tracked := strings.Split(workspace.Git("ls-files"), "\n")
	var data []projectfiles.CodeRepository
	for _, repository := range repositories {
		data = append(data, projectfiles.CodeRepository{Name: repository.Name, Remote: repository.Remote})
	}
	want, err := projectfiles.Files(projectfiles.Workspace, projectfiles.Data{Project: "orchid", Repositories: data}, renderProjectTemplate)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, file := range want {
		paths = append(paths, file.Path)
	}
	if !slices.Equal(tracked, paths) || !slices.Equal(got.Report.Files, paths) {
		t.Errorf("the commit holds %v and the report lists %v, want %v", tracked, got.Report.Files, paths)
	}
	if problems, work := intakeIn(t, workspace); len(problems) > 0 || len(work) > 0 {
		t.Errorf("intake reports problems %+v and work %v", problems, work)
	}
	if wantCommands := projectfiles.Commands(projectfiles.Workspace, folder, "orchid", data, false); !slices.Equal(got.Report.Commands, wantCommands) {
		t.Errorf("the commands are %v, want %v", got.Report.Commands, wantCommands)
	}

	// Creating the project, then the dry run of registering the folder: each a separate member act.
	daemon := cli.Options{Socket: installation.Socket, Session: installation.Session}
	run := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := cli.Run(args, &stdout, &stderr, daemon); code != cli.ExitOK {
			t.Fatalf("ropeshift %v: exit %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}
	run("project", "create", got.Report.Project)
	found, _, err := kept.Find(ctx, []string{folder}, 1)
	if err != nil || len(found) != 1 {
		t.Fatalf("read clones %+v (%v), want the workspace", found, err)
	}
	// The harness's machine matches a path as it is spelled, so the folder is placed as the reader found it.
	installation.Checkouts.Place(found[0])
	report, err := member.Project(got.Report.Project).AddRepositories(ctx, api.RepositoryAdd{Paths: []string{found[0].Path}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || len(report.Repositories) != 1 || report.Repositories[0].Outcome != ledger.Registered {
		t.Errorf("the dry run reports %+v, want it would register the workspace", report)
	}
	if list := run("repository", "list", "--project", got.Report.Project); strings.Contains(list, "orchid") {
		t.Errorf("a dry run registered the workspace:\n%s", list)
	}
	if _, err := os.Stat(filepath.Join(folder, "repos")); err == nil {
		t.Error("the worker made the clones of the code repositories")
	}
}

package worker_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/gittest"
)

// render is a renderer of its own, because this package never imports the templates: each file says which
// template it came from and which project it names.
func render(name string, data projectfiles.Data) (string, error) {
	lines := []string{"template " + name, "project " + data.Project}
	for _, repository := range data.Repositories {
		lines = append(lines, "repository "+repository.Name+"="+repository.Remote)
	}
	if name == "workspace/gitignore" {
		// The workspace's own .gitignore ignores the folder its code repositories sit in.
		lines = append(lines, "/repos/")
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// templateRig is a worker that writes a shape's starting files on real folders, with the git of this machine in
// a configuration the test owns.
type templateRig struct {
	*rig
	root    string
	state   string
	env     []string
	kept    *checkouts.File
	writes  *control.TemplateWrites
	subject *worker.Worker
	// folders stands in front of the machine's folders when a test must act between two of the worker's steps.
	folders ports.ProjectFolders
	// forge is the forge account the worker reaches, and nil has no gh.
	forge ports.ForgeAccounts
}

func newTemplateRig(t *testing.T, env []string) *templateRig {
	t.Helper()
	base := newRig(t)
	if env == nil {
		env = gittest.New(t).Env()
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &templateRig{rig: base, root: root, state: state, env: env, forge: &lateForge{}}
	r.kept = &checkouts.File{Path: filepath.Join(state, checkouts.FileName), Env: env, Reserved: []string{state}}
	// A worker that opened its session a moment ago is connected, whatever the rig's own heartbeat is.
	r.writes = control.NewTemplateWrites(base.store.Installation(scope), 60_000, nil)
	r.subject = r.workerOn(base.link, func(err error) { t.Errorf("the worker reported %v", err) })
	if err := r.subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return r
}

// workerOn is a worker on a link that writes folders as this rig's machine does, and hands what it could not do
// to report.
func (r *templateRig) workerOn(link ports.WorkerLink, report func(error)) *worker.Worker {
	return worker.New(link, r.journal, r.clock, worker.Config{
		Name: "test",
		JoinToken: func(ctx context.Context) (string, error) {
			return r.plane.CreateJoinToken(ctx, member)
		},
		Report: report,
		Templates: &worker.TemplateRuntime{
			Folders: r.foldersOrDefault(), Checkouts: r.kept, Forge: r.forge, Render: render, StateDir: r.state,
		},
	})
}

// foldersOrDefault is the folders this rig's worker writes: the machine's, unless a test put its own in front.
func (r *templateRig) foldersOrDefault() ports.ProjectFolders {
	if r.folders != nil {
		return r.folders
	}
	return projectfolders.Folders{Env: r.env}
}

func (r *templateRig) folder(parts ...string) string {
	return filepath.Join(append([]string{r.root}, parts...)...)
}

// ask makes a member's request of this rig's worker and returns its ID.
func (r *templateRig) ask(in ledger.TemplateWriteRequest) string {
	r.t.Helper()
	view, err := r.writes.Request(r.t.Context(), r.subject.Auth().WorkerID, in, member)
	if err != nil {
		r.t.Fatal(err)
	}
	return view.ID
}

// settle lets the worker take what it is offered, and reads the request.
func (r *templateRig) settle(id string) ledger.TemplateWriteView {
	r.t.Helper()
	if err := r.subject.Converge(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	return r.view(id)
}

func (r *templateRig) view(id string) ledger.TemplateWriteView {
	r.t.Helper()
	view, err := r.writes.View(r.t.Context(), r.subject.Auth().WorkerID, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return view
}

func (r *templateRig) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// filesUnder lists every file under a folder but its git directory.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(at string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, at)
		if rel == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// shapeFiles are the paths a shape writes for a folder, from the table `ropeshift new` writes.
func shapeFiles(t *testing.T, shape projectfiles.Shape, folder string, repositories []ledger.TemplateWriteRepository) []string {
	t.Helper()
	data := projectfiles.Data{Project: projectfiles.NameOf(filepath.Base(folder))}
	for _, repository := range repositories {
		data.Repositories = append(data.Repositories, projectfiles.CodeRepository{Name: repository.Name, Remote: repository.Remote})
	}
	files, err := projectfiles.Files(shape, data, render)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

// Each shape writes exactly the files `ropeshift new` writes, into a folder that is absent and into one that is
// empty, and the commands it reports are the ones `new` prints.
func TestEachShapeWritesItsFilesIntoAnAbsentAndAnEmptyFolder(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	repositories := []ledger.TemplateWriteRepository{{Name: "api", Remote: "git@example.com:acme/api.git"}, {Name: "web", Remote: "https://example.com/acme/web.git"}}
	cases := []struct {
		shape        projectfiles.Shape
		repositories []ledger.TemplateWriteRepository
		project      string
	}{
		{projectfiles.Workspace, repositories, ""},
		{projectfiles.Monorepo, nil, ""},
		{projectfiles.Repository, nil, "harbor"},
	}
	for _, empty := range []bool{false, true} {
		for _, c := range cases {
			name := string(c.shape) + map[bool]string{false: " into an absent folder", true: " into an empty folder"}[empty]
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				r := newTemplateRig(t, nil)
				folder := r.folder("My Project")
				if empty {
					if err := os.Mkdir(folder, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				view := r.settle(r.ask(ledger.TemplateWriteRequest{Shape: string(c.shape), Path: folder, Repositories: c.repositories, Project: c.project}))
				if view.State != ledger.TemplateWriteWritten || view.Report == nil {
					t.Fatalf("the request is %+v", view)
				}
				want := shapeFiles(t, c.shape, folder, c.repositories)
				if got := filesUnder(t, folder); !slices.Equal(got, want) {
					t.Errorf("the folder holds %v, want %v", got, want)
				}
				if !slices.Equal(view.Report.Files, want) {
					t.Errorf("the report lists %v, want %v", view.Report.Files, want)
				}
				if _, err := os.Stat(filepath.Join(folder, ".git")); err == nil {
					t.Error("a write with no first commit made a repository")
				}
				project := projectfiles.NameOf("My Project")
				named := project
				if c.project != "" {
					named = c.project
				}
				var data []projectfiles.CodeRepository
				for _, repository := range c.repositories {
					data = append(data, projectfiles.CodeRepository{Name: repository.Name, Remote: repository.Remote})
				}
				if commands := projectfiles.CommandsStaging(c.shape, folder, named, data, projectfiles.Paths(c.shape, data), "main"); !slices.Equal(view.Report.Commands, commands) || view.Report.Project != project {
					t.Errorf("the report names project %q and commands %v, want %q and %v", view.Report.Project, view.Report.Commands, project, commands)
				}
				if view.Report.Clone || view.Registrable {
					t.Error("a folder with no repository of its own reads as a clone")
				}
			})
		}
	}
}

// The first commit holds exactly the files written, the commands leave out the three git commands it made, and
// the folder reads as a clone.
func TestAFirstCommitHoldsExactlyTheFilesWritten(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r := newTemplateRig(t, nil)
	folder := r.folder("harbor")
	view := r.settle(r.ask(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true}))
	if view.State != ledger.TemplateWriteWritten || view.Report.Commit == "" || view.Report.CommitError != "" || !view.Report.Clone {
		t.Fatalf("the request is %+v", view)
	}
	committed := strings.Fields(r.git(folder, "ls-tree", "-r", "--name-only", "HEAD"))
	if want := shapeFiles(t, projectfiles.Monorepo, folder, nil); !slices.Equal(committed, want) {
		t.Errorf("the first commit holds %v, want %v", committed, want)
	}
	if head := r.git(folder, "rev-parse", "HEAD"); head != view.Report.Commit {
		t.Errorf("the report names commit %s, and HEAD is %s", view.Report.Commit, head)
	}
	if subject := r.git(folder, "log", "-1", "--format=%s"); subject != "chore: start harbor" {
		t.Errorf("the commit says %q", subject)
	}
	if want := projectfiles.Commands(projectfiles.Monorepo, folder, "harbor", nil, false); !slices.Equal(view.Report.Commands, want) {
		t.Errorf("the commands are %v, want %v", view.Report.Commands, want)
	}
}

// A commit git refuses, as it does with no identity, keeps the files and says why, and the commands still carry
// the git steps.
func TestAFailedFirstCommitKeepsTheFilesAndSaysWhy(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	home := t.TempDir()
	config := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(config, []byte("[user]\n\tuseConfigOnly = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=" + config, "GIT_CONFIG_NOSYSTEM=1"}
	r := newTemplateRig(t, env)
	folder := r.folder("harbor")
	view := r.settle(r.ask(ledger.TemplateWriteRequest{Shape: "repository", Path: folder, FirstCommit: true}))
	if view.State != ledger.TemplateWriteWritten || view.Report.Commit != "" || view.Report.CommitError == "" || view.Report.Clone || view.Registrable {
		t.Fatalf("the request is %+v", view)
	}
	if want := shapeFiles(t, projectfiles.Repository, folder, nil); !slices.Equal(filesUnder(t, folder), want) {
		t.Errorf("the folder holds %v, want %v", filesUnder(t, folder), want)
	}
	if want := projectfiles.CommandsStaging(projectfiles.Repository, folder, "harbor", nil, projectfiles.Paths(projectfiles.Repository, nil), "main"); !slices.Equal(view.Report.Commands, want) {
		t.Errorf("the commands are %v, want %v", view.Report.Commands, want)
	}
}

// capturedReports stands between a worker and the control plane and keeps what the worker reports, for the
// requests a test hands the worker in its desired state.
type capturedReports struct {
	ports.WorkerLink
	mu      sync.Mutex
	reports []ledger.TemplateWriteReport
}

func (c *capturedReports) ReportTemplateWrite(_ context.Context, report ledger.TemplateWriteReport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, report)
	return nil
}

func (c *capturedReports) all() []ledger.TemplateWriteReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.reports)
}

func (c *capturedReports) only(t *testing.T) ledger.TemplateWriteReport {
	t.Helper()
	reports := c.all()
	if len(reports) != 1 {
		t.Fatalf("%d reports: %+v", len(reports), reports)
	}
	return reports[0]
}

// offer hands a worker requests in its desired state, as the control plane would, and lets it act on them.
func (r *templateRig) offer(t *testing.T, link ports.WorkerLink, requests ...ledger.DesiredTemplateWrite) {
	t.Helper()
	subject := r.workerOn(link, func(err error) { t.Errorf("the worker reported %v", err) })
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.journal.SaveDesired(t.Context(), ledger.DesiredState{Rev: 1, TemplateWrites: requests}); err != nil {
		t.Fatal(err)
	}
	if _, err := subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	subject.Wait()
}

// Each check the worker makes ends in a refusal with its own reason, in the order the checks run.
func TestAWorkerRefusesAFolderItCannotWrite(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r := newTemplateRig(t, nil)
	present := r.folder("present")
	full := r.folder("full")
	aFile := r.folder("a-file")
	link := r.folder("a-link")
	for _, dir := range []string{present, full} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, text := range map[string]string{filepath.Join(full, "notes.txt"): "mine", aFile: "x"} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(present, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		path   string
		reason string
	}{
		{"a relative path", "relative/harbor", ledger.TemplateRefusedNotAbsolute},
		{"a parent element", r.root + "/present/../harbor", ledger.TemplateRefusedParentElement},
		// A path that is not its own clean form reaches a link through a spelling Lstat follows, so none is taken.
		{"a trailing slash", r.folder("harbor") + "/", ledger.TemplateRefusedNotClean},
		{"a double slash", r.root + "//harbor", ledger.TemplateRefusedNotClean},
		{"a dot element", r.root + "/./harbor", ledger.TemplateRefusedNotClean},
		{"a link with a trailing slash", link + "/", ledger.TemplateRefusedNotClean},
		{"no parent", r.folder("missing", "harbor"), ledger.TemplateRefusedNoParent},
		{"a link", link, ledger.TemplateRefusedLink},
		{"a folder of ropeshift's own state", filepath.Join(r.state, "new"), ledger.TemplateRefusedReserved},
		{"ropeshift's state itself", r.state, ledger.TemplateRefusedReserved},
		{"a file", aFile, ledger.TemplateRefusedNotDirectory},
		{"a folder with files", full, ledger.TemplateRefusedNotEmpty},
	}
	for _, c := range cases {
		captured := &capturedReports{WorkerLink: r.link}
		r.offer(t, captured, ledger.DesiredTemplateWrite{ID: "request-" + strings.ReplaceAll(c.name, " ", "-"), Shape: "monorepo", Path: c.path, ExpiresAt: 1 << 60})
		report := captured.only(t)
		if report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != c.reason || report.Detail == "" {
			t.Errorf("%s: the report is %+v, want a refusal for %s", c.name, report, c.reason)
		}
	}
	if _, err := os.Stat(filepath.Join(full, "AGENTS.md")); err == nil {
		t.Error("a folder with files of its own was written into")
	}
	if got := filesUnder(t, present); len(got) != 0 {
		t.Errorf("the folder a link led to holds %v", got)
	}
}

// A folder in the ignored repos/ of a registered clone is written, and one in its tracked tree, or the clone
// itself, is refused with the clone and its project named.
func TestAFolderInsideARegisteredCloneIsWrittenOnlyWhereTheCloneIgnoresIt(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	clone := gittest.New(t)
	clone.Commit(".gitignore", "repos/\n")
	clone.Commit("docs/readme.md", "docs\n")
	if err := os.Mkdir(filepath.Join(clone.Dir, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newTemplateRig(t, clone.Env())
	real, err := filepath.EvalSymlinks(clone.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.kept.Keep(t.Context(), "project-1", "planning", real); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(real, "repos", "api")
	view := r.settle(r.ask(ledger.TemplateWriteRequest{Shape: "repository", Path: ignored, FirstCommit: true}))
	if view.State != ledger.TemplateWriteWritten || view.Report.Commit == "" || !view.Report.Clone {
		t.Fatalf("a folder in the ignored repos/ was not written: %+v", view)
	}
	if want := shapeFiles(t, projectfiles.Repository, ignored, nil); !slices.Equal(filesUnder(t, ignored), want) {
		t.Errorf("the folder holds %v, want %v", filesUnder(t, ignored), want)
	}
	if status := clone.Git("status", "--porcelain"); status != "" {
		t.Errorf("the clone it lies in changed:\n%s", status)
	}
	tracked := filepath.Join(real, "docs", "new")
	view = r.settle(r.ask(ledger.TemplateWriteRequest{Shape: "monorepo", Path: tracked}))
	if view.State != ledger.TemplateWriteRefused || view.Report.Reason != ledger.TemplateRefusedInsideClone ||
		!strings.Contains(view.Report.Detail, real) || !strings.Contains(view.Report.Detail, "project-1") {
		t.Errorf("the tracked tree: the request is %+v", view)
	}
	if _, err := os.Stat(tracked); err == nil {
		t.Error("a folder in the tracked tree of a registered clone was made")
	}
	inside := filepath.Join(real, "docs")
	view = r.settle(r.ask(ledger.TemplateWriteRequest{Shape: "monorepo", Path: inside}))
	if view.State != ledger.TemplateWriteRefused {
		t.Errorf("a folder of the clone's tracked tree: the request is %+v", view)
	}
}

// Inside a clone nobody registered that tracks the folder, the files are written and no git command runs, so the
// clone's own history takes them, and the commands carry no git steps.
func TestAFolderInAnUnregisteredCloneThatTracksItGetsNoGitCommand(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	clone := gittest.New(t)
	clone.Commit("services/readme.md", "services\n")
	r := newTemplateRig(t, clone.Env())
	folder := filepath.Join(clone.Dir, "services", "harbor")
	view := r.settle(r.ask(ledger.TemplateWriteRequest{Shape: "repository", Path: folder, FirstCommit: true}))
	if view.State != ledger.TemplateWriteWritten || view.Report.Commit != "" || view.Report.CommitError != "" || view.Report.Clone {
		t.Fatalf("the request is %+v", view)
	}
	if _, err := os.Stat(filepath.Join(folder, ".git")); err == nil {
		t.Error("a folder the clone tracks got a repository of its own")
	}
	if want := projectfiles.Commands(projectfiles.Repository, folder, "harbor", nil, false); !slices.Equal(view.Report.Commands, want) {
		t.Errorf("the commands are %v, want %v", view.Report.Commands, want)
	}
	if count := clone.Git("rev-list", "--count", "HEAD"); count != "1" {
		t.Errorf("the clone has %s commits, and the write made none", count)
	}
}

// afterWrite runs a step once the folders have written, which is the moment between the worker's write and its commit.
type afterWrite struct {
	ports.ProjectFolders
	step func(path string)
}

func (a afterWrite) Write(ctx context.Context, path string, files []ports.FolderFile, want ports.FolderID) (ports.FolderID, error) {
	id, err := a.ProjectFolders.Write(ctx, path, files, want)
	if err == nil {
		a.step(path)
	}
	return id, err
}

// A folder swapped for a link to a sibling between the write and the first commit is not committed in: the worker runs git
// nowhere, and refuses the request with the folder's change as the reason.
func TestAFolderSwappedForALinkBeforeTheFirstCommitIsRefused(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r := newTemplateRig(t, nil)
	folder, moved, sibling := r.folder("harbor"), r.folder("moved"), r.folder("sibling")
	r.folders = afterWrite{ProjectFolders: projectfolders.Folders{Env: r.env}, step: func(path string) {
		for _, step := range []func() error{
			func() error { return os.Rename(path, moved) },
			func() error { return os.Mkdir(sibling, 0o755) },
			func() error { return os.Symlink(sibling, path) },
		} {
			if err := step(); err != nil {
				t.Error(err)
			}
		}
	}}
	subject := r.workerOn(r.link, func(err error) { t.Errorf("the worker reported %v", err) })
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject = subject
	id := r.ask(ledger.TemplateWriteRequest{Shape: "monorepo", Path: folder, FirstCommit: true})
	view := r.settle(id)
	if view.State != ledger.TemplateWriteRefused || view.Report.Reason != ledger.TemplateRefusedChanged || view.Report.Commit != "" || view.Report.Detail == "" {
		t.Fatalf("the request is %+v, want a refusal that the folder changed", view)
	}
	for _, dir := range []string{sibling, moved} {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			t.Errorf("git ran in %s", dir)
		}
	}
}

// A report that never reached the control plane, as when a worker crashes after the write, leaves the files and a
// journal that says started. The worker that restarts writes nothing twice, makes no second commit, and reports
// once.
func TestACrashBetweenTheWriteAndTheReportWritesNothingTwiceAndReportsOnce(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r := newTemplateRig(t, nil)
	folder := r.folder("harbor")
	id := r.ask(ledger.TemplateWriteRequest{
		Shape: "workspace", Path: folder, FirstCommit: true,
		Repositories: []ledger.TemplateWriteRepository{{Name: "api", Remote: "git@example.com:acme/api.git"}},
	})
	// The first worker writes the folder and commits, and the control plane never hears of it.
	r.link.FailTemplateReport.Store(true)
	var lost []error
	crashed := r.workerOn(r.link, func(err error) { lost = append(lost, err) })
	if err := crashed.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := crashed.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(lost) != 1 || !strings.Contains(lost[0].Error(), "reporting it") {
		t.Fatalf("the first worker reported %v, want its lost report", lost)
	}
	if view := r.view(id); view.State != ledger.TemplateWriteOffered {
		t.Fatalf("the request is %s after a report that never arrived", view.State)
	}
	first := r.git(folder, "rev-parse", "HEAD")
	before := filesUnder(t, folder)
	journal, err := os.ReadFile(filepath.Join(r.state, "template-writes", id+".json"))
	if err != nil || !bytes.Contains(journal, []byte(`"started"`)) {
		t.Fatalf("the journal says %q (%v), want started", journal, err)
	}
	// The worker that starts after the crash takes the same request again.
	r.link.FailTemplateReport.Store(false)
	restarted := r.workerOn(r.link, func(err error) { t.Errorf("the restarted worker reported %v", err) })
	if err := restarted.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	view := r.view(id)
	if view.State != ledger.TemplateWriteWritten || view.Report.Commit != first || !slices.Equal(view.Report.Files, before) {
		t.Fatalf("the request is %+v after the restart", view)
	}
	if count := r.git(folder, "rev-list", "--count", "HEAD"); count != "1" {
		t.Errorf("the folder has %s commits", count)
	}
	if got := filesUnder(t, folder); !slices.Equal(got, before) {
		t.Errorf("the folder holds %v after the restart, want %v", got, before)
	}
	journal, err = os.ReadFile(filepath.Join(r.state, "template-writes", id+".json"))
	if err != nil || !bytes.Contains(journal, []byte(`"reported"`)) {
		t.Errorf("the journal says %q (%v), want reported", journal, err)
	}
	if err := restarted.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if view := r.view(id); view.State != ledger.TemplateWriteWritten {
		t.Errorf("the request is %s", view.State)
	}
	reported := 0
	err = r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := tx.Events(0, 1000)
		for _, event := range events {
			if event.Type == "template_write.reported" {
				reported++
			}
		}
		return err
	})
	if err != nil || reported != 1 {
		t.Errorf("the log holds %d reports (%v), want one", reported, err)
	}
}

// A restart that finds a .git with no commit takes it as its own only when the journal says the worker ran git init. With
// no such record, the repository is somebody else's, so the worker commits nothing in it, keeps the files, and says why.
func TestAResumeWithARepositoryItNeverJournaledMakesNoCommit(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r := newTemplateRig(t, nil)
	folder := r.folder("harbor")
	// The folder holds the files of its shape and a repository that no journal of this worker's made.
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := projectfiles.Files(projectfiles.Monorepo, projectfiles.Data{Project: "harbor"}, render)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(folder, file.Path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, file.Path), []byte(file.Text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.git(folder, "init", "--quiet")
	journal := `{"state":"started","commit":true,"needs_repository":true}`
	if err := os.MkdirAll(filepath.Join(r.state, "template-writes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.state, "template-writes", "request-resume.json"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	captured := &capturedReports{WorkerLink: r.link}
	r.offer(t, captured, ledger.DesiredTemplateWrite{ID: "request-resume", Shape: "monorepo", Path: folder, FirstCommit: true, ExpiresAt: 1 << 60})
	report := captured.only(t)
	if report.Outcome != ledger.TemplateOutcomeWritten || report.Commit != "" || !strings.Contains(report.CommitError, "did not make") || report.Clone {
		t.Errorf("the report is %+v, want the files written and a refused commit", report)
	}
	if out, err := exec.Command("git", "-C", folder, "rev-parse", "--verify", "HEAD").CombinedOutput(); err == nil {
		t.Errorf("a commit was made in a repository the worker never journaled: %s", out)
	}
}

// resumeRig is a folder that holds the files of its shape and a repository with no commit, and a journal file for a request
// that offers it.
func resumeRig(t *testing.T, journal string) (*templateRig, string, *capturedReports) {
	t.Helper()
	r := newTemplateRig(t, nil)
	folder := r.folder("harbor")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := projectfiles.Files(projectfiles.Monorepo, projectfiles.Data{Project: "harbor"}, render)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(folder, file.Path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, file.Path), []byte(file.Text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.git(folder, "init", "--quiet")
	if err := os.MkdirAll(filepath.Join(r.state, "template-writes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.state, "template-writes", "request-resume.json"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	return r, folder, &capturedReports{WorkerLink: r.link}
}

// A journal written before a repository's progress had a record of its own holds the git init and the folder at the top
// level. A worker that reads it keeps both, so the repository it initialized is its own and the folder it checked is the
// one it holds.
func TestAResumeKeepsTheGitInitTheEarlierJournalRecorded(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r, folder, captured := resumeRig(t, `{"state":"started","commit":true,"needs_repository":true,"initialized":true}`)
	r.offer(t, captured, ledger.DesiredTemplateWrite{ID: "request-resume", Shape: "monorepo", Path: folder, FirstCommit: true, ExpiresAt: 1 << 60})
	report := captured.only(t)
	if report.Outcome != ledger.TemplateOutcomeWritten || report.Commit == "" || report.CommitError != "" {
		t.Errorf("the report is %+v, want the first commit made in the repository the journal says the worker initialized", report)
	}
}

func TestAResumeKeepsTheFolderTheEarlierJournalRecorded(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r, folder, captured := resumeRig(t, `{"state":"started","commit":true,"needs_repository":true,"initialized":true,"folder":"0:0"}`)
	r.offer(t, captured, ledger.DesiredTemplateWrite{ID: "request-resume", Shape: "monorepo", Path: folder, FirstCommit: true, ExpiresAt: 1 << 60})
	report := captured.only(t)
	if report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedChanged {
		t.Errorf("the report is %+v, want a refusal: the folder is not the one the journal names", report)
	}
}

// A request whose journal says it was reported, and that the control plane still offers, gets the journaled report
// again and writes nothing.
func TestARequestStillOfferedAfterItsReportGetsTheJournaledReportAgain(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r := newTemplateRig(t, nil)
	folder := r.folder("harbor")
	journal := `{"state":"reported","commit":false,"needs_repository":true,"report":{"request":"request-1","outcome":"written","project":"harbor","files":["AGENTS.md"]}}`
	if err := os.MkdirAll(filepath.Join(r.state, "template-writes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.state, "template-writes", "request-1.json"), []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	captured := &capturedReports{WorkerLink: r.link}
	r.offer(t, captured, ledger.DesiredTemplateWrite{ID: "request-1", Shape: "monorepo", Path: folder, ExpiresAt: 1 << 60})
	got := captured.only(t)
	if got.Request != "request-1" || got.Outcome != ledger.TemplateOutcomeWritten || got.Project != "harbor" || !slices.Equal(got.Files, []string{"AGENTS.md"}) {
		t.Errorf("the worker reported %+v, want the journaled report", got)
	}
	if _, err := os.Stat(folder); err == nil {
		t.Error("a request that was reported wrote its folder again")
	}
}

// A request nobody started that is past its expiry is not taken, so a worker that reconnects late writes nothing.
func TestAWorkerWritesNothingForARequestPastItsExpiry(t *testing.T) {
	t.Parallel()
	gittest.Require(t)
	r := newTemplateRig(t, nil)
	folder := r.folder("harbor")
	captured := &capturedReports{WorkerLink: r.link}
	r.offer(t, captured, ledger.DesiredTemplateWrite{ID: "request-late", Shape: "monorepo", Path: folder, ExpiresAt: r.clock.Now().UnixMilli() - 1})
	if reports := captured.all(); len(reports) != 0 {
		t.Errorf("the worker reported %+v", reports)
	}
	if _, err := os.Stat(folder); err == nil {
		t.Error("an expired request was written")
	}
}

// A workspace's code repositories are named in its manifest and cloned by nobody: the worker runs no git clone,
// and the checkouts it keeps stay as they were. The test puts a git of its own first on the PATH, which logs each
// command, so it runs by itself.
func TestAWorkspaceWriteRunsNoGitCloneAndKeepsTheCheckoutsFile(t *testing.T) {
	gittest.Require(t)
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "git.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := newTemplateRig(t, nil)
	if err := r.kept.Keep(t.Context(), "project-1", "planning", r.folder("elsewhere")); err != nil {
		t.Fatal(err)
	}
	checkoutsFile := filepath.Join(r.state, checkouts.FileName)
	before, err := os.ReadFile(checkoutsFile)
	if err != nil {
		t.Fatal(err)
	}
	folder := r.folder("harbor")
	view := r.settle(r.ask(ledger.TemplateWriteRequest{
		Shape: "workspace", Path: folder, FirstCommit: true,
		Repositories: []ledger.TemplateWriteRepository{{Name: "api", Remote: "git@example.com:acme/api.git"}},
	}))
	if view.State != ledger.TemplateWriteWritten || view.Report.Commit == "" {
		t.Fatalf("the request is %+v", view)
	}
	logged, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "init") || !strings.Contains(string(logged), "commit") {
		t.Errorf("the log shows no first commit:\n%s", logged)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(logged)), "\n") {
		if strings.HasPrefix(line, "clone") || strings.Contains(line, " clone ") {
			t.Errorf("the worker ran git %s", line)
		}
	}
	if after, err := os.ReadFile(checkoutsFile); err != nil || !bytes.Equal(before, after) {
		t.Errorf("the checkouts file changed: %q (%v)", after, err)
	}
	if _, err := os.Stat(filepath.Join(folder, "repos", "api")); err == nil {
		t.Error("the worker cloned a code repository")
	}
}

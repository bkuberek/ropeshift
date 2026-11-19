package worker_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/worker"
)

// creationRig is a worker's machine held in memory for the repositories a plan approval creates: the planning
// repository's clone at /work/tt, which this worker keeps and which ignores repos/, inside /work, and a forge signed
// in as test, a member of acme.
type creationRig struct {
	t       *testing.T
	folders *fake.ProjectFolders
	kept    *fake.Checkouts
	forge   *fake.ForgeAccounts
	state   string
}

func newCreationRig(t *testing.T) *creationRig {
	t.Helper()
	r := &creationRig{t: t, folders: fake.NewProjectFolders(), kept: fake.NewCheckouts(), forge: fake.NewForgeAccounts(), state: t.TempDir()}
	r.folders.Clone("/work/tt")
	if err := r.kept.Keep(t.Context(), "tt-id", "tt", "/work/tt"); err != nil {
		t.Fatal(err)
	}
	r.kept.Ignore("/work/tt", "repos")
	r.forge.Orgs = []string{"acme"}
	return r
}

// creator is the repository creator of a worker on this rig's machine that reaches the forge through forge.
func (r *creationRig) creator(forge ports.ForgeAccounts) ports.RepositoryCreator {
	subject := worker.New(nil, nil, clock.System{}, worker.Config{
		Name: "atlas",
		Templates: &worker.TemplateRuntime{
			Folders: r.folders, Checkouts: r.kept, Forge: forge, Render: render, StateDir: r.state,
		},
	})
	return subject.RepositoryCreator()
}

// journal is the path of a creation's journal in this rig's state directory.
func (r *creationRig) journal(job ledger.RepositoryCreation) string {
	return filepath.Join(r.state, "repository-creations", job.Key+".json")
}

// nothingDone fails the test when the worker wrote, committed, created, or journaled anything for job.
func (r *creationRig) nothingDone(job ledger.RepositoryCreation) {
	r.t.Helper()
	if done := r.folders.Done(); len(done) != 0 {
		r.t.Errorf("the worker did %v", done)
	}
	if created := r.forge.Repositories(); len(created) != 0 {
		r.t.Errorf("the worker created %v", created)
	}
	if _, err := os.Stat(r.journal(job)); !errors.Is(err, fs.ErrNotExist) {
		r.t.Errorf("a journal is at %s (%v)", r.journal(job), err)
	}
}

// creation is the job of creating the repository at a path, as the approval approval-1 names it, on GitHub under acme.
func creation(at string) ledger.RepositoryCreation {
	name := path.Base(at)
	return ledger.RepositoryCreation{
		Key: ledger.CreationKey("approval-1", name), Project: "tt", Name: name, Path: at,
		Bound: "/work", PlanningClone: "/work/tt",
		Create: ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "acme"}, ForgeName: name,
	}
}

// A check that refuses writes nothing: no file, no folder above the path, no forge repository, and no journal.
func TestARefusedRepositoryCreationWritesNothing(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.forge.SignIn = ports.ForgeAccount{State: ledger.ForgeAccountSignedOut}
	job := creation("/work/tt/repos/api")
	creator := r.creator(r.forge)

	checked, err := creator.Check(t.Context(), job)
	if err != nil || checked.Refusal == nil || checked.Refusal.Reason != ledger.TemplateRefusedGHSignedOut {
		t.Fatalf("the check is %+v (%v), want refused as gh_signed_out", checked, err)
	}
	report, err := creator.Create(t.Context(), job)
	if err != nil || report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedGHSignedOut {
		t.Fatalf("the creation reports %+v (%v), want refused as gh_signed_out", report, err)
	}
	r.nothingDone(job)
	if r.folders.Inspected("/work/tt/repos").Exists {
		t.Error("a refused creation made the folder above its path")
	}
}

// A creation writes the repository shape's files, makes the first commit on the branch it names, creates the forge
// repository with its visibility, adds origin with the URL gh's protocol gives, pushes, and reports each step done.
// Its check, run first, says what it will do and writes nothing.
func TestARepositoryCreationWritesCommitsCreatesAddsOriginAndPushes(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.folders.Dir("/work/tt/repos")
	r.forge.SignIn.GitProtocol = "ssh"
	job := creation("/work/tt/repos/api")
	job.Create.Visibility = ledger.VisibilityPublic
	job.DefaultBranch = "trunk"
	creator := r.creator(r.forge)

	checked, err := creator.Check(t.Context(), job)
	want := ledger.CreationCheck{URL: "git@github.com:acme/api.git", Login: "test", Branch: "trunk"}
	if err != nil || checked != want {
		t.Fatalf("the check is %+v (%v), want %+v", checked, err, want)
	}
	r.nothingDone(job)

	report, err := creator.Create(t.Context(), job)
	if err != nil || report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("the creation reports %+v (%v)", report, err)
	}
	files, err := projectfiles.Files(projectfiles.Repository, projectfiles.Data{Project: "api"}, render)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if text, ok := r.folders.Text(job.Path + "/" + file.Path); !ok || text != file.Text {
			t.Errorf("%s holds %q (%v), want %q", file.Path, text, ok, file.Text)
		}
	}
	if state, err := r.folders.Remote(t.Context(), job.Path); err != nil || state.Head == "" || state.Branch != "trunk" {
		t.Errorf("the repository is %+v (%v), want a first commit on trunk", state, err)
	}
	if created := r.forge.Repositories(); !slices.Equal(created, []fake.Created{{Owner: "acme", Name: "api", Public: true}}) {
		t.Errorf("the forge created %+v, want acme/api public", created)
	}
	if origin := r.folders.Origin(job.Path); origin != want.URL {
		t.Errorf("origin is %q, want %q", origin, want.URL)
	}
	if pushes := r.folders.Pushes(); len(pushes) != 1 || pushes[0].Dir != job.Path || pushes[0].Branch != "trunk" {
		t.Errorf("the pushes are %+v, want one of trunk", pushes)
	}
	if len(report.Repositories) != 1 || steps(report.Repositories[0]) != "create=done origin=done push=done" || report.Repositories[0].Remote != want.URL {
		t.Errorf("the repositories reported are %+v", report.Repositories)
	}
	if _, err := os.Stat(r.journal(job)); err != nil {
		t.Errorf("no journal of the creation: %v", err)
	}
}

// crashingForge stops the run that calls Create once the forge has created the repository, before the run hears of
// it, as a worker that dies there does. It counts the reads of whether a repository exists.
type crashingForge struct {
	*fake.ForgeAccounts
	mu     sync.Mutex
	crash  bool
	exists int
}

func (f *crashingForge) Exists(ctx context.Context, host, owner, name string) (bool, error) {
	f.mu.Lock()
	f.exists++
	f.mu.Unlock()
	return f.ForgeAccounts.Exists(ctx, host, owner, name)
}

func (f *crashingForge) Create(ctx context.Context, host string, repository ports.CreateRepository) error {
	if err := f.ForgeAccounts.Create(ctx, host, repository); err != nil {
		return err
	}
	if f.crash {
		runtime.Goexit()
	}
	return nil
}

func (f *crashingForge) existsReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exists
}

// A run that stopped after the create step started resumes from its journal: it reads that the repository exists, so it
// creates nothing twice, and then adds origin and pushes.
func TestARepositoryCreationStoppedAfterTheCreateStartedResumesAndCreatesNothingTwice(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.folders.Dir("/work/tt/repos")
	job := creation("/work/tt/repos/api")

	crashing := &crashingForge{ForgeAccounts: r.forge, crash: true}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, _ = r.creator(crashing).Create(context.Background(), job)
	}()
	<-stopped
	journal, err := os.ReadFile(r.journal(job))
	if err != nil || !strings.Contains(string(journal), `"started"`) {
		t.Fatalf("the journal is %s (%v), want a step started", journal, err)
	}
	if created := r.forge.Repositories(); len(created) != 1 {
		t.Fatalf("the forge created %+v before the stop", created)
	}

	resumed := &crashingForge{ForgeAccounts: r.forge}
	report, err := r.creator(resumed).Create(t.Context(), job)
	if err != nil || report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("the resumed creation reports %+v (%v)", report, err)
	}
	if len(report.Repositories) != 1 || steps(report.Repositories[0]) != "create=done origin=done push=done" {
		t.Errorf("the repositories reported are %+v", report.Repositories)
	}
	if created := r.forge.Repositories(); len(created) != 1 {
		t.Errorf("the forge created %+v, want acme/api once", created)
	}
	if resumed.existsReads() == 0 {
		t.Error("the resume never read whether the repository exists")
	}
	if pushes := r.folders.Pushes(); len(pushes) != 1 {
		t.Errorf("the pushes are %+v, want one", pushes)
	}
}

// A forge repository that exists, with no journal of this creation, is never taken over: the creation is refused with the
// remedy, and nothing is written.
func TestARepositoryCreationRefusesAForgeRepositoryThatExists(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.folders.Dir("/work/tt/repos")
	r.forge.Exist("acme", "api")
	job := creation("/work/tt/repos/api")

	report, err := r.creator(r.forge).Create(t.Context(), job)
	if err != nil || report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedRemoteExists {
		t.Fatalf("the creation reports %+v (%v), want refused as remote_exists", report, err)
	}
	if want := "acme/api already exists on GitHub; clone it to repos/api and write `remote` in place of `create`"; report.Detail != want {
		t.Errorf("the refusal says %q, want %q", report.Detail, want)
	}
	r.nothingDone(job)
}

// The folders between the nearest one that exists and the path are made once the checks pass, inside the bound. A path
// whose nearest existing folder leads outside the bound, or that is the planning clone itself, is refused, and nothing
// is made.
func TestARepositoryCreationMakesTheMissingFoldersInsideTheBoundOnly(t *testing.T) {
	t.Parallel()
	t.Run("missing repos folder", func(t *testing.T) {
		t.Parallel()
		r := newCreationRig(t)
		job := creation("/work/tt/repos/api")
		report, err := r.creator(r.forge).Create(t.Context(), job)
		if err != nil || report.Outcome != ledger.TemplateOutcomeWritten {
			t.Fatalf("the creation reports %+v (%v)", report, err)
		}
		if !r.folders.Inspected("/work/tt/repos").Directory {
			t.Error("the creation did not make repos/")
		}
		if r.folders.Commit(job.Path) == "" {
			t.Error("the creation made no first commit")
		}
	})
	t.Run("leads outside the bound", func(t *testing.T) {
		t.Parallel()
		r := newCreationRig(t)
		r.folders.Dir("/elsewhere")
		r.folders.Link("/work/tt/out", "/elsewhere")
		job := creation("/work/tt/out/nested/api")
		report, err := r.creator(r.forge).Create(t.Context(), job)
		if err != nil || report.Outcome != ledger.TemplateOutcomeRefused || !strings.Contains(report.Detail, "/elsewhere/nested/api") {
			t.Fatalf("the creation reports %+v (%v), want refused naming /elsewhere/nested/api", report, err)
		}
		if r.folders.Inspected("/elsewhere/nested").Exists {
			t.Error("a refused creation made a folder outside the bound")
		}
		r.nothingDone(job)
	})
	t.Run("the planning clone", func(t *testing.T) {
		t.Parallel()
		r := newCreationRig(t)
		job := creation("/work/tt")
		report, err := r.creator(r.forge).Create(t.Context(), job)
		if err != nil || report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedInsideClone {
			t.Fatalf("the creation reports %+v (%v), want refused as path_inside_clone", report, err)
		}
		r.nothingDone(job)
	})
}

// A path inside the planning clone that the clone does not ignore is refused with the line that makes it ignore the path.
func TestARepositoryCreationInsideThePlanningCloneNeedsTheCloneToIgnoreIt(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.folders.Dir("/work/tt/code")
	job := creation("/work/tt/code/api")
	report, err := r.creator(r.forge).Create(t.Context(), job)
	if err != nil || report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != ledger.TemplateRefusedInsideClone {
		t.Fatalf("the creation reports %+v (%v), want refused as path_inside_clone", report, err)
	}
	if !strings.Contains(report.Detail, "/code/api/") || !strings.Contains(report.Detail, ".gitignore") {
		t.Errorf("the refusal says %q, which names no line to add to .gitignore", report.Detail)
	}
	r.nothingDone(job)
}

// refusedAs fails the test unless both the check and the creation of job refuse it for reason.
func refusedAs(t *testing.T, creator ports.RepositoryCreator, job ledger.RepositoryCreation, reason string) ledger.TemplateWriteReport {
	t.Helper()
	checked, err := creator.Check(t.Context(), job)
	if err != nil || checked.Refusal == nil || checked.Refusal.Reason != reason {
		t.Fatalf("the check is %+v (%v), want refused as %s", checked, err, reason)
	}
	report, err := creator.Create(t.Context(), job)
	if err != nil || report.Outcome != ledger.TemplateOutcomeRefused || report.Reason != reason {
		t.Fatalf("the creation reports %+v (%v), want refused as %s", report, err, reason)
	}
	return report
}

// A key that is not 64 lowercase hex characters names no journal file, so the creation is refused before a journal is
// read or written.
func TestARepositoryCreationRefusesAKeyThatNamesNoJournal(t *testing.T) {
	t.Parallel()
	key := ledger.CreationKey("approval-1", "api")
	for name, bad := range map[string]string{
		"empty": "", "a path": "../../escape", "upper case": strings.ToUpper(key), "short": key[:63], "long": key + "0",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newCreationRig(t)
			r.folders.Dir("/work/tt/repos")
			job := creation("/work/tt/repos/api")
			job.Key = bad
			refusedAs(t, r.creator(r.forge), job, ledger.TemplateRefusedInvalid)
			if entries, err := os.ReadDir(r.state); err != nil || len(entries) != 0 {
				t.Errorf("the state directory holds %v (%v)", entries, err)
			}
			if done, created := r.folders.Done(), r.forge.Repositories(); len(done) != 0 || len(created) != 0 {
				t.Errorf("the worker did %v and created %v", done, created)
			}
		})
	}
}

// A job no manifest entry gives is refused before anything is read of the forge or written.
func TestARepositoryCreationRefusesAJobNoManifestEntryGives(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*ledger.RepositoryCreation){
		"a forge name that is not the path's last element": func(job *ledger.RepositoryCreation) { job.ForgeName = "web" },
		"a branch git refuses":                             func(job *ledger.RepositoryCreation) { job.DefaultBranch = "+main" },
		"another forge":                                    func(job *ledger.RepositoryCreation) { job.Create.Forge = "gitlab" },
		"a bound that is not absolute":                     func(job *ledger.RepositoryCreation) { job.Bound = "work" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newCreationRig(t)
			r.folders.Dir("/work/tt/repos")
			job := creation("/work/tt/repos/api")
			change(&job)
			refusedAs(t, r.creator(r.forge), job, ledger.TemplateRefusedInvalid)
			r.nothingDone(job)
		})
	}
}

// The nearest thing above a missing folder that is not a folder is refused, and nothing is made.
func TestARepositoryCreationRefusesAFileWhereAFolderAboveItGoes(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.folders.Place("/work/tt/repos", "not a folder")
	job := creation("/work/tt/repos/api")
	report := refusedAs(t, r.creator(r.forge), job, ledger.TemplateRefusedNoParent)
	if !strings.Contains(report.Detail, "/work/tt/repos") {
		t.Errorf("the refusal says %q, which does not name /work/tt/repos", report.Detail)
	}
	r.nothingDone(job)
}

// A creation whose push failed answers its check from its journal, with the account it ran under, even once gh reads
// signed out, and a retry pushes from where it stopped and creates nothing twice.
func TestARepositoryCreationThatFailedAnswersFromItsJournalAndARetryFinishesIt(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.folders.Dir("/work/tt/repos")
	job := creation("/work/tt/repos/api")
	r.folders.FailPush(job.Path, errors.New("remote: push refused"))
	creator := r.creator(r.forge)
	report, err := creator.Create(t.Context(), job)
	if err != nil || len(report.Repositories) != 1 || steps(report.Repositories[0]) != "create=done origin=done push=failed" {
		t.Fatalf("the creation reports %+v (%v), want its push failed", report, err)
	}

	r.forge.SignIn = ports.ForgeAccount{State: ledger.ForgeAccountSignedOut}
	checked, err := creator.Check(t.Context(), job)
	if want := (ledger.CreationCheck{URL: "https://github.com/acme/api.git", Login: "test", Branch: "main"}); err != nil || checked != want {
		t.Fatalf("the check of the started creation is %+v (%v), want %+v", checked, err, want)
	}

	r.folders.FailPush(job.Path, nil)
	report, err = creator.Create(t.Context(), job)
	if err != nil || report.Outcome != ledger.TemplateOutcomeWritten || len(report.Repositories) != 1 || steps(report.Repositories[0]) != "create=done origin=done push=done" {
		t.Fatalf("the retry reports %+v (%v), want every step done", report, err)
	}
	if created, pushes := r.forge.Repositories(), r.folders.Pushes(); len(created) != 1 || len(pushes) != 2 || len(report.Remaining) != 0 {
		t.Errorf("the retry created %v and pushed %v, leaving %q", created, pushes, report.Remaining)
	}
}

// A journal is held to the creation it was written for: a job with its key that names another repository is refused,
// and nothing more is done.
func TestARepositoryCreationIsHeldToTheRepositoryItsJournalNames(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*ledger.RepositoryCreation){
		"another path": func(job *ledger.RepositoryCreation) {
			job.Path, job.Name, job.ForgeName = "/work/tt/repos/web", "web", "web"
		},
		"another owner":      func(job *ledger.RepositoryCreation) { job.Create.Owner = "test" },
		"another visibility": func(job *ledger.RepositoryCreation) { job.Create.Visibility = ledger.VisibilityPublic },
		"another branch":     func(job *ledger.RepositoryCreation) { job.DefaultBranch = "trunk" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newCreationRig(t)
			r.folders.Dir("/work/tt/repos")
			job := creation("/work/tt/repos/api")
			r.folders.FailPush(job.Path, errors.New("remote: push refused"))
			if _, err := r.creator(r.forge).Create(t.Context(), job); err != nil {
				t.Fatal(err)
			}
			done := r.folders.Done()
			other := job
			change(&other)
			refusedAs(t, r.creator(r.forge), other, ledger.TemplateRefusedInvalid)
			if !slices.Equal(r.folders.Done(), done) || len(r.forge.Repositories()) != 1 {
				t.Errorf("the worker went on to do %v and created %v", r.folders.Done(), r.forge.Repositories())
			}
		})
	}
}

// A creation that names no branch starts on the worker's default branch, which its check names, and its journal keeps
// that branch for a retry after the default changed.
func TestARepositoryCreationWithNoBranchStartsOnTheDefaultBranch(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.folders.Dir("/work/tt/repos")
	r.folders.SetDefaultBranch("devel")
	job := creation("/work/tt/repos/api")
	creator := r.creator(r.forge)
	if checked, err := creator.Check(t.Context(), job); err != nil || checked.Branch != "devel" {
		t.Fatalf("the check is %+v (%v), want branch devel", checked, err)
	}
	r.folders.FailPush(job.Path, errors.New("remote: push refused"))
	if _, err := creator.Create(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	r.folders.SetDefaultBranch("other")
	if checked, err := creator.Check(t.Context(), job); err != nil || checked.Branch != "devel" {
		t.Errorf("the check after the default changed is %+v (%v), want branch devel", checked, err)
	}
	r.folders.FailPush(job.Path, nil)
	if _, err := creator.Create(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if pushes := r.folders.Pushes(); len(pushes) != 2 || pushes[1].Branch != "devel" {
		t.Errorf("the pushes are %+v, want the retry to push devel", pushes)
	}
}

// A link above a missing folder that leads to no folder is refused before anything is made, because no folder can be
// made through it.
func TestARepositoryCreationRefusesALinkAboveItThatLeadsNowhere(t *testing.T) {
	t.Parallel()
	r := newCreationRig(t)
	r.folders.Dir("/work/tt/repos")
	r.folders.Link("/work/tt/repos/gone", "/work/tt/repos/missing")
	job := creation("/work/tt/repos/gone/api")
	report := refusedAs(t, r.creator(r.forge), job, ledger.TemplateRefusedNoParent)
	if !strings.Contains(report.Detail, "/work/tt/repos/gone") {
		t.Errorf("the refusal says %q, which does not name the link", report.Detail)
	}
	r.nothingDone(job)
}

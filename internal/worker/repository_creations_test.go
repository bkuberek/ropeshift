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

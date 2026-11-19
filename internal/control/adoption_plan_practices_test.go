package control_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
)

const practicesTip = "1111111111111111111111111111111111111111"

// checking is a machine's plan that reads each repository's practices through the fake reads.
func (m *adoptionMachine) checking(f *fake.Practices, in control.AdoptionRequest) (planning.AdoptionPlan, error) {
	m.t.Helper()
	sources := practicerun.Sources{Snapshots: f, Secrets: f, Forge: f, Clock: fake.NewClock(time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC))}
	return control.NewAdoptions(control.NewRepositories(m.store())).Checking(sources).Plan(m.t.Context(), "acme", in, m.checkouts, m.layouts, projectMember)
}

func practicedRepository(t *testing.T, plan planning.AdoptionPlan, path string) planning.PlannedRepository {
	t.Helper()
	for _, repository := range plan.Repositories {
		if repository.Path == path {
			return repository
		}
	}
	t.Fatalf("the plan lists no repository at %s", path)
	return planning.PlannedRepository{}
}

// A plan that asks for the practices lists them for each repository it would register, read at the clone's default
// branch when no record names a base, and a plan that does not ask reads nothing.
func TestAnAdoptionPlanListsThePracticesOfEachRepository(t *testing.T) {
	t.Parallel()
	m := newAdoptionMachine(t)
	workspaceOf(m, "AGENTS.md")
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	var f fake.Practices
	f.SetSnapshot("/work/ws/repos/api", practices.Snapshot{Commit: practicesTip, Paths: []string{"go.mod"}, Read: func(string) (string, bool) {
		return "module acme.test/api\n\nrequire example.test/dep v1.0.0\n", true
	}})
	f.SetSnapshot("/work/ws", practices.Snapshot{Commit: practicesTip, Paths: []string{"README.md"}})

	none, err := m.checking(&f, control.AdoptionRequest{Root: "/work/ws"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.SnapshotCalls) != 0 {
		t.Fatalf("a plan that asked for no practices read %v", f.SnapshotCalls)
	}
	for _, repository := range none.Repositories {
		if repository.Practices != nil || repository.PracticesNote != "" {
			t.Errorf("%s holds practices in a plan that asked for none", repository.Path)
		}
	}

	plan, err := m.checking(&f, control.AdoptionRequest{Root: "/work/ws", Practices: true})
	if err != nil {
		t.Fatal(err)
	}
	api := practicedRepository(t, plan, "/work/ws/repos/api")
	if api.Practices == nil || api.Practices.BaseBranch != "main" || api.Practices.Commit != practicesTip {
		t.Fatalf("the api repository holds %+v", api.Practices)
	}
	if !slices.ContainsFunc(api.Practices.Findings, func(f practices.Finding) bool { return f.ID == "lockfile:go" }) {
		t.Errorf("the api repository's findings are %v", api.Practices.Findings)
	}
	if got := len(plan.Repositories); got != 2 || len(f.SnapshotCalls) != 2 {
		t.Errorf("%d repositories were planned and %d were read", got, len(f.SnapshotCalls))
	}
	if len(f.SettingsCalls) != 0 {
		t.Errorf("the forge was read: %v", f.SettingsCalls)
	}
}

// A repository the project holds is read at the base branch its record names, which is the branch a registration
// records.
func TestAnAdoptionPlanReadsPracticesAtTheBaseTheRecordNames(t *testing.T) {
	t.Parallel()
	m := newAdoptionMachine(t)
	workspaceOf(m, "AGENTS.md")
	store := m.store()
	repositories := control.NewRepositories(store)
	if _, err := repositories.Add(t.Context(), "acme", control.RepositoryAdd{Paths: []string{"/work/ws"}}, m.checkouts, projectMember); err != nil {
		t.Fatal(err)
	}
	base := "release"
	if _, err := repositories.Change(t.Context(), "acme", "root-ws", control.RepositoryChange{Base: &base}, projectMember); err != nil {
		t.Fatal(err)
	}
	var f fake.Practices
	plan, err := m.checking(&f, control.AdoptionRequest{Root: "/work/ws", Practices: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := practicedRepository(t, plan, "/work/ws"); got.Outcome != string(ledger.AlreadyRegistered) || got.Practices == nil || got.Practices.BaseBranch != base {
		t.Errorf("the registered repository reads %+v", got)
	}
	if len(f.SnapshotCalls) != 1 || f.SnapshotCalls[0][1] != base {
		t.Errorf("the reads were %v, want one at %s", f.SnapshotCalls, base)
	}
}

// A repository the check cannot read gets a note that names why and no path, and the other repositories keep theirs.
func TestAnAdoptionPlanNotesARepositoryItCouldNotRead(t *testing.T) {
	t.Parallel()
	m := newAdoptionMachine(t)
	workspaceOf(m, "AGENTS.md")
	var f fake.Practices
	f.Fail = errors.New("git -C /work/ws rev-parse main: fatal: ambiguous argument\n")
	plan, err := m.checking(&f, control.AdoptionRequest{Root: "/work/ws", Practices: true})
	if err != nil {
		t.Fatal(err)
	}
	got := practicedRepository(t, plan, "/work/ws")
	if got.Practices != nil || !strings.Contains(got.PracticesNote, "ambiguous argument") || strings.Contains(got.PracticesNote, "/work") || strings.Contains(got.PracticesNote, "\n") {
		t.Errorf("the repository reads %+v and the note %q", got.Practices, got.PracticesNote)
	}
	if got.Outcome != string(ledger.Registered) {
		t.Errorf("a repository the check could not read reads %q", got.Outcome)
	}
}

// blockingIn blocks the secret scan of one directory until its context ends, and reads every other as the fake does.
type blockingIn struct {
	dir  string
	rest *fake.Practices
}

func (b blockingIn) Scan(ctx context.Context, dir, tip, from string) (ports.SecretScan, error) {
	if dir == b.dir {
		<-ctx.Done()
		return ports.SecretScan{}, ctx.Err()
	}
	return b.rest.Scan(ctx, dir, tip, from)
}

// A repository whose check runs past the limit gets a note and no practices, and the plan still lists the others.
func TestAnAdoptionPlanGoesOnPastARepositoryWhoseCheckRunsLong(t *testing.T) {
	t.Parallel()
	m := newAdoptionMachine(t)
	workspaceOf(m, "AGENTS.md")
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	var f fake.Practices
	f.SetSnapshot("/work/ws", practices.Snapshot{Commit: practicesTip, Paths: []string{"README.md"}})
	f.SetSnapshot("/work/ws/repos/api", practices.Snapshot{Commit: practicesTip, Paths: []string{"go.mod"}})
	sources := practicerun.Sources{Snapshots: &f, Secrets: blockingIn{dir: "/work/ws", rest: &f}, Forge: &f, Clock: fake.NewClock(time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC))}
	plan, err := control.NewAdoptions(control.NewRepositories(m.store())).Checking(sources).Limiting(30*time.Millisecond).Plan(t.Context(), "acme", control.AdoptionRequest{Root: "/work/ws", Practices: true}, m.checkouts, m.layouts, projectMember)
	if err != nil {
		t.Fatalf("a check past its limit failed the plan: %v", err)
	}
	slow := practicedRepository(t, plan, "/work/ws")
	if slow.Practices != nil || !strings.Contains(slow.PracticesNote, "not read within") {
		t.Errorf("the slow repository reads %+v and the note %q", slow.Practices, slow.PracticesNote)
	}
	if other := practicedRepository(t, plan, "/work/ws/repos/api"); other.Practices == nil {
		t.Errorf("the other repository holds no practices: %q", other.PracticesNote)
	}
}

// A request that ends while a repository's check runs ends the plan.
func TestAnAdoptionPlanEndsWithTheRequestThatAskedForIt(t *testing.T) {
	t.Parallel()
	m := newAdoptionMachine(t)
	workspaceOf(m, "AGENTS.md")
	var f fake.Practices
	f.SetSnapshot("/work/ws", practices.Snapshot{Commit: practicesTip, Paths: []string{"README.md"}})
	sources := practicerun.Sources{Snapshots: &f, Secrets: blockingIn{dir: "/work/ws", rest: &f}, Forge: &f, Clock: fake.NewClock(time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC))}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := control.NewAdoptions(control.NewRepositories(m.store())).Checking(sources).Plan(ctx, "acme", control.AdoptionRequest{Root: "/work/ws", Practices: true}, m.checkouts, m.layouts, projectMember)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a plan whose request ended gave %v", err)
	}
}

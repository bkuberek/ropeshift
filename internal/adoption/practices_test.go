package adoption_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
)

const (
	cloneDir = "/work/ws/repos/api"
	tip      = "1111111111111111111111111111111111111111"
)

func practiceSources(f *fake.Practices) practicerun.Sources {
	return practicerun.Sources{Snapshots: f, Secrets: f, Forge: f, Clock: fake.NewClock(time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC))}
}

func plannedAPI() planning.PlannedRepository {
	return planning.PlannedRepository{Name: "api", Path: cloneDir, ID: "3c4d5e6f", Outcome: string(ledger.Registered)}
}

func findingIDs(result *practices.Result) []string {
	var ids []string
	for _, finding := range result.Findings {
		ids = append(ids, finding.ID)
	}
	slices.Sort(ids)
	return ids
}

// A clone's practices are read at the base branch a registration would record, with no forge binding, the whole
// history, and the default merge authority, and the result is listed with the repository.
func TestPracticesAreReadAtTheBaseWithNoBindingAndTheWholeHistory(t *testing.T) {
	var f fake.Practices
	f.SetSnapshot(cloneDir, practices.Snapshot{Commit: tip, Paths: []string{"go.mod", "main.go"}, Read: func(string) (string, bool) {
		return "module acme.test/api\n\nrequire example.test/dep v1.0.0\n", true
	}})
	got, err := adoption.CheckPractices(t.Context(), practiceSources(&f), plannedAPI(), cloneDir, "trunk", adoption.PracticesLimit)
	if err != nil {
		t.Fatal(err)
	}
	if got.Practices == nil || got.PracticesNote != "" {
		t.Fatalf("the repository holds %+v and the note %q", got.Practices, got.PracticesNote)
	}
	if want := []string{"ci", "codeowners", "dependency-updates", "lockfile:go", "pre-commit", "security-policy", "tests:go"}; !slices.Equal(findingIDs(got.Practices), want) {
		t.Errorf("the findings are %v, want %v", findingIDs(got.Practices), want)
	}
	if got.Practices.Commit != tip || got.Practices.BaseBranch != "trunk" || got.Practices.Repository != "3c4d5e6f" {
		t.Errorf("the result reads commit %q, base %q, repository %q", got.Practices.Commit, got.Practices.BaseBranch, got.Practices.Repository)
	}
	if !slices.Equal(f.SnapshotCalls[0][:], []string{cloneDir, "trunk"}) || f.ScanCalls[0][2] != "" || len(f.SettingsCalls) != 0 {
		t.Errorf("the reads were %v, %v, and %v", f.SnapshotCalls, f.ScanCalls, f.SettingsCalls)
	}
	for _, id := range []practices.ID{practices.BranchProtection, practices.RequiredReview, practices.License} {
		for _, status := range got.Practices.Practices {
			if status.ID == id && status.Status != practices.StatusNotApplicable {
				t.Errorf("%s is %s with no forge binding", id, status.Status)
			}
		}
	}
	if plannedAPI().Practices != nil {
		t.Error("the repository the caller held changed")
	}
}

// A clone the check cannot read has no practices and a note that says why, on one line and with no path of the
// machine in it.
func TestAClonePracticesCannotReadGetsANoteWithNoPath(t *testing.T) {
	var f fake.Practices
	f.Fail = errors.New("git -C " + cloneDir + " rev-parse: fatal: not a git repository\n" + cloneDir + "/.git")
	got, err := adoption.CheckPractices(t.Context(), practiceSources(&f), plannedAPI(), cloneDir, "main", adoption.PracticesLimit)
	if err != nil {
		t.Fatal(err)
	}
	if got.Practices != nil {
		t.Errorf("the repository holds the result %+v", got.Practices)
	}
	if !strings.Contains(got.PracticesNote, "not a git repository") || strings.Contains(got.PracticesNote, cloneDir) || strings.Contains(got.PracticesNote, "\n") {
		t.Errorf("the note reads %q", got.PracticesNote)
	}
}

// The note is bounded however long the reason is.
func TestThePracticesNoteIsBounded(t *testing.T) {
	var f fake.Practices
	f.Fail = errors.New(strings.Repeat("why ", 2000))
	got, err := adoption.CheckPractices(t.Context(), practiceSources(&f), plannedAPI(), cloneDir, "main", adoption.PracticesLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PracticesNote) == 0 || len(got.PracticesNote) > practicerun.MaxReason+100 {
		t.Errorf("the note holds %d bytes", len(got.PracticesNote))
	}
}

// failingScan is a secret scan that fails with its error.
type failingScan struct{ err error }

func (s failingScan) Scan(context.Context, string, string, string) (ports.SecretScan, error) {
	return ports.SecretScan{}, s.err
}

// No path in a practice's own reason either: an unknown practice names why, and the reason is masked as a note is.
func TestAPracticeReasonNamesNoPath(t *testing.T) {
	var f fake.Practices
	f.SetSnapshot(cloneDir, practices.Snapshot{Commit: tip, Paths: []string{"go.mod"}})
	sources := practiceSources(&f)
	sources.Secrets = failingScan{err: errors.New("git -C " + cloneDir + " log: exit 128")}
	got, err := adoption.CheckPractices(t.Context(), sources, plannedAPI(), cloneDir, "main", adoption.PracticesLimit)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, status := range got.Practices.Practices {
		if status.ID == practices.Secrets {
			seen = true
			if status.Status != practices.StatusUnknown || strings.Contains(status.Reason, cloneDir) || !strings.Contains(status.Reason, "<checkout>") {
				t.Errorf("the secrets practice reads %+v", status)
			}
		}
	}
	if !seen {
		t.Error("the result holds no status for secrets")
	}
}

// A repository with no base branch is not read, and a run with no reads to make leaves the repository as it was.
func TestPracticesAreNotReadWithNoBaseOrNoSources(t *testing.T) {
	var f fake.Practices
	got, err := adoption.CheckPractices(t.Context(), practiceSources(&f), plannedAPI(), cloneDir, "", adoption.PracticesLimit)
	if err != nil || got.Practices != nil || !strings.Contains(got.PracticesNote, "base branch") {
		t.Errorf("a clone with no base reads %+v, %q: %v", got.Practices, got.PracticesNote, err)
	}
	if len(f.SnapshotCalls) != 0 {
		t.Errorf("a clone with no base was read: %v", f.SnapshotCalls)
	}
	got, err = adoption.CheckPractices(t.Context(), practicerun.Sources{}, plannedAPI(), cloneDir, "main", adoption.PracticesLimit)
	if err != nil || got.Practices != nil || got.PracticesNote != "" {
		t.Errorf("a run with no sources reads %+v, %q: %v", got.Practices, got.PracticesNote, err)
	}
}

// A request that ends ends the plan, instead of reading as a clone the check could not read.
func TestAnEndedRequestEndsThePracticesRun(t *testing.T) {
	var f fake.Practices
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := adoption.CheckPractices(ctx, practiceSources(&f), plannedAPI(), cloneDir, "main", adoption.PracticesLimit); !errors.Is(err, context.Canceled) {
		t.Errorf("an ended request gave %v", err)
	}
}

// blockingScan is a secret scan that runs until its context ends, as a scan of a history too large to read in time does.
type blockingScan struct{ started chan struct{} }

func (s blockingScan) Scan(ctx context.Context, _, _, _ string) (ports.SecretScan, error) {
	if s.started != nil {
		close(s.started)
	}
	<-ctx.Done()
	return ports.SecretScan{}, ctx.Err()
}

// A check that runs past its limit leaves the repository with no practices and a note that names the limit, and the
// plan goes on: the request that asked for it has not ended.
func TestACheckThatRunsPastItsLimitGetsANoteAndTheRunGoesOn(t *testing.T) {
	var f fake.Practices
	f.SetSnapshot(cloneDir, practices.Snapshot{Commit: tip, Paths: []string{"go.mod"}})
	sources := practiceSources(&f)
	sources.Secrets = blockingScan{}
	got, err := adoption.CheckPractices(t.Context(), sources, plannedAPI(), cloneDir, "main", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("a check past its limit ended the plan: %v", err)
	}
	if got.Practices != nil || got.PracticesNote != "the practices were not read within 0.02 s" {
		t.Errorf("the repository holds %+v and the note %q", got.Practices, got.PracticesNote)
	}
	if got.Outcome != string(ledger.Registered) {
		t.Errorf("the outcome reads %q", got.Outcome)
	}
}

// The default limit is the one the docs state.
func TestTheDefaultPracticesLimitIsThirtySeconds(t *testing.T) {
	if adoption.PracticesLimit != 30*time.Second {
		t.Errorf("the limit is %s", adoption.PracticesLimit)
	}
}

// A request that ends while a check runs still ends the plan, whatever the limit.
func TestARequestThatEndsDuringACheckEndsThePlan(t *testing.T) {
	var f fake.Practices
	f.SetSnapshot(cloneDir, practices.Snapshot{Commit: tip, Paths: []string{"go.mod"}})
	sources := practiceSources(&f)
	started := make(chan struct{})
	sources.Secrets = blockingScan{started: started}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()
	if _, err := adoption.CheckPractices(ctx, sources, plannedAPI(), cloneDir, "main", time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("an ended request gave %v", err)
	}
}

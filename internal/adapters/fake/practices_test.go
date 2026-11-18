package fake

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

// The settings of a repository come back with the error set for it, so a test can make a read fail with the facts the
// read still settled, as a rate limit does, and each read is listed.
func TestPracticesSettingsReturnsTheFactsAndTheErrorOfARepository(t *testing.T) {
	var fake Practices
	repo := ports.ForgeRepo{Binding: ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}}
	other := ports.ForgeRepo{Binding: ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "gadgets"}}
	facts := practices.ForgeFacts{Visibility: practices.VisibilityPublic, ReviewUnknown: "rate limited"}
	fake.SetSettings(repo.Binding.String(), facts)
	limited := &ports.RateLimitedError{}
	fake.SetSettingsError(repo.Binding.String(), limited)

	got, err := fake.Settings(t.Context(), repo, "main")
	if got != facts || !errors.Is(err, ports.ErrForgeRateLimited) {
		t.Fatalf("settings are %+v with %v, want %+v with the rate limit", got, err, facts)
	}
	if got, err := fake.Settings(t.Context(), other, "trunk"); got != (practices.ForgeFacts{}) || err != nil {
		t.Errorf("a repository with none set reads %+v with %v", got, err)
	}
	fake.SetSettingsError(repo.Binding.String(), nil)
	if _, err := fake.Settings(t.Context(), repo, "main"); err != nil {
		t.Errorf("a cleared error is still %v", err)
	}
	want := [][2]string{{repo.Binding.String(), "main"}, {other.Binding.String(), "trunk"}, {repo.Binding.String(), "main"}}
	if len(fake.SettingsCalls) != len(want) {
		t.Fatalf("settings were read as %v, want %v", fake.SettingsCalls, want)
	}
	for i := range want {
		if fake.SettingsCalls[i] != want[i] {
			t.Errorf("read %d was %v, want %v", i, fake.SettingsCalls[i], want[i])
		}
	}
}

// A failure set on every read still fails the settings read, with no facts.
func TestPracticesFailFailsTheSettingsRead(t *testing.T) {
	boom := errors.New("boom")
	fake := Practices{Fail: boom}
	repo := ports.ForgeRepo{Binding: ledger.ForgeBinding{Host: "github.com", Owner: "acme", Repo: "widgets"}}
	fake.SetSettings(repo.Binding.String(), practices.ForgeFacts{Protected: true})
	if got, err := fake.Settings(t.Context(), repo, "main"); !errors.Is(err, boom) || got != (practices.ForgeFacts{}) {
		t.Fatalf("settings are %+v with %v", got, err)
	}
}

// Each snapshot read is listed with the directory and the commit it was asked for, and a directory with none set
// reads an empty snapshot of that commit.
func TestPracticesSnapshotListsEachRead(t *testing.T) {
	var fake Practices
	fake.SetSnapshot("/a", practices.Snapshot{Commit: "abc", Paths: []string{"go.mod"}})
	got, err := fake.Snapshot(t.Context(), "/a", "main")
	if err != nil || got.Commit != "abc" {
		t.Fatalf("the snapshot is %+v with %v", got, err)
	}
	if got, err := fake.Snapshot(t.Context(), "/b", "trunk"); err != nil || got.Commit != "trunk" || len(got.Paths) != 0 {
		t.Errorf("a directory with none set reads %+v with %v", got, err)
	}
	want := [][2]string{{"/a", "main"}, {"/b", "trunk"}}
	if len(fake.SnapshotCalls) != len(want) || fake.SnapshotCalls[0] != want[0] || fake.SnapshotCalls[1] != want[1] {
		t.Errorf("snapshots were read as %v, want %v", fake.SnapshotCalls, want)
	}
}

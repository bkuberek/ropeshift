package control

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

func TestACreationIsRunningOnlyWhileItsKeyIsMarked(t *testing.T) {
	r := NewRepositories(nil)
	if r.Creating("approval-1", "api") {
		t.Fatal("a creation nothing started reads as running")
	}
	key := ledger.CreationKey("approval-1", "api")
	r.markCreating(key)
	if !r.Creating("approval-1", "api") {
		t.Error("a creation whose key is marked does not read as running")
	}
	if r.Creating("approval-1", "web") || r.Creating("approval-2", "api") {
		t.Error("the key of one creation marks another")
	}
	r.clearCreating(key)
	if r.Creating("approval-1", "api") {
		t.Error("a creation whose key is cleared reads as running")
	}
}

func TestReadingWhetherACreationRunsWaitsForNoRegistration(t *testing.T) {
	r := NewRepositories(nil)
	r.registering.Lock()
	defer r.registering.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.Creating("approval-1", "api")
	}()
	wg.Wait()
}

func TestTheOutcomesOfARegistrationKeepEachCreationBothWays(t *testing.T) {
	creation := &ledger.CreationOutcome{Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: ledger.VisibilityPrivate, URL: "git@github.com:acme/api.git", Makefile: "api=git@github.com:acme/api.git=repos/api"}
	added := []Added{
		{Name: "api", Manifest: "repos/api", Remote: creation.URL, Path: "/work/repos/api", Outcome: ledger.Created, Creation: creation},
		{Name: "docs", Manifest: "repos/docs", Outcome: ledger.Planned},
		{Name: "web", Manifest: "repos/web", Outcome: ledger.RegistrationRefused, Reason: "web is not registered: gh is signed out", RefusalCode: "gh_signed_out"},
	}
	record := outcomesOf(added)
	if record[2].RefusalCode != "gh_signed_out" || record[0].RefusalCode != "" {
		t.Fatalf("the outcomes keep the refusal codes %q and %q", record[2].RefusalCode, record[0].RefusalCode)
	}
	if record[0].Creation != creation || record[1].Creation != nil {
		t.Fatalf("the outcomes keep the creations %+v and %+v", record[0].Creation, record[1].Creation)
	}
	back, err := recordedAdded(nil, &ledger.ManifestRegistration{Repositories: record})
	if err != nil || !reflect.DeepEqual(back, added) {
		t.Errorf("a recorded registration reads back as %+v (%v), want %+v", back, err, added)
	}
}

// A retry or a skip of a creation that runs now in this process is an illegal move that names the entry, and reads nothing
// from the ledger, so it never waits for the run it would collide with.
func TestARetryOrASkipOfARunningCreationIsAnIllegalMove(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	r := NewRepositories(nil)
	r.markCreating(ledger.CreationKey("approval-1", "api"))
	_, err := r.RetryCreation(t.Context(), "scope", "approval-1", "api", nil, nil, "", member)
	if !errors.Is(err, workorders.ErrIllegalMove) || err.Error() != "no such transition: api is being created now" {
		t.Errorf("a retry of a running creation answered %v", err)
	}
	if _, err = r.SkipCreation(t.Context(), "scope", "approval-1", "api", "built by hand", member); !errors.Is(err, workorders.ErrIllegalMove) {
		t.Errorf("a skip of a running creation answered %v", err)
	}
}

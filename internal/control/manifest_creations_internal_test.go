package control

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
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

func eventOf(t *testing.T, rev int64, kind string, data any) ledger.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Event{Rev: rev, Type: kind, Data: raw}
}

// What a member does about a creation that waits comes from the code the creator refused with, and from no word of the
// reason, of whichever of the approval's registration and the creation's finish is newer: the record's own remedy first,
// the repository to clone and name as the entry's remote when it exists on the forge and the record has none, and else the
// command the creator's check gave.
func TestWhatAMemberDoesAboutACreationComesFromTheRefusalCode(t *testing.T) {
	row := authority.Approval{ID: "ap-1", Plan: "tt", PlanVersion: 1}
	entry := planning.ManifestRepository{Name: "api", Path: "repos/api"}
	creation := func(remedy string) *ledger.CreationOutcome {
		return &ledger.CreationOutcome{Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: ledger.VisibilityPrivate, Remedy: remedy}
	}
	recorded := func(outcome ledger.ManifestOutcome) *ledger.ManifestRegistration {
		outcome.Name, outcome.Path, outcome.Resolved, outcome.Outcome = "api", "repos/api", "/work/repos/api", ledger.RegistrationRefused
		return &ledger.ManifestRegistration{Repositories: []ledger.ManifestOutcome{outcome}}
	}
	finished := func(rev int64, code, reason string, remedy string) ledger.Event {
		return eventOf(t, rev, ledger.EventRepositoryCreationFinished, ledger.CreationFinishedEvent{
			Name: "api", Key: "k", Outcome: ledger.CreationRefused, Reason: reason, RefusalCode: code, CreationOutcome: *creation(remedy),
		})
	}
	registered := func(rev int64) ledger.Event {
		return eventOf(t, rev, ledger.EventManifestRegistered, ledger.ManifestRegistration{})
	}
	const clone = "clone acme/api at /work/repos/api and write `remote` in place of `create` in the manifest entry for api, or skip it"
	for name, tt := range map[string]struct {
		events  []ledger.Event
		record  *ledger.ManifestRegistration
		running bool
		want    string
	}{
		"the record's own remedy, with skip named": {
			events: []ledger.Event{registered(5)},
			record: recorded(ledger.ManifestOutcome{RefusalCode: ledger.TemplateRefusedRemoteExists, Reason: "exists", Creation: creation("clone it where the path leads")}),
			want:   "clone it where the path leads, or skip it",
		},
		"an existing repository whose record has no remedy": {
			events: []ledger.Event{registered(5)},
			record: recorded(ledger.ManifestOutcome{RefusalCode: ledger.TemplateRefusedRemoteExists, Reason: "acme/api exists", Creation: creation("")}),
			want:   clone,
		},
		"a reason that holds the word and a record with no code": {
			events: []ledger.Event{registered(5)},
			record: recorded(ledger.ManifestOutcome{Reason: "remote_exists: it holds the word", Creation: creation("gh auth login --hostname github.com")}),
			want:   "run `gh auth login --hostname github.com`",
		},
		"a reason that holds the word, no code, and no command": {
			events: []ledger.Event{registered(5)},
			record: recorded(ledger.ManifestOutcome{Reason: "remote_exists: it holds the word", Creation: creation("")}),
			want:   "",
		},
		"another refusal's command": {
			events: []ledger.Event{registered(5)},
			record: recorded(ledger.ManifestOutcome{RefusalCode: "gh_signed_out", Reason: "gh is signed out", Creation: creation("gh auth login --hostname github.com")}),
			want:   "run `gh auth login --hostname github.com`",
		},
		"the finish, when it is newer than the record": {
			events: []ledger.Event{registered(3), finished(7, ledger.TemplateRefusedRemoteExists, "acme/api exists", "")},
			record: recorded(ledger.ManifestOutcome{RefusalCode: "gh_timeout", Reason: "slow", Creation: creation("")}),
			want:   clone,
		},
		"the record, when it is newer than the finish": {
			events: []ledger.Event{finished(3, ledger.TemplateRefusedRemoteExists, "acme/api exists", ""), registered(9)},
			record: recorded(ledger.ManifestOutcome{RefusalCode: "gh_timeout", Reason: "slow", Creation: creation("wait, then retry")}),
			want:   "run `wait, then retry`",
		},
		"a creation that runs": {
			events:  []ledger.Event{registered(5)},
			record:  recorded(ledger.ManifestOutcome{RefusalCode: ledger.TemplateRefusedRemoteExists, Reason: "exists", Creation: creation("")}),
			running: true,
			want:    "",
		},
	} {
		item, listed, err := uncreated(row, entry, tt.events, tt.record, tt.running)
		if err != nil || !listed {
			t.Errorf("%s: listed %v: %v", name, listed, err)
			continue
		}
		if item.Remedy != tt.want {
			t.Errorf("%s: the remedy is %q, want %q", name, item.Remedy, tt.want)
		}
	}
}

// A check that refuses because another creation holds the path says to preview again: it is no creation that waits for a
// member, so it is never an item, whichever record holds it. A creation that failed before stays the item it was.
func TestAPathBusyRefusalIsNeverAnItemOfNeedsYou(t *testing.T) {
	row := authority.Approval{ID: "ap-1", Plan: "tt", PlanVersion: 1}
	entry := planning.ManifestRepository{Name: "api", Path: "repos/api"}
	outcome := ledger.CreationOutcome{Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: ledger.VisibilityPrivate}
	busy := &ledger.ManifestRegistration{Repositories: []ledger.ManifestOutcome{{
		Name: "api", Path: "repos/api", Resolved: "/work/repos/api", Outcome: ledger.RegistrationRefused, RefusalCode: ledger.TemplateRefusedBusy, Reason: "another creation writes it", Creation: &outcome,
	}}}
	registered := eventOf(t, 9, ledger.EventManifestRegistered, ledger.ManifestRegistration{})
	finished := func(rev int64, state ledger.CreationState, code string) ledger.Event {
		return eventOf(t, rev, ledger.EventRepositoryCreationFinished, ledger.CreationFinishedEvent{Name: "api", Key: "k", Outcome: state, Reason: "why", RefusalCode: code, CreationOutcome: outcome})
	}
	for name, tt := range map[string]struct {
		events []ledger.Event
		listed bool
		state  UncreatedState
	}{
		"a refusal only the registration holds":             {events: []ledger.Event{registered}},
		"a creation that finished refused as busy":          {events: []ledger.Event{finished(3, ledger.CreationRefused, ledger.TemplateRefusedBusy)}},
		"a busy retry of a creation that failed":            {events: []ledger.Event{finished(3, ledger.CreationFailed, ""), registered}, listed: true, state: CreationFailed},
		"a creation that finished refused for another code": {events: []ledger.Event{finished(3, ledger.CreationRefused, "gh_signed_out")}, listed: true, state: CreationRefused},
	} {
		item, listed, err := uncreated(row, entry, tt.events, busy, false)
		if err != nil || listed != tt.listed || (listed && item.State != tt.state) {
			t.Errorf("%s: listed %v as %q (%v), want listed %v as %q", name, listed, item.State, err, tt.listed, tt.state)
		}
	}
}

// A path_busy refusal is transient, so it never replaces the record of an earlier failure: a creation that failed or was
// interrupted, and whose later attempt finished refused as busy, stays an item with the earlier failure's reason.
func TestAPathBusyFinishDoesNotHideAnEarlierFailure(t *testing.T) {
	row := authority.Approval{ID: "ap-1", Plan: "tt", PlanVersion: 1}
	entry := planning.ManifestRepository{Name: "api", Path: "repos/api"}
	outcome := ledger.CreationOutcome{Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: ledger.VisibilityPrivate}
	started := func(rev int64) ledger.Event {
		return eventOf(t, rev, ledger.EventRepositoryCreationStarted, ledger.CreationStartedEvent{Name: "api", Key: "k", Path: "/work/repos/api"})
	}
	finished := func(rev int64, state ledger.CreationState, code, reason string) ledger.Event {
		return eventOf(t, rev, ledger.EventRepositoryCreationFinished, ledger.CreationFinishedEvent{Name: "api", Key: "k", Outcome: state, Reason: reason, RefusalCode: code, CreationOutcome: outcome})
	}
	for name, tt := range map[string]struct {
		events []ledger.Event
		listed bool
		state  UncreatedState
		reason string
	}{
		"a failure, then a busy finish": {
			events: []ledger.Event{started(1), finished(2, ledger.CreationFailed, "", "the push failed"), started(3), finished(4, ledger.CreationRefused, ledger.TemplateRefusedBusy, "another creation writes it")},
			listed: true, state: CreationFailed, reason: "the push failed",
		},
		"an interruption, then a busy finish": {
			events: []ledger.Event{started(1), started(2), finished(3, ledger.CreationRefused, ledger.TemplateRefusedBusy, "another creation writes it")},
			listed: true, state: CreationInterrupted,
		},
		"a refusal, then a busy finish": {
			events: []ledger.Event{started(1), finished(2, ledger.CreationRefused, "gh_signed_out", "gh is signed out"), started(3), finished(4, ledger.CreationRefused, ledger.TemplateRefusedBusy, "another creation writes it")},
			listed: true, state: CreationRefused, reason: "gh is signed out",
		},
		"a busy finish and nothing before it": {
			events: []ledger.Event{started(1), finished(2, ledger.CreationRefused, ledger.TemplateRefusedBusy, "another creation writes it")},
		},
		"a created repository, then a busy finish": {
			events: []ledger.Event{started(1), finished(2, ledger.CreationCreated, "", ""), started(3), finished(4, ledger.CreationRefused, ledger.TemplateRefusedBusy, "another creation writes it")},
		},
	} {
		item, listed, err := uncreated(row, entry, tt.events, nil, false)
		if err != nil || listed != tt.listed || (listed && (item.State != tt.state || item.Reason != tt.reason)) {
			t.Errorf("%s: listed %v as %q with reason %q (%v), want listed %v as %q with reason %q", name, listed, item.State, item.Reason, err, tt.listed, tt.state, tt.reason)
		}
	}
}

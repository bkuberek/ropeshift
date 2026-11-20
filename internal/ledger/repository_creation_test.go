package ledger

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCreationKeyIsStableAndNamesTheApprovalAndTheEntry(t *testing.T) {
	base := CreationKey("approval-1", "api")
	if again := CreationKey("approval-1", "api"); again != base {
		t.Errorf("the key of one approval and name reads %q, then %q", base, again)
	}
	if len(base) != 64 || base != lowerHex(base) {
		t.Errorf("the key %q is not 64 lowercase hex characters", base)
	}
	for name, other := range map[string]string{
		"another approval":      CreationKey("approval-2", "api"),
		"another name":          CreationKey("approval-1", "web"),
		"a name moved into it":  CreationKey("approval-1a", "pi"),
		"the pair run together": CreationKey("approval-1api", ""),
	} {
		if other == base {
			t.Errorf("%s has the key of the first pair", name)
		}
	}
}

func lowerHex(text string) string {
	out := []byte(text)
	for i, c := range out {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			out[i] = '?'
		}
	}
	return string(out)
}

// event is an event about a plan approval, of one creation event type.
func event(kind string, data any) Event {
	raw, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return Event{Type: kind, Subject: Subject{Kind: "approval", ID: "approval-1"}, Data: raw}
}

func TestACreationEventReadsAsTheOutcomeItFlattens(t *testing.T) {
	finished := CreationFinishedEvent{
		Name: "api", Key: CreationKey("approval-1", "api"), Outcome: CreationRefused, Reason: "the push was refused", RefusalCode: "remote_exists",
		CreationOutcome: CreationOutcome{
			Forge: ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: VisibilityPrivate, URL: "git@github.com:acme/api.git",
			Steps: []RemoteStep{{Step: RemoteStepPush, Outcome: RemoteStepFailed, Detail: "denied"}}, Remaining: []string{"git push"},
		},
	}
	raw, err := json.Marshal(finished)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name", "key", "outcome", "reason", "refusal_code", "forge", "owner", "forge_name", "visibility", "url", "steps", "remaining"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("a finished creation does not carry %q at its top level: %s", key, raw)
		}
	}
	var back CreationFinishedEvent
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, finished) {
		t.Errorf("a finished creation reads back as %+v (%v), want %+v", back, err, finished)
	}
}

func TestACreationManifestOutcomeKeepsItsCreation(t *testing.T) {
	record := ManifestRegistration{Repositories: []ManifestOutcome{
		{Name: "api", Path: "repos/api", Outcome: Created, Creation: &CreationOutcome{Forge: ForgeGitHub, Owner: "acme", ForgeName: "api", Makefile: "api=git@github.com:acme/api.git=repos/api"}},
		{Name: "docs", Path: "repos/docs", Outcome: Planned},
		{Name: "web", Path: "repos/web", Outcome: RegistrationRefused, Reason: "web is not registered: gh is signed out", RefusalCode: "gh_signed_out"},
	}}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var back ManifestRegistration
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, record) {
		t.Errorf("a registration with a creation reads back as %+v (%v), want %+v", back, err, record)
	}
	if !strings.Contains(string(raw), `"reason":"web is not registered: gh is signed out","refusal_code":"gh_signed_out"`) || strings.Count(string(raw), "refusal_code") != 1 {
		t.Errorf("a refused entry writes its code after its reason, and no other entry writes one: %s", raw)
	}
	if strings.Contains(string(raw), `"creation":null`) {
		t.Errorf("an entry with no creation writes one: %s", raw)
	}
}

func TestTheLatestCreationIsWhereTheEventsAboutAnEntryLeaveIt(t *testing.T) {
	key := CreationKey("approval-1", "api")
	started := CreationStartedEvent{Name: "api", Key: key, Path: "/work/api", Forge: ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: VisibilityPrivate}
	for _, tt := range []struct {
		name   string
		events []Event
		state  CreationState
		reason string
	}{
		{name: "no event", state: CreationUntried},
		{name: "a registration's event", events: []Event{{Type: EventManifestRegistered, Data: json.RawMessage(`{"repositories":[]}`)}}, state: CreationUntried},
		{name: "another entry's events", events: []Event{
			event(EventRepositoryCreationStarted, CreationStartedEvent{Name: "web", Key: CreationKey("approval-1", "web")}),
		}, state: CreationUntried},
		{name: "started and not finished", events: []Event{event(EventRepositoryCreationStarted, started)}, state: CreationStarted},
		{name: "finished created", events: []Event{
			event(EventRepositoryCreationStarted, started),
			event(EventRepositoryCreationFinished, CreationFinishedEvent{Name: "api", Key: key, Outcome: CreationCreated}),
		}, state: CreationCreated},
		{name: "finished failed", events: []Event{
			event(EventRepositoryCreationStarted, started),
			event(EventRepositoryCreationFinished, CreationFinishedEvent{Name: "api", Key: key, Outcome: CreationFailed, Reason: "push failed"}),
		}, state: CreationFailed, reason: "push failed"},
		{name: "failed, then started again", events: []Event{
			event(EventRepositoryCreationStarted, started),
			event(EventRepositoryCreationFinished, CreationFinishedEvent{Name: "api", Key: key, Outcome: CreationFailed, Reason: "push failed"}),
			event(EventRepositoryCreationStarted, started),
		}, state: CreationStarted},
		{name: "refused, then skipped", events: []Event{
			event(EventRepositoryCreationFinished, CreationFinishedEvent{Name: "api", Key: key, Outcome: CreationRefused, Reason: "not empty"}),
			event(EventRepositoryCreationSkipped, CreationSkippedEvent{Name: "api", Reason: "later"}),
		}, state: CreationSkipped, reason: "later"},
		{name: "skipped, then started", events: []Event{
			event(EventRepositoryCreationSkipped, CreationSkippedEvent{Name: "api", Reason: "later"}),
			event(EventRepositoryCreationStarted, started),
		}, state: CreationStarted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LatestCreation(tt.events, "api")
			if err != nil || got.State != tt.state || got.Reason != tt.reason {
				t.Fatalf("the latest creation is %+v (%v), want state %q and reason %q", got, err, tt.state, tt.reason)
			}
			if tt.state == CreationStarted && (got.Started == nil || got.Started.Key != key || got.Finished != nil) {
				t.Errorf("a started creation reads %+v", got)
			}
			if tt.state == CreationFailed && (got.Finished == nil || got.Finished.Reason != "push failed") {
				t.Errorf("a failed creation reads %+v", got)
			}
		})
	}
	if _, err := LatestCreation([]Event{{Type: EventRepositoryCreationStarted, Data: json.RawMessage(`{`)}}, "api"); err == nil {
		t.Error("an event that is no JSON reads as a creation")
	}
}

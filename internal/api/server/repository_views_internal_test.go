package server

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

func TestAClientReadsEachCreationOfARegistrationAsRunAndAsRecorded(t *testing.T) {
	creation := &ledger.CreationOutcome{
		Forge: ledger.ForgeGitHub, Owner: "acme", ForgeName: "api", Visibility: ledger.VisibilityPrivate, DefaultBranch: "main",
		URL: "git@github.com:acme/api.git", Makefile: "api=git@github.com:acme/api.git=repos/api",
	}
	added := []control.Added{
		{Name: "api", Manifest: "repos/api", Remote: creation.URL, Path: "/work/repos/api", Outcome: ledger.Created, Creation: creation},
		{Name: "docs", Manifest: "repos/docs", Outcome: ledger.Planned},
		{Name: "web", Manifest: "repos/web", Outcome: ledger.RegistrationRefused, RefusalCode: "gh_signed_out"},
	}
	ran := addedViews(added)
	if ran[2].RefusalCode != "gh_signed_out" || ran[0].RefusalCode != "" {
		t.Errorf("a run's views carry the refusal codes %q and %q", ran[2].RefusalCode, ran[0].RefusalCode)
	}
	if ran[0].Creation != creation || ran[1].Creation != nil {
		t.Errorf("a run's views carry the creations %+v and %+v", ran[0].Creation, ran[1].Creation)
	}
	recorded, err := recordedViews(nil, &ledger.ManifestRegistration{Repositories: []ledger.ManifestOutcome{
		{Name: "api", Path: "repos/api", Outcome: ledger.Created, Creation: creation}, {Name: "docs", Path: "repos/docs", Outcome: ledger.Planned},
		{Name: "web", Path: "repos/web", Outcome: ledger.RegistrationRefused, RefusalCode: "gh_signed_out"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Repositories[2].RefusalCode != "gh_signed_out" || recorded.Repositories[0].RefusalCode != "" {
		t.Errorf("a record's views carry the refusal codes %q and %q", recorded.Repositories[2].RefusalCode, recorded.Repositories[0].RefusalCode)
	}
	if !reflect.DeepEqual(recorded.Repositories[0].Creation, creation) || recorded.Repositories[1].Creation != nil {
		t.Errorf("a record's views carry the creations %+v and %+v", recorded.Repositories[0].Creation, recorded.Repositories[1].Creation)
	}
}

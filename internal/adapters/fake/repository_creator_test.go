package fake_test

import (
	"context"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var _ ports.RepositoryCreator = (*fake.RepositoryCreator)(nil)

func creationJob(name string) ledger.RepositoryCreation {
	return ledger.RepositoryCreation{
		Key: ledger.CreationKey("approval-1", name), Project: "harbor", Name: name, Path: "/work/harbor/repos/" + name,
		Create: ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "acme"}, ForgeName: name,
	}
}

// A creator with nothing set passes every check and writes every job, and records each job it was given.
func TestRepositoryCreatorRecordsEachJobAndAnswersWhatATestSet(t *testing.T) {
	creator := fake.NewRepositoryCreator()
	api, web := creationJob("api"), creationJob("web")

	check, err := creator.Check(t.Context(), api)
	if err != nil || check.Refusal != nil || check.URL != "" {
		t.Fatalf("an unset check answers %+v (%v)", check, err)
	}
	report, err := creator.Create(t.Context(), api)
	if err != nil || report.Outcome != ledger.TemplateOutcomeWritten {
		t.Fatalf("an unset create answers %+v (%v)", report, err)
	}

	refusal := &ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedGHSignedOut}
	creator.SetCheck("web", ledger.CreationCheck{Refusal: refusal, URL: "git@github.com:acme/web.git", Login: "octo", Branch: "trunk"})
	creator.SetReport("web", ledger.TemplateWriteReport{Outcome: ledger.TemplateOutcomeWritten, Commit: "1a2b3c4"})
	if check, err = creator.Check(t.Context(), web); err != nil || check.Refusal != refusal || check.URL != "git@github.com:acme/web.git" || check.Login != "octo" || check.Branch != "trunk" {
		t.Errorf("a set check answers %+v (%v)", check, err)
	}
	if report, err = creator.Create(t.Context(), web); err != nil || report.Commit != "1a2b3c4" {
		t.Errorf("a set report answers %+v (%v)", report, err)
	}

	if got := creator.Checked(); len(got) != 2 || got[0].Name != "api" || got[1] != web {
		t.Errorf("the checks recorded %+v", got)
	}
	if got := creator.Created(); len(got) != 2 || got[0].Name != "api" || got[1] != web {
		t.Errorf("the creations recorded %+v", got)
	}
}

// A test fails a check or a create for one name, and the other names are not touched.
func TestRepositoryCreatorFailsOneNameAsTold(t *testing.T) {
	creator := fake.NewRepositoryCreator()
	gone := errors.New("GitHub did not answer within 30 seconds; read the preview again")
	creator.FailCheck("web", gone)
	creator.FailCreate("web", errors.New("the journal cannot be written"))

	if _, err := creator.Check(t.Context(), creationJob("web")); !errors.Is(err, gone) {
		t.Errorf("a failed check answers %v", err)
	}
	if _, err := creator.Create(t.Context(), creationJob("web")); err == nil {
		t.Error("a failed create answered no error")
	}
	if _, err := creator.Check(t.Context(), creationJob("api")); err != nil {
		t.Errorf("a check of another name failed: %v", err)
	}
	if _, err := creator.Create(t.Context(), creationJob("api")); err != nil {
		t.Errorf("a create of another name failed: %v", err)
	}
	if got := creator.Created(); len(got) != 2 {
		t.Errorf("a failed create is still a job the creator was given: %+v", got)
	}
}

// A job whose context ended is not run.
func TestRepositoryCreatorStopsOnAnEndedContext(t *testing.T) {
	creator := fake.NewRepositoryCreator()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := creator.Check(ctx, creationJob("api")); err == nil {
		t.Error("a check ran after its context ended")
	}
	if _, err := creator.Create(ctx, creationJob("api")); err == nil {
		t.Error("a create ran after its context ended")
	}
}

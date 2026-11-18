package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/test/apitest"
)

// A repository's CI keys are edited through the repository change a member already makes. The view reads every
// key with the defaults a member left alone, a value outside its range is refused with the key named, and the
// record keeps nothing of a refused change.
func TestARepositoryChangeEditsTheCIKeys(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	ctx := t.Context()
	before, err := d.Client.Repository(ctx, aRepository)
	if err != nil || before.CI.Custom || before.CI.Effective().SteadyInterval != "5m" {
		t.Fatalf("a repository nobody edited reads %+v: %v", before.CI, err)
	}
	view, err := d.Client.SetRepository(ctx, aRepository, api.RepositoryChange{CI: map[string]string{"ci_steady_interval": "2m", "ci_auto_rerun": "0"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := view.CI.Effective(); got.SteadyInterval != "2m" || got.AutoRerun != 0 || got.NoHistoryP85 != "20m" || !view.CI.Custom {
		t.Errorf("the view reads %+v", view.CI)
	}
	for name, change := range map[string]map[string]string{
		"a value outside its range": {"ci_steady_interval": "10s"},
		"a key nobody offers":       {"ci_made_up": "1"},
		"one bad key among good":    {"ci_min_interval": "1m", "ci_auto_rerun": "9"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.Client.SetRepository(ctx, aRepository, api.RepositoryChange{CI: change})
			refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		})
	}
	if after, err := d.Client.Repository(ctx, aRepository); err != nil || after.CI != view.CI {
		t.Errorf("a refused change left %+v, was %+v: %v", after.CI, view.CI, err)
	}
}

// For a delegate, a change to a repository's CI keys is a card only a member confirms, whatever the delegate policy
// exempts, and the change waits for it.
func TestADelegatesCIChangeIsACard(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	ctx := t.Context()
	_, err := delegate.SetRepository(ctx, aRepository, api.RepositoryChange{CI: map[string]string{"ci_steady_interval": "2m"}})
	card := preparedCard(t, err)
	if card.Subject != "repository:"+aRepository || card.Summary != "Change forge settings: web" {
		t.Fatalf("the card binds %s titled %q", card.Subject, card.Summary)
	}
	if record := repositoryRecord(t, d, aRepository); record.CI.Custom {
		t.Fatalf("the change ran before a member confirmed it: %+v", record.CI)
	}
	confirmed, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	if err != nil || confirmed.State != authority.ApprovalUsed {
		t.Fatalf("the confirmation: %+v %v", confirmed, err)
	}
	if record := repositoryRecord(t, d, aRepository); record.CI.Effective().SteadyInterval != "2m" {
		t.Errorf("the confirmed change left %+v", record.CI)
	}
}

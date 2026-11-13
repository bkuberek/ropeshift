package server_test

import (
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestARepositoryNameNeverReadsAsAnotherIdentity refuses the name that would let a call aimed at one
// repository's identity resolve to another repository.
func TestARepositoryNameNeverReadsAsAnotherIdentity(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	ctx := t.Context()
	for _, name := range []string{aRepository, aRepository[:7], aRepository[:20]} {
		_, err := d.Client.SetRepository(ctx, anotherRepository, api.RepositoryChange{Name: &name})
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
	short, own := aRepository[:6], anotherRepository[:12]
	if _, err := d.Client.SetRepository(ctx, anotherRepository, api.RepositoryChange{Name: &short}); err != nil {
		t.Errorf("a name shorter than a prefix that names a repository: %v", err)
	}
	if _, err := d.Client.SetRepository(ctx, anotherRepository, api.RepositoryChange{Name: &own}); err != nil {
		t.Errorf("a name that is a prefix of the repository's own identity: %v", err)
	}
}

// forceName gives a record a name past every check, as a ledger written before the checks could hold it.
func forceName(t *testing.T, d *apitest.Daemon, id, name string) {
	t.Helper()
	err := d.Store.Write(t.Context(), apitest.Scope, "test.rename", func(tx ports.WriteTx) error {
		record, err := tx.Repositories().Repository(id)
		if err != nil {
			return err
		}
		record.Name = name
		if err := tx.Repositories().Settings(record); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "test.renamed", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: id}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestANameShapedLikeAnIdentityIsRefusedBeforeThatRepositoryExists refuses the name a delegate could
// compute from a clone's first commit before that repository is registered.
func TestANameShapedLikeAnIdentityIsRefusedBeforeThatRepositoryExists(t *testing.T) {
	d := apitest.Start(t)
	detected(t, d, anotherRepository, "main")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	for _, name := range []string{aRepository, strings.ToUpper(aRepository), aRepository + aRepository[:24]} {
		_, err := delegate.SetRepository(t.Context(), anotherRepository, api.RepositoryChange{Name: &name})
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
		_, err = d.Client.SetRepository(t.Context(), anotherRepository, api.RepositoryChange{Name: &name})
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
}

// TestAFullIdentityAlwaysNamesItsOwnRecord holds a card, and a member's own call, to the record whose
// identity a path spells, even where another record's name spells that identity.
func TestAFullIdentityAlwaysNamesItsOwnRecord(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	forceName(t, d, anotherRepository, aRepository)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	ctx := t.Context()
	raised := ledger.ReviewApproves
	_, err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).SetRepository(ctx, aRepository, api.RepositoryChange{MergeAuthority: &raised})
	card := preparedCard(t, err)
	if card.Subject != "repository:"+aRepository || card.Summary != "Raise merge authority: web" || !strings.HasSuffix(cardDetail(t, card).Path, "/repositories/"+aRepository) {
		t.Fatalf("the card binds %s on %s titled %q", card.Subject, cardDetail(t, card).Path, card.Summary)
	}
	confirmed, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	if err != nil || confirmed.State != authority.ApprovalUsed {
		t.Fatalf("the confirmation: %+v %v", confirmed, err)
	}
	if record := repositoryRecord(t, d, aRepository); record.Level() != ledger.ReviewApproves {
		t.Errorf("the repository the card names is at %s", record.Level())
	}
	if record := repositoryRecord(t, d, anotherRepository); record.Level() != ledger.PersonMerges {
		t.Errorf("the repository whose name spells its identity is at %s", record.Level())
	}
	base := "trunk"
	if _, err := d.Client.SetRepository(ctx, aRepository, api.RepositoryChange{Base: &base}); err != nil {
		t.Fatal(err)
	}
	if record := repositoryRecord(t, d, aRepository); record.Base() != "trunk" {
		t.Errorf("a member's call by the full identity changed another record: base %q", record.Base())
	}
}

// TestRegisteringARepositoryRenamesANameThatSpellsItsIdentity keeps a new record's identity its own: a
// record whose name spells that identity, or a prefix that would name it, is named again as automatic
// naming names a record, with the change on record.
func TestRegisteringARepositoryRenamesANameThatSpellsItsIdentity(t *testing.T) {
	for _, shadow := range []string{aRepository, aRepository[:9]} {
		d := apitest.Start(t)
		detected(t, d, anotherRepository, "main")
		forceName(t, d, anotherRepository, shadow)
		detected(t, d, aRepository, "main")
		renamed := repositoryRecord(t, d, anotherRepository)
		if renamed.Name == shadow || ledger.ShadowsIdentity(renamed.Name, aRepository) || renamed.Name == "" {
			t.Errorf("%s: the shadowing record is named %q", shadow, renamed.Name)
		}
		if made := repositoryRecord(t, d, aRepository); made.Name == "" || made.Name == renamed.Name {
			t.Errorf("%s: the new record is named %q beside %q", shadow, made.Name, renamed.Name)
		}
		var changed bool
		err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
			events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectRepository, ID: anotherRepository})
			for _, event := range events {
				changed = changed || event.Type == ledger.EventRepositoryChanged
			}
			return err
		})
		if err != nil || !changed {
			t.Errorf("%s: the rename left no event: %v", shadow, err)
		}
	}
}

package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

const anotherRepository = "9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a291807"

// twoRepositories registers two repositories and names them, so a test can tell them apart.
func twoRepositories(t *testing.T, d *apitest.Daemon) {
	t.Helper()
	detected(t, d, aRepository, "main")
	detected(t, d, anotherRepository, "main")
	for id, name := range map[string]string{aRepository: "web", anotherRepository: "api"} {
		if _, err := d.Client.SetRepository(t.Context(), id, api.RepositoryChange{Name: &name}); err != nil {
			t.Fatal(err)
		}
	}
}

func repositoryRecord(t *testing.T, d *apitest.Daemon, id string) ledger.Repository {
	t.Helper()
	var record ledger.Repository
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Repositories().Repository(id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// TestARepositoryCardBindsTheRecordItsNameOrPrefixNames holds a card to the one record its call names, by
// that record's identity, whatever the call spelled: a name, a prefix, or the identity itself.
func TestARepositoryCardBindsTheRecordItsNameOrPrefixNames(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, d.Socket, created.Token, apitest.ProjectName)
	ctx := t.Context()
	raised := ledger.ReviewApproves
	for _, spelled := range []string{"web", aRepository[:8], aRepository} {
		_, err := delegate.SetRepository(ctx, spelled, api.RepositoryChange{MergeAuthority: &raised})
		card := preparedCard(t, err)
		detail := cardDetail(t, card)
		if card.Subject != "repository:"+aRepository || !strings.HasSuffix(detail.Path, "/repositories/"+aRepository) || card.Summary != "Raise merge authority: web" {
			t.Errorf("%s: a card of %s on %s titled %q", spelled, card.Subject, detail.Path, card.Summary)
		}
	}
	base := "trunk"
	if _, err := delegate.SetRepository(ctx, "web", api.RepositoryChange{Base: &base}); err != nil {
		t.Fatalf("an operational change by name: %v", err)
	}
	if record := repositoryRecord(t, d, aRepository); record.Base() != "trunk" {
		t.Errorf("the change by name left base %q", record.Base())
	}
	err := delegate.Do(ctx, http.MethodPost, apitest.Route("/v1/repositories/web/remove"), struct{}{}, nil)
	removal := preparedCard(t, err)
	if removal.Subject != "repository:"+aRepository || !strings.HasSuffix(cardDetail(t, removal).Path, "/repositories/"+aRepository+"/remove") {
		t.Errorf("a removal by name binds %s on %s", removal.Subject, cardDetail(t, removal).Path)
	}
	other := "release"
	if _, err := d.Client.SetRepository(ctx, aRepository, api.RepositoryChange{Base: &other}); err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.DecideApproval(ctx, removal.ID, api.DecideApproval{Digest: removal.SubjectDigest, Allow: true})
	refusedWith(t, err, http.StatusConflict, api.CodeStaleDigest)
}

// TestACardReplaysOnlyAgainstItsRecord holds the replay to the record the card binds even where a name
// held before names were checked reads as that record's identity: the card goes void, and neither record
// changes.
func TestACardReplaysOnlyAgainstItsRecord(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	ctx := t.Context()
	raised := ledger.ReviewApproves
	_, err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).SetRepository(ctx, aRepository, api.RepositoryChange{MergeAuthority: &raised})
	card := preparedCard(t, err)
	// A name that reads as the other record's identity, as a ledger could hold one from before names were
	// checked. It is an event about the record it renames, so the card's own digest does not move.
	err = d.Store.Write(ctx, apitest.Scope, "test.rename", func(tx ports.WriteTx) error {
		record, err := tx.Repositories().Repository(anotherRepository)
		if err != nil {
			return err
		}
		record.Name = aRepository
		if err := tx.Repositories().Settings(record); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "test.renamed", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}, Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: anotherRepository}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	refusedWith(t, err, http.StatusConflict, api.CodeStaleDigest)
	for _, id := range []string{aRepository, anotherRepository} {
		if record := repositoryRecord(t, d, id); record.Level() != ledger.PersonMerges {
			t.Errorf("repository %s is at %s", id, record.Level())
		}
	}
	row, err := d.Client.Approval(ctx, card.ID)
	if err != nil || row.State != authority.ApprovalVoid {
		t.Errorf("the card is %+v: %v", row, err)
	}
}

// TestAConfirmedCardRunsWhenItsClientGoesAway holds a confirmation to its end: the card was approved, so
// its call runs and its outcome is kept even when the member's client disconnects before the answer.
func TestAConfirmedCardRunsWhenItsClientGoesAway(t *testing.T) {
	d := apitest.Start(t)
	createReady(t, d, "w1")
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	_, err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).CancelWork(t.Context(), "w1", api.CancelWork{Reason: "Dropped."})
	card := preparedCard(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	server.WhenConfirmed(t, func(served context.Context) {
		cancel()
		select {
		case <-served.Done():
		case <-time.After(5 * time.Second):
		}
	})
	if _, err := d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true}); err == nil {
		t.Fatal("the confirmation answered a client that went away")
	}
	deadline := time.Now().Add(10 * time.Second)
	var row authority.Approval
	for time.Now().Before(deadline) {
		if row, err = d.Client.Approval(t.Context(), card.ID); err == nil && row.State != authority.ApprovalApproved {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if row.State != authority.ApprovalUsed {
		t.Fatalf("the card is %s: %s", row.State, row.Reason)
	}
	if order, err := d.Client.WorkOrder(t.Context(), "w1"); err != nil || order.State != workorders.Canceled {
		t.Errorf("the confirmed call left %+v: %v", order, err)
	}
}

// TestASubjectThatMovesAfterTheConfirmationVoidsTheCard binds the card again just before its call runs: a
// change to its record between the member's confirmation and the call voids it, and nothing runs.
func TestASubjectThatMovesAfterTheConfirmationVoidsTheCard(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	ctx := t.Context()
	raised := ledger.ReviewApproves
	_, err := asDelegate(t, d.Socket, created.Token, apitest.ProjectName).SetRepository(ctx, "web", api.RepositoryChange{MergeAuthority: &raised})
	card := preparedCard(t, err)
	server.WhenConfirmed(t, func(context.Context) {
		base := "trunk"
		if _, err := d.Client.SetRepository(ctx, aRepository, api.RepositoryChange{Base: &base}); err != nil {
			t.Error(err)
		}
	})
	_, err = d.Client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true})
	refusedWith(t, err, http.StatusConflict, api.CodeStaleDigest)
	if record := repositoryRecord(t, d, aRepository); record.Level() != ledger.PersonMerges {
		t.Errorf("the repository is at %s", record.Level())
	}
	if row, err := d.Client.Approval(ctx, card.ID); err != nil || row.State != authority.ApprovalVoid {
		t.Errorf("the card is %+v: %v", row, err)
	}
}

// TestRevokingADelegateVoidsItsOpenCards ends what a revoked delegate prepared: revoking it, or signing out
// of every session, voids its open cards, and a card of a delegate that no longer acts cannot be confirmed.
func TestRevokingADelegateVoidsItsOpenCards(t *testing.T) {
	d := apitest.Start(t)
	ctx := t.Context()
	createReady(t, d, "w1")
	createReady(t, d, "w2")
	createReady(t, d, "w3")
	cardOf := func(token, work string) authority.Approval {
		t.Helper()
		_, err := asDelegate(t, d.Socket, token, apitest.ProjectName).CancelWork(ctx, work, api.CancelWork{Reason: "Dropped."})
		return preparedCard(t, err)
	}
	revoked, signedOut, behind := aDelegate(t, d.Client.Client, apitest.ProjectName), aDelegate(t, d.Client.Client, apitest.ProjectName), aDelegate(t, d.Client.Client, apitest.ProjectName)
	first, second, third := cardOf(revoked.Token, "w1"), cardOf(signedOut.Token, "w2"), cardOf(behind.Token, "w3")
	if _, err := d.Client.RevokeDelegate(ctx, revoked.ID); err != nil {
		t.Fatal(err)
	}
	if row, err := d.Client.Approval(ctx, first.ID); err != nil || row.State != authority.ApprovalVoid {
		t.Errorf("a revoked delegate's card is %+v: %v", row, err)
	}
	// A credential revoked where no card is voided, as a revocation the sweep did not reach.
	if _, err := control.NewDelegates(d.Store).Revoke(ctx, apitest.Scope, behind.ID, ledger.Actor{Kind: ledger.ActorMember, ID: memberOf(t, d)}); err != nil {
		t.Fatal(err)
	}
	_, err := d.Client.DecideApproval(ctx, third.ID, api.DecideApproval{Digest: third.SubjectDigest, Allow: true})
	refusedWith(t, err, http.StatusConflict, api.CodeRequestClosed)
	if order, err := d.Client.WorkOrder(ctx, "w3"); err != nil || order.State == workorders.Canceled {
		t.Errorf("a revoked delegate's card ran: %+v %v", order, err)
	}
	second2 := api.NewUnixClient(d.Socket)
	defer second2.Close()
	if _, err := apitest.SignIn(ctx, second2); err != nil {
		t.Fatal(err)
	}
	if _, err := second2.SignOutAll(ctx); err != nil {
		t.Fatal(err)
	}
	var state authority.ApprovalState
	err = d.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		row, err := tx.Approvals().Approval(second.ID)
		state = row.State
		return err
	})
	if err != nil || state != authority.ApprovalVoid {
		t.Errorf("a card of a delegate signing out everywhere revoked is %s: %v", state, err)
	}
}

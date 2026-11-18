package server_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// A repository's view, and the list of repositories, carry the workspace a worker reported for it as one that does not
// ignore its folder for lane worktrees, and leave it out for a repository whose report names none.
func TestARepositoryViewCarriesAWorkspaceThatDoesNotIgnoreItsWorktrees(t *testing.T) {
	d := apitest.Start(t)
	twoRepositories(t, d)
	ctx := t.Context()
	first, err := d.Client.Repository(ctx, aRepository)
	if err != nil {
		t.Fatal(err)
	}
	var other string
	list, err := d.Client.Repositories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range list.Repositories {
		if view.ID != first.ID {
			other = view.ID
		}
	}
	err = d.Store.Write(ctx, apitest.Scope, "test.forge_health", func(tx ports.WriteTx) error {
		for _, health := range []ledger.ForgeHealth{
			{Worker: apitest.WorldWorker, Repository: first.ID, State: ledger.ForgeHealthy, UnignoredWorkspace: "/work/ws", CheckedAt: 1},
			{Worker: apitest.WorldWorker, Repository: other, State: ledger.ForgeHealthy, CheckedAt: 1},
		} {
			if _, err := tx.Forge().SaveHealth(health); err != nil {
				return err
			}
			_, err := tx.Append(ledger.EventDraft{
				Type: ledger.EventForgeHealth, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: apitest.WorldWorker}, Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: health.Repository},
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := d.Client.Repository(ctx, aRepository)
	if err != nil || len(view.Forge) != 1 || view.Forge[0].UnignoredWorkspace != "/work/ws" {
		t.Errorf("the view carries %+v: %v", view.Forge, err)
	}
	if list, err = d.Client.Repositories(ctx); err != nil {
		t.Fatal(err)
	}
	for _, listed := range list.Repositories {
		want := ""
		if listed.ID == first.ID {
			want = "/work/ws"
		}
		if len(listed.Forge) != 1 || listed.Forge[0].UnignoredWorkspace != want {
			t.Errorf("repository %s lists %+v, want the unignored workspace %q", listed.ID, listed.Forge, want)
		}
	}
}

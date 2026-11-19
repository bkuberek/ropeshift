package sqlstore_test

import (
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// A template write keeps the name it was asked for in the installation database, and reads it back with its project, so
// the worker is offered the one and the member reads the other.
func TestTheInstallationKeepsATemplateWritesNameBesideItsProject(t *testing.T) {
	installation, err := sqlite.OpenInstallation(filepath.Join(t.TempDir(), sqlite.InstallationFileName), sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = installation.Close() })
	if err := installation.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	actor := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	record := ledger.TemplateWriteRecord{
		ID: "request-1", Worker: "worker-1", RequestedBy: actor.ID, RequestedAt: 1, ExpiresAt: 1 << 60, State: ledger.TemplateWriteOffered,
		Request: ledger.TemplateWriteRequest{Shape: "repository", Path: "/srv/harbor", Name: "harbor-api", Project: "harbor"},
	}
	err = installation.Write(t.Context(), "test.template_write", func(tx ports.SharedTx) error {
		if err := tx.TemplateWrites().Insert(record); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.inserted", Actor: actor, Subject: ledger.Subject{Kind: "template_write", ID: record.ID}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var found ledger.TemplateWriteRecord
	var offered []ledger.TemplateWriteRecord
	err = installation.Read(t.Context(), func(tx ports.SharedReadTx) error {
		var err error
		if found, err = tx.TemplateWrites().Find(record.ID); err != nil {
			return err
		}
		offered, err = tx.TemplateWrites().Offered("worker-1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if found.Request.Name != "harbor-api" || found.Request.Project != "harbor" {
		t.Errorf("the request reads name %q and project %q", found.Request.Name, found.Request.Project)
	}
	if len(offered) != 1 || offered[0].Desired().Name != "harbor-api" {
		t.Errorf("the worker is offered %+v", offered)
	}
}

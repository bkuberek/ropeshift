package exit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/localauth"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// legacyPassphrase is the member's passphrase in a ledger a build with no projects wrote.
const legacyPassphrase = "the passphrase from before the upgrade"

// writeLegacyLedger writes the ledger file a build with one ledger left: its workspace, a member with a
// passphrase, and a work order, with no installation database beside it.
func writeLegacyLedger(t *testing.T, file string) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(file, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	scope := ledger.WorkspaceID("default")
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "solo"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: scope, Name: "Solo"}, system); err != nil {
		t.Fatal(err)
	}
	if err := localauth.New(store, localauth.DefaultParams).Enroll(ctx, scope, ports.Proof{Passphrase: legacyPassphrase}, system); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewWorkOrders(store).Create(ctx, scope, workorders.WorkOrder{
		ID: "w1", Kind: workorders.KindChange, Title: "Kept by the upgrade", MaxAttempts: 3,
	}, ledger.Actor{Kind: ledger.ActorMember, ID: localauth.Member}); err != nil {
		t.Fatal(err)
	}
}

// TestAnInstallationWithOneLedgerUpgradesAtItsFirstStart is the upgrade through the binary (ADR-0021):
// a daemon started on a ledger with no installation database beside it serves that ledger as its
// default project, from the same file, and its member signs in with the passphrase the ledger held,
// which now lives in the installation database.
func TestAnInstallationWithOneLedgerUpgradesAtItsFirstStart(t *testing.T) {
	in := newInstall(t)
	in.member = false
	state := in.paths().StateDir
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(state, api.LedgerFileName)
	writeLegacyLedger(t, file)
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	d := in.start()
	in.waitReady(d)

	client := api.NewUnixClient(in.paths().ControlSocket())
	defer client.Close()
	ctx := t.Context()
	var refused *api.Error
	if err := client.SetPassphrase(ctx, api.SetPassphrase{Passphrase: "a passphrase nobody chose"}); !errors.As(err, &refused) || refused.Detail.Code != api.CodeSessionRequired {
		t.Fatalf("setting a passphrase with no session on an upgraded installation: %v", err)
	}
	session, err := client.SignIn(ctx, api.SignIn{Passphrase: legacyPassphrase})
	if err != nil {
		t.Fatalf("the member's passphrase from before the upgrade: %v", err)
	}
	in.session = session.Token
	if got := in.show("w1").Title; got != "Kept by the upgrade" {
		t.Errorf("the default project holds %q", got)
	}
	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("the ledger file moved or was replaced")
	}
	if _, err := os.Stat(filepath.Join(state, sqlite.InstallationFileName)); err != nil {
		t.Errorf("no installation database beside the ledger: %v", err)
	}
	d.kill()

	check, err := sqlite.OpenDB(file)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var members int64
	if err := check.QueryRow("SELECT count(*) FROM members").Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Errorf("the ledger still holds %d members", members)
	}
}

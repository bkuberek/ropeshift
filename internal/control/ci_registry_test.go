package control_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// The map from a repository's name to the variable that holds its key is kept in memory, so a delivery never reads
// every project's store: it is read again when a repository record changes, which a heartbeat sees as a project's
// revision moving, and when it has grown old.

type registryRig struct {
	registry *control.CIRegistry
	loads    atomic.Int64
	now      time.Time
	held     control.CIRegistrations
}

func newRegistryRig(t *testing.T) *registryRig {
	t.Helper()
	r := &registryRig{now: time.UnixMilli(1_700_000_000_000)}
	r.held = control.CIRegistrations{
		Repositories: map[string][]control.CIRegistration{
			control.CIKey("github.com", "acme", "widgets"): {{Project: "pa", Binding: hintedRepo, SecretEnv: "ROPESHIFT_HOOK_W"}},
		},
		Revisions: map[string]int64{"pa": 5, "pb": 7},
	}
	r.registry = control.NewCIRegistry(func(context.Context) (control.CIRegistrations, error) {
		r.loads.Add(1)
		return r.held, nil
	}, func() time.Time { return r.now })
	return r
}

func (r *registryRig) lookup(t *testing.T, host, owner, repo string) []control.CIRegistration {
	t.Helper()
	found, err := r.registry.CIRepository(t.Context(), host, owner, repo)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestTheRegistryIsReadOnceForAnyNumberOfDeliveries(t *testing.T) {
	t.Parallel()
	r := newRegistryRig(t)
	for range 50 {
		if found := r.lookup(t, "github.com", "acme", "widgets"); len(found) != 1 || found[0].SecretEnv != "ROPESHIFT_HOOK_W" {
			t.Fatalf("a registered repository is found as %+v", found)
		}
		if found := r.lookup(t, "GitHub.com", "ACME", "Widgets"); len(found) != 1 {
			t.Fatalf("a name in another case is found as %+v", found)
		}
		if found := r.lookup(t, "github.com", "acme", "nobody-has-this"); len(found) != 0 {
			t.Fatalf("a repository nobody registered is found as %+v", found)
		}
	}
	if loads := r.loads.Load(); loads != 1 {
		t.Errorf("150 lookups read the stores %d times, want 1", loads)
	}
}

func TestTheRegistryIsReadAgainWhenItHasGrownOld(t *testing.T) {
	t.Parallel()
	r := newRegistryRig(t)
	r.lookup(t, "github.com", "acme", "widgets")
	r.now = r.now.Add(20 * time.Second)
	r.lookup(t, "github.com", "acme", "widgets")
	if loads := r.loads.Load(); loads != 1 {
		t.Fatalf("a lookup 20 seconds later read the stores again: %d reads", loads)
	}
	r.now = r.now.Add(5 * time.Minute)
	r.held.Repositories = map[string][]control.CIRegistration{}
	if found := r.lookup(t, "github.com", "acme", "widgets"); len(found) != 0 || r.loads.Load() != 2 {
		t.Errorf("a lookup after five minutes found %+v after %d reads", found, r.loads.Load())
	}
}

func TestTheRegistryIsReadAgainWhenAProjectsRevisionMoves(t *testing.T) {
	t.Parallel()
	r := newRegistryRig(t)
	r.lookup(t, "github.com", "acme", "widgets")
	// The heartbeat's revisions say what each project stands at, and the installation's own, which is not a project's.
	r.registry.Observe(map[string]int64{ledger.RevisionInstallation: 99, "pa": 5, "pb": 7})
	r.lookup(t, "github.com", "acme", "widgets")
	if loads := r.loads.Load(); loads != 1 {
		t.Fatalf("revisions that did not move read the stores again: %d reads", loads)
	}
	r.registry.Observe(map[string]int64{ledger.RevisionInstallation: 99, "pa": 6, "pb": 7})
	r.held.Repositories = map[string][]control.CIRegistration{}
	if found := r.lookup(t, "github.com", "acme", "widgets"); len(found) != 0 || r.loads.Load() != 2 {
		t.Fatalf("after a revision moved, a lookup found %+v after %d reads", found, r.loads.Load())
	}
	// A project that came or went changes the set the registry was read from.
	r.held.Revisions = map[string]int64{"pa": 6, "pb": 7}
	r.registry.Observe(map[string]int64{"pa": 6, "pb": 7, "pc": 1})
	r.lookup(t, "github.com", "acme", "widgets")
	if loads := r.loads.Load(); loads != 3 {
		t.Errorf("a new project did not read the stores again: %d reads", loads)
	}
}

// Through a plane over two real projects: a repository record that changes is seen by the next delivery once a
// heartbeat has seen its project's revision move, and not before, so a delivery reads no store.
func TestAChangedRepositoryRecordReachesDeliveriesAfterAHeartbeat(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	find := func() []control.CIRegistration {
		t.Helper()
		found, err := r.plane.CIRepository(t.Context(), "github.com", "acme", "widgets")
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	if found := find(); len(found) != 0 {
		t.Fatalf("an installation with no repository finds %+v", found)
	}
	err := r.stores["pa"].Write(t.Context(), "pa", "test.register", func(tx ports.WriteTx) error {
		record := ledger.NewRepository("repository-1")
		record.ForgeBinding = "github.com/acme/widgets"
		settings, err := ledger.CISettings{}.Set("ci_webhook_secret_env", "ROPESHIFT_HOOK_WIDGETS")
		if err != nil {
			return err
		}
		record.CI = settings
		if _, err := tx.Repositories().Insert(record); err != nil {
			return err
		}
		if err := tx.Repositories().Settings(record); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryRegistered, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "test"},
			Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: "repository-1"}, Data: []byte(`{}`),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if found := find(); len(found) != 0 {
		t.Fatalf("a lookup before any heartbeat read the stores again and found %+v", found)
	}
	beat := ledger.Heartbeat{Auth: r.auth, Epoch: r.epoch, Protocol: ledger.ProtocolVersion, Nonce: "n"}
	if _, err := r.plane.Heartbeat(t.Context(), beat); err != nil {
		t.Fatal(err)
	}
	found := find()
	if len(found) != 1 || found[0].SecretEnv != "ROPESHIFT_HOOK_WIDGETS" || found[0].Project != "pa" {
		t.Errorf("after a heartbeat the lookup finds %+v", found)
	}
}

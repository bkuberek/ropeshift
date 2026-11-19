package entry

import (
	"context"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/ghcli"
	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// The worker a solo daemon runs reads a checkout with this machine's git, the forge with the person's `gh`, and finds
// each checkout as its lanes do.
func TestTheSoloWorkerRunsThePracticesCheckThroughGitAndGh(t *testing.T) {
	var asked [2]string
	runtime := soloPractices("/state", func(_ context.Context, project, id string) (string, error) {
		asked = [2]string{project, id}
		return "/clone", nil
	})
	if _, ok := runtime.Sources.Snapshots.(practicegit.Snapshots); !ok {
		t.Errorf("the snapshots are read through %T", runtime.Sources.Snapshots)
	}
	if _, ok := runtime.Sources.Secrets.(practicegit.SecretScanner); !ok {
		t.Errorf("the secrets are scanned through %T", runtime.Sources.Secrets)
	}
	if _, ok := runtime.Sources.Forge.(ghcli.Forge); !ok {
		t.Errorf("the forge is read through %T", runtime.Sources.Forge)
	}
	if runtime.Sources.Clock == nil || runtime.StateDir != "/state" {
		t.Errorf("the runtime is %+v", runtime)
	}
	if dir, err := runtime.Repository(t.Context(), "project-1", "repo-1"); err != nil || dir != "/clone" || asked != [2]string{"project-1", "repo-1"} {
		t.Errorf("the checkout is %q (%v) for %v", dir, err, asked)
	}
}

// A request no worker reported by its expiry is stored as expired by the tick the daemon runs, which a report that
// comes later does not undo.
func TestTheDaemonExpiresPracticesRequests(t *testing.T) {
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: soloScope}, tickActor("solo")); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	err = store.Write(t.Context(), soloScope, "test.practices", func(tx ports.WriteTx) error {
		if err := tx.Practices().Insert(ledger.PracticesRequest{
			ID: "req-1", Repository: "repo-1", Worker: "worker-1", Cause: ledger.PracticesMember, RequestedBy: "member-1",
			RequestedAt: now - 5000, ExpiresAt: now - 1000, State: ledger.PracticesOffered,
		}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.inserted", Actor: tickActor("solo"), Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: "req-1"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ticks := soloTicks(registryOf(store, soloScope), soloScope, soloOptions{wakeTick: time.Second}, fake.NewCheckouts(), questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "practices" })
	if at < 0 {
		t.Fatalf("ticks %+v", ticks)
	}
	if err := ticks[at].Run(t.Context()); err != nil {
		t.Fatalf("the tick: %v", err)
	}
	err = store.Read(t.Context(), soloScope, func(tx ports.ReadTx) error {
		request, err := tx.Practices().Request("req-1")
		if err != nil || request.State != ledger.PracticesExpired {
			t.Errorf("the request is %+v (%v), want expired", request, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

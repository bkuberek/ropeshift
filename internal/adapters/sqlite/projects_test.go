package sqlite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

var member = ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}

func openState(t *testing.T, dir string) *sqlite.State {
	t.Helper()
	state, err := sqlite.OpenState(t.Context(), dir, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state
}

func createProject(t *testing.T, state *sqlite.State, id ledger.WorkspaceID, name string) (ledger.Project, *sqlstore.Store) {
	t.Helper()
	project, store, err := state.CreateProject(t.Context(), ledger.Project{ID: id, Name: name}, member)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return project, store
}

func newWorkOrder(t *testing.T, store ports.Store, scope ledger.WorkspaceID, id, title string) {
	t.Helper()
	_, err := control.NewWorkOrders(store).Create(t.Context(), scope, workorders.WorkOrder{
		ID: id, Kind: workorders.KindChange, Title: title, MaxAttempts: 3,
	}, member)
	if err != nil {
		t.Fatal(err)
	}
}

// countRows counts the rows of a table in a SQLite file, around every Store.
func countRows(t *testing.T, file, table string) int64 {
	t.Helper()
	db, err := sqlite.OpenDB(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEachProjectKeepsItsLedgerInAFileOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	if alpha.Database != "projects/pa.db" || beta.Database != "projects/pb.db" {
		t.Fatalf("the registry holds %q and %q", alpha.Database, beta.Database)
	}
	for _, p := range []ledger.Project{alpha, beta} {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p.Database)))
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("%s has no file of its own: %v", p.Name, err)
		}
	}
	if info, err := os.Stat(filepath.Join(dir, sqlite.ProjectsDir)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the projects folder: %v, %v", info, err)
	}
	ctx := t.Context()
	newWorkOrder(t, alphaStore, alpha.ID, "wo-alpha", "Only in alpha")
	if _, err := control.NewQuestions(betaStore, questions.BatchPolicy{}).RecordQuestion(ctx, beta.ID,
		questions.Question{ID: "q-beta", Text: "What is the retry budget?"}, member); err != nil {
		t.Fatal(err)
	}

	err := betaStore.Read(ctx, beta.ID, func(tx ports.ReadTx) error {
		if _, err := tx.WorkOrders().Get("wo-alpha"); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("beta reads alpha's work order: %v", err)
		}
		events, err := tx.Events(0, 1000)
		for _, e := range events {
			if e.Subject.ID == "wo-alpha" {
				t.Errorf("beta's log holds %+v", e)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = alphaStore.Read(ctx, alpha.ID, func(tx ports.ReadTx) error {
		hits, err := tx.Search(ports.SearchQuery{Text: "retry budget", Kinds: questions.OpenKinds(), Limit: 10})
		if len(hits) != 0 {
			t.Errorf("alpha's search finds beta's question: %+v", hits)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := betaStore.Read(ctx, alpha.ID, func(ports.ReadTx) error { return nil }); !errors.Is(err, ledger.ErrUnknownWorkspace) {
		t.Errorf("beta's ledger opens alpha's workspace: %v", err)
	}
	if n := countRows(t, filepath.Join(dir, "projects", "pb.db"), "work_orders"); n != 0 {
		t.Errorf("beta's file holds %d work orders", n)
	}
	if n := countRows(t, filepath.Join(dir, "projects", "pa.db"), "questions"); n != 0 {
		t.Errorf("alpha's file holds %d questions", n)
	}

	reopened, err := state.OpenProject(ctx, alpha)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	err = reopened.Read(ctx, alpha.ID, func(tx ports.ReadTx) error {
		_, err := tx.WorkOrders().Get("wo-alpha")
		return err
	})
	if err != nil {
		t.Errorf("a reopened project lost its work order: %v", err)
	}
}

func TestACreatedProjectIsRefusedWhereAnotherProjectStands(t *testing.T) {
	state := openState(t, t.TempDir())
	createProject(t, state, "pa", "alpha")
	ctx := t.Context()
	if _, _, err := state.CreateProject(ctx, ledger.Project{ID: "pz", Name: "alpha"}, member); !errors.Is(err, ledger.ErrProjectExists) {
		t.Errorf("a second alpha: %v", err)
	}
	for _, id := range []ledger.WorkspaceID{"../escape", "a/b", ledger.InstallationScope} {
		if _, _, err := state.CreateProject(ctx, ledger.Project{ID: id, Name: "other"}, member); !errors.Is(err, ledger.ErrInvalidProject) {
			t.Errorf("project ID %q: %v", id, err)
		}
	}
	if _, err := state.OpenProject(ctx, ledger.Project{ID: "px", Name: "x", State: ledger.ProjectActive, Database: "../outside.db"}); !errors.Is(err, ledger.ErrInvalidProject) {
		t.Errorf("a database outside the state directory: %v", err)
	}
}

// sharedTables are the records every project shares, which no project's file holds (ADR-0021).
var sharedTables = []string{"members", "member_sessions", "workers", "join_grants", "worker_autonomy", "run_windows", "orchestrators"}

// writeShared writes one record of each kind the installation holds, through a project's Store, with an
// event in that project's log.
func writeShared(t *testing.T, store ports.Store, scope ledger.WorkspaceID) {
	t.Helper()
	err := store.Write(t.Context(), scope, "test.shared", func(tx ports.WriteTx) error {
		if err := tx.Members().SaveMember(authority.MemberRecord{ID: "member-1", IdentitySubject: "local", DisplayName: "M", Status: "active", ProofDigest: "hash"}); err != nil {
			return err
		}
		if err := tx.Members().InsertSession(authority.Session{Digest: "session", Member: "member-1", CreatedAt: tx.Now(), ExpiresAt: tx.Now() + 60_000}); err != nil {
			return err
		}
		if err := tx.Workers().InsertJoinGrant(ledger.JoinGrant{ID: "grant", Digest: "grant-digest", CreatedBy: "member-1"}); err != nil {
			return err
		}
		if err := tx.Workers().Insert(ledger.WorkerRecord{Worker: ledger.Worker{ID: "worker-1", Name: "laptop"}, ProofDigest: "proof"}); err != nil {
			return err
		}
		if err := tx.Workers().SaveAutonomy(authority.WorkerAutonomy{WorkerID: "worker-1", Level: authority.LevelAttended}); err != nil {
			return err
		}
		if err := tx.Workers().InsertWindow(authority.RunWindow{ID: "window", WorkerID: "worker-1", OpenedBy: "member-1", OpenedKind: string(ledger.ActorMember), OpenedAt: tx.Now(), ClosesAt: tx.Now() + 60_000}); err != nil {
			return err
		}
		if err := tx.Workers().SaveOrchestrator(ledger.Orchestrator{ID: "orchestrator-1", WorkerID: "worker-1", Desired: ledger.OrchestratorRunning, Run: 1}); err != nil {
			return err
		}
		if _, err := tx.Policies().Insert(authority.PolicyVersion{ID: "autonomy-1", Kind: authority.PolicyAutonomy, Version: 1, Digest: "d", Body: "b", Settings: "{}", AuthoredBy: "member-1", CreatedAt: tx.Now()}); err != nil {
			return err
		}
		if _, err := tx.Policies().Insert(authority.PolicyVersion{ID: "decision-1", Kind: authority.PolicyDecision, Version: 1, Digest: "d", Body: "b", Settings: "{}", AuthoredBy: "member-1", CreatedAt: tx.Now()}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.shared", Actor: member, Subject: ledger.Subject{Kind: "test", ID: "shared"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMembersAndWorkersLiveInTheInstallationDatabase(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	writeShared(t, alphaStore, alpha.ID)

	installationFile := filepath.Join(dir, sqlite.InstallationFileName)
	for _, table := range sharedTables {
		if n := countRows(t, installationFile, table+" WHERE workspace_id = '"+string(ledger.InstallationScope)+"'"); n != 1 {
			t.Errorf("the installation holds %d rows of %s", n, table)
		}
		for _, p := range []ledger.Project{alpha, beta} {
			if n := countRows(t, filepath.Join(dir, filepath.FromSlash(p.Database)), table); n != 0 {
				t.Errorf("%s's ledger holds %d rows of %s", p.Name, n, table)
			}
		}
	}
	if n := countRows(t, installationFile, "policy_versions"); n != 1 {
		t.Errorf("the installation holds %d policy versions, want the autonomy policy alone", n)
	}
	if n := countRows(t, filepath.Join(dir, "projects", "pa.db"), "policy_versions WHERE kind = 'decision'"); n != 1 {
		t.Errorf("alpha's ledger holds %d decision policies", n)
	}

	// One sign-in and one worker serve every project, and the event stays in the project that wrote it.
	err := betaStore.Read(t.Context(), beta.ID, func(tx ports.ReadTx) error {
		if _, err := tx.Members().Member("member-1"); err != nil {
			t.Errorf("beta does not see the member: %v", err)
		}
		if _, err := tx.Workers().Find("worker-1"); err != nil {
			t.Errorf("beta does not see the worker: %v", err)
		}
		if _, err := tx.Policies().InForce(authority.PolicyAutonomy); err != nil {
			t.Errorf("beta does not see the autonomy policy: %v", err)
		}
		if _, err := tx.Policies().InForce(authority.PolicyDecision); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("beta sees alpha's decision policy: %v", err)
		}
		events, err := tx.Events(0, 100)
		for _, e := range events {
			if e.Type == "test.shared" {
				t.Errorf("beta's log holds alpha's event %+v", e)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestASharedChangeCommitsOnlyWithItsProjectsTransaction(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	alpha, store := createProject(t, state, "pa", "alpha")
	ctx := t.Context()
	refused := errors.New("refused")
	err := store.Write(ctx, alpha.ID, "test.refused", func(tx ports.WriteTx) error {
		if err := tx.Workers().Insert(ledger.WorkerRecord{Worker: ledger.Worker{ID: "worker-1"}, ProofDigest: "p"}); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("a refused write: %v", err)
	}
	err = store.Write(ctx, alpha.ID, "test.silent", func(tx ports.WriteTx) error {
		return tx.Workers().Insert(ledger.WorkerRecord{Worker: ledger.Worker{ID: "worker-2"}, ProofDigest: "p"})
	})
	if !errors.Is(err, ports.ErrNoEvent) {
		t.Fatalf("a shared change with no event: %v", err)
	}
	if n := countRows(t, filepath.Join(dir, sqlite.InstallationFileName), "workers"); n != 0 {
		t.Errorf("the installation kept %d workers from writes that did not commit", n)
	}
}

func TestAWriteThatTouchesNoSharedRecordNeverWaitsOnTheInstallation(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	alpha, store := createProject(t, state, "pa", "alpha")
	db, err := sqlite.OpenDB(filepath.Join(dir, sqlite.InstallationFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	held, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback() }()
	if _, err := held.Exec("UPDATE installation SET rev = rev WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = control.NewWorkOrders(store).Create(ctx, alpha.ID, workorders.WorkOrder{
		ID: "wo-1", Kind: workorders.KindChange, Title: "Written while the installation is busy", MaxAttempts: 3,
	}, member)
	if err != nil {
		t.Fatalf("a write with the installation busy: %v", err)
	}
	if err := store.Read(ctx, alpha.ID, func(tx ports.ReadTx) error {
		_, err := tx.WorkOrders().Get("wo-1")
		return err
	}); err != nil {
		t.Errorf("a read with the installation busy: %v", err)
	}
}

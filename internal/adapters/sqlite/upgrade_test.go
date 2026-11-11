package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The installation of one ledger that the upgrade starts from, as a build with no projects left it.
const (
	ledgerName   = "ledger.db"
	legacyScope  = ledger.WorkspaceID("default")
	legacyWorker = "worker-1"
	legacyLane   = "lane-1"
	legacyTTL    = int64(10 * 60 * 1000)
)

var (
	upgradeActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "solo"}
	firstProject = ledger.Workspace{ID: legacyScope, Name: "Solo"}
	usable       = ledger.Manifest{Drivers: []ledger.DriverManifest{{
		Driver: "claude-code", Present: true, Qualified: true, SignIn: ledger.SignIn{Kind: ledger.SignInAPIKey, Status: ledger.SignedIn},
	}}}
)

// legacy is what a ledger held before its upgrade: a worker with a running lane, among every other kind
// of record.
type legacy struct {
	auth  ledger.WorkerAuth
	epoch int64
	grant workorders.Grant
	// desired is what the worker was told to run, and lane its running lane's row.
	desired []ledger.DesiredLease
	lane    ledger.LaneRow
	// image is the ledger file's bytes, so each run of an upgrade starts from the same ledger.
	image []byte
}

var legacyOnce = sync.OnceValues(buildLegacy)

// buildLegacy writes a ledger the way a build with one ledger did: its own store, with no installation,
// holding members, sessions, workers, a join grant, autonomy, a run window, an orchestrator, the autonomy
// policy, a decision policy, a repository, a question, and a work order with a running lane.
func buildLegacy() (legacy, error) {
	dir, err := os.MkdirTemp("", "legacy-ledger-")
	if err != nil {
		return legacy{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, ledgerName)
	store, err := sqlite.Open(file, sqlstore.Options{})
	if err != nil {
		return legacy{}, err
	}
	out, err := fillLegacy(context.Background(), store)
	if closeErr := store.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return legacy{}, err
	}
	out.image, err = os.ReadFile(file)
	return out, err
}

func fillLegacy(ctx context.Context, store *sqlstore.Store) (legacy, error) {
	if err := store.Migrate(ctx); err != nil {
		return legacy{}, err
	}
	if _, err := store.CreateWorkspace(ctx, firstProject, upgradeActor); err != nil {
		return legacy{}, err
	}
	plane := control.NewWorkers(store, legacyScope, control.WorkerOptions{HeartbeatMs: 1000, LeaseTTLMs: legacyTTL})
	token, err := plane.CreateJoinToken(ctx, member)
	if err != nil {
		return legacy{}, err
	}
	if _, err := plane.CreateJoinToken(ctx, member); err != nil {
		return legacy{}, err
	}
	credential, err := plane.Register(ctx, ledger.Registration{JoinToken: token, WorkerID: legacyWorker, Name: "laptop"})
	if err != nil {
		return legacy{}, err
	}
	out := legacy{auth: ledger.WorkerAuth{WorkerID: legacyWorker, Credential: credential.Credential}}
	session, err := plane.OpenSession(ctx, ledger.SessionRequest{Auth: out.auth, Protocol: ledger.ProtocolVersion, Manifest: usable})
	if err != nil {
		return legacy{}, err
	}
	out.epoch = session.Epoch
	if _, err := control.NewWorkOrders(store).Record(ctx, legacyScope, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: "wo-running", Kind: workorders.KindChange, Title: "Runs across the upgrade"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, member); err != nil {
		return legacy{}, err
	}
	if _, err := control.NewReviews(store).Pass(ctx, legacyScope, "wo-running", member); err != nil {
		return legacy{}, err
	}
	leases := control.NewLeases(store)
	out.grant, err = leases.Claim(ctx, legacyScope, workorders.ClaimRequest{WorkOrder: "wo-running", ClaimID: "claim-1", Worker: legacyWorker, TTLMs: legacyTTL}, member)
	if err != nil {
		return legacy{}, err
	}
	if _, err := leases.StartLane(ctx, legacyScope, out.grant.Lease, legacyLane, ledger.Actor{Kind: ledger.ActorWorker, ID: legacyWorker}); err != nil {
		return legacy{}, err
	}
	desired, err := plane.Desired(ctx, ledger.DesiredRequest{Auth: out.auth})
	if err != nil {
		return legacy{}, err
	}
	out.desired = desired.Leases
	err = store.Read(ctx, legacyScope, func(tx ports.ReadTx) error {
		out.lane, err = tx.Lanes().Row(legacyLane)
		return err
	})
	if err != nil {
		return legacy{}, err
	}
	if _, err := control.NewQuestions(store, questions.BatchPolicy{}).RecordQuestion(ctx, legacyScope,
		questions.Question{ID: "q-1", Text: "What is the retry budget?"}, member); err != nil {
		return legacy{}, err
	}
	if _, err := control.NewRepositories(store).Detect(ctx, legacyScope, control.DetectedRepository{
		ID: "/src/app", Remote: "git@github.com:acme/app.git", DefaultBranch: "main",
	}, upgradeActor); err != nil {
		return legacy{}, err
	}
	err = store.Write(ctx, legacyScope, "test.legacy", func(tx ports.WriteTx) error {
		if err := tx.Members().SaveMember(authority.MemberRecord{ID: "member-1", IdentitySubject: "local", DisplayName: "M", Status: "active", ProofDigest: "passphrase-hash"}); err != nil {
			return err
		}
		if err := tx.Members().InsertSession(authority.Session{Digest: "session-1", Member: "member-1", CreatedAt: tx.Now(), ExpiresAt: tx.Now() + legacyTTL}); err != nil {
			return err
		}
		if err := tx.Workers().SaveAutonomy(authority.WorkerAutonomy{WorkerID: legacyWorker, Level: authority.LevelAttended}); err != nil {
			return err
		}
		if err := tx.Workers().InsertWindow(authority.RunWindow{ID: "window-1", WorkerID: legacyWorker, OpenedBy: "member-1", OpenedKind: string(ledger.ActorMember), OpenedAt: tx.Now(), ClosesAt: tx.Now() + legacyTTL}); err != nil {
			return err
		}
		if err := tx.Workers().SaveOrchestrator(ledger.Orchestrator{ID: "orchestrator-1", WorkerID: legacyWorker, Desired: ledger.OrchestratorStopped, Run: 2, Generation: 3}); err != nil {
			return err
		}
		autonomy, err := authority.NewAutonomyVersion("autonomy-1", "member-1", authority.AutonomyDraft{
			Body: authority.BuiltInAutonomyBody, Policy: authority.DefaultAutonomyPolicy(),
		}, tx.Now())
		if err != nil {
			return err
		}
		decision := authority.PolicyVersion{ID: "decision-1", Kind: authority.PolicyDecision, Version: 1, Digest: authority.PolicyDigest("b"), Body: "b", Settings: "{}", AuthoredBy: "member-1", CreatedAt: tx.Now()}
		for _, version := range []authority.PolicyVersion{autonomy, decision} {
			if _, err := tx.Policies().Insert(version); err != nil {
				return err
			}
		}
		_, err = tx.Append(ledger.EventDraft{Type: "test.legacy", Actor: member, Subject: ledger.Subject{Kind: "test", ID: "legacy"}})
		return err
	})
	return out, err
}

// legacyDir writes the legacy ledger into a new state directory.
func legacyDir(t *testing.T) (string, legacy) {
	t.Helper()
	before, err := legacyOnce()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ledgerName), before.image, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, before
}

func upgrade(t *testing.T, dir string) (*sqlite.State, *sqlstore.Store) {
	t.Helper()
	state, store, err := sqlite.Upgrade(t.Context(), dir, ledgerName, firstProject, upgradeActor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = state.Close()
	})
	return state, store
}

// cellSeparator parts the cells of a dumped row, and no record's text holds it.
const cellSeparator = "\x1f"

// dump reads every row of every table of a SQLite file, each column named, so two files compare
// whatever order their columns were added in.
func dump(t *testing.T, file string) map[string][]string {
	t.Helper()
	db, err := sqlite.OpenDB(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	columns := map[string][]string{}
	rows, err := db.Query(`SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p
 WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' ORDER BY m.name, p.name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		columns[table] = append(columns[table], column)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for table, names := range columns {
		rows, err := db.Query(`SELECT "` + strings.Join(names, `", "`) + `" FROM "` + table + `"`)
		if err != nil {
			t.Fatal(err)
		}
		values := make([]any, len(names))
		pointers := make([]any, len(names))
		for i := range values {
			pointers[i] = &values[i]
		}
		out[table] = []string{}
		for rows.Next() {
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			cells := make([]string, len(names))
			for i, name := range names {
				cells[i] = fmt.Sprintf("%s=%v", name, values[i])
			}
			out[table] = append(out[table], strings.Join(cells, cellSeparator))
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		slices.Sort(out[table])
	}
	return out
}

// shared lists the rows of the shared tables, and the autonomy policy, with the scope they carry
// replaced by the one given, so a ledger's rows and the installation's compare.
func shared(rows map[string][]string, scope ledger.WorkspaceID) map[string][]string {
	out := map[string][]string{}
	for _, table := range append(slices.Clone(sharedTables), "policy_versions") {
		out[table] = []string{}
		for _, row := range rows[table] {
			cells := strings.Split(row, cellSeparator)
			if table == "policy_versions" && !slices.Contains(cells, "kind=autonomy") {
				continue
			}
			for i, cell := range cells {
				if strings.HasPrefix(cell, "workspace_id=") {
					cells[i] = "workspace_id=" + string(scope)
				}
			}
			out[table] = append(out[table], strings.Join(cells, cellSeparator))
		}
		slices.Sort(out[table])
	}
	return out
}

func TestTheUpgradeKeepsTheWorkspaceTheFileAndEveryRecord(t *testing.T) {
	dir, _ := legacyDir(t)
	file := filepath.Join(dir, ledgerName)
	before := dump(t, file)
	beforeInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	state, store := upgrade(t, dir)
	ctx := t.Context()

	projects, err := state.Installation.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("the registry holds %+v", projects)
	}
	p := projects[0]
	if p.ID != legacyScope || p.Name != ledger.DefaultProjectName || p.Database != ledgerName || p.State != ledger.ProjectActive {
		t.Errorf("the default project reads %+v", p)
	}
	afterInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Error("the ledger file moved or was replaced")
	}

	after := dump(t, file)
	for table, rows := range before {
		want := rows
		switch {
		case slices.Contains(sharedTables, table):
			want = []string{}
		case table == "policy_versions":
			want = slices.DeleteFunc(slices.Clone(rows), func(row string) bool {
				return slices.Contains(strings.Split(row, cellSeparator), "kind=autonomy")
			})
		}
		if !slices.Equal(after[table], want) {
			t.Errorf("table %s held\n%v\nand holds\n%v", table, want, after[table])
		}
	}
	if len(after) != len(before) {
		t.Errorf("the ledger had %d tables and has %d", len(before), len(after))
	}
	moved := shared(before, ledger.InstallationScope)
	kept := shared(dump(t, filepath.Join(dir, sqlite.InstallationFileName)), ledger.InstallationScope)
	for table, rows := range moved {
		if len(rows) == 0 {
			t.Errorf("the legacy ledger held no %s, so the test proves nothing about it", table)
		}
		if !slices.Equal(kept[table], rows) {
			t.Errorf("the installation holds\n%v\nof %s, and the ledger held\n%v", kept[table], table, rows)
		}
	}

	index, err := state.Installation.RepositoryIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []ledger.IndexedRepository{{Identity: "github.com/acme/app", Project: legacyScope, Repository: "/src/app"}}; !slices.Equal(index, want) {
		t.Errorf("the repository index holds %+v", index)
	}
	events, err := state.Installation.Events(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	if !slices.Equal(types, []string{ledger.EventProjectCreated, ledger.EventRecordsMoved}) {
		t.Errorf("the installation's log holds %v", types)
	}
	err = store.Read(ctx, legacyScope, func(tx ports.ReadTx) error {
		if _, err := tx.Members().Member("member-1"); err != nil {
			return err
		}
		_, err := tx.WorkOrders().Get("wo-running")
		return err
	})
	if err != nil {
		t.Errorf("the default project reads its records through the installation: %v", err)
	}
}

func TestAnUpgradedInstallationStartsAgainWithNoChange(t *testing.T) {
	dir, _ := legacyDir(t)
	state, store, err := sqlite.Upgrade(t.Context(), dir, ledgerName, firstProject, upgradeActor)
	if err != nil {
		t.Fatal(err)
	}
	ledgerBefore := dump(t, filepath.Join(dir, ledgerName))
	installationBefore := dump(t, filepath.Join(dir, sqlite.InstallationFileName))
	_ = store.Close()
	_ = state.Close()
	upgrade(t, dir)
	if after := dump(t, filepath.Join(dir, ledgerName)); !mapsEqual(after, ledgerBefore) {
		t.Error("a second start changed the ledger")
	}
	if after := dump(t, filepath.Join(dir, sqlite.InstallationFileName)); !mapsEqual(after, installationBefore) {
		t.Error("a second start changed the installation database")
	}
}

func TestAFreshInstallationStartsWithItsDefaultProjectInTheLedgerFile(t *testing.T) {
	dir := t.TempDir()
	state, store := upgrade(t, dir)
	ctx := t.Context()
	p, err := state.Installation.Project(ctx, ledger.DefaultProjectName)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != firstProject.ID || p.Database != ledgerName {
		t.Errorf("a fresh installation's first project reads %+v", p)
	}
	if err := store.Read(ctx, firstProject.ID, func(tx ports.ReadTx) error {
		w, err := tx.Workspace()
		if err == nil && w.Name != firstProject.Name {
			t.Errorf("the workspace is named %q", w.Name)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestARunningLaneSurvivesTheUpgrade(t *testing.T) {
	dir, before := legacyDir(t)
	_, store := upgrade(t, dir)
	ctx := t.Context()
	plane := control.NewWorkers(store, legacyScope, control.WorkerOptions{HeartbeatMs: 1000, LeaseTTLMs: legacyTTL})
	session, err := plane.OpenSession(ctx, ledger.SessionRequest{Auth: before.auth, Protocol: ledger.ProtocolVersion, Manifest: usable})
	if err != nil {
		t.Fatalf("the worker's credential no longer opens a session: %v", err)
	}
	if session.Epoch != before.epoch+1 {
		t.Errorf("the worker's epoch went from %d to %d", before.epoch, session.Epoch)
	}
	if err := control.NewLeases(store).Renew(ctx, legacyScope, before.grant.Lease, legacyTTL); err != nil {
		t.Errorf("the running lane's lease no longer renews: %v", err)
	}
	desired, err := plane.Desired(ctx, ledger.DesiredRequest{Auth: before.auth})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.desired) != 1 || before.desired[0].WorkOrder != "wo-running" || before.desired[0].Fence != before.grant.Fence {
		t.Fatalf("before the upgrade the worker was told %+v", before.desired)
	}
	if !reflect.DeepEqual(desired.Leases, before.desired) {
		t.Errorf("the worker was told\n%+v\nand is told\n%+v", before.desired, desired.Leases)
	}
	err = store.Read(ctx, legacyScope, func(tx ports.ReadTx) error {
		order, err := tx.WorkOrders().Get("wo-running")
		if err != nil {
			return err
		}
		if order.State != workorders.Running {
			t.Errorf("the work order is %s", order.State)
		}
		lane, err := tx.Lanes().Row(legacyLane)
		if err == nil && !reflect.DeepEqual(lane, before.lane) {
			t.Errorf("the lane was\n%+v\nand is\n%+v", before.lane, lane)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEachUpgradeStepRepeatsAfterACrash(t *testing.T) {
	steps := sqlite.UpgradeSteps()
	if len(steps) < 5 {
		t.Fatalf("the upgrade has steps %v", steps)
	}
	referenceDir, before := legacyDir(t)
	upgrade(t, referenceDir)
	want := endState(t, referenceDir)
	for n := range steps {
		t.Run("after "+steps[n], func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ledgerName), before.image, 0o600); err != nil {
				t.Fatal(err)
			}
			original := shared(dump(t, filepath.Join(dir, ledgerName)), ledger.InstallationScope)
			if err := sqlite.UpgradeThrough(t.Context(), dir, ledgerName, firstProject, upgradeActor, n+1); err != nil {
				t.Fatal(err)
			}
			checkNothingLost(t, dir, original)
			upgrade(t, dir)
			if got := endState(t, dir); !mapsEqual(got, want) {
				for key := range want {
					if !slices.Equal(got[key], want[key]) {
						t.Errorf("%s ends as\n%v\nand an upgrade with no crash ends as\n%v", key, got[key], want[key])
					}
				}
			}
		})
	}
}

// checkNothingLost holds after a crash at any step: every shared record the ledger held is still in the
// ledger, in the installation, or in both.
func checkNothingLost(t *testing.T, dir string, original map[string][]string) {
	t.Helper()
	inLedger := shared(dump(t, filepath.Join(dir, ledgerName)), ledger.InstallationScope)
	inInstallation := map[string][]string{}
	if _, err := os.Stat(filepath.Join(dir, sqlite.InstallationFileName)); err == nil {
		inInstallation = shared(dump(t, filepath.Join(dir, sqlite.InstallationFileName)), ledger.InstallationScope)
	}
	for table, rows := range original {
		for _, row := range rows {
			if !slices.Contains(inLedger[table], row) && !slices.Contains(inInstallation[table], row) {
				t.Errorf("a crash lost %s row %s", table, row)
			}
		}
	}
}

// endState is what an upgrade leaves, apart from the times and IDs a run makes for itself.
func endState(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for table, rows := range dump(t, filepath.Join(dir, ledgerName)) {
		out["ledger "+table] = rows
	}
	installation := dump(t, filepath.Join(dir, sqlite.InstallationFileName))
	for table, rows := range installation {
		switch table {
		case "installation_events":
			for _, row := range rows {
				out["installation "+table] = append(out["installation "+table], keep(row, "rev", "type", "actor_kind", "actor_id", "subject_kind", "subject_id", "data"))
			}
		case "projects":
			for _, row := range rows {
				out["installation "+table] = append(out["installation "+table], keep(row, "id", "name", "display_name", "state", "database"))
			}
		case "schema_migrations":
			for _, row := range rows {
				out["installation "+table] = append(out["installation "+table], keep(row, "version", "name"))
			}
		default:
			out["installation "+table] = rows
		}
	}
	return out
}

// keep reads only the named cells of a dumped row.
func keep(row string, names ...string) string {
	var kept []string
	for _, cell := range strings.Split(row, cellSeparator) {
		name, _, _ := strings.Cut(cell, "=")
		if slices.Contains(names, name) {
			kept = append(kept, cell)
		}
	}
	return strings.Join(kept, cellSeparator)
}

func mapsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, rows := range a {
		if !slices.Equal(rows, b[key]) {
			return false
		}
	}
	return true
}

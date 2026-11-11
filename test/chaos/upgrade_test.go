//go:build chaos

package chaos

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/contract"
)

// The upgrade from one ledger to the default project (ADR-0021) runs in a state directory of its own,
// inside the world's target, so the world's ledger stays what its checks read.
const (
	upgradeLedger = "ledger.db"
	upgradeScope  = ledger.WorkspaceID("default")
)

// upgradeSteps are the steps of an upgrade where a kill lands: after each commit, and inside the copy and
// the deletion before they commit.
var upgradeSteps = []string{"migrated", "opened", "registered", "copying", "copied", "deleting", "deleted", "indexed"}

// sharedKeys names each shared record by its key, so a check can find it in the ledger or the
// installation whatever scope it carries there.
var sharedKeys = map[string]string{
	"members":         "SELECT id FROM members",
	"member_sessions": "SELECT digest FROM member_sessions",
	"join_grants":     "SELECT id FROM join_grants",
	"workers":         "SELECT id FROM workers",
	"worker_autonomy": "SELECT worker_id FROM worker_autonomy",
	"run_windows":     "SELECT id FROM run_windows",
	"orchestrators":   "SELECT id FROM orchestrators",
	"policy_versions": "SELECT id FROM policy_versions WHERE kind = 'autonomy'",
}

func init() { childRoles["upgrade"] = roleUpgrade }

// roleUpgrade starts a state directory the way the daemon does.
func roleUpgrade(c *child) error {
	state, store, err := sqlite.Upgrade(c.ctx, c.spec.Path, upgradeLedger, ledger.Workspace{ID: upgradeScope, Name: "Solo"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "solo"})
	if err != nil {
		return err
	}
	_ = store.Close()
	return state.Close()
}

func upgradeCase() crashCase {
	return crashCase{
		name:    "upgrade one ledger to the default project",
		op:      "installation.upgrade",
		steps:   upgradeSteps,
		prepare: prepareUpgrade,
		between: func(_ *world, spec childSpec) error { return checkNothingMovedIsLost(spec.Path) },
		settled: func(_ *world, spec childSpec) error { return checkUpgraded(spec.Path) },
	}
}

// prepareUpgrade writes the ledger a build with no projects left, holding one record of every shared
// kind and a repository.
func prepareUpgrade(w *world) (childSpec, error) {
	dir := filepath.Join(w.target.Dir, "upgrade")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return childSpec{}, err
	}
	ctx := context.Background()
	store, err := sqlite.Open(filepath.Join(dir, upgradeLedger), sqlstore.Options{})
	if err != nil {
		return childSpec{}, err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return childSpec{}, err
	}
	system := ledger.Actor{Kind: ledger.ActorSystem, ID: "solo"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: upgradeScope, Name: "Solo"}, system); err != nil {
		return childSpec{}, err
	}
	if _, err := control.NewRepositories(store).Detect(ctx, upgradeScope, control.DetectedRepository{
		ID: "/src/app", Remote: "https://github.com/acme/app.git", DefaultBranch: "main",
	}, system); err != nil {
		return childSpec{}, err
	}
	err = store.Installation(upgradeScope).Write(ctx, "chaos.legacy", func(tx ports.SharedTx) error {
		now := tx.Now()
		if err := tx.Members().SaveMember(authority.MemberRecord{ID: "member-1", IdentitySubject: "local", DisplayName: "M", Status: "active", ProofDigest: "hash"}); err != nil {
			return err
		}
		if err := tx.Members().InsertSession(authority.Session{Digest: "session-1", Member: "member-1", CreatedAt: now, ExpiresAt: now + longTTL}); err != nil {
			return err
		}
		if err := tx.Workers().InsertJoinGrant(ledger.JoinGrant{ID: "grant-1", Digest: "digest", CreatedBy: "member-1"}); err != nil {
			return err
		}
		if err := tx.Workers().Insert(ledger.WorkerRecord{Worker: ledger.Worker{ID: "worker-1", Name: "laptop"}, ProofDigest: "proof"}); err != nil {
			return err
		}
		if err := tx.Workers().SaveAutonomy(authority.WorkerAutonomy{WorkerID: "worker-1", Level: authority.LevelAttended}); err != nil {
			return err
		}
		if err := tx.Workers().InsertWindow(authority.RunWindow{ID: "window-1", WorkerID: "worker-1", OpenedBy: "member-1", OpenedKind: string(ledger.ActorMember), OpenedAt: now, ClosesAt: now + longTTL}); err != nil {
			return err
		}
		if err := tx.Workers().SaveOrchestrator(ledger.Orchestrator{ID: "orchestrator-1", WorkerID: "worker-1", Desired: ledger.OrchestratorStopped, Run: 1}); err != nil {
			return err
		}
		autonomy, err := authority.NewAutonomyVersion("autonomy-1", "member-1", authority.AutonomyDraft{Body: authority.BuiltInAutonomyBody, Policy: authority.DefaultAutonomyPolicy()}, now)
		if err != nil {
			return err
		}
		if _, err := tx.Policies().Insert(autonomy); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "chaos.legacy", Actor: system, Subject: ledger.Subject{Kind: "chaos", ID: "legacy"}})
		return err
	})
	return childSpec{Role: "upgrade", Path: dir}, err
}

// keysIn reads each shared table's keys in a SQLite file, or none when the file does not exist.
func keysIn(file string) (map[string][]string, error) {
	out := map[string][]string{}
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return out, nil
	}
	db, err := sqlite.OpenDB(file)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	for table, query := range sharedKeys {
		keys, err := column(db, query)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", table, err)
		}
		out[table] = keys
	}
	return out, nil
}

func column(db *sql.DB, query string) ([]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// legacyKeys are the shared records prepareUpgrade wrote.
var legacyKeys = map[string][]string{
	"members": {"member-1"}, "member_sessions": {"session-1"}, "join_grants": {"grant-1"}, "workers": {"worker-1"},
	"worker_autonomy": {"worker-1"}, "run_windows": {"window-1"}, "orchestrators": {"orchestrator-1"}, "policy_versions": {"autonomy-1"},
}

// checkNothingMovedIsLost holds wherever a kill lands: every shared record is in the ledger, the
// installation, or both.
func checkNothingMovedIsLost(dir string) error {
	inLedger, err := keysIn(filepath.Join(dir, upgradeLedger))
	if err != nil {
		return err
	}
	inInstallation, err := keysIn(filepath.Join(dir, sqlite.InstallationFileName))
	if err != nil {
		return err
	}
	for table, keys := range legacyKeys {
		for _, key := range keys {
			if !slices.Contains(inLedger[table], key) && !slices.Contains(inInstallation[table], key) {
				return contract.Violatedf("a killed upgrade lost %s %s", table, key)
			}
		}
	}
	return nil
}

// checkUpgraded is the end state of a finished upgrade: the ledger is the project `default`, every
// shared record is in the installation and none in the ledger, the installation logged one creation
// and one copy, and the repository index names the repository.
func checkUpgraded(dir string) error {
	ctx := context.Background()
	inLedger, err := keysIn(filepath.Join(dir, upgradeLedger))
	if err != nil {
		return err
	}
	inInstallation, err := keysIn(filepath.Join(dir, sqlite.InstallationFileName))
	if err != nil {
		return err
	}
	for table, keys := range legacyKeys {
		if len(inLedger[table]) != 0 {
			return contract.Violatedf("the ledger still holds %s %v", table, inLedger[table])
		}
		if len(inInstallation[table]) != len(keys) {
			return contract.Violatedf("the installation holds %s %v, want %d", table, inInstallation[table], len(keys))
		}
	}
	installation, err := sqlite.OpenInstallation(filepath.Join(dir, sqlite.InstallationFileName), sqlstore.Options{})
	if err != nil {
		return err
	}
	defer installation.Close()
	projects, err := installation.Projects(ctx)
	if err != nil {
		return err
	}
	if len(projects) != 1 || projects[0].ID != upgradeScope || projects[0].Name != ledger.DefaultProjectName || projects[0].Database != upgradeLedger {
		return contract.Violatedf("the registry holds %+v", projects)
	}
	events, err := installation.Events(ctx, 0, 100)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, e := range events {
		counts[e.Type]++
	}
	if counts[ledger.EventProjectCreated] != 1 || counts[ledger.EventRecordsMoved] != 1 || len(events) != 2 {
		return contract.Violatedf("the installation's log holds %v", counts)
	}
	index, err := installation.RepositoryIndex(ctx)
	if err != nil {
		return err
	}
	if len(index) != 1 || index[0].Identity != "github.com/acme/app" || index[0].Project != upgradeScope {
		return contract.Violatedf("the repository index holds %+v", index)
	}
	return nil
}
